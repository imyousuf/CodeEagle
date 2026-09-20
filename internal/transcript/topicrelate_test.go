package transcript

import (
	"context"
	"fmt"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/pkg/jev"
)

// meetingFiledUnder builds an enriched session filed under the given topics.
func meetingFiledUnder(path, id, title string, topics ...string) *Result {
	res := sampleResult(path)
	res.Session.ID = id
	res.Analysis = &Analysis{Title: title, Summary: "About " + title + "."}
	for i, name := range topics {
		res.Analysis.Topics = append(res.Analysis.Topics, Topic{
			Name: name, Summary: "What was said about " + name + ".",
			StartTime: float64(i * 60), EndTime: float64(i*60 + 60),
		})
	}
	return res
}

func topicID(name string) string { return graph.NewNodeID(string(graph.NodeTopic), "", name) }

// unitVectors places labels on axes so that nearest-neighbour proposals are
// predictable: "release notes" is nearest to "schema migration".
func unitVectors(id string) ([]float32, bool) {
	switch id {
	case topicID("schema migration"):
		return []float32{1, 0, 0}, true
	case topicID("client rollout"):
		return []float32{0, 1, 0}, true
	case topicID("release notes"):
		return []float32{0.9, 0.1, 0}, true
	}
	return nil, false
}

