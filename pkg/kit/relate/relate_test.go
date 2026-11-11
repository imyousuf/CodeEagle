package relate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/pkg/kit/decide"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph/memstore"
)

// scriptedJudge answers each yes/no question from a script keyed by the
// pair's A document and the question's name, and keeps every call.
type scriptedJudge struct {
	answer func(state PairState, name string) float64
	fail   func(state PairState) error

	mu    sync.Mutex
	calls []judgeCall
	usage decide.Usage
}

type judgeCall struct {
	state     any
	questions map[string]decide.Question
}

func (j *scriptedJudge) Decide(_ context.Context, state any, questions map[string]decide.Question) (map[string]decide.Verdict, error) {
	j.mu.Lock()
	j.calls = append(j.calls, judgeCall{state: state, questions: questions})
	j.mu.Unlock()
	st, _ := state.(PairState)
	if j.fail != nil {
		if err := j.fail(st); err != nil {
			return nil, err
		}
	}
	j.mu.Lock()
	j.usage.Requests++
	j.usage.InputTokens += 10 * len(questions)
	j.usage.OutputTokens++
	j.mu.Unlock()
	out := make(map[string]decide.Verdict, len(questions))
	for name := range questions {
		p := 0.0
		if j.answer != nil {
			p = j.answer(st, name)
		}
		out[name] = decide.YesNoVerdict(p)
	}
	return out, nil
}

func (j *scriptedJudge) Name() string { return "scripted" }

func (j *scriptedJudge) Usage() decide.Usage {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.usage
}

func (j *scriptedJudge) callCount() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.calls)
}

// answers scripts a judge by the A document's path.
func answers(by map[string]map[string]float64) func(PairState, string) float64 {
	return func(st PairState, name string) float64 { return by[st.A.Path][name] }
}

var now = time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)

func doc(id, hash string) Doc { return Doc{ID: id, Hash: hash, Title: strings.TrimSuffix(id, ".md")} }

func TestJudgeAsksOnePairPerDecide(t *testing.T) {
	j := &scriptedJudge{answer: answers(map[string]map[string]float64{
		"a.md": {qRelated: 0.9, qChangesBA: 0.8},
		"c.md": {qRelated: 0.2},
	})}
	cands := []*Candidate{
		{A: doc("a.md", "h1"), B: doc("b.md", "h2"), Signals: []string{"ref"}, Facts: []string{"b.md names a.md"}},
		{A: doc("c.md", "h3"), B: doc("d.md", "h4")},
		{A: doc("e.md", "h5"), B: doc("f.md", "h6")},
	}
	judgments, report, err := New(j).Judge(context.Background(), cands)
	if err != nil {
		t.Fatal(err)
	}
	if j.callCount() != 3 || report.Requests != 3 || report.Failed != 0 {
		t.Fatalf("%d calls, report %+v; want one per pair", j.callCount(), report)
	}
	seen := make(map[string]bool)
	for _, c := range j.calls {
		st := c.state.(PairState)
		seen[st.A.Path+">"+st.B.Path] = true
		if len(c.questions) != 7 {
			t.Errorf("%d questions about %s, want 7", len(c.questions), st.A.Path)
		}
	}
	if len(seen) != 3 {
		t.Errorf("states seen: %v", seen)
	}
	if report.InputTokens != 210 || report.OutputTokens != 3 || report.Judge != "scripted" {
		t.Errorf("report = %+v", report)
	}
	// In the order of the candidates, with the probabilities by name and
	// nothing decided yet.
	if len(judgments) != 3 || judgments[0].Candidate != cands[0] || judgments[2].Candidate != cands[2] {
		t.Fatalf("judgments out of order")
	}
	got := judgments[0].Verdict
	want := Verdict{Other: "b.md", PRelated: 0.9, PChanges: [2]float64{0, 0.8}, Signals: []string{"ref"},
		SigA: "h1", SigB: "h2", Judge: "scripted"}
	if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", want) {
		t.Errorf("verdict = %+v\nwant      %+v", got, want)
	}
}

