package vectorstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

func addDocs(t *testing.T, g *mockGraphStore, n int, base time.Time) []*graph.Node {
	t.Helper()
	var nodes []*graph.Node
	for i := range n {
		node := &graph.Node{
			ID: fmt.Sprintf("d%d", i), Type: graph.NodeDocument, Name: fmt.Sprintf("spec%d.md", i),
			DocComment: fmt.Sprintf("retry policy section %d about backoff and jitter", i),
			UpdatedAt:  base.Add(time.Duration(i) * time.Hour),
		}
		if err := g.AddNode(context.Background(), node); err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, node)
	}
	return nodes
}

// TestReconcileEmbedsWhatTheIndexLacks covers the gap this fixes: only a full
// rebuild used to embed anything, so a document indexed after the last
// rebuild was invisible to search by meaning.
func TestReconcileEmbedsWhatTheIndexLacks(t *testing.T) {
	vs, g, _ := setupTestVectorStore(t)
	ctx := context.Background()
	addDocs(t, g, 3, time.Now())

	added, removed, err := vs.Reconcile(ctx, 0)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if added != 3 || removed != 0 {
		t.Errorf("added %d, removed %d; want 3, 0", added, removed)
	}
	results, err := vs.Search(ctx, "retry policy backoff", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Error("reconciled documents are not searchable")
	}

	if added, _, _ := vs.Reconcile(ctx, 0); added != 0 {
		t.Errorf("a second reconcile embedded %d nodes again", added)
	}
}

func TestReconcileDropsVectorsOfNodesThatAreGone(t *testing.T) {
	vs, g, _ := setupTestVectorStore(t)
	ctx := context.Background()
	nodes := addDocs(t, g, 3, time.Now())
	if _, _, err := vs.Reconcile(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if err := g.DeleteNode(ctx, nodes[1].ID); err != nil {
		t.Fatal(err)
	}

	_, removed, err := vs.Reconcile(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("removed %d stale nodes, want 1", removed)
	}
	if stale, missing, _ := vs.Staleness(ctx); stale != 0 || missing != 0 {
		t.Errorf("after reconcile: %d stale, %d missing", stale, missing)
	}
}

// TestReconcileHonoursTheLimitNewestFirst covers the backfill cap, which keeps
// one sync from embedding a whole corpus. What it does embed should be what
// changed most recently.
func TestReconcileHonoursTheLimitNewestFirst(t *testing.T) {
	vs, g, emb := setupTestVectorStore(t)
	ctx := context.Background()
	nodes := addDocs(t, g, 5, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	added, _, err := vs.Reconcile(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if added != 2 {
		t.Fatalf("embedded %d nodes, the limit is 2", added)
	}
	if emb.callCount == 0 {
		t.Fatal("nothing was embedded")
	}
	for _, want := range []string{nodes[4].ID, nodes[3].ID} {
		if _, ok := vs.Vector(want); !ok {
			t.Errorf("the newest node %s was not among those embedded", want)
		}
	}
	if _, missing, _ := vs.Staleness(ctx); missing != 3 {
		t.Errorf("%d still missing, want 3 left for the next run", missing)
	}
}

// TestEmbedMissingNeverRemoves covers a store that sees only part of the
// graph, such as the meetings scope alone. Everything it cannot see would look
// deleted to it, so it must only add.
func TestEmbedMissingNeverRemoves(t *testing.T) {
	vs, g, _ := setupTestVectorStore(t)
	ctx := context.Background()
	docs := addDocs(t, g, 2, time.Now())
	if _, _, err := vs.Reconcile(ctx, 0); err != nil {
		t.Fatal(err)
	}
	meeting := &graph.Node{ID: "m1", Type: graph.NodeMeeting, Name: "Platform sync",
		DocComment: "discussed raising the retry cap to five"}
	if err := g.AddNode(ctx, meeting); err != nil {
		t.Fatal(err)
	}
	// From a meetings-only view the documents are absent.
	for _, d := range docs {
		_ = g.DeleteNode(ctx, d.ID)
	}

	n, err := vs.EmbedMissing(ctx, 0, graph.NodeMeeting)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("embedded %d meeting nodes, want 1", n)
	}
	for _, d := range docs {
		if _, ok := vs.Vector(d.ID); !ok {
			t.Errorf("EmbedMissing removed the vector of %s, which it could not see", d.ID)
		}
	}
}

// TestReembeddingTheOnlyNodeDoesNotPanic covers coder/hnsw v0.6.1, which
// panics on Add once Delete has emptied the graph. Re-embedding a lone node
// deletes its vector first, so a one-node index could not be updated at all.
func TestReembeddingTheOnlyNodeDoesNotPanic(t *testing.T) {
	vs, g, _ := setupTestVectorStore(t)
	ctx := context.Background()
	nodes := addDocs(t, g, 1, time.Now())
	if err := vs.IndexNodes(ctx, nodes); err != nil {
		t.Fatal(err)
	}
	nodes[0].DocComment = "rewritten section text"
	if err := vs.IndexNodes(ctx, nodes); err != nil {
		t.Fatalf("re-embedding the only node: %v", err)
	}
	if _, ok := vs.Vector(nodes[0].ID); !ok {
		t.Error("the re-embedded node has no vector")
	}
}
