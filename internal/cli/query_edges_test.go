package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

// TestResolveNodeRefRefusesToGuess covers the "AI strategy" failure: a topic
// and two meeting segments share the name, and answering with the first
// gave a confident-looking result for the wrong node.
func TestResolveNodeRefRefusesToGuess(t *testing.T) {
	store, err := embedded.NewStore(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()

	topic := &graph.Node{ID: "t1", Type: graph.NodeTopic, Name: "AI strategy"}
	seg1 := &graph.Node{ID: "s1", Type: graph.NodeTopicSegment, Name: "AI strategy", FilePath: "/s/1/session.json"}
	seg2 := &graph.Node{ID: "s2", Type: graph.NodeTopicSegment, Name: "AI strategy", FilePath: "/s/2/session.json"}
	fn := &graph.Node{ID: "f1", Type: graph.NodeFunction, Name: "Plan", Package: "strategy"}
	for _, n := range []*graph.Node{topic, seg1, seg2, fn} {
		if err := store.AddNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}

	_, err = resolveNodeRef(ctx, store, "AI strategy", "", "")
	if err == nil {
		t.Fatal("three nodes named alike resolved to one")
	}
	for _, want := range []string{"3 nodes", "t1", "s1", "s2", "Topic", "TopicSegment", "--node-type"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}

	// Narrowing by type, or naming the id, resolves it.
	if got, err := resolveNodeRef(ctx, store, "AI strategy", "", "Topic"); err != nil || got.ID != "t1" {
		t.Errorf("by node type = %v, %v", got, err)
	}
	if got, err := resolveNodeRef(ctx, store, "s2", "", ""); err != nil || got.ID != "s2" {
		t.Errorf("by id = %v, %v", got, err)
	}
	if got, err := resolveNodeRef(ctx, store, "Plan", "strategy", ""); err != nil || got.ID != "f1" {
		t.Errorf("by package = %v, %v", got, err)
	}
	if _, err := resolveNodeRef(ctx, store, "nothing", "", ""); err == nil {
		t.Error("an unknown name resolved")
	}
}
