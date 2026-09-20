package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

func TestBackpopContentHash(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	// Create a temp file on disk.
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "hello.go")
	content := []byte("package main\n\nfunc main() {}\n")
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	expectedHash := computeContentHash(content)

	// Add a file node without content_hash.
	node := &graph.Node{
		ID:       graph.NewNodeID(string(graph.NodeFile), "hello.go", "hello.go"),
		Type:     graph.NodeFile,
		Name:     "hello.go",
		FilePath: "hello.go",
	}
	if err := store.AddNode(ctx, node); err != nil {
		t.Fatal(err)
	}

	// Run backpop.
	count, err := BackpopContentHash(ctx, store, []string{tmpDir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 node updated, got %d", count)
	}

	// Verify content_hash and mime_type were set.
	nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeFile})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 file node, got %d", len(nodes))
	}
	if nodes[0].Properties[graph.PropContentHash] != expectedHash {
		t.Errorf("content_hash = %q, want %q", nodes[0].Properties[graph.PropContentHash], expectedHash)
	}
	if nodes[0].Properties[graph.PropMimeType] == "" {
		t.Error("mime_type should be set after backpop")
	}
}

func TestBackpopContentHashSkipsAlreadyPopulated(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	// Add a file node with content_hash already set.
	node := &graph.Node{
		ID:       graph.NewNodeID(string(graph.NodeFile), "done.go", "done.go"),
		Type:     graph.NodeFile,
		Name:     "done.go",
		FilePath: "done.go",
		Properties: map[string]string{
			graph.PropContentHash: "sha256:abc123",
			graph.PropMimeType:    "text/x-go",
		},
	}
	if err := store.AddNode(ctx, node); err != nil {
		t.Fatal(err)
	}

	count, err := BackpopContentHash(ctx, store, []string{"/nonexistent"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 updates for already-populated nodes, got %d", count)
	}
}

func TestBackpopContentHashDocumentNodes(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	// Create a temp image file.
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "photo.jpg")
	content := []byte("fake jpeg content")
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	// Add a Document node without content_hash.
	node := &graph.Node{
		ID:       graph.NewNodeID(string(graph.NodeDocument), "photo.jpg", "photo.jpg"),
		Type:     graph.NodeDocument,
		Name:     "photo.jpg",
		FilePath: "photo.jpg",
	}
	if err := store.AddNode(ctx, node); err != nil {
		t.Fatal(err)
	}

	count, err := BackpopContentHash(ctx, store, []string{tmpDir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 node updated, got %d", count)
	}

	nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeDocument})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 document node, got %d", len(nodes))
	}
	if nodes[0].Properties[graph.PropMimeType] != "image/jpeg" {
		t.Errorf("mime_type = %q, want %q", nodes[0].Properties[graph.PropMimeType], "image/jpeg")
	}
}

func TestBackpopContentHashSkipsDirectory(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	// Add a Directory node — should not be processed.
	node := &graph.Node{
		ID:       graph.NewNodeID(string(graph.NodeDirectory), "src", "src"),
		Type:     graph.NodeDirectory,
		Name:     "src",
		FilePath: "src",
	}
	if err := store.AddNode(ctx, node); err != nil {
		t.Fatal(err)
	}

	count, err := BackpopContentHash(ctx, store, []string{"/nonexistent"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 updates for Directory nodes, got %d", count)
	}
}