func TestJudgeReportsFailedPairs(t *testing.T) {
	boom := errors.New("boom")
	j := &scriptedJudge{fail: func(st PairState) error {
		if st.A.Path == "c.md" {
			return boom
		}
		return nil
	}}
	cands := []*Candidate{{A: doc("a.md", "1"), B: doc("b.md", "2")}, {A: doc("c.md", "3"), B: doc("d.md", "4")}}
	judgments, report, err := New(j).Judge(context.Background(), cands)
	if err != nil || len(judgments) != 1 || report.Failed != 1 || report.Requests != 1 {
		t.Fatalf("%d judgments, report %+v, %v; want the failed pair left out", len(judgments), report, err)
	}

	_, report, err = New(j).Judge(context.Background(), cands[1:])
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "judge c.md and d.md") || report.Judge != "" {
		t.Errorf("every pair failing: report %+v, err %v", report, err)
	}
}

func TestDecideDirection(t *testing.T) {
	g := DefaultGates()
	old := Doc{ID: "a.md", Date: "2026-06-02"}
	newer := Doc{ID: "b.md", Date: "2026-09-14"}
	undated := Doc{ID: "b.md"}
	chat := Doc{ID: "b.jsonl", Record: true}
	postmortem := Doc{ID: "incidents/outage.md", Record: true}
	notes := Doc{ID: "notes.md", Record: true}
	tests := []struct {
		name string
		a, b Doc
		v    Verdict
		want string // kind:from>to, comma separated
	}{
		{"unrelated", old, newer, Verdict{PRelated: 0.5, PChanges: [2]float64{0, 0.99}}, ""},
		{"b changes a", old, newer, Verdict{PRelated: 0.9, PChanges: [2]float64{0.1, 0.9}}, "changes:b.md>a.md"},
		{"b replaces a", old, newer, Verdict{PRelated: 0.9, PChanges: [2]float64{0.1, 0.9}, PReplaces: [2]float64{0.1, 0.97}},
			"changes:b.md>a.md,replaces:b.md>a.md"},
		{"replaces needs changes", old, newer, Verdict{PRelated: 0.9, PReplaces: [2]float64{0.1, 0.97}}, "companion:a.md>b.md"},
		{"replaces needs changes the same way", old, newer, Verdict{PRelated: 0.9, PChanges: [2]float64{0.9, 0.1}, PReplaces: [2]float64{0.1, 0.97}},
			"changes:a.md>b.md"},
		{"both ways, later wins", old, newer, Verdict{PRelated: 0.9, PChanges: [2]float64{0.9, 0.9}}, "changes:b.md>a.md"},
		{"both ways, later wins from A", newer, Doc{ID: "c.md", Date: "2026-01-01"}, Verdict{PRelated: 0.9, PChanges: [2]float64{0.9, 0.9}}, "changes:b.md>c.md"},
		{"both ways, no dates, neither", old, undated, Verdict{PRelated: 0.9, PChanges: [2]float64{0.9, 0.9}}, "companion:a.md>b.md"},
		{"both ways, same date, neither", old, Doc{ID: "b.md", Date: "2026-06-02"}, Verdict{PRelated: 0.9, PChanges: [2]float64{0.9, 0.9}}, "companion:a.md>b.md"},
		{"records", old, postmortem, Verdict{PRelated: 0.9, PRecords: [2]float64{0.1, 0.9}}, "records:incidents/outage.md>a.md"},
		{"a spec records nothing", old, newer, Verdict{PRelated: 0.9, PRecords: [2]float64{0.1, 0.9}}, "companion:a.md>b.md"},
		{"records both ways, undated specs, neither", old, undated, Verdict{PRelated: 0.9, PRecords: [2]float64{0.9, 0.9}}, "companion:a.md>b.md"},
		{"records both ways, the chat is the record", old, chat, Verdict{PRelated: 0.9, PRecords: [2]float64{0.9, 0.9}}, "records:b.jsonl>a.md"},
		{"a record of a record is not one", notes, postmortem, Verdict{PRelated: 0.9, PRecords: [2]float64{0.1, 0.95}}, "companion:notes.md>incidents/outage.md"},
		{"changes and records together", old, postmortem, Verdict{PRelated: 0.9, PChanges: [2]float64{0.1, 0.8}, PRecords: [2]float64{0.1, 0.9}},
			"changes:incidents/outage.md>a.md,records:incidents/outage.md>a.md"},
		{"two routines' dated outputs are companions", Doc{ID: "brief/2026-10-14.md", Series: "brief"}, Doc{ID: "recap/2026-10-13.md", Series: "recap"},
			Verdict{PRelated: 0.95, PChanges: [2]float64{0.9, 0.1}, PRecords: [2]float64{0.9, 0.1}}, "companion:brief/2026-10-14.md>recap/2026-10-13.md"},
		{"a later output of one series changes an earlier", Doc{ID: "brief/2026-10-14.md", Series: "brief"}, Doc{ID: "brief/2026-10-19.md", Series: "brief"},
			Verdict{PRelated: 0.95, PChanges: [2]float64{0.1, 0.9}}, "changes:brief/2026-10-19.md>brief/2026-10-14.md"},
	}
	for _, tt := range tests {
		v := tt.v
		v.Links = []Link{{Kind: "stale"}}
		v.Decide(tt.a, tt.b, g)
		var got []string
		for _, l := range v.Links {
			got = append(got, l.Kind+":"+l.From+">"+l.To)
		}
		if strings.Join(got, ",") != tt.want {
			t.Errorf("%s: %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestDecideCarriesTheProbabilityOfEachLink(t *testing.T) {
	v := Verdict{PRelated: 0.91, PChanges: [2]float64{0.2, 0.8}, PReplaces: [2]float64{0.1, 0.96}}
	v.Decide(Doc{ID: "a"}, Doc{ID: "b"}, DefaultGates())
	if len(v.Links) != 2 || v.Links[0].P != 0.8 || v.Links[1].P != 0.96 {
		t.Errorf("links = %+v", v.Links)
	}
	v = Verdict{PRelated: 0.91}
	v.Decide(Doc{ID: "a"}, Doc{ID: "b"}, DefaultGates())
	if len(v.Links) != 1 || v.Links[0].P != 0.91 {
		t.Errorf("companion = %+v, want it at the related probability", v.Links)
	}
}

func TestDatedSeries(t *testing.T) {
	for p, want := range map[string]string{
		"jarvis/vt_daily_brief/2026-10-14.md":  "jarvis/vt_daily_brief",
		"reviews/2026-09-09_pr-6784-review.md": "reviews",
		"2026-10-14.md":                        ".",
		"docs/spec.md":                         "",
		"docs/2026-10-14 notes.md":             "",
		"specs/opal-7194-plan.md":              "",
	} {
		if got := DatedSeries(p); got != want {
			t.Errorf("DatedSeries(%q) = %q, want %q", p, got, want)
		}
	}
}

func TestGatesAreOptions(t *testing.T) {
	j := &scriptedJudge{answer: func(PairState, string) float64 { return 0 }}
	j.answer = answers(map[string]map[string]float64{"a.md": {qRelated: 0.95, qChangesBA: 0.8}})
	cands := func() []*Candidate { return []*Candidate{{A: doc("a.md", "1"), B: doc("b.md", "2")}} }

	l := NewMemLedger(nil)
	if _, err := New(j).Relate(context.Background(), cands(), l, now); err != nil {
		t.Fatal(err)
	}
	if links := l.Verdict("a.md", "b.md").Links; len(links) != 1 || links[0].Kind != Changes {
		t.Errorf("under the default gates: %+v", links)
	}

	strict := DefaultGates()
	strict.Changes = 0.9
	r := New(j, WithGates(strict))
	if r.Gates() != strict {
		t.Errorf("Gates() = %+v", r.Gates())
	}
	stats, err := r.Relate(context.Background(), cands(), l, now)
	if err != nil {
		t.Fatal(err)
	}
	if links := l.Verdict("a.md", "b.md").Links; len(links) != 1 || links[0].Kind != Companion || stats.Accepted[Companion] != 1 {
		t.Errorf("under a stricter changes gate: %+v, accepted %v", links, stats.Accepted)
	}
}

func TestRelateKeepsEveryVerdict(t *testing.T) {
	j := &scriptedJudge{answer: answers(map[string]map[string]float64{"a.md": {qRelated: 0.95}, "c.md": {qRelated: 0.3}})}
	l := NewMemLedger(nil)
	cands := []*Candidate{{A: doc("a.md", "1"), B: doc("b.md", "2")}, {A: doc("c.md", "3"), B: doc("d.md", "4")}}
	stats, err := New(j).Relate(context.Background(), cands, l, now)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Judged != 2 || stats.Accepted[Companion] != 1 || stats.Judging.Requests != 2 {
		t.Errorf("stats = %+v", stats)
	}
	rejected := l.Verdict("c.md", "d.md")
	if rejected == nil || len(rejected.Links) != 0 || !rejected.JudgedAt.Equal(now) {
		t.Errorf("a rejected pair is kept too, so it is not asked again: %+v", rejected)
	}
	if !Judged(l, cands[1]) {
		t.Error("a judged pair would be asked again")
	}
	cands[1].B.Hash = "4b"
	if Judged(l, cands[1]) {
		t.Error("a pair is judged again when either side changes")
	}
}

// runCorpus is three documents, a, b and c, where b names a and c names b.
func runCorpus(hashes map[string]string) ([]Doc, Proposer) {
	var docs []Doc
	for i, id := range []string{"a.md", "b.md", "c.md"} {
		docs = append(docs, Doc{ID: id, Hash: hashes[id], UpdatedAt: now.Add(time.Duration(i) * time.Hour)})
	}
	by := make(map[string]Doc)
	for _, d := range docs {
		by[d.ID] = d
	}
	pairs := [][2]string{{"a.md", "b.md"}, {"b.md", "c.md"}}
	propose := func(ids ...string) []*Candidate {
		want := make(map[string]bool)
		for _, id := range ids {
			want[id] = true
		}
		var out []*Candidate
		for _, p := range pairs {
			if want[p[0]] || want[p[1]] {
				out = append(out, NewCandidate(by[p[1]], by[p[0]]))
			}
		}
		return out
	}
	return docs, propose
}

func TestRunJudgesAPairAgainOnlyWhenEitherSideChanges(t *testing.T) {
	ctx := context.Background()
	j := &scriptedJudge{answer: func(PairState, string) float64 { return 0.9 }}
	var saved []byte
	l := NewMemLedger(func(_ context.Context, data []byte) error { saved = data; return nil })
	hashes := map[string]string{"a.md": "a1", "b.md": "b1", "c.md": "c1"}
	r := New(j)

	docs, propose := runCorpus(hashes)
	rep, err := r.Run(ctx, docs, propose, l, DefaultRunOptions(), now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Considered != 3 || rep.Stats.Judged != 2 || j.callCount() != 2 {
		t.Fatalf("first run: %+v, %d calls", rep, j.callCount())
	}
	for _, id := range []string{"a.md", "b.md", "c.md"} {
		if l.Considered(id) != hashes[id] {
			t.Errorf("%s considered under %q", id, l.Considered(id))
		}
	}
	if !strings.Contains(string(saved), `"considered"`) {
		t.Errorf("the ledger was not saved: %s", saved)
	}

	if rep, err = r.Run(ctx, docs, propose, l, DefaultRunOptions(), now); err != nil || rep.Considered != 0 || j.callCount() != 2 {
		t.Errorf("an unchanged corpus: %+v, %v, %d calls; want nothing considered or asked", rep, err, j.callCount())
	}

	// c changes: its one pair is asked again, and a's pair is not.
	hashes["c.md"] = "c2"
	docs, propose = runCorpus(hashes)
	rep, err = r.Run(ctx, docs, propose, l, DefaultRunOptions(), now)
	if err != nil || rep.Considered != 1 || rep.Stats.Judged != 1 || j.callCount() != 3 {
		t.Fatalf("after an edit: %+v, %v, %d calls", rep, err, j.callCount())
	}
	if v := l.Verdict("b.md", "c.md"); v.SigB != "c2" {
		t.Errorf("the pair's verdict is under %q, want the new content", v.SigB)
	}
	last := j.calls[2].state.(PairState)
	if last.A.Path != "b.md" || last.B.Path != "c.md" {
		t.Errorf("asked about %s and %s", last.A.Path, last.B.Path)
	}
}

func TestRunLeavesUnfinishedDocumentsForALaterRun(t *testing.T) {
	ctx := context.Background()
	hashes := map[string]string{"a.md": "a1", "b.md": "b1", "c.md": "c1"}
	docs, propose := runCorpus(hashes)

	// The pair of b and c fails: b and c stay pending, a is done.
	j := &scriptedJudge{fail: func(st PairState) error {
		if st.B.Path == "c.md" {
			return errors.New("timeout")
		}
		return nil
	}}
	l := NewMemLedger(nil)
	rep, err := New(j).Run(ctx, docs, propose, l, DefaultRunOptions(), now)
	if err != nil || rep.Stats.Judging.Failed != 1 {
		t.Fatalf("%+v, %v", rep, err)
	}
	if l.Considered("a.md") != "a1" || l.Considered("b.md") != "" || l.Considered("c.md") != "" {
		t.Errorf("considered: a %q, b %q, c %q; want only a", l.Considered("a.md"), l.Considered("b.md"), l.Considered("c.md"))
	}

	// One pair per run defers the other, with the same effect.
	l = NewMemLedger(nil)
	j = &scriptedJudge{}
	rep, err = New(j).Run(ctx, docs, propose, l, RunOptions{MaxPairs: 1}, now)
	if err != nil || rep.Deferred != 1 || rep.Stats.Judged != 1 {
		t.Fatalf("%+v, %v", rep, err)
	}
	if l.Considered("a.md") != "a1" || l.Considered("b.md") != "" || l.Considered("c.md") != "" {
		t.Errorf("considered after deferring: a %q, b %q, c %q", l.Considered("a.md"), l.Considered("b.md"), l.Considered("c.md"))
	}
	if rep, _ = New(j).Run(ctx, docs, propose, l, RunOptions{MaxPairs: 1}, now); rep.Stats.Judged != 1 || l.Considered("c.md") != "c1" {
		t.Errorf("the next run: %+v, c %q", rep, l.Considered("c.md"))
	}

	// At most one document per run, newest first.
	l = NewMemLedger(nil)
	rep, _ = New(&scriptedJudge{}).Run(ctx, docs, propose, l, RunOptions{MaxDocs: 1}, now)
	if rep.Considered != 1 || rep.Waiting != 2 || l.Considered("c.md") != "c1" || l.Considered("a.md") != "" {
		t.Errorf("bounded: %+v; c %q, a %q", rep, l.Considered("c.md"), l.Considered("a.md"))
	}
}

func TestNilJudgeAssertsNothing(t *testing.T) {
	ctx := context.Background()
	r := New(nil)
	cands := []*Candidate{{A: doc("a.md", "1"), B: doc("b.md", "2")}}
	judgments, report, err := r.Judge(ctx, cands)
	if err != nil || judgments != nil || report != (Report{}) {
		t.Errorf("Judge: %v, %+v, %v", judgments, report, err)
	}
	saves := 0
	l := NewMemLedger(func(context.Context, []byte) error { saves++; return nil })
	stats, err := r.Relate(ctx, cands, l, now)
	if err != nil || stats.Judged != 0 || len(stats.Accepted) != 0 || l.Verdict("a.md", "b.md") != nil {
		t.Errorf("Relate: %+v, %v", stats, err)
	}
	docs, propose := runCorpus(map[string]string{"a.md": "a1", "b.md": "b1", "c.md": "c1"})
	rep, err := r.Run(ctx, docs, propose, l, DefaultRunOptions(), now)
	if err != nil || rep.Considered != 0 || l.Considered("a.md") != "" || saves != 0 {
		t.Errorf("Run: %+v, %v, %d saves; want nothing considered", rep, err, saves)
	}
}

func TestCustomWordingAndStateReachTheJudge(t *testing.T) {
	j := &scriptedJudge{}
	w := Wording{
		Related:  decide.YesNo("Same work?", "yes it is", "no it is not"),
		Changes:  func(x, y string) decide.Question { return decide.YesNo("Does "+x+" update "+y+"?", "", "") },
		Replaces: func(x, y string) decide.Question { return decide.YesNo("Does "+x+" supersede "+y+"?", "", "") },
		Records:  func(x, y string) decide.Question { return decide.YesNo("Does "+x+" report on "+y+"?", "", "") },
	}
	state := func(c *Candidate) any { return map[string]string{"first": c.A.ID, "second": c.B.ID} }
	_, _, err := New(j, WithWording(w), WithState(state)).Judge(context.Background(),
		[]*Candidate{{A: doc("a.md", "1"), B: doc("b.md", "2")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(j.calls) != 1 {
		t.Fatalf("%d calls", len(j.calls))
	}
	q := j.calls[0].questions
	want := map[string]string{
		"related": "Same work?", "changes_ab": "Does A update B?", "changes_ba": "Does B update A?",
		"replaces_ab": "Does A supersede B?", "replaces_ba": "Does B supersede A?",
		"records_ab": "Does A report on B?", "records_ba": "Does B report on A?",
	}
	if len(q) != len(want) {
		t.Errorf("questions = %v", q)
	}
	for name, ask := range want {
		if q[name].Ask != ask || !q[name].IsYesNo() {
			t.Errorf("%s = %+v, want %q", name, q[name], ask)
		}
	}
	if q["related"].WhenTrue != "yes it is" || q["related"].WhenFalse != "no it is not" {
		t.Errorf("related criteria = %+v", q["related"])
	}
	if got := fmt.Sprint(j.calls[0].state); got != "map[first:a.md second:b.md]" {
		t.Errorf("state = %s", got)
	}
}

func TestDefaultWordingSpellsOutEveryAnswer(t *testing.T) {
	q := DefaultWording().questions()
	names := make([]string, 0, len(q))
	for name, question := range q {
		names = append(names, name)
		if !question.IsYesNo() || question.WhenTrue == "" || question.WhenFalse == "" {
			t.Errorf("%s = %+v, want a yes/no question with both criteria", name, question)
		}
	}
	sort.Strings(names)
	if got := strings.Join(names, ","); got != "changes_ab,changes_ba,records_ab,records_ba,related,replaces_ab,replaces_ba" {
		t.Errorf("questions = %s", got)
	}
	if q["changes_ba"].Ask != "Does document B change part of what document A specifies?" {
		t.Errorf("changes_ba = %q", q["changes_ba"].Ask)
	}
}

func TestDefaultStateIsTheTwoCardsAndTheFacts(t *testing.T) {
	c := &Candidate{
		A: Doc{ID: "docs/spec.md", Title: "Spec", Type: "spec", Status: "Draft", Date: "2026-10-04",
			Outline: []string{"Goals"}, Hash: "not shown", Record: true, Series: "not shown"},
		B:         Doc{ID: "docs/notes.md", Title: "Notes", Type: "notes"},
		ExcerptsA: []Excerpt{{Section: "opening", Text: "Status: Draft"}},
		Facts:     []string{"docs/notes.md names docs/spec.md"},
	}
	got, err := json.Marshal(DefaultState(c))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"document_a":{"path":"docs/spec.md","type_of_document":"spec","title":"Spec",` +
		`"status_the_document_states":"Draft","date_the_document_states":"2026-10-04",` +
		`"headings_of_the_document":["Goals"],"excerpts_of_the_document":[{"section":"opening","text":"Status: Draft"}]},` +
		`"document_b":{"path":"docs/notes.md","type_of_document":"notes","title":"Notes",` +
		`"headings_of_the_document":null,"excerpts_of_the_document":null},` +
		`"facts_about_the_pair":["docs/notes.md names docs/spec.md"]}`
	if string(got) != want {
		t.Errorf("state =\n%s\nwant\n%s", got, want)
	}
}

func TestJudgeNameIsAnOption(t *testing.T) {
	j := &scriptedJudge{}
	judgments, report, err := New(j, WithJudgeName(func() string { return "jev-1.13.0" })).Judge(context.Background(),
		[]*Candidate{{A: doc("a.md", "1"), B: doc("b.md", "2")}})
	if err != nil || judgments[0].Verdict.Judge != "jev-1.13.0" || report.Judge != "jev-1.13.0" {
		t.Errorf("verdict judge %q, report %+v, %v", judgments[0].Verdict.Judge, report, err)
	}
}

func TestSelectAppliesTheCaps(t *testing.T) {
	pair := func(a, b string) *Candidate { return &Candidate{A: Doc{ID: a}, B: Doc{ID: b}} }
	cands := []*Candidate{pair("a", "b"), pair("a", "c"), pair("a", "d"), pair("b", "c"), pair("c", "d"), pair("e", "f")}
	judged := func(c *Candidate) bool { return c.Key() == "b\x00c" }
	selected, deferred := Select(cands, judged, 2, 3)
	var got []string
	for _, c := range cands {
		got = append(got, c.A.ID+c.B.ID+"="+c.Status)
	}
	want := "ab=selected,ac=selected,ad=past the caps,bc=judged,cd=selected,ef=deferred"
	if strings.Join(got, ",") != want {
		t.Errorf("statuses = %s\nwant       %s", strings.Join(got, ","), want)
	}
	if len(selected) != 3 || len(deferred) != 1 || deferred[0] != cands[5] {
		t.Errorf("%d selected, %d deferred", len(selected), len(deferred))
	}
	if selected, _ = Select(cands, nil, 0, 0); len(selected) != len(cands) {
		t.Errorf("no caps selected %d of %d", len(selected), len(cands))
	}
}

func TestNewCandidateOrdersThePair(t *testing.T) {
	c := NewCandidate(Doc{ID: "z.md"}, Doc{ID: "a.md"})
	if c.A.ID != "a.md" || c.B.ID != "z.md" || c.Key() != "a.md\x00z.md" {
		t.Errorf("candidate = %s, %s", c.A.ID, c.B.ID)
	}
}

func TestMemLedgerRoundTripsAndForgets(t *testing.T) {
	l := NewMemLedger(nil)
	a, b, c := doc("a.md", "1"), doc("b.md", "2"), doc("c.md", "3")
	l.Put(a, b, &Verdict{Other: "b.md", PRelated: 0.9, SigA: "1", SigB: "2", Judge: "scripted", JudgedAt: now,
		Links: []Link{{Kind: Companion, From: "a.md", To: "b.md", P: 0.9}}})
	l.Put(b, c, &Verdict{Other: "c.md", SigA: "2", SigB: "3"})
	l.Consider("a.md", "1")
	raw, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	back := NewMemLedger(nil)
	if err := json.Unmarshal(raw, back); err != nil {
		t.Fatal(err)
	}
	if v := back.Verdict("a.md", "b.md"); v == nil || v.PRelated != 0.9 || !v.JudgedAt.Equal(now) || len(v.Links) != 1 {
		t.Errorf("verdict after a round trip = %+v", v)
	}
	if back.Considered("a.md") != "1" || !Judged(back, &Candidate{A: a, B: b}) {
		t.Errorf("considered %q", back.Considered("a.md"))
	}
	back.Forget("b.md")
	if back.Verdict("a.md", "b.md") != nil || back.Verdict("b.md", "c.md") != nil {
		t.Error("forgetting a document keeps verdicts on its pairs")
	}
	var empty MemLedger
	if err := json.Unmarshal([]byte(`{}`), &empty); err != nil || empty.Verdict("a", "b") != nil {
		t.Errorf("an empty ledger: %v", err)
	}
	empty.Put(a, b, &Verdict{})
	if empty.Verdict("a.md", "b.md") == nil {
		t.Error("a decoded empty ledger takes verdicts")
	}
}

func TestWriteEdgesFollowsTheVerdict(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	nodeID := func(doc string) string { return "node:" + doc }
	for _, id := range []string{"a.md", "b.md", "c.md"} {
		if err := store.AddNode(ctx, &graph.Node{ID: nodeID(id), Type: graph.NodeDocument, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	edges := func() []string {
		var out []string
		all, err := store.GetEdges(ctx, nodeID("a.md"), "")
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range all {
			out = append(out, fmt.Sprintf("%s:%s>%s@%s/%s", e.Type, e.SourceID, e.TargetID, e.Properties["probability"], e.Properties["judge"]))
		}
		sort.Strings(out)
		return out
	}
	// An unrelated edge on the same node stays put.
	if err := store.AddEdge(ctx, &graph.Edge{ID: "keep", Type: graph.EdgeChanges, SourceID: nodeID("c.md"), TargetID: nodeID("a.md")}); err != nil {
		t.Fatal(err)
	}

	v := &Verdict{Other: "b.md", PRelated: 0.95, PChanges: [2]float64{0.1, 0.8}, PReplaces: [2]float64{0.1, 0.97}, Judge: "scripted"}
	v.Decide(Doc{ID: "a.md"}, Doc{ID: "b.md"}, DefaultGates())
	if err := WriteEdges(ctx, store, "a.md", "b.md", v, nodeID); err != nil {
		t.Fatal(err)
	}
	want := "Changes:node:b.md>node:a.md@0.800/scripted,Changes:node:c.md>node:a.md@/,Supersedes:node:b.md>node:a.md@0.970/scripted"
	if got := strings.Join(edges(), ","); got != want {
		t.Errorf("edges = %s\nwant    %s", got, want)
	}

	// Stricter gates leave only a companion, and the old edges go.
	v.Decide(Doc{ID: "a.md"}, Doc{ID: "b.md"}, Gates{Related: 0.9, Changes: 0.9, Replaces: 0.99, Records: 0.9})
	if err := WriteEdges(ctx, store, "a.md", "b.md", v, nodeID); err != nil {
		t.Fatal(err)
	}
	want = "Changes:node:c.md>node:a.md@/,CompanionOf:node:a.md>node:b.md@0.950/scripted"
	if got := strings.Join(edges(), ","); got != want {
		t.Errorf("edges after re-deciding = %s\nwant                  %s", got, want)
	}
	if EdgeType(Replaces) != graph.EdgeSupersedes || EdgeType("unknown") != "" {
		t.Error("edge types")
	}
}
