package vectorstore

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand"
	"path/filepath"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// TestSearchWidensPastStaleVectors: a vector whose node has left the graph
// used to be skipped without replacement, so an index a third behind the
// graph returned a third fewer results than asked for — and nothing said so.
func TestSearchWidensPastStaleVectors(t *testing.T) {
	vs, graphStore, _ := setupTestVectorStore(t)
	ctx := context.Background()

	var nodes []*graph.Node
	for i := range 12 {
		n := &graph.Node{
			ID: fmt.Sprintf("n%d", i), Type: graph.NodeFunction,
			Name:       fmt.Sprintf("Handler%d", i),
			DocComment: fmt.Sprintf("handles request kind %d for the payments service", i),
		}
		nodes = append(nodes, n)
		if err := graphStore.AddNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	if err := vs.IndexNodes(ctx, nodes); err != nil {
		t.Fatal(err)
	}

	// Half the nodes leave the graph; their vectors stay behind.
	for i := 0; i < 12; i += 2 {
		if err := graphStore.DeleteNode(ctx, nodes[i].ID); err != nil {
			t.Fatal(err)
		}
	}

	results, err := vs.Search(ctx, "payments request handler", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 4 {
		t.Fatalf("got %d results, want the 4 asked for despite stale vectors", len(results))
	}
	for _, r := range results {
		if _, err := graphStore.GetNode(ctx, r.Node.ID); err != nil {
			t.Errorf("result %s is not in the graph", r.Node.ID)
		}
	}
	if vs.StaleInLastSearch() == 0 {
		t.Error("stale vectors were skipped without being counted")
	}

	stale, missing, err := vs.Staleness(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stale != 6 || missing != 0 {
		t.Errorf("Staleness = %d stale, %d missing; want 6 and 0", stale, missing)
	}

	// A node added to the graph but never embedded is the other half of
	// being behind.
	late := &graph.Node{ID: "late", Type: graph.NodeFunction, Name: "Late", DocComment: "arrived after the index was built"}
	if err := graphStore.AddNode(ctx, late); err != nil {
		t.Fatal(err)
	}
	if _, missing, err = vs.Staleness(ctx); err != nil || missing != 1 {
		t.Errorf("missing = %d, %v; want 1", missing, err)
	}
}

// randomEmbedder gives every distinct text its own direction, seeded by the
// text, so the index has the connectivity a real embedding space has. The
// hash-based mock bunches texts together and the walk stalls after a few
// hops, which says nothing about ef.
type randomEmbedder struct{ dims int }

func (r *randomEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		h := fnv.New64a()
		h.Write([]byte(text))
		rng := rand.New(rand.NewSource(int64(h.Sum64())))
		vec := make([]float32, r.dims)
		var norm float64
		for j := range vec {
			vec[j] = float32(rng.NormFloat64())
			norm += float64(vec[j] * vec[j])
		}
		for j := range vec {
			vec[j] /= float32(math.Sqrt(norm))
		}
		out[i] = vec
	}
	return out, nil
}

func (r *randomEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	v, err := r.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return v[0], nil
}
func (r *randomEmbedder) Dimensions() int   { return r.dims }
func (r *randomEmbedder) Name() string      { return "random" }
func (r *randomEmbedder) ModelName() string { return "random-embed" }

// TestSearchAsksForMoreThanEf: the candidate heap is bounded by ef, so a
// request for more results than that must widen the walk or the tail is
// junk. The widened walk reaches at least as far as the default one, and
// the setting is put back afterwards.
func TestSearchAsksForMoreThanEf(t *testing.T) {
	dir := t.TempDir()
	graphStore := newMockGraphStore()
	embedder := &randomEmbedder{dims: 32}
	vs, err := New(graphStore, embedder, "test", filepath.Join(dir, "vec.idx"), filepath.Join(dir, "vec.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { vs.Close() })
	ctx := context.Background()

	const n = hnswEf * 3
	var nodes []*graph.Node
	for i := range n {
		node := &graph.Node{
			ID: fmt.Sprintf("n%d", i), Type: graph.NodeFunction,
			Name:       fmt.Sprintf("Fn%d", i),
			DocComment: fmt.Sprintf("function number %d doing thing %d", i, i*7),
		}
		nodes = append(nodes, node)
		if err := graphStore.AddNode(ctx, node); err != nil {
			t.Fatal(err)
		}
	}
	if err := vs.IndexNodes(ctx, nodes); err != nil {
		t.Fatal(err)
	}
	q, err := embedder.EmbedQuery(ctx, "function doing thing")
	if err != nil {
		t.Fatal(err)
	}

	narrow := len(vs.idx.Search(q, n)) // ef left at its default
	wide := len(vs.searchLocked(q, n))
	if wide < narrow || wide <= hnswEf {
		t.Errorf("widened search returned %d results, default %d; want at least as many and more than ef=%d",
			wide, narrow, hnswEf)
	}
	if got := vs.idx.EfSearch; got != hnswEf {
		t.Errorf("EfSearch left at %d, want it restored to %d", got, hnswEf)
	}
}
