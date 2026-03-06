package queue

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/docs"
	"github.com/imyousuf/CodeEagle/internal/graph"
)

// mockProvider implements docs.Provider for testing.
type mockProvider struct {
	extractFn  func(ctx context.Context, text string) (*docs.ExtractionResult, error)
	describeFn func(ctx context.Context, data []byte, mime string) (*docs.ExtractionResult, error)
}

func (m *mockProvider) ExtractTopics(ctx context.Context, text string) (*docs.ExtractionResult, error) {
	if m.extractFn != nil {
		return m.extractFn(ctx, text)
	}
	return &docs.ExtractionResult{Summary: "test", Topics: []string{"testing"}}, nil
}

func (m *mockProvider) DescribeImage(ctx context.Context, data []byte, mime string) (*docs.ExtractionResult, error) {
	if m.describeFn != nil {
		return m.describeFn(ctx, data, mime)
	}
	return &docs.ExtractionResult{Summary: "test image", Topics: []string{"photo"}}, nil
}

func (m *mockProvider) Name() string      { return "mock" }
func (m *mockProvider) ModelName() string  { return "test-model" }

// mockStore implements graph.Store for testing.
type mockStore struct {
	nodes map[string]*graph.Node
	edges map[string]*graph.Edge
}

func newMockStore() *mockStore {
	return &mockStore{
		nodes: make(map[string]*graph.Node),
		edges: make(map[string]*graph.Edge),
	}
}

func (s *mockStore) AddNode(_ context.Context, node *graph.Node) error {
	s.nodes[node.ID] = node
	return nil
}

func (s *mockStore) UpdateNode(_ context.Context, node *graph.Node) error {
	s.nodes[node.ID] = node
	return nil
}

func (s *mockStore) DeleteNode(_ context.Context, id string) error {
	delete(s.nodes, id)
	return nil
}

