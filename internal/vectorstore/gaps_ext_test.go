package vectorstore_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/vectorstore"
)

// TestLoadMetaOnlyReadsMetadataWithoutTheIndex: status reads the metadata
// alone, and must not pay for loading the vectors.
func TestLoadMetaOnlyReadsMetadataWithoutTheIndex(t *testing.T) {
	ctx := context.Background()
	g := newMemGraph(
		&graph.Node{ID: "d1", Type: graph.NodeDocument, Name: "a", DocComment: "retry policy"},
		&graph.Node{ID: "d2", Type: graph.NodeDocument, Name: "b", DocComment: "backoff"},
	)
	emb := newFakeEmbedder()
	p := newPaths(t)

	vs := openStore(t, g, emb, p)
	if m, err := vs.LoadMetaOnly(); err != nil || m != nil {
		t.Errorf("LoadMetaOnly of a new index = %+v, %v; want nil, nil", m, err)
	}
	if vs.Meta() != nil {
		t.Error("Meta of a new index is not nil")
	}
	if err := vs.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	mustSaveClose(t, vs)

	vs = openStore(t, g, emb, p)
	m, err := vs.LoadMetaOnly()
	if err != nil || m == nil {
		t.Fatalf("LoadMetaOnly = %+v, %v", m, err)
	}
	if m.Provider != "fake" || m.Model != "fake-embed-v1" || m.Dimensions != 8 || m.NodeCount != 2 ||
		m.Version != 2 || m.TextVersion != vectorstore.EmbeddableTextVersion || m.Rebuilding {
		t.Errorf("LoadMetaOnly = %+v", m)
	}
	if got := vs.Meta(); got == nil || *got != *m {
		t.Errorf("Meta = %+v, want the record LoadMetaOnly returned, %+v", got, m)
	}
	if vs.NeedsReindex() {
		t.Errorf("NeedsReindex with metadata loaded: %s", vs.ReindexReason())
	}
	if vs.Len() != 0 {
		t.Errorf("Len = %d after LoadMetaOnly, want 0: the vectors are not loaded", vs.Len())
	}
	if res, err := vs.Search(ctx, "retry policy", 3); err != nil || len(res) != 0 {
		t.Errorf("Search before Load = %d results, %v; want none", len(res), err)
	}
	if loaded, err := vs.Load(); err != nil || !loaded || vs.Len() != 2 {
		t.Errorf("Load = %v, %v, Len %d; want 2 vectors", loaded, err, vs.Len())
	}
}

// TestEmbedderReturnsTheProviderGiven: federation embeds queries with the
// store's own provider.
func TestEmbedderReturnsTheProviderGiven(t *testing.T) {
	emb := newFakeEmbedder()
	vs := openStore(t, newMemGraph(), emb, newPaths(t))
	if got := vs.Embedder(); got != emb {
		t.Errorf("Embedder = %v, want the provider given to New", got)
	}
	if !vs.Available() {
		t.Error("Available = false for a store with an embedder")
	}
}

// TestSaveAfterRemoveNodeWritesACleanGraph: a graph with deletions is
// rebuilt before it is saved, since coder/hnsw's Delete leaves links to the
// nodes it removed and those load back as nil.
func TestSaveAfterRemoveNodeWritesACleanGraph(t *testing.T) {
	ctx := context.Background()
	g := newMemGraph()
	var nodes []*graph.Node
	for i := range 60 {
		n := &graph.Node{ID: fmt.Sprintf("n%02d", i), Type: graph.NodeDocument, Name: fmt.Sprintf("doc %d", i),
			DocComment: fmt.Sprintf("document %d about topic %d", i, i%7)}
		nodes = append(nodes, n)
		_ = g.AddNode(ctx, n)
	}
	emb := newFakeEmbedder()
	p := newPaths(t)
	vs := openStore(t, g, emb, p)
	if err := vs.IndexNodes(ctx, nodes); err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes[:20] {
		if err := vs.RemoveNode(ctx, n.ID); err != nil {
			t.Fatal(err)
		}
	}
	if vs.Len() != 40 {
		t.Errorf("Len after removing 20 of 60 = %d, want 40", vs.Len())
	}
	mustSaveClose(t, vs)

	_, layers := readHNSW(t, p.idx)
	if len(layers[0]) != 40 {
		t.Errorf("saved base layer holds %d nodes, want 40", len(layers[0]))
	}
	for _, n := range nodes[:20] {
		for l, layer := range layers {
			if _, ok := layer[n.ID+":0"]; ok {
				t.Errorf("removed %s is still in layer %d", n.ID, l)
			}
		}
	}
	assertCleanLayers(t, layers)
	for k := range rawMap(t, p.db) {
		for _, n := range nodes[:20] {
			if k == "chunk:"+n.ID+":0" || k == "text:"+n.ID {
				t.Errorf("vec.db still holds %s for a removed node", k)
			}
		}
	}

	// The reloaded graph takes new vectors and answers searches.
	vs = reopen(t, g, emb, p)
	if vs.Len() != 40 {
		t.Errorf("Len after reload = %d, want 40", vs.Len())
	}
	if _, ok := vs.Vector(nodes[0].ID); ok {
		t.Error("a removed node has a vector after reload")
	}
	if err := vs.IndexNodes(ctx, nodes[:20]); err != nil {
		t.Fatal(err)
	}
	if err := vs.IndexNodes(ctx, nodes[20:30]); err != nil {
		t.Fatal(err)
	}
	if vs.Len() != 60 {
		t.Errorf("Len after re-adding = %d, want 60", vs.Len())
	}
	res, err := vs.Search(ctx, vectorstore.EmbeddableText(nodes[5]), 1)
	if err != nil || len(res) != 1 || res[0].Node.ID != nodes[5].ID {
		t.Errorf("Search after reload = %v, %v; want %s first", res, err, nodes[5].ID)
	}
}

