package agents

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

// meetingFixture builds a small graph with one meeting, two participants, a
// topic, a decision, and two follow-ups.
func meetingFixture(t *testing.T) graph.Store {
	t.Helper()
	store, err := embedded.NewStore(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	ctx := context.Background()
	when := time.Date(2026, 3, 11, 10, 0, 0, 0, time.UTC)

	add := func(n *graph.Node) *graph.Node {
		if err := store.AddNode(ctx, n); err != nil {
			t.Fatalf("add node %s: %v", n.Name, err)
		}
		return n
	}
	link := func(typ graph.EdgeType, from, to string, props map[string]string) {
		e := &graph.Edge{
			ID:         graph.NewNodeID("edge", from, to+":"+string(typ)),
			Type:       typ,
			SourceID:   from,
			TargetID:   to,
			Properties: props,
		}
		if err := store.AddEdge(ctx, e); err != nil {
			t.Fatalf("add edge %s: %v", typ, err)
		}
	}

	path := "/sessions/abc/session.json"
	meeting := add(&graph.Node{
		ID:            graph.NewNodeID(string(graph.NodeMeeting), path, "sess-1"),
		Type:          graph.NodeMeeting,
		Name:          "RAG retrieval status and Q2 planning",
		QualifiedName: "sess-1",
		FilePath:      path,
		UpdatedAt:     when,
		Properties: map[string]string{
			graph.PropMeetingID: "sess-1",
			graph.PropDuration:  "1830",
			graph.PropSummary:   "Reviewed retrieval quality and agreed the Q2 plan.",
			"mentions":          "rag-service",
		},
	})

	imran := add(&graph.Node{
		ID: graph.NewNodeID(string(graph.NodePerson), "", "Imran Yousuf"), Type: graph.NodePerson,
		Name: "Imran Yousuf", QualifiedName: "Imran Yousuf",
		Properties: map[string]string{graph.PropIsOwner: "true", graph.PropAliases: "Imron"},
	})
	jeremiah := add(&graph.Node{
		ID: graph.NewNodeID(string(graph.NodePerson), "", "Jeremiah"), Type: graph.NodePerson,
		Name: "Jeremiah", QualifiedName: "Jeremiah",
	})

	you := add(&graph.Node{
		ID: graph.NewNodeID(string(graph.NodeSpeaker), path, "You"), Type: graph.NodeSpeaker,
		Name: "You", FilePath: path, UpdatedAt: when,
		Properties: map[string]string{graph.PropSpeakingSeconds: "900", graph.PropMeetingID: "sess-1"},
	})
	p1 := add(&graph.Node{
		ID: graph.NewNodeID(string(graph.NodeSpeaker), path, "Person 1"), Type: graph.NodeSpeaker,
		Name: "Person 1", FilePath: path, UpdatedAt: when,
		Properties: map[string]string{graph.PropSpeakingSeconds: "600", graph.PropMeetingID: "sess-1"},
	})
	// A third voice nobody could place — it must still appear as a participant.
	p2 := add(&graph.Node{
		ID: graph.NewNodeID(string(graph.NodeSpeaker), path, "Person 2"), Type: graph.NodeSpeaker,
		Name: "Person 2", FilePath: path, UpdatedAt: when,
		Properties: map[string]string{graph.PropSpeakingSeconds: "60", graph.PropMeetingID: "sess-1"},
	})

	link(graph.EdgeContains, meeting.ID, you.ID, nil)
	link(graph.EdgeContains, meeting.ID, p1.ID, nil)
	link(graph.EdgeContains, meeting.ID, p2.ID, nil)
	link(graph.EdgeIdentifiedAs, you.ID, imran.ID, map[string]string{graph.PropResolution: "owner_anchor"})
	link(graph.EdgeIdentifiedAs, p1.ID, jeremiah.ID, map[string]string{graph.PropResolution: "llm"})
	link(graph.EdgeAttended, imran.ID, meeting.ID, map[string]string{graph.PropSpeakingSeconds: "900"})
	link(graph.EdgeAttended, jeremiah.ID, meeting.ID, map[string]string{graph.PropSpeakingSeconds: "600"})

	segment := add(&graph.Node{
		ID: graph.NewNodeID(string(graph.NodeTopicSegment), path, "sess-1:retrieval"), Type: graph.NodeTopicSegment,
		Name: "retrieval quality", FilePath: path, UpdatedAt: when,
		DocComment: "Recall was the bottleneck, not ranking.",
		Properties: map[string]string{
			graph.PropSummary:   "Recall was the bottleneck, not ranking.",
			graph.PropStartTime: "60", graph.PropEndTime: "600",
			"keywords": "rag, embeddings", graph.PropMeetingID: "sess-1",
		},
	})
	link(graph.EdgeContains, meeting.ID, segment.ID, nil)

	decision := add(&graph.Node{
		ID: graph.NewNodeID(string(graph.NodeDecision), path, "sess-1:decision:0"), Type: graph.NodeDecision,
		Name: "Switch to hybrid retrieval", FilePath: path, UpdatedAt: when,
		Properties: map[string]string{
			graph.PropSummary:   "Switch to hybrid retrieval",
			graph.PropQuote:     "let's do hybrid then",
			"quote_verified":    "true",
			"rationale":         "Recall was the bottleneck",
			graph.PropMeetingID: "sess-1",
		},
	})
	link(graph.EdgeContains, meeting.ID, decision.ID, nil)
	link(graph.EdgeRaisedBy, decision.ID, jeremiah.ID, nil)

	// An unverified quote must be reported as such.
	shaky := add(&graph.Node{
		ID: graph.NewNodeID(string(graph.NodeDecision), path, "sess-1:decision:1"), Type: graph.NodeDecision,
		Name: "Defer reranking", FilePath: path, UpdatedAt: when,
		Properties: map[string]string{
			graph.PropSummary:   "Defer reranking",
			graph.PropQuote:     "a quote nobody said",
			"quote_verified":    "false",
			graph.PropMeetingID: "sess-1",
		},
	})
	link(graph.EdgeContains, meeting.ID, shaky.ID, nil)

	owned := add(&graph.Node{
		ID: graph.NewNodeID(string(graph.NodeActionItem), path, "sess-1:action:0"), Type: graph.NodeActionItem,
		Name: "Finalize the Q2 plan", FilePath: path, UpdatedAt: when,
		Properties: map[string]string{
			graph.PropSummary: "Finalize the Q2 plan", graph.PropAssignee: "Jeremiah",
			graph.PropDueDate: "2026-03-20", graph.PropStatus: "open", graph.PropMeetingID: "sess-1",
		},
	})
	link(graph.EdgeContains, meeting.ID, owned.ID, nil)
	link(graph.EdgeAssignedTo, owned.ID, jeremiah.ID, nil)

	orphan := add(&graph.Node{
		ID: graph.NewNodeID(string(graph.NodeActionItem), path, "sess-1:action:1"), Type: graph.NodeActionItem,
		Name: "Investigate embedding drift", FilePath: path, UpdatedAt: when,
		Properties: map[string]string{
			graph.PropSummary: "Investigate embedding drift", graph.PropStatus: "open",
			graph.PropMeetingID: "sess-1",
		},
	})
	link(graph.EdgeContains, meeting.ID, orphan.ID, nil)

	return store
}

func TestNewMeetingToolsOnlyWhenMeetingsExist(t *testing.T) {
	ctx := context.Background()

	empty, err := embedded.NewStore(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer empty.Close()
	if got := NewMeetingTools(ctx, empty); got != nil {
		t.Errorf("got %d tools for an empty graph, want none", len(got))
	}

	if got := NewMeetingTools(ctx, meetingFixture(t)); len(got) != 4 {
		t.Errorf("got %d tools, want 4", len(got))
	}
	if got := NewMeetingTools(ctx, nil); got != nil {
		t.Error("expected no tools for a nil store")
	}
}

func TestQueryMeetingsByTopic(t *testing.T) {
	store := meetingFixture(t)
	tool := &queryMeetingsTool{store: store}

	out, ok := tool.Execute(context.Background(), map[string]any{"topic": "retrieval"})
	if !ok {
		t.Fatalf("query failed: %s", out)
	}
	for _, want := range []string{"RAG retrieval status", "sess-1", "Imran Yousuf", "Jeremiah"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	// A topic nobody discussed must not match.
	if out, ok := tool.Execute(context.Background(), map[string]any{"topic": "kubernetes"}); ok {
		t.Errorf("unexpected match: %s", out)
	}
}

func TestQueryMeetingsByPersonAndDate(t *testing.T) {
	store := meetingFixture(t)
	tool := &queryMeetingsTool{store: store}
	ctx := context.Background()

	if out, ok := tool.Execute(ctx, map[string]any{"person": "Jeremiah"}); !ok {
		t.Errorf("expected a match for Jeremiah: %s", out)
	}
	if out, ok := tool.Execute(ctx, map[string]any{"person": "Nobody"}); ok {
		t.Errorf("unexpected match for an absent person: %s", out)
	}
	// The meeting is in March; a later cutoff must exclude it.
	if out, ok := tool.Execute(ctx, map[string]any{"since": "2026-06-01"}); ok {
		t.Errorf("unexpected match after cutoff: %s", out)
	}
	if out, ok := tool.Execute(ctx, map[string]any{"since": "not-a-date"}); ok {
		t.Errorf("expected an error for a malformed date: %s", out)
	}
}

func TestQueryMeetingDetail(t *testing.T) {
	store := meetingFixture(t)
	tool := &queryMeetingDetailTool{store: store}

	out, ok := tool.Execute(context.Background(), map[string]any{"meeting_id": "sess-1"})
	if !ok {
		t.Fatalf("query failed: %s", out)
	}
	for _, want := range []string{
		"RAG retrieval status",
		"retrieval quality",
		"Recall was the bottleneck",
		"Switch to hybrid retrieval",
		"Finalize the Q2 plan",
		"due 2026-03-20",
		"unassigned",
		// An unplaced voice is still reported rather than hidden.
		"unidentified",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("detail missing %q:\n%s", want, out)
		}
	}

	// The unverified quote must be flagged, not presented as fact.
	if !strings.Contains(out, "NOT found verbatim") {
		t.Errorf("unverified quote was not flagged:\n%s", out)
	}
	if !strings.Contains(out, "verified against the transcript") {
		t.Errorf("verified quote was not marked:\n%s", out)
	}
}

func TestQueryMeetingDetailUnknownID(t *testing.T) {
	tool := &queryMeetingDetailTool{store: meetingFixture(t)}
	if out, ok := tool.Execute(context.Background(), map[string]any{"meeting_id": "nope"}); ok {
		t.Errorf("expected failure: %s", out)
	}
	if out, ok := tool.Execute(context.Background(), map[string]any{}); ok {
		t.Errorf("expected failure with no id: %s", out)
	}
}

func TestQueryPersonActivity(t *testing.T) {
	store := meetingFixture(t)
	tool := &queryPersonActivityTool{store: store}

	out, ok := tool.Execute(context.Background(), map[string]any{"name": "Jeremiah"})
	if !ok {
		t.Fatalf("query failed: %s", out)
	}
	for _, want := range []string{"Jeremiah", "Meetings attended (1)", "Finalize the Q2 plan", "Switch to hybrid retrieval"} {
		if !strings.Contains(out, want) {
			t.Errorf("activity missing %q:\n%s", want, out)
		}
	}
}

func TestQueryPersonActivityResolvesAliases(t *testing.T) {
	store := meetingFixture(t)
	tool := &queryPersonActivityTool{store: store}

	// "Imron" is how the transcriber heard "Imran" — it must resolve.
	out, ok := tool.Execute(context.Background(), map[string]any{"name": "Imron"})
	if !ok {
		t.Fatalf("alias lookup failed: %s", out)
	}
	if !strings.Contains(out, "Imran Yousuf") {
		t.Errorf("alias did not resolve to the person:\n%s", out)
	}
	if !strings.Contains(out, "recordings these are") {
		t.Errorf("owner was not identified as such:\n%s", out)
	}
}

func TestQueryPersonActivityUnknownNameListsKnown(t *testing.T) {
	tool := &queryPersonActivityTool{store: meetingFixture(t)}
	out, ok := tool.Execute(context.Background(), map[string]any{"name": "Nobody"})
	if ok {
		t.Error("expected failure for an unknown person")
	}
	// Failing usefully: tell the agent who it could have asked about.
	if !strings.Contains(out, "Jeremiah") {
		t.Errorf("error should list known people:\n%s", out)
	}
}

func TestQueryActionItems(t *testing.T) {
	store := meetingFixture(t)
	tool := &queryActionItemsTool{store: store}
	ctx := context.Background()

	out, ok := tool.Execute(ctx, nil)
	if !ok {
		t.Fatalf("query failed: %s", out)
	}
	if !strings.Contains(out, "Finalize the Q2 plan") || !strings.Contains(out, "Investigate embedding drift") {
		t.Errorf("expected both follow-ups:\n%s", out)
	}

	out, ok = tool.Execute(ctx, map[string]any{"person": "Jeremiah"})
	if !ok {
		t.Fatalf("owner filter failed: %s", out)
	}
	if strings.Contains(out, "Investigate embedding drift") {
		t.Errorf("owner filter leaked an unowned item:\n%s", out)
	}

	out, ok = tool.Execute(ctx, map[string]any{"unassigned": true})
	if !ok {
		t.Fatalf("unassigned filter failed: %s", out)
	}
	if strings.Contains(out, "Finalize the Q2 plan") {
		t.Errorf("unassigned filter leaked an owned item:\n%s", out)
	}
}

func TestIntArg(t *testing.T) {
	tests := []struct {
		args map[string]any
		want int
	}{
		{map[string]any{"limit": float64(5)}, 5},
		{map[string]any{"limit": 7}, 7},
		{map[string]any{"limit": "9"}, 9},
		{map[string]any{"limit": float64(0)}, 10},
		{map[string]any{"limit": "abc"}, 10},
		{map[string]any{}, 10},
		{nil, 10},
	}
	for _, tt := range tests {
		if got := intArg(tt.args, "limit", 10); got != tt.want {
			t.Errorf("intArg(%v) = %d, want %d", tt.args, got, tt.want)
		}
	}
}
