package relations

// Characterization tests for what this package sends to the decision model.
//
// The document-pair judge is about to move out of this package and call the
// decision model through shared plumbing, and none of that may change a byte
// the service receives: the model reads the state as text, so even a
// reordered key is a different question. Each test here drives the real code
// against a Jev endpoint on localhost and pins every request body two ways,
// under testdata/golden:
//
//   - <name>.raw.jsonl holds each body exactly as sent, one per line, so a
//     change of key order or escaping fails;
//   - <name>.json holds the same bodies decoded and re-encoded with sorted
//     keys and indentation, so a failure shows which field moved.
//
// Requests sent concurrently are ordered by their bytes. Regenerate both,
// only when a change is intended, with:
//
//	UPDATE_GOLDEN=1 go test -race -count=1 -run Golden ./internal/relations/
//
// The goldens can be regenerated carelessly, so each test also states the
// questions and the state it expects in code, built with the jev
// constructors so that a noul and a noul with criteria are told apart.

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

	"github.com/imyousuf/CodeEagle/pkg/jev"
)

// jevGoldenUpdate rewrites golden files rather than comparing against them.
var jevGoldenUpdate = os.Getenv("UPDATE_GOLDEN") != ""

// jevGoldenRequest is a request body as it arrives on the wire.
type jevGoldenRequest struct {
	Model     string                     `json:"model"`
	State     json.RawMessage            `json:"state"`
	Questions map[string]json.RawMessage `json:"questions"`
}

// jevGoldenServer is a Jev endpoint that records every request body as sent
// and answers each noul from a script.
type jevGoldenServer struct {
	t      *testing.T
	srv    *httptest.Server
	answer func(req *jevGoldenRequest, name string) float64

	mu     sync.Mutex
	bodies [][]byte
}

func newJevGoldenServer(t *testing.T, answer func(req *jevGoldenRequest, name string) float64) *jevGoldenServer {
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
	answers := make(jev.Answers, len(req.Questions))
	for name := range req.Questions {
		answers[name] = jev.Answer{Type: jev.TypeNoul, Noul: s.answer(&req, name)}
	}
	reply, err := json.Marshal(jev.Response{
		Model:   jev.DefaultModel,
		Answers: answers,
		Usage:   jev.Usage{InputTokens: 100 * len(answers), OutputTokens: 3},
	})
	if err != nil {
		s.t.Errorf("encode reply: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
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

// sortedRequests returns the bodies ordered by their bytes, for requests
// sent concurrently.
func (s *jevGoldenServer) sortedRequests() [][]byte {
	out := s.requests()
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i], out[j]) < 0 })
	return out
}

// stateString reads one string field of a request's state, for scripting.
func (r *jevGoldenRequest) stateString(path ...string) string {
	var v any
	if err := json.Unmarshal(r.State, &v); err != nil {
		return ""
	}
	for _, p := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return ""
		}
		v = m[p]
	}
	s, _ := v.(string)
	return s
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
	return jevGoldenEncode(t, v)
}

func jevGoldenEncode(t *testing.T, v any) string {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
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
	return []byte(jevGoldenCanonical(t, raw))
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
	checkJevGolden(t, name+".json", []byte(jevGoldenEncode(t, decoded)))
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
		if got, want := jevGoldenCanonical(t, req.State), string(jevGoldenJSON(t, wantState)); got != want {
			t.Errorf("%s: state differs:\n%s", label, jevGoldenFirstDiff(want, got))
		}
	}
	if got, want := jevGoldenCanonical(t, req.Questions), string(jevGoldenJSON(t, wantQuestions)); got != want {
		t.Errorf("%s: questions differ:\n%s", label, jevGoldenFirstDiff(want, got))
	}
}

// --- document <-> document judge ------------------------------------------