// TestRemovingTheOnlyNodeLeavesAUsableIndex: deleting the last vector used to
// leave a graph the next Add panicked on.
func TestRemovingTheOnlyNodeLeavesAUsableIndex(t *testing.T) {
	ctx := context.Background()
	a := &graph.Node{ID: "a", Type: graph.NodeDocument, Name: "a", DocComment: "first"}
	b := &graph.Node{ID: "b", Type: graph.NodeDocument, Name: "b", DocComment: "second"}
	g := newMemGraph(a, b)
	emb := newFakeEmbedder()
	p := newPaths(t)
	vs := openStore(t, g, emb, p)
	if err := vs.IndexNode(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := vs.RemoveNode(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if vs.Len() != 0 {
		t.Errorf("Len = %d, want 0", vs.Len())
	}
	if res, err := vs.Search(ctx, "first", 3); err != nil || len(res) != 0 {
		t.Errorf("Search of an empty index = %d results, %v; want none", len(res), err)
	}
	// Re-embedding the only node removes and adds it in one step.
	if err := vs.IndexNode(ctx, b); err != nil {
		t.Fatal(err)
	}
	b.DocComment = "second, revised"
	if err := vs.IndexNode(ctx, b); err != nil {
		t.Fatal(err)
	}
	mustSaveClose(t, vs)
	vs = reopen(t, g, emb, p)
	if vs.Len() != 1 {
		t.Errorf("Len after reload = %d, want 1", vs.Len())
	}
	res, err := vs.Search(ctx, vectorstore.EmbeddableText(b), 1)
	if err != nil || len(res) != 1 || res[0].ChunkText != vectorstore.EmbeddableText(b) {
		t.Errorf("Search = %v, %v; want b's revised text", res, err)
	}
}

// TestEmbedMissingHonoursLimitAndTypes: EmbedMissing embeds what the index
// lacks of the types asked for, newest first, up to the limit, and never
// removes anything.
func TestEmbedMissingHonoursLimitAndTypes(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	g := newMemGraph()
	for i := range 5 {
		_ = g.AddNode(ctx, &graph.Node{ID: fmt.Sprintf("d%d", i), Type: graph.NodeDocument, Name: fmt.Sprintf("d%d", i),
			DocComment: fmt.Sprintf("document %d", i), UpdatedAt: base.Add(time.Duration(i) * time.Hour)})
	}
	for i := range 3 {
		_ = g.AddNode(ctx, &graph.Node{ID: fmt.Sprintf("f%d", i), Type: graph.NodeFunction, Name: fmt.Sprintf("F%d", i),
			Signature: fmt.Sprintf("func F%d()", i)})
	}
	_ = g.AddNode(ctx, &graph.Node{ID: "t0", Type: graph.NodeTopic, Name: "retry policy"})
	_ = g.AddNode(ctx, &graph.Node{ID: "blank", Type: graph.NodeDocument, Name: "blank.md"})
	_ = g.AddNode(ctx, &graph.Node{ID: "file", Type: graph.NodeFile, Name: "main.go", DocComment: "not embeddable"})

	vs := openStore(t, g, newFakeEmbedder(), newPaths(t))
	has := func(ids ...string) {
		t.Helper()
		for _, id := range ids {
			if _, ok := vs.Vector(id); !ok {
				t.Errorf("Vector(%s) missing", id)
			}
		}
	}
	lacks := func(ids ...string) {
		t.Helper()
		for _, id := range ids {
			if _, ok := vs.Vector(id); ok {
				t.Errorf("Vector(%s) present", id)
			}
		}
	}

	if n, err := vs.EmbedMissing(ctx, 2, graph.NodeDocument); err != nil || n != 2 {
		t.Fatalf("EmbedMissing(2, Document) = %d, %v; want 2", n, err)
	}
	has("d4", "d3")
	lacks("d2", "d1", "d0", "f0", "t0")

	if n, _ := vs.EmbedMissing(ctx, 0, graph.NodeDocument); n != 3 {
		t.Errorf("EmbedMissing(0, Document) = %d, want the other 3", n)
	}
	if n, _ := vs.EmbedMissing(ctx, 0, graph.NodeDocument); n != 0 {
		t.Errorf("EmbedMissing(0, Document) again = %d, want 0", n)
	}
	lacks("blank", "f0", "t0")

	// A type that is not embeddable is skipped even when asked for.
	if n, _ := vs.EmbedMissing(ctx, 0, graph.NodeFile); n != 0 {
		t.Errorf("EmbedMissing(0, File) = %d, want 0", n)
	}
	lacks("file")

	if n, _ := vs.EmbedMissing(ctx, 0, graph.NodeFunction, graph.NodeTopic); n != 4 {
		t.Errorf("EmbedMissing(0, Function, Topic) = %d, want 4", n)
	}
	has("f0", "f1", "f2", "t0")

	// A node whose text changed is embedded again.
	f0, _ := g.GetNode(ctx, "f0")
	f0.Signature = "func F0(ctx context.Context)"
	if n, _ := vs.EmbedMissing(ctx, 0); n != 1 {
		t.Errorf("EmbedMissing after a change = %d, want 1", n)
	}

	// A node gone from the graph keeps its vector: EmbedMissing judges no
	// staleness, even over every type.
	_ = g.DeleteNode(ctx, "d0")
	before := vs.Len()
	if n, _ := vs.EmbedMissing(ctx, 0); n != 0 {
		t.Errorf("EmbedMissing with nothing missing = %d, want 0", n)
	}
	has("d0")
	if vs.Len() != before {
		t.Errorf("EmbedMissing changed Len from %d to %d", before, vs.Len())
	}
	if stale, missing, _ := vs.Staleness(ctx); stale != 1 || missing != 0 {
		t.Errorf("Staleness = %d, %d; want 1 stale, 0 missing", stale, missing)
	}
}

// TestStalenessCountsWhatSearchCannotSee: indexed nodes gone from the graph,
// and embeddable nodes with text the index lacks or holds an older text of.
func TestStalenessCountsWhatSearchCannotSee(t *testing.T) {
	ctx := context.Background()
	g := newMemGraph()
	a := &graph.Node{ID: "a", Type: graph.NodeDocument, Name: "a", DocComment: "alpha"}
	b := &graph.Node{ID: "b", Type: graph.NodeMeeting, Name: "b", DocComment: "beta"}
	c := &graph.Node{ID: "c", Type: graph.NodeFunction, Name: "c", DocComment: "gamma"}
	for _, n := range []*graph.Node{a, b, c} {
		_ = g.AddNode(ctx, n)
	}
	vs := openStore(t, g, newFakeEmbedder(), newPaths(t))
	if stale, missing, err := vs.Staleness(ctx); err != nil || stale != 0 || missing != 3 {
		t.Errorf("Staleness of an empty index = %d, %d, %v; want 0, 3", stale, missing, err)
	}
	if err := vs.IndexNodes(ctx, []*graph.Node{a, b, c}); err != nil {
		t.Fatal(err)
	}
	if stale, missing, _ := vs.Staleness(ctx); stale != 0 || missing != 0 {
		t.Errorf("Staleness of a current index = %d, %d; want 0, 0", stale, missing)
	}

	// Not counted: nodes with nothing to embed, and types that are not embedded.
	_ = g.AddNode(ctx, &graph.Node{ID: "blank", Type: graph.NodeDocument, Name: "blank"})
	_ = g.AddNode(ctx, &graph.Node{ID: "file", Type: graph.NodeFile, Name: "f", DocComment: "x"})
	if stale, missing, _ := vs.Staleness(ctx); stale != 0 || missing != 0 {
		t.Errorf("Staleness with unembeddable nodes = %d, %d; want 0, 0", stale, missing)
	}

	_ = g.DeleteNode(ctx, "b")
	c.DocComment = "gamma, revised"
	_ = g.AddNode(ctx, &graph.Node{ID: "d", Type: graph.NodeActionItem, Name: "d", DocComment: "delta"})
	if stale, missing, _ := vs.Staleness(ctx); stale != 1 || missing != 2 {
		t.Errorf("Staleness = %d, %d; want 1 stale (b), 2 missing (c changed, d new)", stale, missing)
	}
	// Staleness only reports; Reconcile closes the gap.
	if added, removed, err := vs.Reconcile(ctx, 1); err != nil || added != 1 || removed != 1 {
		t.Errorf("Reconcile(1) = %d added, %d removed, %v; want 1, 1", added, removed, err)
	}
	if stale, missing, _ := vs.Staleness(ctx); stale != 0 || missing != 1 {
		t.Errorf("Staleness after Reconcile(1) = %d, %d; want 0, 1", stale, missing)
	}
}
