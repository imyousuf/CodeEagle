package transcript

// Characterization tests for what this package sends to the decision model.
//
// The topic judge, the search reranker and the speaker adjudication are about
// to be rebuilt over shared plumbing, and none of that may change a single
// byte the service receives: the model reads the state as text, so even a
// reordered key is a different question. Each test here drives the real code
// against a Jev endpoint on localhost and pins every request body two ways,
// under testdata/golden:
//
//   - <name>.raw.jsonl holds each body exactly as sent, one per line, so a
//     change of key order or escaping fails;
//   - <name>.json holds the same bodies decoded and re-encoded with sorted
//     keys and indentation, so a failure shows which field moved.
//
// Regenerate both, only when a change is intended, with:
//
//	UPDATE_GOLDEN=1 go test -race -count=1 -run Golden ./internal/transcript/
//
// The goldens can be regenerated carelessly, so each test also states the
// questions and the state it expects in code, built with the jev
// constructors so that a noul, a noul with criteria and a choice are told
// apart.

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
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/decide"
	"github.com/imyousuf/CodeEagle/pkg/jev"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
)

// jevGoldenUpdate rewrites golden files rather than comparing against them.
var jevGoldenUpdate = os.Getenv("UPDATE_GOLDEN") != ""

// jevGoldenQuestion is a question as it arrives on the wire.
type jevGoldenQuestion struct {
	Type         jev.QuestionType `json:"type"`
	Instructions string           `json:"instructions"`
	Criteria     json.RawMessage  `json:"criteria"`
}

// jevGoldenRequest is a request body as it arrives on the wire.
type jevGoldenRequest struct {
	Model     string                       `json:"model"`
	State     json.RawMessage              `json:"state"`
	Questions map[string]jevGoldenQuestion `json:"questions"`
}

// jevGoldenAnswerer scripts the answer to one question. refuse makes the
// whole request fail as too large, the one refusal callers act on.
type jevGoldenAnswerer func(req *jevGoldenRequest, name string) (answer jev.Answer, refuse bool)

// jevGoldenServer is a Jev endpoint that records every request body as sent
// and answers from a script.
type jevGoldenServer struct {
	t      *testing.T
	srv    *httptest.Server
	answer jevGoldenAnswerer

	mu     sync.Mutex
	bodies [][]byte
}