// wantDocQuestions are the seven questions asked about every pair.
func wantDocQuestions() jev.Questions {
	about := "the work, design or decision"
	q := jev.Questions{
		"related": jev.NoulWithCriteria(
			"Are document A and document B about the same piece of work, so that a reader of one would want to know about the other?",
			"they concern the same feature, system, plan, incident or decision: one builds on, revises, records or accompanies the other",
			"they share vocabulary or a broad area only; a reader of one would gain nothing from the other",
		),
	}
	for _, d := range []struct{ name, x, y string }{{"ab", "A", "B"}, {"ba", "B", "A"}} {
		q["changes_"+d.name] = jev.NoulWithCriteria(
			"Does document "+d.x+" change part of what document "+d.y+" specifies?",
			d.x+" revises, renames, re-homes or overrides some of "+about+" in "+d.y+", so that part of "+d.y+" is out of date while the rest may still hold",
			d.x+" leaves what "+d.y+" specifies as it is: it adds to it, depends on it, records something about it, or is about something else",
		)
		q["replaces_"+d.name] = jev.NoulWithCriteria(
			"Does document "+d.x+" replace document "+d.y+" as a whole?",
			d.x+" is the current account of everything "+d.y+" covered, so "+d.y+" as a whole is no longer the account to follow",
			d.y+" still holds at least in part: "+d.x+" changes only some of it, adds to it, or is about something else",
		)
		q["records_"+d.name] = jev.NoulWithCriteria(
			"Is document "+d.x+" an account of something that happened involving what document "+d.y+" describes?",
			d.x+" reports an incident, outage, investigation, review, meeting, conversation or decision about the system, feature or plan that "+d.y+" describes",
			d.x+" is itself a design, plan or reference rather than an account of events, or the events it reports concern something else",
		)
	}
	return q
}

// docPairs are two candidates: a spec and the incident write-up that cites
// it, with the sections the pair rests on and a fact carrying characters
// JSON escapes; and two versions of a document, with no sections named, a
// long first section and more headings than a card shows.
func docPairs() []*DocCandidate {
	spec := &Subject{Path: "docs/specs/retry-budget.md", Title: "Retry budget design", Hash: "h-spec",
		Preamble: "**Status:** Draft, 2026-10-04\nOwner: platform team. Retries are budgeted per client.",
		Sections: []*Section{
			{Heading: "Goals", HeadingPath: "Retry budget design > Goals", Text: "Bound the load a retrying client adds during an outage."},
			{Heading: "Limits", HeadingPath: "Retry budget design > Limits", Text: "Clients retry at most three times.\n\nTracked in PLAT-412."},
			{Heading: "Rollout", HeadingPath: "Retry budget design > Rollout", Text: "Behind a flag, one region at a time."},
		}}
	incident := &Subject{Path: "docs/incidents/2026-10-20-retry-storm-postmortem.md", Title: "Retry storm", Hash: "h-incident",
		Sections: []*Section{
			{Heading: "Impact", HeadingPath: "Retry storm > Impact", Text: "Checkout failed for 40 minutes."},
			{Heading: "Root cause", HeadingPath: "Retry storm > Root cause", Text: "Clients ignored the retry budget and retried without jitter."},
			{Heading: "Timeline", HeadingPath: "Retry storm > Timeline", Text: "09:02 alerts fired. 09:42 mitigated."},
		}}
	billing := &Subject{Path: "docs/billing.md", Title: "Invoice rendering", Hash: "h-bill",
		Sections: []*Section{{Heading: "Templates", HeadingPath: "Invoice rendering > Templates", Text: "Invoices render from templates."}}}
	var v2Sections []*Section
	for i := range 16 {
		text := fmt.Sprintf("Section %d of the second version.", i+1)
		if i == 0 {
			text = strings.Repeat("Invoices now render from a layout engine instead of templates. ", 14)
		}
		v2Sections = append(v2Sections, &Section{Heading: fmt.Sprintf("Part %02d", i+1),
			HeadingPath: fmt.Sprintf("Invoice rendering v2 > Part %02d", i+1), Text: text})
	}
	billingV2 := &Subject{Path: "docs/billing-v2.md", Title: "Invoice rendering v2", Hash: "h-bill-v2", Sections: v2Sections}
	return []*DocCandidate{
		{A: spec, B: incident, Signals: []string{DocSignalRef, DocSignalNear},
			Facts:     []string{`docs/incidents/2026-10-20-retry-storm-postmortem.md names docs/specs/retry-budget.md: "the retry budget <spec> did not cover bursts & retries"`},
			SectionsA: []*Section{spec.Sections[1]}, SectionsB: []*Section{incident.Sections[1]}},
		{A: billing, B: billingV2, Signals: []string{DocSignalName},
			Facts: []string{"their file names are versions of one name"}},
	}
}

