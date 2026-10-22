package relations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

const retryDoc = "# Retry budget design\n\n## Limits\n\nClients retry at most three times. " +
	"Every call backs off exponentially with full jitter, and a shared budget caps retries per client " +
	"so a slow dependency cannot be overwhelmed by its own callers.\n\n## Jitter\n\n" +
	"Full jitter on every backoff, drawn uniformly between zero and the current ceiling, " +
	"so that synchronised clients spread out instead of retrying in waves. The ceiling doubles " +
	"after every failed attempt and is capped at thirty seconds.\n"

// incrementalStore indexes one document and two meetings, one of which has
// no vectors yet, into a store written through the relations scope.
func incrementalStore(t *testing.T) (*embedded.BranchStore, fakeVectors) {
	store, _, vec := incrementalStores(t)
	return store, vec
}

// incrementalStores also returns the store the documents were indexed into.
func incrementalStores(t *testing.T) (*embedded.BranchStore, *embedded.BranchStore, fakeVectors) {
	t.Helper()
	base, err := embedded.NewBranchStore(t.TempDir(), "default", []string{"default", embedded.MeetingScope, embedded.RelationScope})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { base.Close() })
	store := base.View(embedded.RelationScope, []string{"default", embedded.MeetingScope, embedded.RelationScope})
	indexMarkdown(t, base, "docs/retry.md", retryDoc, nil)

	meetings := base.View(embedded.MeetingScope, []string{embedded.MeetingScope})
	ctx := context.Background()
	for _, n := range []*graph.Node{
		{ID: "m1", Type: graph.NodeMeeting, Name: "Platform sync", UpdatedAt: day,
			Properties: map[string]string{graph.PropMeetingID: "s1", graph.PropContentHash: "c1"}},
		{ID: "g1", Type: graph.NodeTopicSegment, Name: "Retry cap",
			Properties: map[string]string{graph.PropMeetingID: "s1", graph.PropStartTime: "10", graph.PropEndTime: "90",
				graph.PropSummary: "Agreed to raise the retry cap to five."}},
		{ID: "m2", Type: graph.NodeMeeting, Name: "Late recording", UpdatedAt: day,
			Properties: map[string]string{graph.PropMeetingID: "s2", graph.PropContentHash: "c2"}},
		{ID: "g2", Type: graph.NodeTopicSegment, Name: "Retries again",
			Properties: map[string]string{graph.PropMeetingID: "s2", graph.PropStartTime: "0", graph.PropEndTime: "50",
				graph.PropSummary: "Retries came up again."}},
	} {
		if err := meetings.AddNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	limits := graph.NewNodeID(string(graph.NodeDocument), "docs/retry.md", "section:Limits")
	jitter := graph.NewNodeID(string(graph.NodeDocument), "docs/retry.md", "section:Jitter")
	// The late recording's segment has no vector yet.
	return store, base, fakeVectors{limits: {1, 0, 0}, jitter: {0.9, 0.1, 0}, "g1": {0.95, 0.05, 0}}
}

func briefStub() *stubLLM {
	return &stubLLM{reply: `{"headline":{"text":"cap raised","support":[{"label":"M1","quote":"retry cap five"}]},` +
		`"current":[],"events":[],"unresolved":[],"conflicts":[]}`}
}

func runIncremental(t *testing.T, store *embedded.BranchStore, vec fakeVectors, asker *scriptedAsker, llm *stubLLM, popts Options) *IncrementalReport {
	t.Helper()
	writer := NewBriefWriter(llm, nil, spokenStub{}, nil, BriefOptions{}, func() time.Time { return day })
	rep, err := Incremental(context.Background(), store, vec, NewJudge(asker, spokenStub{}), writer, nil, popts, 0.8, IncrementalOptions{})
	if err != nil {
		t.Fatalf("Incremental: %v", err)
	}
	return rep
}