func newJevGoldenServer(t *testing.T, answer jevGoldenAnswerer) *jevGoldenServer {
	t.Helper()
	s := &jevGoldenServer{t: t, answer: answer}
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

	var req jevGoldenRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.t.Errorf("decode request: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	names := make([]string, 0, len(req.Questions))
	for name := range req.Questions {
		names = append(names, name)
	}
	sort.Strings(names)
	answers := make(jev.Answers, len(names))
	for _, name := range names {
		ans, refuse := s.answer(&req, name)
		if refuse {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"detail":{"error_type":"max_tokens_exceeded","message":"too many tokens"}}`))
			return
		}
		answers[name] = ans
	}
	reply, err := json.Marshal(jev.Response{
		Model:   jev.DefaultModel,
		Answers: answers,
		Usage:   jev.Usage{InputTokens: 100 * len(names), OutputTokens: 3},
	})
	if err != nil {
		s.t.Errorf("encode reply: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Typesafe-Request-Id", "req_golden")
	_, _ = w.Write(reply)
}

// client is the asker the CLI builds, pointed at this server.
func (s *jevGoldenServer) client() *jev.Client {
	s.t.Helper()
	c, err := jev.New("test-key", jev.WithBaseURL(s.srv.URL), jev.WithMaxRetries(0))
	if err != nil {
		s.t.Fatal(err)
	}
	return c
}

// requests returns the bodies received so far, in arrival order.
func (s *jevGoldenServer) requests() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.bodies...)
}

// decoded parses one recorded body.
func (s *jevGoldenServer) decoded(i int) *jevGoldenRequest {
	s.t.Helper()
	bodies := s.requests()
	if i >= len(bodies) {
		s.t.Fatalf("request %d of %d", i, len(bodies))
	}
	var req jevGoldenRequest
	if err := json.Unmarshal(bodies[i], &req); err != nil {
		s.t.Fatal(err)
	}
	return &req
}

// noulAnswer answers a noul.
func noulAnswer(p float64) jev.Answer { return jev.Answer{Type: jev.TypeNoul, Noul: p} }

// jevGoldenCanonical decodes JSON and encodes it again with sorted keys,
// numbers exactly as written, and no HTML escaping, indented when indent is
// set. Two documents with the same content encode the same.
func jevGoldenCanonical(t *testing.T, raw []byte, indent bool) string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return jevGoldenEncode(t, v, indent)
}

func jevGoldenEncode(t *testing.T, v any, indent bool) string {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(v); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.String()
}

// jevGoldenJSON renders any value the way the goldens hold it.
func jevGoldenJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return []byte(jevGoldenCanonical(t, raw, true))
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

// jevGoldenFirstDiff names the first line at which two texts differ.
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

// checkJevRequests pins request bodies: <name>.raw.jsonl byte for byte, and
// <name>.json decoded and indented.
func checkJevRequests(t *testing.T, name string, bodies [][]byte) {
	t.Helper()
	var raw bytes.Buffer
	decoded := make([]any, 0, len(bodies))
	for _, b := range bodies {
		if bytes.ContainsRune(b, '\n') {
			t.Fatalf("a request body holds a raw newline, which the raw golden cannot keep on one line: %s", b)
		}
		raw.Write(b)
		raw.WriteByte('\n')
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			t.Fatal(err)
		}
		decoded = append(decoded, v)
	}
	checkJevGolden(t, name+".raw.jsonl", raw.Bytes())
	checkJevGolden(t, name+".json", []byte(jevGoldenEncode(t, decoded, true)))
}

// wantJevRequest asserts one body field by field: the pinned model, the state
// against the expected value, and the questions against ones built with the
// jev constructors. A nil state is left to the golden.
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
	if wantState != nil {
		if got, want := jevGoldenCanonical(t, req.State, true), string(jevGoldenJSON(t, wantState)); got != want {
			t.Errorf("%s: state differs:\n%s", label, jevGoldenFirstDiff(want, got))
		}
	}
	if got, want := jevGoldenCanonical(t, req.Questions, true), string(jevGoldenJSON(t, wantQuestions)); got != want {
		t.Errorf("%s: questions differ:\n%s", label, jevGoldenFirstDiff(want, got))
	}
}

// --- topic judge ----------------------------------------------------------

// Literal question text of the topic judge, as the service receives it.
const (
	topicRelatedWhenTrue  = "a and b name one subject, or one is a part of the other, or both are parts of one broader subject; a person asking about either would want to see meetings filed under the other"
	topicRelatedWhenFalse = "a and b are about different things; a person asking about one would not want meetings filed under the other, even if a meeting happened to discuss both"
	topicSameWhenTrue     = "the two labels are two wordings of one subject; any meeting filed under one belongs under the other"
	topicSameWhenFalse    = "the two labels name different subjects, however closely related"
)

func wantTopicQuestions(pairs int) jev.Questions {
	q := jev.Questions{}
	for i := range pairs {
		name := fmt.Sprintf("p%d", i)
		q["related_"+name] = jev.NoulWithCriteria(
			fmt.Sprintf("In the pair whose id is %q, is what was said under topic a about the same thing as what was said under topic b, or about a part of it, or is a part of it?", name),
			topicRelatedWhenTrue, topicRelatedWhenFalse)
		q["same_"+name] = jev.NoulWithCriteria(
			fmt.Sprintf("In the pair whose id is %q, do the labels of topic a and topic b name one subject in different words?", name),
			topicSameWhenTrue, topicSameWhenFalse)
	}
	return q
}

// goldenTopicProfile builds a profile with what the judge shows of a topic.
func goldenTopicProfile(name, aliases string, meetings int, quote string, summaries ...string) *TopicProfile {
	p := profileNamed(name, meetings)
	if aliases != "" {
		p.Node.Properties = map[string]string{graph.PropAliases: aliases}
	}
	p.Summaries = summaries
	p.Quote = quote
	return p
}

// goldenTopicPairs are three pairs covering what a card can carry: other
// wordings, summaries long enough to clip and spaced oddly, a quote, a topic
// shared by two pairs (one card), and the co-occurrence counts.
func goldenTopicPairs() []*TopicPair {
	long := "Imran argued   that current models lack\nrecursive self-improvement, " +
		strings.Repeat("and the group went back and forth on whether benchmarks measure generality ", 5) + "end."
	agi := goldenTopicProfile("AGI feasibility debate", "AGI debate, feasibility of AGI", 3,
		"  We treat AGI claims as\tmarketing until a model retrains itself.  ",
		long, "Mona asked whether AGI is a useful word at all.")
	alignment := goldenTopicProfile("AI alignment risk", "", 1, "", "Superintelligence risk and who should own alignment work.")
	rsi := goldenTopicProfile("recursive self improvement", "", 2, "")
	sticky := goldenTopicProfile("sticky board redesign", "sticky board v2", 2, strings.Repeat("Ship the new sticky board layout behind a flag ", 6),
		"The sticky board gets swimlanes.")
	hitl := goldenTopicProfile("HITL vs sticky notes", "", 1, "", "Whether human review replaces sticky notes.")
	return []*TopicPair{
		{A: agi, B: alignment, SharedMeetings: 1, Adjacent: 2},
		{A: agi, B: rsi},
		{A: sticky, B: hitl, SharedMeetings: 2, Adjacent: 1},
	}
}

// wantTopicCard is a topic card as the state holds it.
func wantTopicCard(name string, extra map[string]any) map[string]any {
	card := map[string]any{"id": topicID(name), "label": name}
	for k, v := range extra {
		card[k] = v
	}
	return card
}

func TestTopicJudgeRequestGolden(t *testing.T) {
	answers := map[string]float64{
		"related_p0": 0.81, "same_p0": 0.12,
		"related_p1": 0.42, "same_p1": 0.03,
		"related_p2": 0.65, "same_p2": 0.3,
	}
	srv := newJevGoldenServer(t, func(_ *jevGoldenRequest, name string) (jev.Answer, bool) {
		return noulAnswer(answers[name]), false
	})
	pairs := goldenTopicPairs()
	verdicts, report, err := NewTopicJudge(srv.client()).Judge(context.Background(), pairs)
	if err != nil {
		t.Fatal(err)
	}

	bodies := srv.requests()
	if len(bodies) != 1 {
		t.Fatalf("%d requests, want the three pairs in one", len(bodies))
	}
	checkJevRequests(t, "topic_judge", bodies)

	longSaid := "Imran argued that current models lack recursive self-improvement, " +
		strings.TrimSpace(strings.Repeat("and the group went back and forth on whether benchmarks measure generality ", 5))
	longSaid = string([]rune(longSaid)[:maxSummaryChars-1]) + "…"
	wantState := map[string]any{
		"topics": []any{
			wantTopicCard("AGI feasibility debate", map[string]any{
				"other_wordings_meetings_used": []string{"AGI debate", "feasibility of AGI"},
				"meetings_filed_under":         3,
				"what_meetings_filed_under_it_said": []string{
					longSaid,
					"Mona asked whether AGI is a useful word at all.",
				},
				"verbatim_quote_from_a_decision_filed_under_it": "We treat AGI claims as marketing until a model retrains itself.",
			}),
			wantTopicCard("AI alignment risk", map[string]any{
				"meetings_filed_under":              1,
				"what_meetings_filed_under_it_said": []string{"Superintelligence risk and who should own alignment work."},
			}),
			wantTopicCard("recursive self improvement", map[string]any{"meetings_filed_under": 2}),
			wantTopicCard("sticky board redesign", map[string]any{
				"other_wordings_meetings_used":                  []string{"sticky board v2"},
				"meetings_filed_under":                          2,
				"what_meetings_filed_under_it_said":             []string{"The sticky board gets swimlanes."},
				"verbatim_quote_from_a_decision_filed_under_it": string([]rune(strings.TrimSpace(strings.Repeat("Ship the new sticky board layout behind a flag ", 6)))[:maxQuoteChars-1]) + "…",
			}),
			wantTopicCard("HITL vs sticky notes", map[string]any{
				"meetings_filed_under":              1,
				"what_meetings_filed_under_it_said": []string{"Whether human review replaces sticky notes."},
			}),
		},
		"pairs": []any{
			map[string]any{"pair": "p0", "a": topicID("AGI feasibility debate"), "b": topicID("AI alignment risk"),
				"meetings_filed_under_both": 1, "times_consecutive_segments_of_one_meeting": 2},
			map[string]any{"pair": "p1", "a": topicID("AGI feasibility debate"), "b": topicID("recursive self improvement"),
				"meetings_filed_under_both": 0, "times_consecutive_segments_of_one_meeting": 0},
			map[string]any{"pair": "p2", "a": topicID("sticky board redesign"), "b": topicID("HITL vs sticky notes"),
				"meetings_filed_under_both": 2, "times_consecutive_segments_of_one_meeting": 1},
		},
	}
	wantJevRequest(t, "topic judge", bodies[0], wantState, wantTopicQuestions(3))

	if len(verdicts) != 3 {
		t.Fatalf("%d verdicts", len(verdicts))
	}
	for i, want := range [][2]float64{{0.81, 0.12}, {0.42, 0.03}, {0.65, 0.3}} {
		if v := verdicts[i]; v.Pair != pairs[i] || v.Related != want[0] || v.Same != want[1] {
			t.Errorf("verdict %d = %+v, want related %v same %v for its own pair", i, v, want[0], want[1])
		}
	}
	if report.Requests != 1 || report.InputTokens != 600 || report.Model != jev.DefaultModel {
		t.Errorf("report = %+v", report)
	}
}

// TestTopicJudgeBatchRequestsGolden pins where batches split: eleven pairs
// go as ten and one, each batch naming its pairs from p0.
func TestTopicJudgeBatchRequestsGolden(t *testing.T) {
	srv := newJevGoldenServer(t, func(_ *jevGoldenRequest, name string) (jev.Answer, bool) {
		return noulAnswer(0.5), false
	})
	var pairs []*TopicPair
	for i := range 11 {
		pairs = append(pairs, &TopicPair{
			A: profileNamed(fmt.Sprintf("topic %02d", i), 1),
			B: profileNamed(fmt.Sprintf("other %02d", i), 2),
		})
	}
	verdicts, report, err := NewTopicJudge(srv.client()).Judge(context.Background(), pairs)
	if err != nil {
		t.Fatal(err)
	}
	bodies := srv.requests()
	if len(bodies) != 2 || len(verdicts) != 11 || report.Requests != 2 {
		t.Fatalf("%d requests, %d verdicts, report %+v; want 2, 11", len(bodies), len(verdicts), report)
	}
	checkJevRequests(t, "topic_judge_batches", bodies)
	wantJevRequest(t, "second batch", bodies[1], map[string]any{
		"topics": []any{
			wantTopicCard("topic 10", map[string]any{"meetings_filed_under": 1}),
			wantTopicCard("other 10", map[string]any{"meetings_filed_under": 2}),
		},
		"pairs": []any{map[string]any{"pair": "p0", "a": topicID("topic 10"), "b": topicID("other 10"),
			"meetings_filed_under_both": 0, "times_consecutive_segments_of_one_meeting": 0}},
	}, wantTopicQuestions(1))
	if got := len(srv.decoded(0).Questions); got != 20 {
		t.Errorf("first batch asked %d questions, want 20", got)
	}
}

// TestTopicJudgeHalvingRequestsGolden pins the requests after the service
// refuses a batch as too large: the whole batch, then its halves.
func TestTopicJudgeHalvingRequestsGolden(t *testing.T) {
	var mu sync.Mutex
	refused := false
	srv := newJevGoldenServer(t, func(req *jevGoldenRequest, name string) (jev.Answer, bool) {
		mu.Lock()
		defer mu.Unlock()
		if !refused && len(req.Questions) == 6 {
			refused = true
			return jev.Answer{}, true
		}
		return noulAnswer(0.7), false
	})
	verdicts, report, err := NewTopicJudge(srv.client()).Judge(context.Background(), goldenTopicPairs())
	if err != nil {
		t.Fatal(err)
	}
	bodies := srv.requests()
	if len(bodies) != 3 || len(verdicts) != 3 || report.Requests != 2 {
		t.Fatalf("%d requests, %d verdicts, report %+v; want 3 requests (one refused) and 3 verdicts", len(bodies), len(verdicts), report)
	}
	checkJevRequests(t, "topic_judge_halving", bodies)
	for i, want := range []int{6, 2, 4} {
		if got := len(srv.decoded(i).Questions); got != want {
			t.Errorf("request %d asked %d questions, want %d", i, got, want)
		}
	}
	wantJevRequest(t, "second half", bodies[2], nil, wantTopicQuestions(2))
}

// --- search reranker ------------------------------------------------------

const (
	rerankWhenTrue  = "its passages discuss the subject the question asks about, at length or as a decision or follow-up"
	rerankWhenFalse = "the question's words occur only in passing, in another sense, or in passages about something else"
)

func wantRerankQuestions(n int) jev.Questions {
	q := jev.Questions{}
	for i := range n {
		q[fmt.Sprintf("c%d", i)] = jev.NoulWithCriteria(
			fmt.Sprintf("Does the candidate meeting whose index is %d answer the question in `question`?", i),
			rerankWhenTrue, rerankWhenFalse)
	}
	return q
}

// TestRerankRequestGolden drives the path `meetings search` takes: the hits
// FindMeetings returns for a question, reordered by the decision model.
func TestRerankRequestGolden(t *testing.T) {
	store := searchFixture(t)
	found, err := FindMeetings(context.Background(), store, Query{Text: "AGI migration"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Hits) != 2 {
		t.Fatalf("%d hits, want both fixture meetings", len(found.Hits))
	}
	before := []string{found.Hits[0].Meeting.Name, found.Hits[1].Meeting.Name}

	srv := newJevGoldenServer(t, func(req *jevGoldenRequest, name string) (jev.Answer, bool) {
		// The second candidate in word-match order is the one that answers.
		if name == "c1" {
			return noulAnswer(0.92), false
		}
		return noulAnswer(0.18), false
	})
	rep, err := NewReranker(srv.client()).Rerank(context.Background(), "AGI migration", found.Hits)
	if err != nil {
		t.Fatal(err)
	}
	bodies := srv.requests()
	if len(bodies) != 1 {
		t.Fatalf("%d requests, want 1", len(bodies))
	}
	checkJevRequests(t, "rerank", bodies)

	// Word matching ties the two, so they arrive in the order the meetings
	// were considered.
	wantState := map[string]any{
		"question": "AGI migration",
		"candidate_meetings": []any{
			map[string]any{
				"index": 0, "date": "2026-01-01", "title": "AGI feasibility debate with the platform team",
				"participants_identified": []string{"Imran Yousuf"},
				"passages_containing_question_words": []string{
					"[topic segment AGI feasibility] Imran argued models lack self-improvement, so this is not AGI.",
					"[decision] Treat AGI claims as marketing until a model retrains itself",
					"[title] AGI feasibility debate with the platform team",
					"[topic label] AGI feasibility",
				},
			},
			map[string]any{
				"index": 1, "date": "2026-01-01", "title": "Schema migration status",
				"participants_identified": []string{"Imran Yousuf", "Kevin", "Mona"},
				"passages_containing_question_words": []string{
					"[topic segment schema migration] The migration ran overnight and was verified.",
					"[decision] Ship once the client is updated",
					"[title] Schema migration status",
					"[meeting summary] The team confirmed the schema migration completed and agreed to ship once the client is updated.",
				},
			},
		},
	}
	wantJevRequest(t, "rerank", bodies[0], wantState, wantRerankQuestions(2))

	after := []string{found.Hits[0].Meeting.Name, found.Hits[1].Meeting.Name}
	if after[0] != before[1] || after[1] != before[0] {
		t.Errorf("order after rerank = %v, want the judged one first (was %v)", after, before)
	}
	if !found.Hits[0].Judged || found.Hits[0].Relevance != 0.92 || found.Hits[1].Relevance != 0.18 {
		t.Errorf("relevance = %v, %v", found.Hits[0].Relevance, found.Hits[1].Relevance)
	}
	if rep.Judged != 2 || rep.InputTokens != 200 || rep.Model != jev.DefaultModel {
		t.Errorf("report = %+v", rep)
	}
}

// TestRerankHalvingRequestsGolden pins what follows a refusal: the
// candidates halved from the tail, renamed from c0 again.
func TestRerankHalvingRequestsGolden(t *testing.T) {
	var mu sync.Mutex
	refused := false
	srv := newJevGoldenServer(t, func(req *jevGoldenRequest, name string) (jev.Answer, bool) {
		mu.Lock()
		defer mu.Unlock()
		if !refused {
			refused = true
			return jev.Answer{}, true
		}
		return noulAnswer(0.5), false
	})
	hits := rerankHits("one", "two", "three", "four", "five")
	rep, err := NewReranker(srv.client()).Rerank(context.Background(), "the question", hits)
	if err != nil {
		t.Fatal(err)
	}
	bodies := srv.requests()
	if len(bodies) != 2 || rep.Judged != 2 {
		t.Fatalf("%d requests, %d judged; want the refused one and a half", len(bodies), rep.Judged)
	}
	checkJevRequests(t, "rerank_halving", bodies)
	wantJevRequest(t, "after halving", bodies[1], map[string]any{
		"question": "the question",
		"candidate_meetings": []any{
			map[string]any{"index": 0, "date": "2026-01-01", "title": "one", "participants_identified": []string{"Kevin"},
				"passages_containing_question_words": []string{"[topic segment seg] what was said in one", "[title] one"}},
			map[string]any{"index": 1, "date": "2026-01-02", "title": "two", "participants_identified": []string{"Kevin"},
				"passages_containing_question_words": []string{"[topic segment seg] what was said in two", "[title] two"}},
		},
	}, wantRerankQuestions(2))
}

// --- speaker adjudication -------------------------------------------------

// recordingJudge records every call to Decide exactly as made and answers
// from a table keyed by the label each question names.
type recordingJudge struct {
	verdictFor map[string]decide.Verdict

	mu    sync.Mutex
	calls []recordedDecide
}

type recordedDecide struct {
	State     any                        `json:"state"`
	Questions map[string]decide.Question `json:"questions"`
}

func (j *recordingJudge) Name() string { return "recording" }

func (j *recordingJudge) Decide(_ context.Context, state any, questions map[string]decide.Question) (map[string]decide.Verdict, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.calls = append(j.calls, recordedDecide{State: state, Questions: questions})
	out := make(map[string]decide.Verdict, len(questions))
	for key, q := range questions {
		if v, ok := j.verdictFor[labelFromAsk(q.Ask)]; ok {
			out[key] = v
			continue
		}
		out[key] = decide.Verdict{Choice: unresolvedOption, Confidence: 0.99}
	}
	return out, nil
}

// adjudicatedSession has the host, two people the host names, and a voice
// nobody answers, so one Identify both screens for background audio and
// adjudicates the people.
//
// Every label speaks for a different length of time. Evidence orders
// speakers by speaking time alone, over a map, so two labels that tie are
// numbered speaker_0 and speaker_1 in either order from one run to the next.
func adjudicatedSession() *Session {
	s := build(
		[3]string{"You", SourceMic, "Morning. Shall we start with the retention job?"},
		[3]string{"Person 1", SourceMonitor, "I looked at it yesterday, the query does a full scan."},
		[3]string{"You", SourceMic, "Right. Kevin, did the composite index get dropped?"},
		[3]string{"Person 1", SourceMonitor, "It did. I can put a migration up today."},
		[3]string{"Person 2", SourceMonitor, "Before we move on, I want to flag the alerting gap."},
		[3]string{"You", SourceMic, "Go ahead, Mona."},
		[3]string{"Person 2", SourceMonitor, "We only hear about it when a customer tells us."},
		[3]string{"Person 7", SourceMonitor, "Are you familiar with this chili? The scorpion pepper."},
		[3]string{"Person 7", SourceMonitor, "It has a fruity heat that builds at the back of the throat."},
		[3]string{"Person 7", SourceMonitor, "Now this next sauce is the one that people fear the most."},
	)
	s.Title = "Retention sync"
	// You 27s, Person 7 24s, Person 1 18s, Person 2 16s.
	for i := range s.Segments {
		switch {
		case s.Segments[i].Speaker == "Person 7":
			s.Segments[i].EndTime = s.Segments[i].StartTime + 8
		case i == 6:
			s.Segments[i].EndTime = s.Segments[i].StartTime + 7
		}
	}
	return s
}

// The literal options of the background screen.
var wantBackgroundOptions = map[string]string{
	"participant":      "A person taking part in the meeting",
	"background_media": "Not a person in the meeting at all — a television, a video being demonstrated, a podcast, an advertisement, a sports or news broadcast, or a conversation elsewhere that the microphone caught",
	"mixed":            "Both: this label holds some meeting speech and some background audio that the recorder filed together",
	"cannot_tell":      "There is not enough here to tell",
}

const wantCandidateEvidenceNote = "Produced by a pattern matcher reading direct address, self-introduction, thanks and hand-offs, and resolved against the order the turns actually occurred in. Where a quote and the direction given for it fit that order, this is reliable — more so than re-reading a long transcript to work out who answered whom. It misses people who are never addressed by name, and it errs in four ways worth rejecting: a product, model or company name read as a person; a voice from a recording, video or television playing in the room; a name said in negation or doubt (\"I'm not sure if this is X\"); and a common word the transcriber capitalised. Judge the candidate on whether it names a participant in this meeting, not on whether the matcher could in principle be wrong."

// TestSpeakerJudgeCallsGolden pins, at the decide.Judge seam, every call
// speaker identification makes: the background screen, then adjudication.
// internal/decide pins the Jev bytes such calls become.
func TestSpeakerJudgeCallsGolden(t *testing.T) {
	judge := &recordingJudge{verdictFor: map[string]decide.Verdict{
		"Person 7": {Choice: optionBackground, Confidence: 0.93},
		"Person 1": {Choice: "Kevin Mitchell", Confidence: 0.97, RunnerUp: unresolvedOption, RunnerUpShare: 0.02},
	}}
	a := NewAnalyzer(nil, Options{
		Owner: "Imran Yousuf", OwnerAliases: []string{"Imron"},
		Roster:        []string{"Kevin Mitchell", "Mona Patel", "Priya Shah"},
		KnownPeople:   func() []string { return []string{"Saki Tanaka"} },
		MinConfidence: 0.7,
	}).WithJudge(judge)
	s := adjudicatedSession()
	identities, err := a.Identify(context.Background(), s, &Usage{})
	if err != nil {
		t.Fatal(err)
	}
	if len(judge.calls) != 2 {
		t.Fatalf("%d judge calls, want the screen and the adjudication", len(judge.calls))
	}
	checkJevGolden(t, "speaker_judge_calls.json", jevGoldenJSON(t, judge.calls))
	type outcome struct {
		Label, Name, Evidence, Method string
		Confidence                    float64
	}
	var outcomes []outcome
	for _, id := range identities {
		outcomes = append(outcomes, outcome{id.Label, id.Name, id.Evidence, id.Method, id.Confidence})
	}
	checkJevGolden(t, "speaker_identities.json", jevGoldenJSON(t, outcomes))
	wantOutcomes := []outcome{
		{"You", "Imran Yousuf", "microphone audio is the recording owner", MethodOwnerAnchor, 1},
		{"Person 7", "", "Are you familiar with this chili? The scorpion pepper.", MethodBackground, 0.93},
		{"Person 1", "Kevin Mitchell", "Kevin, did the composite index get dropped?", MethodJudge, 0.97},
		{"Person 2", "", "", MethodJudge, 0},
	}
	if g, w := fmt.Sprint(outcomes), fmt.Sprint(wantOutcomes); g != w {
		t.Errorf("identities = %s\nwant         %s", g, w)
	}

	// The background screen.
	screen := judge.calls[0]
	wantScreenQuestions := map[string]decide.Question{
		"voice_0": {
			Ask:     `Was the voice labelled "Person 7" a person taking part in this meeting, or something else the microphone picked up?`,
			Options: wantBackgroundOptions,
		},
	}
	wantScreenState := map[string]any{
		"situation":        "A meeting was recorded. The recorder separated the voices it heard, but some of what it heard may not be people in the meeting at all — a television, a video being demonstrated on a shared screen, a stream left running nearby.",
		"meeting":          "Retention sync",
		"duration_minutes": "2",
		"voices": map[string]any{
			"Person 7": map[string]any{
				"speaking_seconds":            "24",
				"utterances":                  3,
				"first_spoke_at":              "01:10",
				"last_spoke_at":               "01:38",
				"share_of_its_own_span":       "0.86",
				"nobody_replied_to_it":        true,
				"it_addressed_nobody":         true,
				"voices_it_traded_turns_with": 1,
				"said": []string{
					"Are you familiar with this chili? The scorpion pepper.",
					"It has a fruity heat that builds at the back of the throat.",
					"Now this next sauce is the one that people fear the most.",
				},
			},
		},
		"note": "Every voice here already spoke enough to look like a participant by length alone, and none of them was addressed by name or answered. Judge by what was said.",
	}
	wantDecideCall(t, "screen", screen, wantScreenState, wantScreenQuestions)

	// The adjudication, with Person 7 already set aside.
	names := []string{"Kevin Mitchell", "Mona Patel", "Priya Shah", "Saki Tanaka"}
	wantAdjudicateQuestions := map[string]decide.Question{
		"speaker_0": {
			Ask:     `Which person is the speaker labelled "Person 1"? Answer "unresolved" unless the transcript itself establishes who they are.`,
			Options: identityOptionsFor("Person 1", names),
		},
		"speaker_1": {
			Ask:     `Which person is the speaker labelled "Person 2"? Answer "unresolved" unless the transcript itself establishes who they are.`,
			Options: identityOptionsFor("Person 2", names),
		},
	}
	wantAdjudicateState := map[string]any{
		"meeting":          "Retention sync",
		"date":             "2026-01-01 09:00",
		"transcript":       s.Transcript(),
		"host":             `The speaker labelled "You" is Imran Yousuf, who made the recording. No other speaker is Imran Yousuf.`,
		"known_colleagues": "Imran Yousuf, Kevin Mitchell, Mona Patel, Priya Shah, Saki Tanaka",
		// "Go ahead, Mona." matches no hint pattern, so only Kevin is named.
		"names_mentioned":         "Kevin",
		"candidate_evidence_note": wantCandidateEvidenceNote,
		"candidate_evidence": map[string]any{
			"Person 1": "Kevin (strength 2.0) — addressed by this name, and answered in the turn that follows: \"Kevin, did the composite index get dropped?\"",
		},
	}
	wantDecideCall(t, "adjudicate", judge.calls[1], wantAdjudicateState, wantAdjudicateQuestions)
	if !strings.Contains(s.Transcript(), "[00:00] You: Morning.") {
		t.Errorf("transcript rendering changed: %q", s.Transcript())
	}
}

// identityOptionsFor spells out the options one speaker is chosen between.
func identityOptionsFor(label string, names []string) map[string]string {
	out := map[string]string{"unresolved": fmt.Sprintf("The transcript does not establish who %s is", label)}
	for _, n := range names {
		out[n] = fmt.Sprintf("The transcript establishes that %s is %s", label, n)
	}
	return out
}

// wantDecideCall asserts one recorded call field by field.
func wantDecideCall(t *testing.T, label string, got recordedDecide, wantState any, wantQuestions map[string]decide.Question) {
	t.Helper()
	if g, w := string(jevGoldenJSON(t, got.State)), string(jevGoldenJSON(t, wantState)); g != w {
		t.Errorf("%s: state differs:\n%s", label, jevGoldenFirstDiff(w, g))
	}
	if g, w := string(jevGoldenJSON(t, got.Questions)), string(jevGoldenJSON(t, wantQuestions)); g != w {
		t.Errorf("%s: questions differ:\n%s", label, jevGoldenFirstDiff(w, g))
	}
}

// --- topic relater ----------------------------------------------------------

// relaterVectors places labels so nearest-label proposals are predictable.
func relaterVectors(id string) ([]float32, bool) {
	switch id {
	case topicID("schema migration"):
		return []float32{1, 0, 0}, true
	case topicID("client rollout"):
		return []float32{0, 1, 0}, true
	case topicID("release notes"):
		return []float32{0.9, 0.1, 0}, true
	case topicID("incident review"):
		return []float32{0, 0.2, 0.9}, true
	case topicID("on-call rota"):
		return []float32{0.1, 0, 1}, true
	}
	return nil, false
}

// relaterAnswerer answers from the labels a pair names, so the answer does
// not depend on which batch or position the pair landed in.
func relaterAnswerer(t *testing.T) jevGoldenAnswerer {
	related := map[string]float64{
		labelsKey("schema migration", "client rollout"): 0.9,
		labelsKey("schema migration", "release notes"):  0.75,
		labelsKey("incident review", "on-call rota"):    0.62,
	}
	return func(req *jevGoldenRequest, name string) (jev.Answer, bool) {
		var st relateState
		if err := json.Unmarshal(req.State, &st); err != nil {
			t.Error(err)
		}
		labelOf := make(map[string]string)
		for _, c := range st.Topics {
			labelOf[c.ID] = c.Label
		}
		for _, p := range st.Pairs {
			key := labelsKey(labelOf[p.A], labelOf[p.B])
			switch name {
			case relatedQuestion + p.Pair:
				if r, ok := related[key]; ok {
					return noulAnswer(r), false
				}
				return noulAnswer(0.2), false
			case sameQuestion + p.Pair:
				return noulAnswer(0.05), false
			}
		}
		t.Errorf("question %q names no pair", name)
		return noulAnswer(0), false
	}
}

// relaterPairs lists the pairs a recorded request asks about, by label.
func relaterPairs(t *testing.T, body []byte) string {
	t.Helper()
	var req struct {
		State relateState `json:"state"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	labelOf := make(map[string]string)
	for _, c := range req.State.Topics {
		labelOf[c.ID] = c.Label
	}
	var out []string
	for _, p := range req.State.Pairs {
		out = append(out, labelOf[p.A]+" | "+labelOf[p.B])
	}
	return strings.Join(out, ", ")
}

// relatedEdges lists every RelatedTo edge, by id, with its properties.
func relatedEdges(t *testing.T, store graph.Store) []map[string]any {
	t.Helper()
	ctx := context.Background()
	seen := make(map[string]bool)
	var out []map[string]any
	topics, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeTopic})
	if err != nil {
		t.Fatal(err)
	}
	name := make(map[string]string)
	for _, n := range topics {
		name[n.ID] = n.Name
	}
	for _, n := range topics {
		edges, err := store.GetEdges(ctx, n.ID, graph.EdgeRelatedTo)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range edges {
			if seen[e.ID] {
				continue
			}
			seen[e.ID] = true
			out = append(out, map[string]any{"id": e.ID, "source": name[e.SourceID], "target": name[e.TargetID], "properties": e.Properties})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["id"].(string) < out[j]["id"].(string) })
	return out
}