func TestDocJudgeRequestGolden(t *testing.T) {
	answers := map[string]map[string]float64{
		"docs/specs/retry-budget.md": {"related": 0.93, "changes_ab": 0.1, "changes_ba": 0.2,
			"replaces_ab": 0.01, "replaces_ba": 0.02, "records_ab": 0.05, "records_ba": 0.91},
		"docs/billing.md": {"related": 0.88, "changes_ab": 0.15, "changes_ba": 0.8,
			"replaces_ab": 0.05, "replaces_ba": 0.6, "records_ab": 0.02, "records_ba": 0.03},
	}
	srv := newJevGoldenServer(t, func(req *jevGoldenRequest, name string) float64 {
		return answers[req.stateString("document_a", "path")][name]
	})
	cands := docPairs()
	judgments, report, err := NewDocJudge(srv.client()).Judge(context.Background(), cands)
	if err != nil {
		t.Fatal(err)
	}
	bodies := srv.sortedRequests()
	if len(bodies) != 2 {
		t.Fatalf("%d requests, want one per pair", len(bodies))
	}
	checkJevRequests(t, "doc_judge", bodies)

	limits := "Clients retry at most three times. Tracked in PLAT-412."
	wantSpecPair := map[string]any{
		"document_a": map[string]any{
			"path": "docs/specs/retry-budget.md", "type_of_document": "spec", "title": "Retry budget design",
			"status_the_document_states": "Draft, 2026-10-04", "date_the_document_states": "2026-10-04",
			"headings_of_the_document": []string{"Retry budget design > Goals", "Retry budget design > Limits", "Retry budget design > Rollout"},
			"excerpts_of_the_document": []any{
				map[string]any{"section": "opening", "text": "**Status:** Draft, 2026-10-04 Owner: platform team. Retries are budgeted per client."},
				map[string]any{"section": "Retry budget design > Limits", "text": limits},
			},
		},
		"document_b": map[string]any{
			"path": "docs/incidents/2026-10-20-retry-storm-postmortem.md", "type_of_document": "incident", "title": "Retry storm",
			"headings_of_the_document": []string{"Retry storm > Impact", "Retry storm > Root cause", "Retry storm > Timeline"},
			"excerpts_of_the_document": []any{
				map[string]any{"section": "Retry storm > Root cause", "text": "Clients ignored the retry budget and retried without jitter."},
			},
		},
		"facts_about_the_pair": []string{`docs/incidents/2026-10-20-retry-storm-postmortem.md names docs/specs/retry-budget.md: "the retry budget <spec> did not cover bursts & retries"`},
	}
	var v2Outline []string
	for i := range 15 {
		v2Outline = append(v2Outline, fmt.Sprintf("Invoice rendering v2 > Part %02d", i+1))
	}
	long := strings.TrimSpace(strings.Repeat("Invoices now render from a layout engine instead of templates. ", 14))
	cut := strings.LastIndex(long[:maxCardExcerpt], " ")
	wantBillingPair := map[string]any{
		"document_a": map[string]any{
			"path": "docs/billing.md", "type_of_document": "other", "title": "Invoice rendering",
			"headings_of_the_document": []string{"Invoice rendering > Templates"},
			"excerpts_of_the_document": []any{map[string]any{"section": "Invoice rendering > Templates", "text": "Invoices render from templates."}},
		},
		"document_b": map[string]any{
			"path": "docs/billing-v2.md", "type_of_document": "other", "title": "Invoice rendering v2",
			"headings_of_the_document": v2Outline,
			"excerpts_of_the_document": []any{map[string]any{"section": "Invoice rendering v2 > Part 01", "text": long[:cut] + "…"}},
		},
		"facts_about_the_pair": []string{"their file names are versions of one name"},
	}
	// Sorted by bytes, the billing pair's body comes first.
	wantJevRequest(t, "billing pair", bodies[0], wantBillingPair, wantDocQuestions())
	wantJevRequest(t, "spec pair", bodies[1], wantSpecPair, wantDocQuestions())

	if len(judgments) != 2 || report.Requests != 2 || report.Failed != 0 || report.InputTokens != 1400 || report.Model != jev.DefaultModel {
		t.Fatalf("%d judgments, report %+v", len(judgments), report)
	}
	sort.Slice(judgments, func(i, j int) bool { return judgments[i].Candidate.A.Path < judgments[j].Candidate.A.Path })
	type outcome struct {
		Candidate *DocCandidate
		Verdict   DocVerdict
	}
	wantVerdicts := []outcome{
		{cands[1], DocVerdict{Other: "docs/billing-v2.md", PRelated: 0.88, PChanges: [2]float64{0.15, 0.8},
			PReplaces: [2]float64{0.05, 0.6}, PRecords: [2]float64{0.02, 0.03},
			Signals: []string{DocSignalName}, SigA: "h-bill", SigB: "h-bill-v2", Judge: jev.DefaultModel}},
		{cands[0], DocVerdict{Other: "docs/incidents/2026-10-20-retry-storm-postmortem.md", PRelated: 0.93,
			PChanges: [2]float64{0.1, 0.2}, PReplaces: [2]float64{0.01, 0.02}, PRecords: [2]float64{0.05, 0.91},
			Signals: []string{DocSignalRef, DocSignalNear}, SigA: "h-spec", SigB: "h-incident", Judge: jev.DefaultModel}},
	}
	for i, want := range wantVerdicts {
		got := judgments[i]
		if got.Candidate != want.Candidate {
			t.Errorf("judgment %d is for %s", i, got.Candidate.A.Path)
		}
		if g, w := fmt.Sprintf("%+v", got.Verdict), fmt.Sprintf("%+v", want.Verdict); g != w {
			t.Errorf("verdict %d = %s\nwant        %s", i, g, w)
		}
	}

	// The gates turn those into: the second version changes the first, and
	// the incident records the spec.
	var links []string
	for _, jd := range judgments {
		v := jd.Verdict
		v.Decide(jd.Candidate.A, jd.Candidate.B, DefaultDocGates())
		for _, l := range v.Links {
			links = append(links, fmt.Sprintf("%s:%s>%s@%v", l.Kind, l.From, l.To, l.P))
		}
	}
	if g, w := strings.Join(links, ", "), "changes:docs/billing-v2.md>docs/billing.md@0.8, records:docs/incidents/2026-10-20-retry-storm-postmortem.md>docs/specs/retry-budget.md@0.91"; g != w {
		t.Errorf("links = %s\nwant    %s", g, w)
	}
}