func (s *mockStore) GetNode(_ context.Context, id string) (*graph.Node, error) {
	n, ok := s.nodes[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return n, nil
}

func (s *mockStore) QueryNodes(_ context.Context, filter graph.NodeFilter) ([]*graph.Node, error) {
	var result []*graph.Node
	for _, n := range s.nodes {
		if filter.Type != "" && n.Type != filter.Type {
			continue
		}
		if filter.FilePath != "" && n.FilePath != filter.FilePath {
			continue
		}
		result = append(result, n)
	}
	return result, nil
}

func (s *mockStore) AddEdge(_ context.Context, edge *graph.Edge) error {
	s.edges[edge.ID] = edge
	return nil
}

func (s *mockStore) DeleteEdge(_ context.Context, id string) error {
	delete(s.edges, id)
	return nil
}

func (s *mockStore) GetEdges(_ context.Context, _ string, _ graph.EdgeType) ([]*graph.Edge, error) {
	return nil, nil
}

func (s *mockStore) GetNeighbors(_ context.Context, _ string, _ graph.EdgeType, _ graph.Direction) ([]*graph.Node, error) {
	return nil, nil
}

func (s *mockStore) DeleteByFile(_ context.Context, _ string) error { return nil }

func (s *mockStore) Stats(_ context.Context) (*graph.GraphStats, error) {
	return &graph.GraphStats{}, nil
}

func (s *mockStore) Close() error { return nil }

// createTestFile creates a file in a temp directory and returns its path.
func createTestFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDocExtractHandler_CacheHit(t *testing.T) {
	store := newMockStore()
	cache, err := docs.OpenCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	filePath := createTestFile(t, "test.md", "# Hello World\n\nSome content about testing.")

	// Pre-populate cache.
	_ = cache.Store(filePath, "sha256:abc", &docs.ExtractionResult{
		Summary: "cached summary",
		Topics:  []string{"cached-topic"},
	})

	// Add a node to the store.
	nodeID := graph.NewNodeID(string(graph.NodeDocument), filePath, filepath.Base(filePath))
	store.AddNode(context.Background(), &graph.Node{
		ID:       nodeID,
		Type:     graph.NodeDocument,
		FilePath: filePath,
		Properties: map[string]string{
			"content_hash": "sha256:abc",
		},
	})

	handler := NewDocExtractHandler(&mockProvider{}, cache, store)
	job := &Job{
		ContentHash: "sha256:abc",
		FilePaths:   []string{filePath},
	}

	result, err := handler.Handle(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected result")
	}

	// Verify node was updated.
	node := store.nodes[nodeID]
	if node.DocComment != "cached summary" {
		t.Errorf("DocComment = %q, want 'cached summary'", node.DocComment)
	}
}

func TestDocExtractHandler_CacheMiss(t *testing.T) {
	store := newMockStore()
	cache, err := docs.OpenCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	filePath := createTestFile(t, "readme.md", "# Getting Started\n\nThis project does amazing things.")

	nodeID := graph.NewNodeID(string(graph.NodeDocument), filePath, "readme.md")
	store.AddNode(context.Background(), &graph.Node{
		ID:       nodeID,
		Type:     graph.NodeDocument,
		FilePath: filePath,
		Properties: map[string]string{
			"content_hash": "sha256:newdoc",
		},
	})

	provider := &mockProvider{
		extractFn: func(_ context.Context, text string) (*docs.ExtractionResult, error) {
			return &docs.ExtractionResult{
				Summary:  "A getting started guide",
				Topics:   []string{"documentation", "setup"},
				Entities: []string{"project"},
			}, nil
		},
	}

	handler := NewDocExtractHandler(provider, cache, store)
	job := &Job{
		ContentHash: "sha256:newdoc",
		FilePaths:   []string{filePath},
	}

	result, err := handler.Handle(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected result")
	}

	// Verify node was updated.
	node := store.nodes[nodeID]
	if node.DocComment != "A getting started guide" {
		t.Errorf("DocComment = %q", node.DocComment)
	}
	if node.Properties["topics"] != "documentation,setup" {
		t.Errorf("topics = %q", node.Properties["topics"])
	}

	// Verify cache was populated.
	cached, _ := cache.Check(filePath, "sha256:newdoc")
	if cached == nil {
		t.Error("expected cache to be populated")
	}
}

func TestDocExtractHandler_ProviderError(t *testing.T) {
	store := newMockStore()

	filePath := createTestFile(t, "test.txt", "Some text content")

	provider := &mockProvider{
		extractFn: func(_ context.Context, _ string) (*docs.ExtractionResult, error) {
			return nil, fmt.Errorf("provider timeout")
		},
	}

	handler := NewDocExtractHandler(provider, nil, store)
	job := &Job{
		ContentHash: "sha256:err",
		FilePaths:   []string{filePath},
	}

	_, err := handler.Handle(context.Background(), job)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDocExtractHandler_NilProvider(t *testing.T) {
	store := newMockStore()

	filePath := createTestFile(t, "test.txt", "Some text content")

	handler := NewDocExtractHandler(nil, nil, store)
	job := &Job{
		ContentHash: "sha256:noprov",
		FilePaths:   []string{filePath},
	}

	result, err := handler.Handle(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	// Should return nil result gracefully.
	if result != nil {
		t.Errorf("expected nil result with nil provider, got %s", result)
	}
}

func TestDocExtractHandler_MultiPath(t *testing.T) {
	store := newMockStore()

	dir := t.TempDir()
	path1 := filepath.Join(dir, "a.md")
	path2 := filepath.Join(dir, "b.md")
	os.WriteFile(path1, []byte("# Hello"), 0644)
	os.WriteFile(path2, []byte("# Hello"), 0644)

	nodeID1 := graph.NewNodeID(string(graph.NodeDocument), path1, "a.md")
	nodeID2 := graph.NewNodeID(string(graph.NodeDocument), path2, "b.md")
	store.AddNode(context.Background(), &graph.Node{
		ID: nodeID1, Type: graph.NodeDocument, FilePath: path1,
		Properties: map[string]string{"content_hash": "sha256:same"},
	})
	store.AddNode(context.Background(), &graph.Node{
		ID: nodeID2, Type: graph.NodeDocument, FilePath: path2,
		Properties: map[string]string{"content_hash": "sha256:same"},
	})

	handler := NewDocExtractHandler(&mockProvider{}, nil, store)
	job := &Job{
		ContentHash: "sha256:same",
		FilePaths:   []string{path1, path2},
	}

	_, err := handler.Handle(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}

	// Both nodes should be updated.
	if store.nodes[nodeID1].DocComment == "" {
		t.Error("node1 DocComment not updated")
	}
	if store.nodes[nodeID2].DocComment == "" {
		t.Error("node2 DocComment not updated")
	}
}

func TestImageDescribeHandler_CacheHit(t *testing.T) {
	store := newMockStore()
	cache, err := docs.OpenCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	filePath := createTestFile(t, "photo.jpg", "fake image data")

	_ = cache.Store(filePath, "sha256:img1", &docs.ExtractionResult{
		Summary: "A photo of a sunset",
		Topics:  []string{"sunset", "nature"},
	})

	nodeID := graph.NewNodeID(string(graph.NodeDocument), filePath, "photo.jpg")
	store.AddNode(context.Background(), &graph.Node{
		ID: nodeID, Type: graph.NodeDocument, FilePath: filePath,
		Properties: map[string]string{"content_hash": "sha256:img1"},
	})

	handler := NewImageDescribeHandler(&mockProvider{}, cache, store, 1024)
	job := &Job{
		ContentHash: "sha256:img1",
		FilePaths:   []string{filePath},
	}

	result, err := handler.Handle(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected result")
	}

	node := store.nodes[nodeID]
	if node.DocComment != "A photo of a sunset" {
		t.Errorf("DocComment = %q", node.DocComment)
	}
}

func TestImageDescribeHandler_NilProvider(t *testing.T) {
	store := newMockStore()

	filePath := createTestFile(t, "photo.png", "fake png data")

	handler := NewImageDescribeHandler(nil, nil, store, 1024)
	job := &Job{
		ContentHash: "sha256:noimg",
		FilePaths:   []string{filePath},
	}

	result, err := handler.Handle(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Error("expected nil result with nil provider")
	}
}
