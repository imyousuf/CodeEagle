package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/parser"
	"github.com/imyousuf/CodeEagle/internal/parser/golang"
	"github.com/imyousuf/CodeEagle/internal/watcher"
)

func setupTestIndexer(t *testing.T) (*Indexer, graph.Store) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "testdb")
	store, err := embedded.NewStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	registry := parser.NewRegistry()
	registry.Register(golang.NewParser())

	idx := NewIndexer(IndexerConfig{
		GraphStore:     store,
		ParserRegistry: registry,
		WatcherConfig: &watcher.WatcherConfig{
			ExcludePatterns: []string{"**/vendor/**", "**/node_modules/**"},
		},
	})

	return idx, store
}

func TestIndexFile(t *testing.T) {
	idx, store := setupTestIndexer(t)
	ctx := context.Background()

	// Create a temp Go file.
	tmpDir := t.TempDir()
	goFile := filepath.Join(tmpDir, "main.go")
	content := `package main

import "fmt"

// Greeter says hello.
type Greeter struct {
	Name string
}

// Greet prints a greeting.
func (g *Greeter) Greet() {
	fmt.Println("Hello, " + g.Name)
}

func main() {
	g := &Greeter{Name: "World"}
	g.Greet()
}
`
	if err := os.WriteFile(goFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Index the file.
	if err := idx.IndexFile(ctx, goFile); err != nil {
		t.Fatal(err)
	}

	// Verify nodes are in the graph.
	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if stats.NodeCount == 0 {
		t.Error("expected nodes in graph after indexing, got 0")
	}
	if stats.EdgeCount == 0 {
		t.Error("expected edges in graph after indexing, got 0")
	}

	// Check for specific node types.
	if stats.NodesByType[graph.NodeFile] == 0 {
		t.Error("expected File node")
	}
	if stats.NodesByType[graph.NodePackage] == 0 {
		t.Error("expected Package node")
	}
	if stats.NodesByType[graph.NodeStruct] == 0 {
		t.Error("expected Struct node")
	}
	if stats.NodesByType[graph.NodeFunction] == 0 {
		t.Error("expected Function node")
	}
	if stats.NodesByType[graph.NodeMethod] == 0 {
		t.Error("expected Method node")
	}

	// Verify IndexStats.
	idxStats := idx.Stats()
	if idxStats.FilesIndexed != 1 {
		t.Errorf("expected FilesIndexed=1, got %d", idxStats.FilesIndexed)
	}
	if idxStats.NodesTotal == 0 {
		t.Error("expected NodesTotal > 0")
	}
}

func TestIndexFileUnsupportedExtension(t *testing.T) {
	idx, _ := setupTestIndexer(t)
	ctx := context.Background()

	// Create a .txt file (no parser registered).
	tmpDir := t.TempDir()
	txtFile := filepath.Join(tmpDir, "readme.txt")
	if err := os.WriteFile(txtFile, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	// Should silently return nil.
	if err := idx.IndexFile(ctx, txtFile); err != nil {
		t.Errorf("expected nil error for unsupported extension, got: %v", err)
	}
}

func TestIndexDirectory(t *testing.T) {
	idx, store := setupTestIndexer(t)
	ctx := context.Background()

	// Create a temp directory with Go files and non-Go files.
	tmpDir := t.TempDir()

	// Go file in root.
	goFile1 := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(goFile1, []byte(`package main

func main() {}
`), 0644); err != nil {
		t.Fatal(err)
	}

	// Go file in subdirectory.
	subDir := filepath.Join(tmpDir, "pkg")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	goFile2 := filepath.Join(subDir, "lib.go")
	if err := os.WriteFile(goFile2, []byte(`package pkg

type Config struct {
	Name string
}

func NewConfig() *Config {
	return &Config{}
}
`), 0644); err != nil {
		t.Fatal(err)
	}

	// Non-Go file.
	txtFile := filepath.Join(tmpDir, "README.txt")
	if err := os.WriteFile(txtFile, []byte("readme"), 0644); err != nil {
		t.Fatal(err)
	}

	// Index the directory.
	if err := idx.IndexDirectory(ctx, tmpDir); err != nil {
		t.Fatal(err)
	}

	// Verify we got nodes from both Go files.
	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if stats.NodeCount < 4 {
		t.Errorf("expected at least 4 nodes (2 files + 2 packages), got %d", stats.NodeCount)
	}

	// Verify FilesIndexed count.
	idxStats := idx.Stats()
	if idxStats.FilesIndexed != 2 {
		t.Errorf("expected FilesIndexed=2, got %d", idxStats.FilesIndexed)
	}
}

func TestIndexDirectoryExcludesPatterns(t *testing.T) {
	idx, store := setupTestIndexer(t)
	ctx := context.Background()

	tmpDir := t.TempDir()

	// Create a Go file in a vendor directory (should be excluded).
	vendorDir := filepath.Join(tmpDir, "vendor", "lib")
	if err := os.MkdirAll(vendorDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vendorDir, "dep.go"), []byte(`package lib

func Dep() {}
`), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a Go file outside vendor.
	if err := os.WriteFile(filepath.Join(tmpDir, "app.go"), []byte(`package main

func main() {}
`), 0644); err != nil {
		t.Fatal(err)
	}

	if err := idx.IndexDirectory(ctx, tmpDir); err != nil {
		t.Fatal(err)
	}

	// Only the non-vendor file should be indexed.
	idxStats := idx.Stats()
	if idxStats.FilesIndexed != 1 {
		t.Errorf("expected FilesIndexed=1 (vendor excluded), got %d", idxStats.FilesIndexed)
	}

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Should not have any nodes from vendor.
	nodes, err := store.QueryNodes(ctx, graph.NodeFilter{FilePath: filepath.Join(vendorDir, "dep.go")})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) > 0 {
		t.Errorf("expected no nodes from vendor directory, got %d", len(nodes))
	}
	_ = stats
}

func TestIncrementalUpdate(t *testing.T) {
	idx, store := setupTestIndexer(t)
	ctx := context.Background()

	tmpDir := t.TempDir()
	goFile := filepath.Join(tmpDir, "calc.go")

	// Version 1: single function.
	v1 := `package calc

func Add(a, b int) int {
	return a + b
}
`
	if err := os.WriteFile(goFile, []byte(v1), 0644); err != nil {
		t.Fatal(err)
	}

	if err := idx.IndexFile(ctx, goFile); err != nil {
		t.Fatal(err)
	}

	stats1, err := store.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nodeCount1 := stats1.NodeCount

	// Version 2: two functions.
	v2 := `package calc

func Add(a, b int) int {
	return a + b
}

func Subtract(a, b int) int {
	return a - b
}
`
	if err := os.WriteFile(goFile, []byte(v2), 0644); err != nil {
		t.Fatal(err)
	}

	// Re-index (should delete old, add new).
	if err := idx.IndexFile(ctx, goFile); err != nil {
		t.Fatal(err)
	}

	stats2, err := store.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Should have more nodes now (added Subtract function).
	if stats2.NodeCount <= nodeCount1 {
		t.Errorf("expected more nodes after adding function: v1=%d, v2=%d", nodeCount1, stats2.NodeCount)
	}

	// Verify Add still exists.
	nodes, err := store.QueryNodes(ctx, graph.NodeFilter{
		Type:     graph.NodeFunction,
		FilePath: goFile,
	})
	if err != nil {
		t.Fatal(err)
	}

	foundAdd := false
	foundSub := false
	for _, n := range nodes {
		if n.Name == "Add" {
			foundAdd = true
		}
		if n.Name == "Subtract" {
			foundSub = true
		}
	}
	if !foundAdd {
		t.Error("expected Add function after re-index")
	}
	if !foundSub {
		t.Error("expected Subtract function after re-index")
	}
}

func TestIncrementalUpdateRemovesOld(t *testing.T) {
	idx, store := setupTestIndexer(t)
	ctx := context.Background()

	tmpDir := t.TempDir()
	goFile := filepath.Join(tmpDir, "svc.go")

	// Version 1: has OldFunc.
	v1 := `package svc

func OldFunc() {}
func KeepFunc() {}
`
	if err := os.WriteFile(goFile, []byte(v1), 0644); err != nil {
		t.Fatal(err)
	}
	if err := idx.IndexFile(ctx, goFile); err != nil {
		t.Fatal(err)
	}

	// Verify OldFunc exists.
	nodes, err := store.QueryNodes(ctx, graph.NodeFilter{
		Type:     graph.NodeFunction,
		FilePath: goFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	foundOld := false
	for _, n := range nodes {
		if n.Name == "OldFunc" {
			foundOld = true
		}
	}
	if !foundOld {
		t.Fatal("expected OldFunc before update")
	}

	// Version 2: OldFunc removed.
	v2 := `package svc

func KeepFunc() {}
func NewFunc() {}
`
	if err := os.WriteFile(goFile, []byte(v2), 0644); err != nil {
		t.Fatal(err)
	}
	if err := idx.IndexFile(ctx, goFile); err != nil {
		t.Fatal(err)
	}

	// Verify OldFunc is gone and NewFunc exists.
	nodes, err = store.QueryNodes(ctx, graph.NodeFilter{
		Type:     graph.NodeFunction,
		FilePath: goFile,
	})
	if err != nil {
		t.Fatal(err)
	}

	foundOld = false
	foundNew := false
	foundKeep := false
	for _, n := range nodes {
		switch n.Name {
		case "OldFunc":
			foundOld = true
		case "NewFunc":
			foundNew = true
		case "KeepFunc":
			foundKeep = true
		}
	}
	if foundOld {
		t.Error("OldFunc should be removed after re-index")
	}
	if !foundNew {
		t.Error("expected NewFunc after re-index")
	}
	if !foundKeep {
		t.Error("expected KeepFunc to persist after re-index")
	}
}

func TestHasChangesAfterIndexFile(t *testing.T) {
	idx, _ := setupTestIndexer(t)
	ctx := context.Background()

	if idx.HasChanges() {
		t.Error("expected HasChanges=false for fresh indexer")
	}

	tmpDir := t.TempDir()
	goFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\nfunc main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := idx.IndexFile(ctx, goFile); err != nil {
		t.Fatal(err)
	}

	if !idx.HasChanges() {
		t.Error("expected HasChanges=true after indexing a file")
	}
}

func TestHasChangesNoFiles(t *testing.T) {
	idx, _ := setupTestIndexer(t)

	if idx.HasChanges() {
		t.Error("expected HasChanges=false for fresh indexer with no files indexed")
	}

	files := idx.ChangedFiles()
	if len(files) != 0 {
		t.Errorf("expected 0 changed files, got %d", len(files))
	}
}

func TestChangedFilesReturnsCopy(t *testing.T) {
	idx, _ := setupTestIndexer(t)
	ctx := context.Background()

	tmpDir := t.TempDir()
	goFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\nfunc main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := idx.IndexFile(ctx, goFile); err != nil {
		t.Fatal(err)
	}

	files1 := idx.ChangedFiles()
	files2 := idx.ChangedFiles()

	if len(files1) != len(files2) {
		t.Fatalf("expected same length, got %d vs %d", len(files1), len(files2))
	}

	// Mutate the first slice — should not affect the second call.
	if len(files1) > 0 {
		files1[0] = "mutated"
		files3 := idx.ChangedFiles()
		if files3[0] == "mutated" {
			t.Error("ChangedFiles returned a reference, not a copy")
		}
	}
}

func TestChangedFilesUnsupportedExtensionNotTracked(t *testing.T) {
	idx, _ := setupTestIndexer(t)
	ctx := context.Background()

	tmpDir := t.TempDir()
	txtFile := filepath.Join(tmpDir, "readme.txt")
	if err := os.WriteFile(txtFile, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	// Unsupported extension should not be tracked.
	if err := idx.IndexFile(ctx, txtFile); err != nil {
		t.Fatal(err)
	}

	if idx.HasChanges() {
		t.Error("expected HasChanges=false after indexing unsupported file type")
	}
}
