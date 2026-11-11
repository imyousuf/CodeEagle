package decide

// Characterization of the bytes JevJudge.Decide sends, before the judge is
// rebuilt over a jev.Asker. internal/transcript pins the (state, questions)
// speaker identification hands to a Judge; this pins what such a call
// becomes on the wire, so the two together cover the whole path.
//
// The goldens under testdata/golden hold each body exactly as sent
// (<name>.raw.jsonl, one per line) and decoded with sorted keys and
// indentation (<name>.json). Regenerate them, only for an intended change,
// with:
//
//	UPDATE_GOLDEN=1 go test -race -count=1 -run Golden ./internal/decide/
//
// The test also states the expected request in code, so a careless
// regeneration still fails.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/jev"
)

// jevGoldenUpdate rewrites golden files rather than comparing against them.
var jevGoldenUpdate = os.Getenv("UPDATE_GOLDEN") != ""

// jevGoldenServer records every request body as sent and answers each
// choice question from a table.
type jevGoldenServer struct {
	t       *testing.T
	srv     *httptest.Server
	answers map[string]jev.Answer

	mu     sync.Mutex
	bodies [][]byte
}

func newJevGoldenServer(t *testing.T, answers map[string]jev.Answer) *jevGoldenServer {
	t.Helper()
	s := &jevGoldenServer{t: t, answers: answers}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *jevGoldenServer) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.t.Errorf("read request: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer test-key" ||
		r.Header.Get("Content-Type") != "application/json" {
		s.t.Errorf("request = %s, Authorization %q, Content-Type %q", r.Method,
			r.Header.Get("Authorization"), r.Header.Get("Content-Type"))
	}
	s.mu.Lock()
	s.bodies = append(s.bodies, body)
	s.mu.Unlock()

	var req struct {
		Questions map[string]json.RawMessage `json:"questions"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		s.t.Errorf("decode request: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	answers := make(jev.Answers, len(req.Questions))
	for name := range req.Questions {
		ans, ok := s.answers[name]
		if !ok {
			s.t.Errorf("no scripted answer for %q", name)
		}
		answers[name] = ans
	}
	reply, err := json.Marshal(jev.Response{
		Model:   jev.DefaultModel,
		Answers: answers,
		Usage:   jev.Usage{InputTokens: 700 * len(answers), OutputTokens: 5},
	})
	if err != nil {
		s.t.Errorf("encode reply: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(reply)
}

func (s *jevGoldenServer) requests() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.bodies...)
}

// goldenJevJudge is the judge the CLI builds, pointed at the server. The one
// line that reaches inside JevJudge is here.
func goldenJevJudge(t *testing.T, s *jevGoldenServer) *JevJudge {
	t.Helper()
	client, err := jev.New("test-key", jev.WithBaseURL(s.srv.URL), jev.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	return &JevJudge{client: client}
}

// jevGoldenCanonical decodes JSON and encodes it again with sorted keys,
// numbers exactly as written and no HTML escaping, indented.
func jevGoldenCanonical(t *testing.T, raw []byte) string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.String()
}

func jevGoldenJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return jevGoldenCanonical(t, raw)
}

// checkJevGolden compares content with testdata/golden/<name>, or writes it
// when UPDATE_GOLDEN is set.
func checkJevGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if jevGoldenUpdate {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v (create it with UPDATE_GOLDEN=1)", err)
	}
	if !bytes.Equal(want, got) {
		t.Errorf("%s no longer matches what the code produces:\n%s\nregenerate with UPDATE_GOLDEN=1 only if the change is intended",
			path, jevGoldenFirstDiff(string(want), string(got)))
	}
}

func jevGoldenFirstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl || i >= len(w) || i >= len(g) {
			return fmt.Sprintf("line %d:\n  golden: %s\n  now:    %s", i+1, wl, gl)
		}
	}
	return "(identical lines; the difference is in line endings)"
}

// checkJevRequests pins request bodies byte for byte and decoded.
func checkJevRequests(t *testing.T, name string, bodies [][]byte) {
	t.Helper()
	var raw, decoded bytes.Buffer
	decoded.WriteString("[\n")
	for i, b := range bodies {
		if bytes.ContainsRune(b, '\n') {
			t.Fatalf("a request body holds a raw newline, which the raw golden cannot keep on one line: %s", b)
		}
		raw.Write(b)
		raw.WriteByte('\n')
		if i > 0 {
			decoded.WriteString(",\n")
		}
		decoded.WriteString(strings.TrimRight(jevGoldenCanonical(t, b), "\n"))
	}
	decoded.WriteString("\n]\n")
	checkJevGolden(t, name+".raw.jsonl", raw.Bytes())
	checkJevGolden(t, name+".json", decoded.Bytes())
}

// wantJevRequest asserts one body: the pinned model, the state, and the
// questions against ones built with the jev constructors.
func wantJevRequest(t *testing.T, label string, body []byte, wantState any, wantQuestions jev.Questions) {
	t.Helper()
	var req struct {
		Model     string          `json:"model"`
		State     json.RawMessage `json:"state"`
		Questions json.RawMessage `json:"questions"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	if req.Model != jev.DefaultModel {
		t.Errorf("%s: model = %q, want %q", label, req.Model, jev.DefaultModel)
	}
	if got, want := jevGoldenCanonical(t, req.State), jevGoldenJSON(t, wantState); got != want {
		t.Errorf("%s: state differs:\n%s", label, jevGoldenFirstDiff(want, got))
	}
	if got, want := jevGoldenCanonical(t, req.Questions), jevGoldenJSON(t, wantQuestions); got != want {
		t.Errorf("%s: questions differ:\n%s", label, jevGoldenFirstDiff(want, got))
	}
}

// Questions and states shaped like the two speaker identification asks.
func goldenIdentityOptions(label string) map[string]string {
	return map[string]string{
		"Kevin Mitchell": "The transcript establishes that " + label + " is Kevin Mitchell",
		"Mona Patel":     "The transcript establishes that " + label + " is Mona Patel",
		"unresolved":     "The transcript does not establish who " + label + " is",
	}
}

var goldenBackgroundOptions = map[string]string{
	"participant":      "A person taking part in the meeting",
	"background_media": "Not a person in the meeting at all — a television, a video being demonstrated, a podcast, an advertisement, a sports or news broadcast, or a conversation elsewhere that the microphone caught",
	"mixed":            "Both: this label holds some meeting speech and some background audio that the recorder filed together",
	"cannot_tell":      "There is not enough here to tell",
}

func goldenIdentityState() map[string]any {
	return map[string]any{
		"meeting":                 "Retention sync",
		"date":                    "2026-01-01 09:00",
		"transcript":              "[00:00] You: Right. Kevin, did the composite index get dropped?\n[00:10] Person 1: It did <again> & I'll fix it.\n",
		"host":                    `The speaker labelled "You" is Imran Yousuf, who made the recording. No other speaker is Imran Yousuf.`,
		"known_colleagues":        "Imran Yousuf, Kevin Mitchell, Mona Patel",
		"names_mentioned":         "Kevin",
		"candidate_evidence":      map[string]string{"Person 1": `Kevin (strength 2.0) — addressed by this name, and answered in the turn that follows: "Kevin, did the composite index get dropped?"`},
		"candidate_evidence_note": "Produced by a pattern matcher reading direct address.",
	}
}

func goldenBackgroundState() map[string]any {
	return map[string]any{
		"situation":        "A meeting was recorded.",
		"meeting":          "Retention sync",
		"duration_minutes": "2",
		"voices": map[string]any{
			"Person 7": map[string]any{
				"speaking_seconds": "24", "utterances": 3, "first_spoke_at": "01:10", "last_spoke_at": "01:38",
				"share_of_its_own_span": "0.86", "nobody_replied_to_it": true, "it_addressed_nobody": true,
				"voices_it_traded_turns_with": 1, "said": []string{"Are you familiar with this chili?"},
			},
		},
		"note": "Judge by what was said.",
	}
}

func TestJevJudgeRequestGolden(t *testing.T) {
	srv := newJevGoldenServer(t, map[string]jev.Answer{
		"speaker_0": {Type: jev.TypeChoice, Choice: "Kevin Mitchell", Confidence: 0.97,
			Probabilities: map[string]float64{"Kevin Mitchell": 0.97, "unresolved": 0.02, "Mona Patel": 0.01}},
		"speaker_1": {Type: jev.TypeChoice, Choice: "unresolved", Confidence: 0.88,
			Probabilities: map[string]float64{"unresolved": 0.88, "Mona Patel": 0.12}},
		"voice_0": {Type: jev.TypeChoice, Choice: "background_media", Confidence: 0.93,
			Probabilities: map[string]float64{"background_media": 0.93, "participant": 0.05, "mixed": 0.015, "cannot_tell": 0.005}},
	})
	judge := goldenJevJudge(t, srv)
	ctx := context.Background()

	screen, err := judge.Decide(ctx, goldenBackgroundState(), map[string]Question{
		"voice_0": {
			Ask:     `Was the voice labelled "Person 7" a person taking part in this meeting, or something else the microphone picked up?`,
			Options: goldenBackgroundOptions,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	identify, err := judge.Decide(ctx, goldenIdentityState(), map[string]Question{
		"speaker_0": {
			Ask:     `Which person is the speaker labelled "Person 1"? Answer "unresolved" unless the transcript itself establishes who they are.`,
			Options: goldenIdentityOptions("Person 1"),
		},
		"speaker_1": {
			Ask:     `Which person is the speaker labelled "Person 2"? Answer "unresolved" unless the transcript itself establishes who they are.`,
			Options: goldenIdentityOptions("Person 2"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	bodies := srv.requests()
	if len(bodies) != 2 {
		t.Fatalf("%d requests, want one per Decide", len(bodies))
	}
	checkJevRequests(t, "jev_judge", bodies)

	// Every question is a choice over the options as given, the ask as its
	// instructions.
	wantJevRequest(t, "screen", bodies[0], goldenBackgroundState(), jev.Questions{
		"voice_0": jev.Choice(
			`Was the voice labelled "Person 7" a person taking part in this meeting, or something else the microphone picked up?`,
			goldenBackgroundOptions),
	})
	wantJevRequest(t, "identify", bodies[1], goldenIdentityState(), jev.Questions{
		"speaker_0": jev.Choice(
			`Which person is the speaker labelled "Person 1"? Answer "unresolved" unless the transcript itself establishes who they are.`,
			goldenIdentityOptions("Person 1")),
		"speaker_1": jev.Choice(
			`Which person is the speaker labelled "Person 2"? Answer "unresolved" unless the transcript itself establishes who they are.`,
			goldenIdentityOptions("Person 2")),
	})

	if got, want := fmt.Sprintf("%+v", screen), "map[voice_0:{Choice:background_media Confidence:0.93 RunnerUp:participant RunnerUpShare:0.05}]"; got != want {
		t.Errorf("screen verdicts = %s\nwant              %s", got, want)
	}
	if got, want := fmt.Sprintf("%+v", identify),
		"map[speaker_0:{Choice:Kevin Mitchell Confidence:0.97 RunnerUp:unresolved RunnerUpShare:0.02} "+
			"speaker_1:{Choice:unresolved Confidence:0.88 RunnerUp:Mona Patel RunnerUpShare:0.12}]"; got != want {
		t.Errorf("identify verdicts = %s\nwant                %s", got, want)
	}
	if got := judge.Usage(); got != (Usage{Requests: 2, InputTokens: 2100, OutputTokens: 10}) {
		t.Errorf("usage = %+v", got)
	}
}