// --- document <-> meeting judge -------------------------------------------

func wantDiscussesQuestions(pairs int) jev.Questions {
	q := jev.Questions{}
	for i := range pairs {
		name := fmt.Sprintf("p%d", i)
		q["discusses_"+name] = jev.NoulWithCriteria(
			fmt.Sprintf("In the pair whose id is %q, do the meeting segments it lists discuss the document?", name),
			"the segments discuss the work, design or decision this document describes, or change, question or report on something in it; a person reading the document would want to know this was said",
			"the segments and the document share vocabulary or a broad area only; what was said would not inform anyone reading this document",
		)
	}
	return q
}

// meetingRecordings holds each meeting's turns by session.
type meetingRecordings map[string]recording

func (r meetingRecordings) Span(m *Meeting, start, end float64) ([]Turn, error) {
	return r[m.SessionID].Span(m, start, end)
}

// judgeChatter is talk about something else, long enough that the stretch
// the judge is shown cannot start at the opening of the segment.
var judgeChatter = strings.Repeat("We spent a while on the offsite logistics and the hotel booking. ", 6)

// judgedMeetings returns the corpus with a decision filed under the
// platform meeting's retry segment, and recordings in which the document
// comes up partway through a long stretch of talk.
func judgedMeetings() ([]*Subject, map[string]*Meeting, meetingRecordings) {
	subjects, meetings := corpus()
	meetings["s-plat"].Decisions = []*Decided{{Text: "Raise the retry cap to five", Quote: "make the retry cap five", Topic: "Retry cap"}}
	rec := meetingRecordings{
		"s-plat": {
			{Speaker: "Person 2", Text: judgeChatter, Start: 10, End: 15},
			{Speaker: "Person 3", Text: judgeChatter, Start: 15, End: 20},
			{Speaker: "Person 2", Text: judgeChatter, Start: 20, End: 25},
			{Speaker: "Person 3", Text: judgeChatter, Start: 25, End: 30},
			{Speaker: "Person 2", Text: judgeChatter, Start: 30, End: 40},
			{Speaker: "Person 1", Text: "On the retry budget design: clients retry at most three times today.", Start: 40, End: 50},
			{Speaker: "Person 2", Text: "Then let's make the retry cap five, and close PLAT-412.", Start: 50, End: 60},
			{Speaker: "Person 3", Text: judgeChatter, Start: 60, End: 90},
		},
		"s-fin": {
			{Speaker: "Person 1", Text: "The invoice  templates need\nthe new logo.", Start: 0, End: 60},
		},
	}
	return subjects, meetings, rec
}

