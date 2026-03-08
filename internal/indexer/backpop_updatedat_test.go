package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

func TestNeedsUpdatedAtBackpop(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	// Empty store — no backpop needed.
	if needsUpdatedAtBackpop(ctx, store) {
		t.Error("expected false for empty store")
	}

	// Add a file node without UpdatedAt.
	node := &graph.Node{
		ID:       graph.NewNodeID(string(graph.NodeFile), "main.go", "main.go"),
		Type:     graph.NodeFile,
		Name:     "main.go",
		FilePath: "main.go",
	}
	if err := store.AddNode(ctx, node); err != nil {
		t.Fatal(err)
	}

	if !needsUpdatedAtBackpop(ctx, store) {
		t.Error("expected true when file node has zero UpdatedAt")
	}

	// Update the node with a timestamp.
	node.UpdatedAt = time.Now()
	if err := store.UpdateNode(ctx, node); err != nil {
		t.Fatal(err)
	}

	if needsUpdatedAtBackpop(ctx, store) {
		t.Error("expected false when all file nodes have UpdatedAt")
	}
}

func TestNeedsUpdatedAtBackpopDocumentNodes(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	// Add only a Document node (no NodeFile) — like a home dir with PDFs/images.
	node := &graph.Node{
		ID:       graph.NewNodeID(string(graph.NodeDocument), "photo.jpg", "photo.jpg"),
		Type:     graph.NodeDocument,
		Name:     "photo.jpg",
		FilePath: "photo.jpg",
	}
	if err := store.AddNode(ctx, node); err != nil {
		t.Fatal(err)
	}

	if !needsUpdatedAtBackpop(ctx, store) {
		t.Error("expected true when Document node has zero UpdatedAt")
	}

	node.UpdatedAt = time.Now()
	if err := store.UpdateNode(ctx, node); err != nil {
		t.Fatal(err)
	}

	if needsUpdatedAtBackpop(ctx, store) {
		t.Error("expected false when Document node has UpdatedAt set")
	}
}

func TestBackpopUpdatedAt(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	// Create a temp file on disk.
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "hello.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatal(err)
	}
	expectedMtime := info.ModTime()

	// Add a file node without UpdatedAt, using relative path.
	node := &graph.Node{
		ID:       graph.NewNodeID(string(graph.NodeFile), "hello.go", "hello.go"),
		Type:     graph.NodeFile,
		Name:     "hello.go",
		FilePath: "hello.go",
	}
	if err := store.AddNode(ctx, node); err != nil {
		t.Fatal(err)
	}

	// Run backpop with tmpDir as repo root.
	count, err := BackpopUpdatedAt(ctx, store, []string{tmpDir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 node updated, got %d", count)
	}

	// Verify UpdatedAt was set to the file's mtime.
	nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeFile})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 file node, got %d", len(nodes))
	}
	if !nodes[0].UpdatedAt.Equal(expectedMtime) {
		t.Errorf("UpdatedAt = %v, want %v", nodes[0].UpdatedAt, expectedMtime)
	}

	// Verify date hierarchy was created.
	dateNodes, _ := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeDate})
	if len(dateNodes) != 1 {
		t.Errorf("expected 1 Date node from backpop, got %d", len(dateNodes))
	}
}

func TestBackpopSkipsAlreadyPopulated(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	// Add a file node with UpdatedAt already set.
	node := &graph.Node{
		ID:        graph.NewNodeID(string(graph.NodeFile), "done.go", "done.go"),
		Type:      graph.NodeFile,
		Name:      "done.go",
		FilePath:  "done.go",
		UpdatedAt: time.Now(),
	}
	if err := store.AddNode(ctx, node); err != nil {
		t.Fatal(err)
	}

	count, err := BackpopUpdatedAt(ctx, store, []string{"/nonexistent"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 updates for already-populated nodes, got %d", count)
	}
}
