package vectorstore_test

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/vectorstore"
)

// The black-box counterparts of stale_test.go: the same behaviour, observed
// through the exported API and the saved files only.

// TestSearchFillsTopKPastStaleVectors: a vector whose node has left the graph
// is skipped, and the search widens until it has the number asked for, while
// counting what it skipped.
func TestSearchFillsTopKPastStaleVectors(t *testing.T) {
	ctx := context.Background()
	g := newMemGraph()
	var nodes []*graph.Node
	for i := range 12 {
		n := &graph.Node{
			ID: fmt.Sprintf("n%02d", i), Type: graph.NodeFunction, Name: fmt.Sprintf("Handler%d", i),
			DocComment: fmt.Sprintf("handles request kind %d for the payments service", i),
		}
		nodes = append(nodes, n)
		_ = g.AddNode(ctx, n)
	}
	vs := openStore(t, g, newFakeEmbedder(), newPaths(t))
	if err := vs.IndexNodes(ctx, nodes); err != nil {
		t.Fatal(err)
	}

	// Half the nodes leave the graph; their vectors stay behind.
	for i := 0; i < 12; i += 2 {
		_ = g.DeleteNode(ctx, nodes[i].ID)
	}
	// A query equal to a deleted node's text puts a stale vector first.
	results, err := vs.Search(ctx, vectorstore.EmbeddableText(nodes[0]), 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 4 {
		t.Fatalf("got %d results, want the 4 asked for despite stale vectors", len(results))
	}
	for _, r := range results {
		if _, err := g.GetNode(ctx, r.Node.ID); err != nil {
			t.Errorf("result %s is not in the graph", r.Node.ID)
		}
	}
	if vs.StaleInLastSearch() == 0 {
		t.Error("stale vectors were skipped without being counted")
	}

	// Asking for more than the live vectors returns every live one.
	results, err = vs.Search(ctx, "payments", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 6 || vs.StaleInLastSearch() != 6 {
		t.Errorf("Search(20) = %d results, %d stale; want 6 and 6", len(results), vs.StaleInLastSearch())
	}

	stale, missing, err := vs.Staleness(ctx)
	if err != nil || stale != 6 || missing != 0 {
		t.Errorf("Staleness = %d stale, %d missing, %v; want 6 and 0", stale, missing, err)
	}
	late := &graph.Node{ID: "late", Type: graph.NodeFunction, Name: "Late", DocComment: "arrived after the index was built"}
	_ = g.AddNode(ctx, late)
	if _, missing, err = vs.Staleness(ctx); err != nil || missing != 1 {
		t.Errorf("missing = %d, %v; want 1", missing, err)
	}
}

// TestSearchBeyondEfReturnsTheExactNearest: asking for more results than the
// graph walk's candidate list (ef, 40) returns that many, and they are the
// true nearest, nearest first. The saved index still records ef as 40.
func TestSearchBeyondEfReturnsTheExactNearest(t *testing.T) {
	ctx := context.Background()
	g := newMemGraph()
	emb := newFakeEmbedder()
	emb.dims = 16
	var nodes []*graph.Node
	for i := range 120 {
		n := &graph.Node{
			ID: fmt.Sprintf("n%03d", i), Type: graph.NodeFunction, Name: fmt.Sprintf("Fn%d", i),
			DocComment: fmt.Sprintf("function number %d doing thing %d", i, i*7),
		}
		nodes = append(nodes, n)
		_ = g.AddNode(ctx, n)
	}
	p := newPaths(t)
	vs := openStore(t, g, emb, p)
	if err := vs.IndexNodes(ctx, nodes); err != nil {
		t.Fatal(err)
	}

	const query, k = "function doing thing", 100
	q := emb.vec(query)
	type cand struct {
		id   string
		dist float64
	}
	var all []cand
	for _, n := range nodes {
		v := emb.vec(vectorstore.EmbeddableText(n))
		var dot, qn, vn float64
		for i := range v {
			dot += float64(v[i]) * float64(q[i])
			qn += float64(q[i]) * float64(q[i])
			vn += float64(v[i]) * float64(v[i])
		}
		all = append(all, cand{n.ID, 1 - dot/(math.Sqrt(qn)*math.Sqrt(vn))})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].dist < all[j].dist })
	var want []string
	for _, c := range all[:k] {
		want = append(want, c.id)
	}

	results, err := vs.Search(ctx, query, k)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != k {
		t.Fatalf("Search(%d) returned %d results", k, len(results))
	}
	var got []string
	for i, r := range results {
		got = append(got, r.Node.ID)
		if i > 0 && r.Score > results[i-1].Score+1e-6 {
			t.Errorf("result %d scores %v, above result %d at %v", i, r.Score, i-1, results[i-1].Score)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("Search(%d) = %v,\nwant the exact nearest %v", k, got, want)
	}

	mustSaveClose(t, vs)
	if h, _ := readHNSW(t, p.idx); h.efSearch != 40 {
		t.Errorf("saved EfSearch = %d after a wide search, want 40", h.efSearch)
	}
}