// The parts of the doc <-> meeting judge's state, as the model sees them.
var (
	judgeOutline       = []string{"Retry budget design > Limits", "Retry budget design > Jitter"}
	judgeLimitsExcerpt = map[string]any{"section": "Retry budget design > Limits", "text": "Clients retry at most three times. Tracked in PLAT-412."}
	judgeJitterExcerpt = map[string]any{"section": "Retry budget design > Jitter", "text": "Full jitter on every backoff."}
)

func judgePlatformSegment(id string) map[string]any {
	// The stretch shown starts at the earliest turn from which both turns
	// naming the document fit in 1,500 characters, so it keeps three of the
	// five turns of other talk before them and drops the two at the opening.
	chatter := strings.TrimSpace(judgeChatter)
	spoken := strings.Join([]string{chatter, chatter, chatter,
		"On the retry budget design: clients retry at most three times today.",
		"Then let's make the retry cap five, and close PLAT-412."}, " ")
	return map[string]any{
		"id": id, "meeting_title": "Platform sync", "meeting_date": "2026-10-14",
		"topic_of_this_stretch_of_the_meeting":           "Retry cap",
		"summary_of_what_was_said":                       "Agreed to raise the retry cap to five and keep the retry budgt design.",
		"verbatim_words_spoken":                          spoken,
		"verbatim_quote_from_a_decision_in_this_meeting": "make the retry cap five",
	}
}

func judgeFinanceSegment(id string) map[string]any {
	return map[string]any{
		"id": id, "meeting_title": "Finance review", "meeting_date": "2026-08-14",
		"topic_of_this_stretch_of_the_meeting": "Invoices",
		"summary_of_what_was_said":             "Invoice templates need the new logo.",
		"verbatim_words_spoken":                "The invoice templates need the new logo.",
	}
}

func judgePlatformPair(pair, seg string) map[string]any {
	return map[string]any{
		"pair": pair, "segment_ids": []string{seg},
		"sections_of_the_document_closest_in_meaning":                   []string{"Retry budget design > Limits"},
		"document_titles_or_headings_whose_words_appear_in_the_meeting": []string{"Retry budget design"},
		"identifiers_appearing_in_both":                                 []string{"plat-412"},
	}
}

func judgeFinancePair(pair, seg string) map[string]any {
	return map[string]any{
		"pair": pair, "segment_ids": []string{seg},
		"sections_of_the_document_closest_in_meaning": []string{"Retry budget design > Jitter"},
	}
}

