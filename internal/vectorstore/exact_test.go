package vectorstore

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// TestSearchIsExactBelowTheLimit: at the sizes this tool indexes, every
// vector is compared, so the nearest neighbour is the nearest neighbour.
// The graph walk it replaces returned none of the ten nearest for any query
// on a real 51,000-vector index.
func TestSearchIsExactBelowTheLimit(t *testing.T) {
	dir := t.TempDir()
	graphStore := newMockGraphStore()
	embedder := &randomEmbedder{dims: 32}
	vs, err := New(graphStore, embedder, "test", filepath.Join(dir, "vec.idx"), filepath.Join(dir, "vec.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { vs.Close() })
	ctx := context.Background()

	const n = 500
	var nodes []*graph.Node
	for i := range n {
		node := &graph.Node{
			ID: fmt.Sprintf("n%d", i), Type: graph.NodeFunction,
			Name: fmt.Sprintf("Fn%d", i), DocComment: fmt.Sprintf("text number %d", i),
		}
		nodes = append(nodes, node)
		if err := graphStore.AddNode(ctx, node); err != nil {
			t.Fatal(err)
		}
	}
	if err := vs.IndexNodes(ctx, nodes); err != nil {
		t.Fatal(err)
	}

	// Querying with a node's own embeddable text must return that node
	// first, for every node: random directions make each one distinct.
	for _, want := range []int{0, 7, 123, 499} {
		results, err := vs.Search(ctx, EmbeddableText(nodes[want]), 3)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 3 || results[0].Node.ID != nodes[want].ID {
			t.Errorf("query for n%d: top = %v", want, ids(results))
		}
		if results[0].Score < results[1].Score || results[1].Score < results[2].Score {
			t.Errorf("query for n%d: scores not descending: %v", want, scores(results))
		}
	}

	// Removing a node removes it from what exact search can see.
	if err := vs.RemoveNode(ctx, nodes[7].ID); err != nil {
		t.Fatal(err)
	}
	results, err := vs.Search(ctx, EmbeddableText(nodes[7]), 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Node.ID == nodes[7].ID {
			t.Error("a removed node was returned")
		}
	}
}

func ids(results []SearchResult) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.Node.ID
	}
	return out
}

func scores(results []SearchResult) []float64 {
	out := make([]float64, len(results))
	for i, r := range results {
		out[i] = r.Score
	}
	return out
}