func TestRelaterJudgesEachMeetingsPairsOnceAsWritten(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	people, err := LoadPersonRegistry(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	asker := &scriptedTopicAsker{
		related: map[string]float64{labelsKey("schema migration", "client rollout"): 0.9},
		same:    map[string]float64{labelsKey("schema migration", "client rollout"): 0.2},
	}
	relater := NewTopicRelater(store, asker, RelaterOptions{NearestK: 1, Vectors: unitVectors})
	w := NewWriter(store, people, WriterOptions{MinConfidence: 0.7, Owner: "Imran Yousuf"}).WithRelater(relater)

	st, err := w.Write(ctx, meetingFiledUnder("/tmp/s/1/session.json", "session-1", "Migration status", "schema migration", "client rollout"))
	if err != nil {
		t.Fatal(err)
	}
	// One pair: the two topics share the meeting, and each is the other's
	// nearest label, which is the same pair proposed twice.
	if st.RelatedPairs != 1 || asker.calls != 1 {
		t.Fatalf("first meeting: %d pairs judged in %d calls, want 1 in 1", st.RelatedPairs, asker.calls)
	}
	related, err := RelatedTopics(ctx, store, topicID("schema migration"), DefaultRelatedGate)
	if err != nil {
		t.Fatal(err)
	}
	if len(related) != 1 || related[0].Topic.Name != "client rollout" || related[0].Probability != 0.9 || related[0].Same != 0.2 {
		t.Fatalf("related = %+v", related)
	}
	edges, _ := store.GetEdges(ctx, topicID("client rollout"), graph.EdgeRelatedTo)
	if len(edges) != 1 || edges[0].Properties[propRelatedSources] != "nearest+same-meeting" || edges[0].Properties[propRelatedJudge] != "scripted" {
		t.Errorf("edge = %+v", edges[0])
	}
	if edges[0].SourceID > edges[0].TargetID {
		t.Errorf("edge not in canonical order: %s -> %s", edges[0].SourceID, edges[0].TargetID)
	}

	// A second meeting reuses one topic and adds one. Its own pair is new;
	// the new topic's nearest label (schema migration) is proposed; the
	// pair already judged is not asked again.
	st, err = w.Write(ctx, meetingFiledUnder("/tmp/s/2/session.json", "session-2", "Release planning", "client rollout", "release notes"))
	if err != nil {
		t.Fatal(err)
	}
	if st.RelatedPairs != 2 || asker.calls != 2 {
		t.Fatalf("second meeting: %d pairs judged in %d calls, want 2 in 1", st.RelatedPairs, asker.calls-1)
	}
	all, err := RelatedTopics(ctx, store, topicID("release notes"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("release notes has %d judged neighbours, want 2: %+v", len(all), all)
	}
	// Both came back unrelated, so nothing clears the gate.
	if gated, _ := RelatedTopics(ctx, store, topicID("release notes"), WideRelatedGate); len(gated) != 0 {
		t.Errorf("unrelated pairs cleared the gate: %+v", gated)
	}

	// Everything proposed has been judged, so the bulk pass has nothing
	// to do and costs nothing.
	pending, _, err := relater.Pending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Errorf("pending = %d, want 0", pending)
	}
	if _, err := relater.RelateAll(ctx, 0); err != nil || asker.calls != 2 {
		t.Errorf("bulk pass made %d further calls (err %v)", asker.calls-2, err)
	}
	if total := relater.Stats(); total.Pairs != 3 || total.Related != 1 || total.Judging.Requests != 2 {
		t.Errorf("stats = %+v", total)
	}
}

func TestRelaterBulkPassJudgesOnlyWhatIsUnjudged(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	people, err := LoadPersonRegistry(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	// Indexed before relating existed: no edges yet.
	w := NewWriter(store, people, WriterOptions{MinConfidence: 0.7, Owner: "Imran Yousuf"})
	for i, topics := range [][]string{{"schema migration", "client rollout"}, {"client rollout", "release notes"}} {
		res := meetingFiledUnder(fmt.Sprintf("/tmp/s/%d/session.json", i), fmt.Sprintf("session-%d", i), "Meeting", topics...)
		if _, err := w.Write(ctx, res); err != nil {
			t.Fatal(err)
		}
	}
	asker := &scriptedTopicAsker{related: map[string]float64{labelsKey("schema migration", "release notes"): 0.7}}
	relater := NewTopicRelater(store, asker, RelaterOptions{NearestK: 1, Vectors: unitVectors, Concurrency: 2})

	// Two same-meeting pairs, plus release notes' nearest label.
	pending, tokens, err := relater.Pending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pending != 3 || tokens != 3*approxTokensPerPair {
		t.Fatalf("pending = %d (%d tokens), want 3", pending, tokens)
	}
	st, err := relater.RelateAll(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if st.Pairs != 1 {
		t.Fatalf("limited pass judged %d", st.Pairs)
	}
	if pending, _, _ = relater.Pending(ctx); pending != 2 {
		t.Errorf("pending after one = %d, want 2", pending)
	}
	if st, err = relater.RelateAll(ctx, 0); err != nil || st.Pairs != 2 {
		t.Fatalf("rest of the pass: %d pairs, err %v", st.Pairs, err)
	}
	related, _ := RelatedTopics(ctx, store, topicID("release notes"), DefaultRelatedGate)
	if len(related) != 1 || related[0].Topic.Name != "schema migration" {
		t.Errorf("related = %+v", related)
	}
	// A fresh relater reads the edges back rather than asking again.
	again := NewTopicRelater(store, asker, RelaterOptions{NearestK: 1, Vectors: unitVectors})
	if pending, _, _ = again.Pending(ctx); pending != 0 {
		t.Errorf("a new relater found %d unjudged pairs", pending)
	}
}

func TestRelaterSurvivesAJudgeOutage(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	people, _ := LoadPersonRegistry(ctx, store)
	var logged []string
	relater := NewTopicRelater(store, failingAsker{}, RelaterOptions{
		Log: func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
	})
	w := NewWriter(store, people, WriterOptions{MinConfidence: 0.7, Owner: "Imran Yousuf"}).WithRelater(relater)
	st, err := w.Write(ctx, meetingFiledUnder("/tmp/s/1/session.json", "session-1", "Meeting", "schema migration", "client rollout"))
	if err != nil {
		t.Fatalf("a judge outage failed the meeting: %v", err)
	}
	if st.Meetings != 1 || st.Topics != 2 || st.RelatedPairs != 0 {
		t.Errorf("stats = %+v", st)
	}
	if len(logged) != 1 {
		t.Errorf("logged %v, want one warning", logged)
	}
}

type failingAsker struct{}

func (failingAsker) Ask(context.Context, any, jev.Questions) (*jev.Response, error) {
	return nil, fmt.Errorf("service down")
}

func TestCandidatesSkipLargeFamilies(t *testing.T) {
	tp := &TopicProfiles{byID: map[string]*TopicProfile{}, cooccur: map[pairKey]int{}, adjacent: map[pairKey]int{}}
	add := func(name, parent string) *TopicProfile {
		p := profileNamed(name, 1)
		p.Parents = []string{parent}
		tp.byID[p.ID()] = p
		return p
	}
	for i := range maxSiblingFamily + 1 {
		add(fmt.Sprintf("broad child %d", i), "broad")
	}
	a, b := add("narrow child a", "narrow"), add("narrow child b", "narrow")
	pairs := tp.Candidates(0)
	if len(pairs) != 1 || pairs[0].Sources != SourceSharedParent {
		t.Fatalf("candidates = %d, want only the narrow family's pair", len(pairs))
	}
	if got := tp.siblings(a); len(got) != 1 || got[0] != b {
		t.Errorf("siblings of a = %v", got)
	}
	if got := tp.siblings(tp.byID[topicID("broad child 0")]); len(got) != 0 {
		t.Errorf("a member of a family of %d proposed %d siblings", maxSiblingFamily+1, len(got))
	}
}