// TestMeetingJudgeRequestGolden pins the document <-> meeting judge, which
// asks the decision model directly as well.
func TestMeetingJudgeRequestGolden(t *testing.T) {
	ctx := context.Background()
	answer := func(req *jevGoldenRequest, name string) float64 {
		var st judgeState
		if err := json.Unmarshal(req.State, &st); err != nil {
			t.Error(err)
		}
		topicOf := make(map[string]string)
		for _, seg := range st.Segments {
			topicOf[seg.ID] = seg.Topic
		}
		for _, pc := range st.Pairs {
			if discussesQuestion+pc.Pair == name && topicOf[pc.Segments[0]] == "Retry cap" {
				return 0.91
			}
		}
		return 0.12
	}

	t.Run("one pair per request", func(t *testing.T) {
		subjects, meetings, rec := judgedMeetings()
		srv := newJevGoldenServer(t, answer)
		judgments, report, err := NewJudge(srv.client(), rec).Judge(ctx, candidatesFor(subjects, meetings))
		if err != nil {
			t.Fatal(err)
		}
		bodies := srv.sortedRequests()
		if len(bodies) != 2 || len(judgments) != 2 || report.Requests != 2 {
			t.Fatalf("%d requests, %d judgments, report %+v", len(bodies), len(judgments), report)
		}
		checkJevRequests(t, "meeting_judge", bodies)

		// Judgments come back in candidate-key order.
		if judgments[0].Candidate.Meeting.SessionID != "s-fin" || judgments[0].P != 0.12 ||
			judgments[1].Candidate.Meeting.SessionID != "s-plat" || judgments[1].P != 0.91 {
			t.Errorf("judgments = %s %v, %s %v", judgments[0].Candidate.Meeting.SessionID, judgments[0].P,
				judgments[1].Candidate.Meeting.SessionID, judgments[1].P)
		}

		wantPlat := map[string]any{
			"document": map[string]any{
				"path": "docs/retry-budget.md", "title": "Retry budget design", "headings_of_the_document": judgeOutline,
				"excerpts_of_the_document": []any{judgeLimitsExcerpt},
			},
			"meeting_segments": []any{judgePlatformSegment("s0")},
			"pairs":            []any{judgePlatformPair("p0", "s0")},
		}
		wantFin := map[string]any{
			"document": map[string]any{
				"path": "docs/retry-budget.md", "title": "Retry budget design", "headings_of_the_document": judgeOutline,
				"excerpts_of_the_document": []any{judgeJitterExcerpt},
			},
			"meeting_segments": []any{judgeFinanceSegment("s0")},
			"pairs":            []any{judgeFinancePair("p0", "s0")},
		}
		for _, b := range bodies {
			if strings.Contains(string(b), "Finance review") {
				wantJevRequest(t, "finance pair", b, wantFin, wantDiscussesQuestions(1))
			} else {
				wantJevRequest(t, "platform pair", b, wantPlat, wantDiscussesQuestions(1))
			}
		}
	})

	t.Run("batched", func(t *testing.T) {
		subjects, meetings, rec := judgedMeetings()
		srv := newJevGoldenServer(t, answer)
		judgments, _, err := NewJudgeWithOptions(srv.client(), rec, JudgeOptions{BatchSize: 10}).Judge(ctx, candidatesFor(subjects, meetings))
		if err != nil {
			t.Fatal(err)
		}
		bodies := srv.requests()
		if len(bodies) != 1 || len(judgments) != 2 {
			t.Fatalf("%d requests, %d judgments; want both pairs in one", len(bodies), len(judgments))
		}
		checkJevRequests(t, "meeting_judge_batched", bodies)
		wantJevRequest(t, "batched", bodies[0], map[string]any{
			"document": map[string]any{
				"path": "docs/retry-budget.md", "title": "Retry budget design", "headings_of_the_document": judgeOutline,
				"excerpts_of_the_document": []any{judgeLimitsExcerpt, judgeJitterExcerpt},
			},
			"meeting_segments": []any{judgePlatformSegment("s0"), judgeFinanceSegment("s1")},
			"pairs":            []any{judgePlatformPair("p0", "s0"), judgeFinancePair("p1", "s1")},
		}, wantDiscussesQuestions(2))
	})
}

// --- brief writer ---------------------------------------------------------

