package vectorstore_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/vectorstore"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
)

// The black-box counterparts of rebuild_test.go.

func versionedDocs(from, to int, version string) []*graph.Node {
	var nodes []*graph.Node
	for i := from; i < to; i++ {
		nodes = append(nodes, &graph.Node{
			ID: fmt.Sprintf("n%04d", i), Type: graph.NodeDocument, Name: fmt.Sprintf("doc %d", i),
			DocComment: fmt.Sprintf("document %d about topic %d, %s", i, i%17, version),
		})
	}
	return nodes
}

// TestReembeddingSurvivesSaveAndLoadRounds re-embeds nodes, saves, and
// re-embeds again in a later process, several times over. coder/hnsw's
// Delete leaves links to the nodes it removed; saved and loaded, they came
// back nil, and the next Add beside one crashed the process.
func TestReembeddingSurvivesSaveAndLoadRounds(t *testing.T) {
	ctx := context.Background()
	g := newMemGraph(versionedDocs(0, 400, "v1")...)
	emb := newFakeEmbedder()
	p := newPaths(t)

	vs := openStore(t, g, emb, p)
	if loaded, err := vs.Load(); err != nil || loaded {
		t.Fatalf("Load of a new index = %v, %v; want false, nil", loaded, err)
	}
	if err := vs.IndexNodes(ctx, versionedDocs(0, 400, "v1")); err != nil {
		t.Fatal(err)
	}
	for round := 2; round <= 4; round++ {
		if err := vs.IndexNodes(ctx, versionedDocs(0, 150, fmt.Sprintf("v%d", round))); err != nil {
			t.Fatal(err)
		}
		mustSaveClose(t, vs)
		_, layers := readHNSW(t, p.idx)
		assertCleanLayers(t, layers)
		vs = reopen(t, g, emb, p)
	}
	if err := vs.IndexNodes(ctx, versionedDocs(0, 150, "v5")); err != nil {
		t.Fatal(err)
	}
	if n := vs.Len(); n != 400 {
		t.Errorf("index holds %d vectors, want 400", n)
	}
	res, err := vs.Search(ctx, vectorstore.EmbeddableText(versionedDocs(7, 8, "v5")[0]), 3)
	if err != nil || len(res) == 0 {
		t.Fatalf("search after re-embedding: %v, %d results", err, len(res))
	}
	if res[0].Node.ID != "n0007" || !strings.HasSuffix(res[0].ChunkText, "v5") {
		t.Errorf("top result = %s %q, want n0007 with its latest text", res[0].Node.ID, res[0].ChunkText)
	}
}

// TestReconcileReembedsChangedTextBlackBox covers a node whose text changed
// after it was embedded, as a relation's does when its brief is rewritten,
// and an index written before text hashes were kept.
func TestReconcileReembedsChangedTextBlackBox(t *testing.T) {
	ctx := context.Background()
	rel := &graph.Node{ID: "r1", Type: graph.NodeRelation, Name: "relation", DocComment: "first brief: the cap is three"}
	doc := &graph.Node{ID: "d1", Type: graph.NodeDocument, Name: "doc", DocComment: "retry policy"}
	g := newMemGraph(rel, doc)
	emb := newFakeEmbedder()
	p := newPaths(t)

	vs := openStore(t, g, emb, p)
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
	res, err := vs.Search(ctx, vectorstore.EmbeddableText(rel), 1)
	if err != nil || len(res) != 1 {
		t.Fatalf("Search = %d results, %v", len(res), err)
	}
	if res[0].Node.ID != "r1" || !strings.Contains(res[0].ChunkText, "five") {
		t.Errorf("top result = %s %q, want the relation's rewritten brief", res[0].Node.ID, res[0].ChunkText)
	}
	mustSaveClose(t, vs)

	// Indexed before hashes were kept: a relation is embedded again once,
	// anything else is left as it is.
	rawWrite(t, p.db, map[string]*string{"text:r1": nil, "text:d1": nil})
	vs = reopen(t, g, emb, p)
	if added, _, _ := vs.Reconcile(ctx, 0); added != 1 {
		t.Errorf("legacy index: reconcile embedded %d nodes, want only the relation", added)
	}
	if added, _, _ := vs.Reconcile(ctx, 0); added != 0 {
		t.Errorf("legacy index: a second reconcile embedded %d nodes, want none", added)
	}
	mustSaveClose(t, vs)
	after := rawMap(t, p.db)
	if _, ok := after["text:d1"]; ok {
		t.Error("reconcile recorded a hash for a node it did not embed")
	}
	if got := after["text:r1"]; got != fnvHex(vectorstore.EmbeddableText(rel)) {
		t.Errorf("text:r1 = %q, want its text's hash", got)
	}
}
