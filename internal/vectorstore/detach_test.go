package vectorstore

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestDetachedReaderLetsAWriterIn covers a long-lived reader, such as an MCP
// session: once its index is loaded and detached, a writer can open the
// vector database, and the reader still searches against whatever graph it
// is given.
func TestDetachedReaderLetsAWriterIn(t *testing.T) {
	dir := t.TempDir()
	idx, db := filepath.Join(dir, "vec.idx"), filepath.Join(dir, "vec.db")
	g := newMockGraphStore()
	emb := &mockEmbedder{dims: 32}
	ctx := context.Background()

	w, err := New(g, emb, "test", idx, db)
	if err != nil {
		t.Fatal(err)
	}
	addDocs(t, g, 3, time.Now())
	if _, _, err := w.Reconcile(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if err := w.Save(); err != nil {
		t.Fatal(err)
	}
	w.Close()

	r, err := NewReadOnly(g, emb, "test", idx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if loaded, err := r.Load(); err != nil || !loaded {
		t.Fatalf("Load: %v %v", loaded, err)
	}
	if err := r.Detach(); err != nil {
		t.Fatal(err)
	}

	w2, err := New(g, emb, "test", idx, db)
	if err != nil {
		t.Fatalf("a writer could not open the database a detached reader had loaded: %v", err)
	}
	w2.Close()

	results, err := r.Search(ctx, "retry policy backoff", 3)
	if err != nil || len(results) == 0 {
		t.Fatalf("a detached reader cannot search: %d results, %v", len(results), err)
	}
	r.UseGraph(newMockGraphStore())
	if results, _ := r.Search(ctx, "retry policy backoff", 3); len(results) != 0 {
		t.Errorf("search resolved %d nodes against a graph that has none", len(results))
	}
}