func TestIncrementalConsidersOnlyWhatIsNewOrChanged(t *testing.T) {
	store, vec := incrementalStore(t)
	asker := &scriptedAsker{p: map[string]float64{"Retry cap": 0.9}}
	llm := briefStub()

	rep := runIncremental(t, store, vec, asker, llm, DefaultOptions())
	if rep.SubjectsConsidered != 1 || rep.MeetingsConsidered != 1 {
		t.Errorf("first run considered %d documents, %d meetings; want 1 and 1 (the unembedded meeting waits)",
			rep.SubjectsConsidered, rep.MeetingsConsidered)
	}
	if rep.Judged.Accepted != 1 || rep.BriefsWritten != 1 {
		t.Errorf("first run: %+v", rep)
	}

	calls, llmCalls := asker.calls, llm.calls
	rep = runIncremental(t, store, vec, asker, llm, DefaultOptions())
	if asker.calls != calls || llm.calls != llmCalls || rep.SubjectsConsidered+rep.MeetingsConsidered != 0 {
		t.Errorf("a second run with nothing new considered %d+%d and made %d judge and %d model calls",
			rep.SubjectsConsidered, rep.MeetingsConsidered, asker.calls-calls, llm.calls-llmCalls)
	}

	// Once the late recording is embedded, it is picked up.
	vec["g2"] = []float32{0.9, 0.1, 0}
	rep = runIncremental(t, store, vec, asker, llm, DefaultOptions())
	if rep.MeetingsConsidered != 1 || rep.SubjectsConsidered != 0 {
		t.Errorf("after embedding the late recording: %d documents, %d meetings considered", rep.SubjectsConsidered, rep.MeetingsConsidered)
	}
}

