package embedded

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// scopedStore opens a store pinned to one scope.
func scopedStore(t *testing.T, dir, scope string) *BranchStore {
	t.Helper()
	store, err := NewBranchStore(filepath.Join(dir, "graph.db"), scope, []string{scope})
	if err != nil {
		t.Fatalf("open store in scope %q: %v", scope, err)
	}
	return store
}

func TestRescopeMovesDataBetweenScopes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Write a small graph under a branch, as an older version would have.
	store := scopedStore(t, dir, "some-branch")
	meeting := &graph.Node{
		ID:            graph.NewNodeID(string(graph.NodeMeeting), "/s/session.json", "sess-1"),
		Type:          graph.NodeMeeting,
		Name:          "Planning",
		QualifiedName: "sess-1",
		FilePath:      "/s/session.json",
	}
	person := &graph.Node{
		ID:   graph.NewNodeID(string(graph.NodePerson), "", "Mona"),
		Type: graph.NodePerson, Name: "Mona", QualifiedName: "Mona",
	}
	for _, n := range []*graph.Node{meeting, person} {
		if err := store.AddNode(ctx, n); err != nil {
			t.Fatalf("add node: %v", err)
		}
	}
	if err := store.AddEdge(ctx, &graph.Edge{
		ID:       graph.NewNodeID("edge", person.ID, meeting.ID+":Attended"),
		Type:     graph.EdgeAttended,
		SourceID: person.ID,
		TargetID: meeting.ID,
	}); err != nil {
		t.Fatalf("add edge: %v", err)
	}
	store.Close()

	// A store pinned to the meeting scope sees nothing yet — which is the bug
	// this migration exists to repair.
	scoped := scopedStore(t, dir, MeetingScope)
	before, err := scoped.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeMeeting})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("meeting scope already had %d meetings", len(before))
	}

	res, err := scoped.Rescope("some-branch", MeetingScope, false)
	if err != nil {
		t.Fatalf("rescope: %v", err)
	}
	if res.Keys == 0 {
		t.Fatal("rescope moved nothing")
	}

	after, err := scoped.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeMeeting})
	if err != nil {
		t.Fatalf("query after: %v", err)
	}
	if len(after) != 1 || after[0].Name != "Planning" {
		t.Fatalf("meetings after migration = %+v, want the one meeting", after)
	}

	// Edges and their indexes must come across too, or the graph arrives
	// without its relationships.
	people, err := scoped.GetNeighbors(ctx, meeting.ID, graph.EdgeAttended, graph.Incoming)
	if err != nil {
		t.Fatalf("neighbors: %v", err)
	}
	if len(people) != 1 || people[0].Name != "Mona" {
		t.Errorf("attendance did not survive the move: %+v", people)
	}
	scoped.Close()

	// The originals are gone, so the data exists in exactly one place.
	old := scopedStore(t, dir, "some-branch")
	defer old.Close()
	left, err := old.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeMeeting})
	if err != nil {
		t.Fatalf("query old scope: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("%d meetings left behind in the old scope", len(left))
	}
}

func TestRescopeIsSafeToRepeat(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	store := scopedStore(t, dir, "branch-a")
	if err := store.AddNode(ctx, &graph.Node{
		ID:   graph.NewNodeID(string(graph.NodeTopic), "", "OAuth"),
		Type: graph.NodeTopic, Name: "OAuth", QualifiedName: "OAuth",
	}); err != nil {
		t.Fatalf("add node: %v", err)
	}
	store.Close()

	scoped := scopedStore(t, dir, MeetingScope)
	defer scoped.Close()

	if _, err := scoped.Rescope("branch-a", MeetingScope, false); err != nil {
		t.Fatalf("first rescope: %v", err)
	}
	// A second run finds nothing left and must not disturb what moved.
	if _, err := scoped.Rescope("branch-a", MeetingScope, false); err != nil {
		t.Fatalf("second rescope: %v", err)
	}

	topics, err := scoped.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeTopic})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(topics) != 1 {
		t.Errorf("topic count = %d after two migrations, want 1", len(topics))
	}
}

func TestRescopeDryRunChangesNothing(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	store := scopedStore(t, dir, "branch-a")
	if err := store.AddNode(ctx, &graph.Node{
		ID:   graph.NewNodeID(string(graph.NodeTopic), "", "OAuth"),
		Type: graph.NodeTopic, Name: "OAuth", QualifiedName: "OAuth",
	}); err != nil {
		t.Fatalf("add node: %v", err)
	}
	store.Close()

	scoped := scopedStore(t, dir, MeetingScope)
	defer scoped.Close()

	res, err := scoped.Rescope("branch-a", MeetingScope, true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if res.Keys == 0 {
		t.Error("dry run reported nothing to move")
	}
	topics, _ := scoped.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeTopic})
	if len(topics) != 0 {
		t.Errorf("dry run moved %d topics; it must change nothing", len(topics))
	}
}

func TestRescopeRejectsBadArguments(t *testing.T) {
	dir := t.TempDir()
	store := scopedStore(t, dir, MeetingScope)
	defer store.Close()

	if _, err := store.Rescope("", MeetingScope, false); err == nil {
		t.Error("expected an error when the source scope is empty")
	}
	// Moving a scope onto itself is a no-op, not an error.
	res, err := store.Rescope(MeetingScope, MeetingScope, false)
	if err != nil {
		t.Errorf("same-scope move returned an error: %v", err)
	}
	if res.Keys != 0 {
		t.Errorf("same-scope move reported %d keys", res.Keys)
	}
}