// TestTopicRelaterRequestsGolden pins the requests relating sends from both
// places it runs: as each meeting is written, and in a bulk pass over a
// corpus written before relating existed, where batches go out
// concurrently. It pins the edges written as well, since search reads them.
func TestTopicRelaterRequestsGolden(t *testing.T) {
	ctx := context.Background()
	meetings := []*Result{
		meetingFiledUnder("/tmp/rel/1/session.json", "session-1", "Migration status", "schema migration", "client rollout"),
		meetingFiledUnder("/tmp/rel/2/session.json", "session-2", "Release planning", "client rollout", "release notes"),
		meetingFiledUnder("/tmp/rel/3/session.json", "session-3", "Ops review", "schema migration", "release notes", "incident review", "on-call rota", "capacity forecast"),
	}

	t.Run("as each meeting is written", func(t *testing.T) {
		store := testStore(t)
		people, err := LoadPersonRegistry(ctx, store)
		if err != nil {
			t.Fatal(err)
		}
		srv := newJevGoldenServer(t, relaterAnswerer(t))
		relater := NewTopicRelater(store, srv.client(), RelaterOptions{NearestK: 1, Vectors: relaterVectors})
		w := NewWriter(store, people, WriterOptions{MinConfidence: 0.7, Owner: "Imran Yousuf"}).WithRelater(relater)
		for _, m := range meetings {
			if _, err := w.Write(ctx, m); err != nil {
				t.Fatal(err)
			}
		}
		bodies := srv.requests()
		if len(bodies) != 3 {
			t.Fatalf("%d requests, want one per meeting", len(bodies))
		}
		checkJevRequests(t, "topic_relater_incremental", bodies)
		checkJevGolden(t, "topic_relater_incremental_edges.json", jevGoldenJSON(t, relatedEdges(t, store)))
		// Pairs go in key order; a pair judged for an earlier meeting is not
		// asked again, and a topic without a vector proposes no neighbour.
		for i, want := range []string{
			"schema migration | client rollout",
			"schema migration | release notes, client rollout | release notes",
			"on-call rota | schema migration, on-call rota | capacity forecast, on-call rota | release notes, " +
				"on-call rota | incident review, schema migration | capacity forecast, schema migration | incident review, " +
				"capacity forecast | release notes, capacity forecast | incident review, release notes | incident review",
		} {
			if got := relaterPairs(t, bodies[i]); got != want {
				t.Errorf("meeting %d asked about %s\nwant %s", i+1, got, want)
			}
			if got := len(srv.decoded(i).Questions); got != 2*(strings.Count(want, ",")+1) {
				t.Errorf("meeting %d asked %d questions", i+1, got)
			}
		}
	})

	t.Run("in a bulk pass", func(t *testing.T) {
		store := testStore(t)
		people, err := LoadPersonRegistry(ctx, store)
		if err != nil {
			t.Fatal(err)
		}
		w := NewWriter(store, people, WriterOptions{MinConfidence: 0.7, Owner: "Imran Yousuf"})
		for _, m := range meetings {
			if _, err := w.Write(ctx, m); err != nil {
				t.Fatal(err)
			}
		}
		srv := newJevGoldenServer(t, relaterAnswerer(t))
		relater := NewTopicRelater(store, srv.client(), RelaterOptions{NearestK: 1, Vectors: relaterVectors, Concurrency: 4})
		st, err := relater.RelateAll(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		bodies := srv.requests()
		if len(bodies) != 2 || st.Pairs != 13 || st.Related != 3 {
			t.Fatalf("%d requests, stats %+v; want 13 pairs in two batches, 3 related", len(bodies), st)
		}
		sort.Slice(bodies, func(i, j int) bool { return bytes.Compare(bodies[i], bodies[j]) < 0 })
		checkJevRequests(t, "topic_relater_bulk", bodies)
		checkJevGolden(t, "topic_relater_bulk_edges.json", jevGoldenJSON(t, relatedEdges(t, store)))
		// Ten pairs to a batch, in key order.
		for i, want := range []string{
			"on-call rota | schema migration, on-call rota | capacity forecast, on-call rota | release notes, " +
				"on-call rota | incident review, schema migration | capacity forecast, schema migration | client rollout, " +
				"schema migration | release notes, schema migration | incident review, capacity forecast | release notes, " +
				"capacity forecast | incident review",
			"client rollout | release notes, client rollout | incident review, release notes | incident review",
		} {
			if got := relaterPairs(t, bodies[i]); got != want {
				t.Errorf("batch %d asked about %s\nwant %s", i, got, want)
			}
		}
	})
}