// TestIncrementalLeavesDeferredWorkForTheNextRun covers the per-run cap: a
// document whose candidates were not all judged is not marked considered.
func TestIncrementalLeavesDeferredWorkForTheNextRun(t *testing.T) {
	store, vec := incrementalStore(t)
	vec["g2"] = []float32{0.9, 0.1, 0}
	asker := &scriptedAsker{p: map[string]float64{"Retry cap": 0.9}}
	tight := Options{K: 5, MaxPerSubject: 8, MaxPerMeeting: 8, MaxPairs: 1}

	runIncremental(t, store, vec, asker, briefStub(), tight)
	state, err := LoadScanState(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := state.Subjects["docs/retry.md"]; ok {
		t.Error("a document with a candidate deferred by the per-run cap was marked considered")
	}
	rep := runIncremental(t, store, vec, asker, briefStub(), tight)
	if rep.Judged.Judged != 1 {
		t.Errorf("the next run judged %d pairs, want the one left over", rep.Judged.Judged)
	}
}

func TestIncrementalOutageMarksNothingConsidered(t *testing.T) {
	store, vec := incrementalStore(t)
	asker := &scriptedAsker{fail: errors.New("no credits")}
	runIncremental(t, store, vec, asker, briefStub(), DefaultOptions())
	state, err := LoadScanState(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Subjects) != 0 || len(state.Meetings) != 0 {
		t.Errorf("an outage marked %d documents and %d meetings considered", len(state.Subjects), len(state.Meetings))
	}
}

func TestIncrementalReconsidersAnEditedDocument(t *testing.T) {
	store, base, vec := incrementalStores(t)
	asker := &scriptedAsker{p: map[string]float64{"Retry cap": 0.9}}
	runIncremental(t, store, vec, asker, briefStub(), DefaultOptions())

	// Re-indexing with new text changes the document's hash.
	indexMarkdown(t, base, "docs/retry.md", retryDoc+"\n## Budget\n\nA shared budget per client.\n",
		map[string]string{graph.PropContentHash: "sha256:edited"})
	rep := runIncremental(t, store, vec, asker, briefStub(), DefaultOptions())
	if rep.SubjectsConsidered != 1 {
		t.Errorf("an edited document was not considered again (%d)", rep.SubjectsConsidered)
	}
}

// TestIncrementalWaitsForVectors covers a project with no vector index:
// without the meaning signal nothing useful can be proposed, so nothing is
// judged and nothing is marked considered.
func TestIncrementalWaitsForVectors(t *testing.T) {
	store, _ := incrementalStore(t)
	asker := &scriptedAsker{p: map[string]float64{"Retry cap": 0.9}}
	rep := runIncremental(t, store, fakeVectors{}, asker, briefStub(), DefaultOptions())
	if !rep.NoVectors || asker.calls != 0 {
		t.Errorf("without vectors: NoVectors=%v, %d judge calls", rep.NoVectors, asker.calls)
	}
	state, err := LoadScanState(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Subjects)+len(state.Meetings) != 0 {
		t.Error("something was marked considered without vectors")
	}
}

// TestIncrementalBoundsWhatOneRunConsiders covers a corpus that existed
// before relations were turned on: it is worked through a bounded batch at a
// time, newest first, rather than in one run.
func TestIncrementalBoundsWhatOneRunConsiders(t *testing.T) {
	store, base, vec := incrementalStores(t)
	for i := 0; i < 120; i++ {
		path := fmt.Sprintf("docs/spec-%03d.md", i)
		indexMarkdown(t, base, path, strings.Replace(retryDoc, "# Retry budget design", fmt.Sprintf("# Spec %d", i), 1), nil)
		vec[graph.NewNodeID(string(graph.NodeDocument), path, "section:Limits")] = []float32{0, 1, 0}
	}
	asker := &scriptedAsker{}
	o := IncrementalOptions{MaxSubjects: 50, MaxMeetings: 25}
	writer := NewBriefWriter(briefStub(), nil, spokenStub{}, nil, BriefOptions{}, func() time.Time { return day })
	seen := 0
	for run := 1; run <= 4; run++ {
		// Pair caps wide enough that nothing is cut, so only the bound on
		// documents per run is in play.
		wide := Options{K: 5, MaxPerSubject: 1000, MaxPerMeeting: 1000, MaxPairs: 1000}
		rep, err := Incremental(context.Background(), store, vec, NewJudge(asker, spokenStub{}), writer, nil, wide, 0.8, o)
		if err != nil {
			t.Fatal(err)
		}
		if rep.SubjectsConsidered > o.MaxSubjects {
			t.Fatalf("run %d considered %d documents, over the bound of %d", run, rep.SubjectsConsidered, o.MaxSubjects)
		}
		seen += rep.SubjectsConsidered
	}
	if seen != 121 {
		t.Errorf("four runs considered %d documents, want all 121 once each", seen)
	}
}

// TestIncrementalDropsWhatTheRelevanceCapsLeave covers the per-document and
// per-meeting caps: they pick the best few candidates, and the rest are not
// retried on every later run.
func TestIncrementalDropsWhatTheRelevanceCapsLeave(t *testing.T) {
	store, vec := incrementalStore(t)
	vec["g2"] = []float32{0.9, 0.1, 0}
	asker := &scriptedAsker{p: map[string]float64{"Retry cap": 0.9}}
	one := Options{K: 5, MaxPerSubject: 1, MaxPerMeeting: 8, MaxPairs: 200}

	rep := runIncremental(t, store, vec, asker, briefStub(), one)
	if rep.Judged.Judged != 1 || rep.Proposal.CutByCaps != 1 {
		t.Fatalf("judged %d, cut %d; want the best pair judged and the other cut", rep.Judged.Judged, rep.Proposal.CutByCaps)
	}
	calls := asker.calls
	rep = runIncremental(t, store, vec, asker, briefStub(), one)
	if asker.calls != calls || rep.SubjectsConsidered+rep.MeetingsConsidered != 0 {
		t.Errorf("the pair the cap dropped was retried: %d more calls, %d+%d considered",
			asker.calls-calls, rep.SubjectsConsidered, rep.MeetingsConsidered)
	}
}

// TestSelectFairlyGivesEachSideItsShare covers pending meetings whose
// candidates all score below those of pending documents: they still get half
// the per-run cap, rather than waiting behind documents on every run.
func TestSelectFairlyGivesEachSideItsShare(t *testing.T) {
	subjects, meetings := corpus()
	p := NewProposer(subjects, meetings, nil, Options{K: 5, MaxPerSubject: 8, MaxPerMeeting: 8, MaxPairs: 4})
	var cands []*Candidate
	forMeeting := map[string]bool{}
	for _, s := range subjects {
		cands = append(cands, &Candidate{Subject: s, Meeting: meetings["s-plat"], Signals: SignalSaid, Cosine: 0.9})
		low := &Candidate{Subject: s, Meeting: meetings["s-fin"], Signals: SignalSaid, Cosine: 0.4}
		forMeeting[low.Key()] = true
		cands = append(cands, low)
	}
	prop := selectFairly(p, cands, forMeeting, nil)
	fromMeetings := 0
	for _, c := range prop.Candidates {
		if forMeeting[c.Key()] {
			fromMeetings++
		}
	}
	if len(prop.Candidates) != 4 || fromMeetings != 2 || len(prop.Deferred) != 2 {
		t.Errorf("selected %d (%d for the meeting), deferred %d; want 4 (2), 2", len(prop.Candidates), fromMeetings, len(prop.Deferred))
	}
}
