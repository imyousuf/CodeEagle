package relations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/pkg/jev"
)

// scriptedAsker answers each pair from a table keyed by the topic of the first
// segment it lists, and can refuse the first request as too large or fail.
type scriptedAsker struct {
	mu         sync.Mutex
	p          map[string]float64
	refuseOnce bool
	fail       error
	calls      int
	states     []judgeState
}

func (s *scriptedAsker) Ask(_ context.Context, state any, questions jev.Questions) (*jev.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	raw, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	var st judgeState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	s.states = append(s.states, st)
	if s.fail != nil {
		return nil, s.fail
	}
	if s.refuseOnce {
		s.refuseOnce = false
		return nil, jev.ErrTooLarge
	}
	topicOf := make(map[string]string)
	for _, seg := range st.Segments {
		topicOf[seg.ID] = seg.Topic
	}
	answers := jev.Answers{}
	for _, pc := range st.Pairs {
		name := discussesQuestion + pc.Pair
		if _, ok := questions[name]; !ok {
			return nil, fmt.Errorf("no question %q", name)
		}
		p, ok := s.p[topicOf[pc.Segments[0]]]
		if !ok {
			p = 0.1
		}
		answers[name] = jev.Answer{Type: jev.TypeNoul, Noul: p}
	}
	return &jev.Response{Model: "jev-test", Answers: answers, Usage: jev.Usage{InputTokens: 500}}, nil
}

// spokenStub returns fixed words for any span.
type spokenStub struct{}

func (spokenStub) Span(_ *Meeting, start, end float64) ([]Turn, error) {
	return []Turn{{Speaker: "Person 1", Text: "let's make the retry cap five", Start: start, End: end}}, nil
}

func candidatesFor(subjects []*Subject, meetings map[string]*Meeting) []*Candidate {
	retry := subjects[0]
	plat, fin := meetings["s-plat"], meetings["s-fin"]
	return []*Candidate{
		{Subject: retry, Meeting: plat, Signals: SignalSaid | SignalName, Cosine: 0.9,
			Sections: []*Section{retry.Sections[0]}, Segments: []*Segment{plat.Segments[0]},
			Names: []string{"Retry budget design"}, IDs: []string{"plat-412"}},
		{Subject: retry, Meeting: fin, Signals: SignalSaid, Cosine: 0.55,
			Sections: []*Section{retry.Sections[1]}, Segments: []*Segment{fin.Segments[0]}},
	}
}

func TestJudgeAsksOneYesNoQuestionPerPair(t *testing.T) {
	subjects, meetings := corpus()
	asker := &scriptedAsker{p: map[string]float64{"Retry cap": 0.84}}
	judgments, report, err := NewJudge(asker, spokenStub{}).Judge(context.Background(), candidatesFor(subjects, meetings))
	if err != nil {
		t.Fatal(err)
	}
	if len(judgments) != 2 || asker.calls != 1 {
		t.Fatalf("%d judgments in %d calls; want 2 in 1 (one document, one request)", len(judgments), asker.calls)
	}
	got := make(map[string]float64)
	for _, jd := range judgments {
		got[jd.Candidate.Meeting.SessionID] = jd.P
	}
	if got["s-plat"] != 0.84 || got["s-fin"] != 0.1 {
		t.Errorf("probabilities = %v", got)
	}
	if report.Requests != 1 || report.InputTokens != 500 || report.Model != "jev-test" {
		t.Errorf("report = %+v", report)
	}

	st := asker.states[0]
	if st.Document.Path != "docs/retry-budget.md" || len(st.Document.Excerpts) == 0 {
		t.Errorf("document card = %+v", st.Document)
	}
	if !strings.Contains(st.Segments[0].SpokenWords, "retry cap five") {
		t.Errorf("the judge was not shown what was actually said: %+v", st.Segments[0])
	}
	if pc := st.Pairs[0]; len(pc.TitleWords) == 0 || len(pc.IDs) == 0 {
		t.Errorf("the literal facts of the pair were not passed: %+v", pc)
	}
}

func TestJudgeHalvesARequestTooLarge(t *testing.T) {
	subjects, meetings := corpus()
	asker := &scriptedAsker{refuseOnce: true}
	judgments, _, err := NewJudge(asker, nil).Judge(context.Background(), candidatesFor(subjects, meetings))
	if err != nil {
		t.Fatal(err)
	}
	if len(judgments) != 2 || asker.calls != 3 {
		t.Errorf("%d judgments in %d calls; want 2 after one refusal and two halves", len(judgments), asker.calls)
	}
}

// TestJudgeOutageLeavesPairsUnjudged covers a failing decision model: the run
// must not invent verdicts, and must say how many pairs it could not judge.
func TestJudgeOutageLeavesPairsUnjudged(t *testing.T) {
	subjects, meetings := corpus()
	asker := &scriptedAsker{fail: errors.New("no credits")}
	judgments, report, err := NewJudge(asker, nil).Judge(context.Background(), candidatesFor(subjects, meetings))
	if err == nil {
		t.Error("an outage was not reported")
	}
	if len(judgments) != 0 || report.Failed != 2 {
		t.Errorf("%d judgments, %d failed; want 0 and 2", len(judgments), report.Failed)
	}
}

