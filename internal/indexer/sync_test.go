package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/parser"
	"github.com/imyousuf/CodeEagle/internal/parser/generic"
	"github.com/imyousuf/CodeEagle/internal/parser/golang"
	"github.com/imyousuf/CodeEagle/internal/parser/markdown"
	"github.com/imyousuf/CodeEagle/internal/watcher"
)

// setupSyncTestIndexer creates an indexer with Go, Markdown, and Generic parsers
// and custom exclude patterns for sync tests.
func setupSyncTestIndexer(t *testing.T, paths []string, excludePatterns []string) (*Indexer, graph.Store) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "syncdb")
	store, err := embedded.NewStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	registry := parser.NewRegistry()
	registry.Register(golang.NewParser())
	registry.Register(markdown.NewParser())
	registry.Register(generic.NewGenericParser(nil, nil, nil, 0))

	idx := NewIndexer(IndexerConfig{
		GraphStore:     store,
		ParserRegistry: registry,
		RepoRoots:      paths,
		WatcherConfig: &watcher.WatcherConfig{
			Paths:           paths,
			ExcludePatterns: excludePatterns,
		},
	})

	return idx, store
}

func TestSyncDirectoryExcludesPatterns(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := t.TempDir()

	// Create files in an excluded directory.
	excludedDir := filepath.Join(tmpDir, "vendor", "lib")
	if err := os.MkdirAll(excludedDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(excludedDir, "dep.go"), []byte("package lib\n\nfunc Dep() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create files in a non-excluded directory.
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	idx, store := setupSyncTestIndexer(t, []string{tmpDir}, []string{"**/vendor/**"})
	ctx := context.Background()

	state := &SyncState{}
	statePath := filepath.Join(configDir, syncStateFile)

	if err := syncDirectory(ctx, idx, tmpDir, state, false, statePath); err != nil {
		t.Fatal(err)
	}

	// Verify: nodes should exist for src/main.go but NOT for vendor/lib/dep.go.
	vendorRelPath := "vendor/lib/dep.go"
	nodes, err := store.QueryNodes(ctx, graph.NodeFilter{FilePath: vendorRelPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) > 0 {
		t.Errorf("expected no nodes from excluded vendor directory, got %d", len(nodes))
	}

	// Verify src/main.go was indexed.
	srcRelPath := "src/main.go"
	nodes, err = store.QueryNodes(ctx, graph.NodeFilter{FilePath: srcRelPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Errorf("expected nodes for src/main.go, got none")
	}
}

func TestSyncDirectoryExcludesNestedPattern(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := t.TempDir()

	// Simulate the exact bug: **/Downloads/aws/** should exclude Downloads/aws/ subtree.
	awsDir := filepath.Join(tmpDir, "Downloads", "aws", "dist", "examples")
	if err := os.MkdirAll(awsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(awsDir, "readme.md"), []byte("# AWS Example\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a non-excluded file in Downloads.
	if err := os.WriteFile(filepath.Join(tmpDir, "Downloads", "notes.md"), []byte("# Notes\n"), 0644); err != nil {
		t.Fatal(err)
	}

	idx, store := setupSyncTestIndexer(t, []string{tmpDir}, []string{"**/Downloads/aws/**"})
	ctx := context.Background()

	state := &SyncState{}
	statePath := filepath.Join(configDir, syncStateFile)

	if err := syncDirectory(ctx, idx, tmpDir, state, false, statePath); err != nil {
		t.Fatal(err)
	}

	// aws subtree should be excluded.
	awsRelPath := "Downloads/aws/dist/examples/readme.md"
	nodes, err := store.QueryNodes(ctx, graph.NodeFilter{FilePath: awsRelPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) > 0 {
		t.Errorf("expected no nodes from excluded Downloads/aws directory, got %d", len(nodes))
	}

	// Downloads/notes.md should be indexed.
	notesRelPath := "Downloads/notes.md"
	nodes, err = store.QueryNodes(ctx, graph.NodeFilter{FilePath: notesRelPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Errorf("expected nodes for Downloads/notes.md, got none")
	}
}

func TestSyncDirectoryExcludesFilePattern(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := t.TempDir()

	// Create a .so file (should be excluded by pattern).
	if err := os.WriteFile(filepath.Join(tmpDir, "libfoo.so"), []byte("binary"), 0644); err != nil {
		t.Fatal(err)
	}
	// Create a normal text file.
	if err := os.WriteFile(filepath.Join(tmpDir, "readme.md"), []byte("# Hello\n"), 0644); err != nil {
		t.Fatal(err)
	}

	idx, store := setupSyncTestIndexer(t, []string{tmpDir}, []string{"**/*.so"})
	ctx := context.Background()

	state := &SyncState{}
	statePath := filepath.Join(configDir, syncStateFile)

	if err := syncDirectory(ctx, idx, tmpDir, state, false, statePath); err != nil {
		t.Fatal(err)
	}

	// .so file should be excluded.
	soRelPath := "libfoo.so"
	nodes, err := store.QueryNodes(ctx, graph.NodeFilter{FilePath: soRelPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) > 0 {
		t.Errorf("expected no nodes for excluded .so file, got %d", len(nodes))
	}

	// readme.md should be indexed.
	mdRelPath := "readme.md"
	nodes, err = store.QueryNodes(ctx, graph.NodeFilter{FilePath: mdRelPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Errorf("expected nodes for readme.md, got none")
	}
}
