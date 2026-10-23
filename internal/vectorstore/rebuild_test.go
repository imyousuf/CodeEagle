package vectorstore

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dgraph-io/badger/v4"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// spreadEmbedder gives each text its own direction, so the graph has real
// neighbourhoods rather than one point.
type spreadEmbedder struct{}

func (spreadEmbedder) vec(text string) []float32 {
	h := fnv.New64a()
	h.Write([]byte(text))
	seed := h.Sum64()
	v := make([]float32, 16)
	var norm float64
	for i := range v {
		seed = seed*6364136223846793005 + 1442695040888963407
		v[i] = float32(int64(seed>>33)%1000) / 1000
		norm += float64(v[i] * v[i])
	}
	for i := range v {
		v[i] /= float32(math.Sqrt(norm))
	}
	return v
}

func (e spreadEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = e.vec(t)
	}
	return out, nil
}
func (e spreadEmbedder) EmbedQuery(_ context.Context, q string) ([]float32, error) {
	return e.vec(q), nil
}
func (spreadEmbedder) Name() string      { return "spread" }
func (spreadEmbedder) ModelName() string { return "spread-v1" }
func (spreadEmbedder) Dimensions() int   { return 16 }

func spreadNodes(from, to int, version string) []*graph.Node {
	var nodes []*graph.Node
	for i := from; i < to; i++ {
		nodes = append(nodes, &graph.Node{ID: fmt.Sprintf("n%04d", i), Type: graph.NodeDocument, Name: fmt.Sprintf("doc %d", i),
			DocComment: fmt.Sprintf("document %d about topic %d, %s", i, i%17, version)})
	}
	return nodes
}

// TestReembeddingSurvivesASaveAndLoad covers re-embedding nodes, saving, and
// re-embedding again in a later process. coder/hnsw's Delete leaves links to
// the nodes it removed; saved and loaded, they came back nil, and the next
// Add beside one crashed the process.
func TestReembeddingSurvivesASaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	store := newMockGraphStore()
	for _, n := range spreadNodes(0, 400, "v1") {
		_ = store.AddNode(ctx, n)
	}
	open := func() *VectorStore {
		vs, err := New(store, spreadEmbedder{}, "test", filepath.Join(dir, "vec.idx"), filepath.Join(dir, "vec.db"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := vs.Load(); err != nil {
			t.Fatal(err)
		}
		return vs
	}
	vs := open()
	if err := vs.IndexNodes(ctx, spreadNodes(0, 400, "v1")); err != nil {
		t.Fatal(err)
	}
	for round := 2; round <= 4; round++ {
		if err := vs.IndexNodes(ctx, spreadNodes(0, 150, fmt.Sprintf("v%d", round))); err != nil {
			t.Fatal(err)
		}
		if err := vs.Save(); err != nil {
			t.Fatal(err)
		}
		vs.Close()
		vs = open()
	}
	if err := vs.IndexNodes(ctx, spreadNodes(0, 150, "v5")); err != nil {
		t.Fatal(err)
	}
	if n := vs.idx.Len(); n != 400 {
		t.Errorf("index holds %d vectors, want 400", n)
	}
	res, err := vs.Search(ctx, "document 7 about topic 7, v5", 3)
	if err != nil || len(res) == 0 {
		t.Fatalf("search after re-embedding: %v, %d results", err, len(res))
	}
	vs.Close()
}

// TestReconcileReembedsChangedText covers a node whose text changed after it
// was embedded, as a relation's does when its brief is rewritten: reconcile
// embeds it again, and leaves unchanged nodes alone.
func TestReconcileReembedsChangedText(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	store := newMockGraphStore()
	rel := &graph.Node{ID: "r1", Type: graph.NodeRelation, Name: "relation", DocComment: "first brief: the cap is three"}
	doc := &graph.Node{ID: "d1", Type: graph.NodeDocument, Name: "doc", DocComment: "retry policy"}
	_ = store.AddNode(ctx, rel)
	_ = store.AddNode(ctx, doc)
	vs, err := New(store, spreadEmbedder{}, "test", filepath.Join(dir, "vec.idx"), filepath.Join(dir, "vec.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer vs.Close()
	if added, _, err := vs.Reconcile(ctx, 0); err != nil || added != 2 {
		t.Fatalf("first reconcile added %d, %v", added, err)
	}
	if added, _, _ := vs.Reconcile(ctx, 0); added != 0 {
		t.Errorf("an unchanged index re-embedded %d nodes", added)
	}
	rel.DocComment = "rewritten brief: the cap is five"
	if added, _, _ := vs.Reconcile(ctx, 0); added != 1 {
		t.Errorf("after a rewrite, reconcile embedded %d nodes, want the relation", added)
	}
	if got := vs.getChunkText("r1", 0); !strings.Contains(got, "five") {
		t.Errorf("relation chunk = %q, want the rewritten brief", got)
	}

	// Indexed before hashes were kept: a relation is embedded again once,
	// anything else is left as it is.
	for _, id := range []string{"r1", "d1"} {
		_ = vs.vecDB.Update(func(txn *badger.Txn) error { return txn.Delete([]byte(prefixText + id)) })
	}
	if added, _, _ := vs.Reconcile(ctx, 0); added != 1 {
		t.Errorf("legacy index: reconcile embedded %d nodes, want only the relation", added)
	}
}