func relationStore(t *testing.T) *embedded.BranchStore {
	t.Helper()
	base, err := embedded.NewBranchStore(t.TempDir(), "default", []string{"default", embedded.MeetingScope, embedded.RelationScope})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { base.Close() })
	return base.View(embedded.RelationScope, []string{"default", embedded.MeetingScope, embedded.RelationScope})
}

// TestRelaterRecordsEveryVerdictAndEdgesOnlyAccepted covers the ledger: a
// rejected pair is kept so it is never asked again, but only an accepted one
// becomes an edge an agent will follow.
func TestRelaterRecordsEveryVerdictAndEdgesOnlyAccepted(t *testing.T) {
	subjects, meetings := corpus()
	subjects[0].RootID = "root-retry"
	store := relationStore(t)
	ctx := context.Background()
	asker := &scriptedAsker{p: map[string]float64{"Retry cap": 0.84, "Invoices": 0.2}}
	rel := NewRelater(store, NewJudge(asker, nil), RelaterOptions{Now: func() time.Time { return day }})

	existing := map[string]*Relation{}
	cands := candidatesFor(subjects, meetings)
	stats, err := rel.Judge(ctx, cands, existing)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Judged != 2 || stats.Accepted != 1 || stats.Relations != 1 {
		t.Errorf("stats = %+v", stats)
	}

	stored, err := LoadRelations(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	r := stored["docs/retry-budget.md"]
	if r == nil || len(r.Links) != 2 {
		t.Fatalf("the ledger did not keep both verdicts: %+v", r)
	}
	if !r.Links["s-plat"].Accepted || r.Links["s-fin"].Accepted {
		t.Errorf("acceptance = plat %v, fin %v", r.Links["s-plat"].Accepted, r.Links["s-fin"].Accepted)
	}
	if len(r.Sections) != 2 {
		t.Errorf("section history = %v", r.Sections)
	}

	edges, _ := store.GetEdges(ctx, RelationID("docs/retry-budget.md"), graph.EdgeDiscussedIn)
	if len(edges) != 1 || edges[0].TargetID != "m-plat" {
		t.Errorf("DiscussedIn edges = %v, want one to the accepted meeting", edges)
	}
	about, _ := store.GetEdges(ctx, RelationID("docs/retry-budget.md"), graph.EdgeAbout)
	if len(about) != 1 || about[0].TargetID != "root-retry" {
		t.Errorf("About edges = %v", about)
	}

	// Judged pairs are set aside on the next run.
	p := NewProposer(subjects, meetings, nil, DefaultOptions())
	prop := p.Select(cands, JudgedBy(stored))
	if len(prop.Candidates) != 0 || prop.AlreadyJudged != 2 {
		t.Errorf("a second run would judge %d pairs again (%d set aside)", len(prop.Candidates), prop.AlreadyJudged)
	}
}

// TestRelaterDropsTheEdgeOfAVerdictReversed covers re-judging after a change:
// a meeting no longer judged to discuss the document loses its edge.
func TestRelaterDropsTheEdgeOfAVerdictReversed(t *testing.T) {
	subjects, meetings := corpus()
	store := relationStore(t)
	ctx := context.Background()
	existing := map[string]*Relation{}
	cands := candidatesFor(subjects, meetings)[:1]

	first := NewRelater(store, NewJudge(&scriptedAsker{p: map[string]float64{"Retry cap": 0.9}}, nil), RelaterOptions{})
	if _, err := first.Judge(ctx, cands, existing); err != nil {
		t.Fatal(err)
	}
	second := NewRelater(store, NewJudge(&scriptedAsker{p: map[string]float64{"Retry cap": 0.2}}, nil), RelaterOptions{})
	if _, err := second.Judge(ctx, cands, existing); err != nil {
		t.Fatal(err)
	}
	edges, _ := store.GetEdges(ctx, RelationID("docs/retry-budget.md"), graph.EdgeDiscussedIn)
	if len(edges) != 0 {
		t.Errorf("a reversed verdict kept its edge: %v", edges)
	}
}

func TestObserveRecordsWhenEachSectionChanged(t *testing.T) {
	subjects, _ := corpus()
	retry := subjects[0]
	r := New(retry.Path)
	r.Observe(retry, day)
	if got := r.Sections["Retry budget design > Limits"].FirstSeen; !got.Equal(day) {
		t.Fatalf("first seen = %v", got)
	}
	later := day.AddDate(0, 0, 7)
	retry.Sections[0].Hash = "limits-v2"
	r.Observe(retry, later)
	if got := r.Sections["Retry budget design > Limits"].FirstSeen; !got.Equal(later) {
		t.Errorf("an edited section kept its old first-seen time %v", got)
	}
	if got := r.Sections["Retry budget design > Jitter"].FirstSeen; !got.Equal(day) {
		t.Errorf("an unchanged section's first-seen time moved to %v", got)
	}
}