// briefDraft is a drafted brief with two conflicts, a decision resting on
// what was said and one resting on the document, so the writer asks the
// decision model about each conflict and about the first decision.
func briefDraft(t *testing.T) string {
	sup := func(label, quote string) []map[string]any {
		return []map[string]any{{"label": label, "quote": quote}}
	}
	return draft(t, map[string]any{
		"headline": map[string]any{"text": "cap is five now", "support": sup("M1", "make the retry cap five")},
		"current":  []any{}, "events": []any{}, "unresolved": []any{}, "timeline": []any{}, "document": []any{},
		"conflicts": []map[string]any{
			{
				"doc_says": "three retries", "discussion_says": "the cap is five", "date": "2026-10-14",
				"doc":  map[string]any{"label": "D1", "quote": "at most three times"},
				"said": map[string]any{"label": "M1", "quote": "make the retry cap five"},
			},
			{
				"doc_says": "full jitter", "discussion_says": "jitter <off> & capped", "date": "2026-10-14",
				"doc":  map[string]any{"label": "D2", "quote": "Full jitter on every backoff"},
				"said": map[string]any{"label": "M1", "quote": "retry cap five"},
			},
		},
		"decisions": []map[string]any{
			{"date": "", "text": "raise the cap to five", "support": sup("M1", "make the retry cap five")},
			{"date": "", "text": "keep full jitter", "support": sup("D2", "Full jitter on every backoff")},
		},
	})
}

func TestBriefWriterRequestsGolden(t *testing.T) {
	srv := newJevGoldenServer(t, func(_ *jevGoldenRequest, name string) float64 {
		switch name {
		case "contradicts_0":
			return 0.9
		case "contradicts_1":
			return 0.4
		case "settled":
			return 0.85
		}
		t.Errorf("unexpected question %q", name)
		return 0
	})
	rel, subj, meetings := briefFixture()
	rep, err := newWriter(&stubLLM{reply: briefDraft(t)}, srv.client(), day).Update(context.Background(), rel, subj, meetings)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Written {
		t.Fatalf("no brief written: %+v", rep)
	}
	bodies := srv.requests()
	if len(bodies) != 3 {
		t.Fatalf("%d requests, want two conflict checks and one settled check", len(bodies))
	}
	checkJevRequests(t, "brief_writer", bodies)

	wantJevRequest(t, "first conflict", bodies[0], map[string]any{
		"what_the_document_says":     "at most three times",
		"what_was_said_in_a_meeting": "make the retry cap five",
		"date_of_the_meeting":        "2026-10-14",
	}, jev.Questions{"contradicts_0": jev.NoulWithCriteria(
		"Does what was said in the meeting contradict what the document says?",
		"the meeting decided or settled something that cannot be true at the same time as the document's passage, so one of them must be out of date",
		"the two are compatible: they say the same thing, one adds detail, or they are about different things; or the meeting only proposed, asked or argued for something without settling it",
	)})
	wantJevRequest(t, "second conflict", bodies[1], map[string]any{
		"what_the_document_says":     "Full jitter on every backoff",
		"what_was_said_in_a_meeting": "retry cap five",
		"date_of_the_meeting":        "2026-10-14",
	}, jev.Questions{"contradicts_1": jev.NoulWithCriteria(
		"Does what was said in the meeting contradict what the document says?",
		"the meeting decided or settled something that cannot be true at the same time as the document's passage, so one of them must be out of date",
		"the two are compatible: they say the same thing, one adds detail, or they are about different things; or the meeting only proposed, asked or argued for something without settling it",
	)})
	wantJevRequest(t, "settled", bodies[2], map[string]any{
		"what_was_said_in_a_meeting":   "make the retry cap five",
		"decision_it_is_claimed_to_be": "raise the cap to five",
	}, jev.Questions{"settled": jev.NoulWithCriteria(
		"Does what was said show the decision being made or settled?",
		"the words show it chosen, agreed, ruled out or reversed: someone with the standing to decide says it is so, or the others agree",
		"the words only propose, suggest, argue for, ask about or plan to consider it, with nobody shown settling it",
	)})

	if rep.ConflictsKept != 1 || rep.ConflictsDemoted != 1 || rep.JudgeRequests != 3 || rep.JudgeTokens != 300 {
		t.Errorf("report = %+v", rep)
	}
	statuses := make(map[string]string)
	for _, d := range rel.Decisions {
		if d.Source == "writer" {
			statuses[d.Text] = d.Status
		}
	}
	if statuses["raise the cap to five"] != StatusDecided || statuses["keep full jitter"] != StatusDecided {
		t.Errorf("writer decisions = %v, want both decided (one checked, one resting on the document)", statuses)
	}
}
