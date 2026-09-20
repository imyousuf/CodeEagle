package transcript

import (
	"context"
	"strconv"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// relate writes a judged pair the way the relater does.
func relate(t *testing.T, store graph.Store, a, b string, probability float64) {
	t.Helper()
	ida, idb := topicID(a), topicID(b)
	if ida > idb {
		ida, idb = idb, ida
	}
	err := store.AddEdge(context.Background(), &graph.Edge{
		ID: graph.NewNodeID("edge", ida, idb+":"+string(graph.EdgeRelatedTo)), Type: graph.EdgeRelatedTo,
		SourceID: ida, TargetID: idb,
		Properties: map[string]string{PropRelatedProbability: strconv.FormatFloat(probability, 'f', 2, 64)},
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestFindMeetingsReachesRelatedTopics covers the failure the adjacency
// graph exists for: a search for "AGI" and a meeting filed under a label
// with none of the words in it.
func TestFindMeetingsReachesRelatedTopics(t *testing.T) {
	ctx := context.Background()
	store := searchFixture(t) // "schema migration" and "AGI feasibility"
	people, _ := LoadPersonRegistry(ctx, store)
	w := NewWriter(store, people, WriterOptions{MinConfidence: 0.7, Owner: "Imran Yousuf"})
	if _, err := w.Write(ctx, meetingFiledUnder("/tmp/sessions/ghi/session.json", "session-ghi", "Capacity planning review", "capacity planning")); err != nil {
		t.Fatal(err)
	}
	relate(t, store, "AGI feasibility", "schema migration", 0.86)
	relate(t, store, "schema migration", "capacity planning", 0.7)

	find := func(breadth Breadth) *Found {
		found, err := FindMeetings(ctx, store, Query{Text: "AGI", Breadth: breadth})
		if err != nil {
			t.Fatal(err)
		}
		return found
	}

	// By default one edge is followed, and the meeting it reaches ranks
	// below the one whose own record matched.
	found := find("")
	if got := titles(found); len(got) != 2 || got[0] != "AGI feasibility debate with the platform team" || got[1] != "Schema migration status" {
		t.Fatalf("default: %v", got)
	}
	if found.Expanded != 1 || found.Complete != 1 {
		t.Errorf("expanded = %d, complete = %d; want 1 and 1", found.Expanded, found.Complete)
	}
	if found.Hits[1].Score >= found.Hits[0].Score {
		t.Errorf("a meeting reached through an edge scored %v against the seed's %v", found.Hits[1].Score, found.Hits[0].Score)
	}
	reached := found.Hits[1]
	if !reached.OnlyRelated() || len(reached.Matches) != 1 {
		t.Fatalf("reached hit matches = %+v", reached.Matches)
	}
	if m := reached.Matches[0]; m.Kind != MatchTopic || m.Label != "schema migration" || m.Via != "AGI feasibility ~ schema migration" || m.Probability != 0.86 || m.Weight != 0.86*relatedHopDecay {
		t.Errorf("match = %+v", m)
	}
	if reached.CoversAll(found.Terms) {
		t.Errorf("a meeting that never said the word is reported as covering it")
	}

	// none follows nothing; narrow admits 0.86; wide takes the second hop
	// at 0.7 that default and narrow refuse.
	if got := titles(find(BreadthNone)); len(got) != 1 {
		t.Errorf("none: %v", got)
	}
	if got := titles(find(BreadthNarrow)); len(got) != 2 {
		t.Errorf("narrow: %v", got)
	}
	wide := find(BreadthWide)
	if got := titles(wide); len(got) != 3 || got[2] != "Capacity planning review" {
		t.Fatalf("wide: %v", got)
	}
	far := wide.Hits[2].Matches[0]
	if far.Via != "AGI feasibility ~ schema migration ~ capacity planning" || far.Probability < 0.6 || far.Probability > 0.61 {
		t.Errorf("two-hop match = %+v", far)
	}
	if wide.Hits[2].Score >= wide.Hits[1].Score {
		t.Errorf("two hops scored %v against one hop's %v", wide.Hits[2].Score, wide.Hits[1].Score)
	}

	// An edge below the gate is not followed, and a label the words match
	// keeps full credit however it is also reached.
	relate(t, store, "AGI feasibility", "schema migration", 0.55)
	if got := titles(find("")); len(got) != 1 {
		t.Errorf("default at 0.55: %v", got)
	}
	found, err := FindMeetings(ctx, store, Query{Text: "migration", Breadth: BreadthWide})
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(found); len(got) != 3 || got[0] != "Schema migration status" || found.Hits[0].Score <= found.Hits[1].Score {
		t.Errorf("migration wide: %v (scores %v, %v)", got, found.Hits[0].Score, found.Hits[1].Score)
	}

	if _, err := FindMeetings(ctx, store, Query{Text: "AGI", Breadth: "everything"}); err == nil {
		t.Error("an unknown breadth was accepted")
	}
}
