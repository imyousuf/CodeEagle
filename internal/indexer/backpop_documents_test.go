package indexer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/parser"
	"github.com/imyousuf/CodeEagle/internal/parser/generic"
	"github.com/imyousuf/CodeEagle/internal/watcher"
)

func genericIndexer(store graph.Store, root string) *Indexer {
	registry := parser.NewRegistry()
	registry.SetFallback(generic.NewGenericParser(nil, nil, nil, 0))
	return NewIndexer(IndexerConfig{
		GraphStore:     store,
		ParserRegistry: registry,
		RepoRoots:      []string{root},
		WatcherConfig:  &watcher.WatcherConfig{Paths: []string{root}},
		Logger:         func(string, ...any) {},
	})
}

// forgetSections returns a document's nodes to how an older version left
// them: one node holding a summary, no sections.
func forgetSections(t *testing.T, store graph.Store) {
	t.Helper()
	ctx := context.Background()
	nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeDocument})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		switch {
		case n.Properties["kind"] == "section":
			if err := store.DeleteNode(ctx, n.ID); err != nil {
				t.Fatal(err)
			}
		case n.Properties["sections"] != "":
			delete(n.Properties, "sections")
			n.Language = ""
			n.DocComment = "a summary"
			if err := store.UpdateNode(ctx, n); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func countSections(t *testing.T, store graph.Store) map[string]int {
	t.Helper()
	nodes, err := store.QueryNodes(context.Background(), graph.NodeFilter{Type: graph.NodeDocument})
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]int)
	for _, n := range nodes {
		if n.Properties["kind"] == "section" {
			out[filepath.Base(n.FilePath)]++
		}
	}
	return out
}

func TestBackfillGivesOldDocumentsSections(t *testing.T) {
	root := t.TempDir()
	stamp := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	store, err := embedded.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	idx := genericIndexer(store, root)
	for _, name := range []string{"a-review.txt", "b-notes.txt", "rows.csv"} {
		path := filepath.Join(root, name)
		body := "Opening line\n\n" + strings.Repeat("The queue stalled behind one long request. ", 20)
		if name == "rows.csv" {
			body = "a,b\n1,2\n"
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := idx.IndexFileWithTimestamp(ctx, path, stamp); err != nil {
			t.Fatal(err)
		}
	}
	forgetSections(t, store)
	idx = genericIndexer(store, root)
	if got := countSections(t, store); len(got) != 0 {
		t.Fatalf("setup left sections: %v", got)
	}

	// Resuming after the first document reaches only the second; the csv
	// holds data, not prose, and gets none.
	first := ""
	nodes, _ := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeDocument})
	for _, n := range nodes {
		if n.Name == "a-review.txt" {
			first = n.FilePath
		}
	}
	n, next, err := BackpopDocumentSections(ctx, idx, []string{root}, first)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || next != "" {
		t.Errorf("re-parsed %d, next %q; want 1 and done", n, next)
	}
	if got := countSections(t, store); got["b-notes.txt"] != 1 || got["a-review.txt"] != 0 || got["rows.csv"] != 0 {
		t.Errorf("sections after a resumed backfill = %v", got)
	}

	n, _, err = BackpopDocumentSections(ctx, idx, []string{root}, "")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || countSections(t, store)["a-review.txt"] != 1 {
		t.Errorf("a fresh backfill re-parsed %d; want only the document still without sections", n)
	}
	if idx.HasChanges() {
		t.Errorf("the backfill reported changes: %v", idx.ChangedFiles())
	}
}
