package vectorstore

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// mockEmbedder is a test embedding provider that returns deterministic vectors.
type mockEmbedder struct {
	dims      int
	callCount int
}

func (m *mockEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	m.callCount += len(texts)
	results := make([][]float32, len(texts))
	for i, text := range texts {
		vec := make([]float32, m.dims)
		// Simple hash-based vector for deterministic results.
		for j, c := range text {
			vec[j%m.dims] += float32(c) / 1000.0
		}
		// Normalize.
		var norm float32
		for _, v := range vec {
			norm += v * v
		}
		norm = float32(math.Sqrt(float64(norm)))
		if norm > 0 {
			for j := range vec {
				vec[j] /= norm
			}
		}
		results[i] = vec
	}
	return results, nil
}

func (m *mockEmbedder) EmbedQuery(_ context.Context, text string) ([]float32, error) {
	results, err := m.Embed(context.Background(), []string{text})
	if err != nil {
		return nil, err
	}
	return results[0], nil
}

func (m *mockEmbedder) Dimensions() int  { return m.dims }
func (m *mockEmbedder) Name() string     { return "mock" }
func (m *mockEmbedder) ModelName() string { return "mock-embed" }

// mockGraphStore is a minimal graph.Store for testing.
type mockGraphStore struct {
	nodes map[string]*graph.Node
}

func newMockGraphStore() *mockGraphStore {
	return &mockGraphStore{nodes: make(map[string]*graph.Node)}
}

func (s *mockGraphStore) AddNode(_ context.Context, node *graph.Node) error {
	s.nodes[node.ID] = node
	return nil
}

func (s *mockGraphStore) UpdateNode(_ context.Context, node *graph.Node) error {
	s.nodes[node.ID] = node
	return nil
}

func (s *mockGraphStore) DeleteNode(_ context.Context, id string) error {
	delete(s.nodes, id)
	return nil
}

func (s *mockGraphStore) GetNode(_ context.Context, id string) (*graph.Node, error) {
	n, ok := s.nodes[id]
	if !ok {
		return nil, fmt.Errorf("node %s not found", id)
	}
	return n, nil
}

func (s *mockGraphStore) QueryNodes(_ context.Context, filter graph.NodeFilter) ([]*graph.Node, error) {
	var results []*graph.Node
	for _, n := range s.nodes {
		if filter.Type != "" && n.Type != filter.Type {
			continue
		}
		results = append(results, n)
	}
	return results, nil
}

func (s *mockGraphStore) AddEdge(_ context.Context, _ *graph.Edge) error     { return nil }
func (s *mockGraphStore) DeleteEdge(_ context.Context, _ string) error        { return nil }
func (s *mockGraphStore) GetEdges(_ context.Context, _ string, _ graph.EdgeType) ([]*graph.Edge, error) {
	return nil, nil
}
func (s *mockGraphStore) GetNeighbors(_ context.Context, _ string, _ graph.EdgeType, _ graph.Direction) ([]*graph.Node, error) {
	return nil, nil
}
func (s *mockGraphStore) DeleteByFile(_ context.Context, _ string) error { return nil }
func (s *mockGraphStore) Stats(_ context.Context) (*graph.GraphStats, error) {
	return &graph.GraphStats{}, nil
}
func (s *mockGraphStore) Close() error { return nil }

func setupTestVectorStore(t *testing.T) (*VectorStore, *mockGraphStore, *mockEmbedder) {
	t.Helper()
	dir := t.TempDir()
	graphStore := newMockGraphStore()
	embedder := &mockEmbedder{dims: 32}

	vs, err := New(
		graphStore, embedder, "test",
		filepath.Join(dir, "vec.idx"),
		filepath.Join(dir, "vec.db"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { vs.Close() })

	return vs, graphStore, embedder
}

func TestVectorStoreIndexAndSearch(t *testing.T) {
	vs, graphStore, _ := setupTestVectorStore(t)
	ctx := context.Background()

	// Add test nodes.
	nodes := []*graph.Node{
		{ID: "n1", Type: graph.NodeFunction, Name: "HandleAuth", DocComment: "Handles user authentication and JWT token validation"},
		{ID: "n2", Type: graph.NodeFunction, Name: "ProcessPayment", DocComment: "Processes credit card payments and refunds"},
		{ID: "n3", Type: graph.NodeFunction, Name: "SendEmail", DocComment: "Sends email notifications to users"},
	}
	for _, n := range nodes {
		if err := graphStore.AddNode(ctx, n); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
		if err := vs.IndexNode(ctx, n); err != nil {
			t.Fatalf("IndexNode: %v", err)
		}
	}

	if vs.Len() != 3 {
		t.Errorf("Len = %d, want 3", vs.Len())
	}

	// Search for authentication-related content.
	results, err := vs.Search(ctx, "authentication login", 3)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Search returned no results")
	}

	// The auth-related node should appear in results.
	found := false
	for _, r := range results {
		if r.Node.ID == "n1" {
			found = true
			if r.Score <= 0 {
				t.Errorf("score should be positive, got %f", r.Score)
			}
		}
	}
	if !found {
		t.Error("expected HandleAuth node in search results")
	}
}

func TestVectorStoreRemoveNode(t *testing.T) {
	vs, graphStore, _ := setupTestVectorStore(t)
	ctx := context.Background()

	node := &graph.Node{ID: "n1", Type: graph.NodeFunction, Name: "Foo", DocComment: "test function"}
	graphStore.AddNode(ctx, node)
	vs.IndexNode(ctx, node)

	if vs.Len() != 1 {
		t.Fatalf("Len = %d, want 1", vs.Len())
	}

	vs.RemoveNode(ctx, "n1")

	if vs.Len() != 0 {
		t.Errorf("Len = %d after remove, want 0", vs.Len())
	}
}

func TestVectorStoreSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	graphStore := newMockGraphStore()
	embedder := &mockEmbedder{dims: 32}
	ctx := context.Background()

	// Create, index, and save.
	vs1, err := New(graphStore, embedder, "test",
		filepath.Join(dir, "vec.idx"),
		filepath.Join(dir, "vec.db"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	node := &graph.Node{ID: "n1", Type: graph.NodeDocument, Name: "TestDoc", DocComment: "This is a test document"}
	graphStore.AddNode(ctx, node)
	vs1.IndexNode(ctx, node)

	if err := vs1.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	vs1.Close()

	// Load into a new store.
	vs2, err := New(graphStore, embedder, "test",
		filepath.Join(dir, "vec.idx"),
		filepath.Join(dir, "vec.db"),
	)
	if err != nil {
		t.Fatalf("New (reload): %v", err)
	}
	defer vs2.Close()

	loaded, err := vs2.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded {
		t.Fatal("Load returned false, expected true")
	}
	if vs2.Len() != 1 {
		t.Errorf("Len after load = %d, want 1", vs2.Len())
	}

	// Search should still work.
	results, err := vs2.Search(ctx, "test document", 5)
	if err != nil {
		t.Fatalf("Search after load: %v", err)
	}
	if len(results) == 0 {
		t.Error("Search after load returned no results")
	}
}

func TestVectorStoreRebuild(t *testing.T) {
	vs, graphStore, embedder := setupTestVectorStore(t)
	ctx := context.Background()

	// Add nodes to graph store.
	nodes := []*graph.Node{
		{ID: "n1", Type: graph.NodeFunction, Name: "Func1", DocComment: "does something"},
		{ID: "n2", Type: graph.NodeMethod, Name: "Method1", DocComment: "does another thing"},
		{ID: "n3", Type: graph.NodeFile, Name: "file.go"}, // not embeddable
	}
	for _, n := range nodes {
		graphStore.AddNode(ctx, n)
	}

	embedder.callCount = 0
	if err := vs.Rebuild(ctx); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	// Only embeddable nodes should be indexed.
	if vs.Len() != 2 {
		t.Errorf("Len after rebuild = %d, want 2", vs.Len())
	}

	meta := vs.Meta()
	if meta == nil {
		t.Fatal("Meta is nil after rebuild")
	}
	if meta.Provider != "mock" {
		t.Errorf("Provider = %q, want mock", meta.Provider)
	}
	if meta.NodeCount != 2 {
		t.Errorf("NodeCount = %d, want 2", meta.NodeCount)
	}
}

func TestVectorStoreNeedsReindex(t *testing.T) {
	vs, _, _ := setupTestVectorStore(t)

	// No meta → needs reindex.
	if !vs.NeedsReindex() {
		t.Error("NeedsReindex should be true when no meta")
	}

	// Set meta matching current embedder.
	vs.meta = &VectorIndexMeta{
		Provider: "mock",
		Model:    "mock-embed",
	}
	if vs.NeedsReindex() {
		t.Error("NeedsReindex should be false when provider/model match")
	}

	// Change provider in meta.
	vs.meta.Provider = "other"
	if !vs.NeedsReindex() {
		t.Error("NeedsReindex should be true when provider differs")
	}
}

func TestVectorStoreNotAvailable(t *testing.T) {
	var vs *VectorStore
	if vs.Available() {
		t.Error("nil VectorStore should not be available")
	}
}

func TestVectorStoreChunkedDocument(t *testing.T) {
	vs, graphStore, _ := setupTestVectorStore(t)
	ctx := context.Background()

	// Create a document large enough to be chunked.
	bigDoc := &graph.Node{
		ID:         "doc1",
		Type:       graph.NodeDocument,
		Name:       "Architecture Analysis",
		DocComment: generateLargeText(3000),
	}
	graphStore.AddNode(ctx, bigDoc)
	if err := vs.IndexNode(ctx, bigDoc); err != nil {
		t.Fatalf("IndexNode: %v", err)
	}

	// With 3000 chars and 1500 chunk size, we should have multiple vectors.
	if vs.Len() < 2 {
		t.Errorf("expected chunked document to produce >= 2 vectors, got %d", vs.Len())
	}

	// Search should return results with chunk text.
	results, err := vs.Search(ctx, "architecture", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Search returned no results for chunked doc")
	}
	if results[0].ChunkText == "" {
		t.Error("expected non-empty chunk text in results")
	}
}

func TestParseChunkKey(t *testing.T) {
	tests := []struct {
		key      string
		wantID   string
		wantIdx  int
	}{
		{"abc123:0", "abc123", 0},
		{"abc123:5", "abc123", 5},
		{"abc123", "abc123", 0},
	}
	for _, tc := range tests {
		id, idx := parseChunkKey(tc.key)
		if id != tc.wantID || idx != tc.wantIdx {
			t.Errorf("parseChunkKey(%q) = (%q, %d), want (%q, %d)", tc.key, id, idx, tc.wantID, tc.wantIdx)
		}
	}
}

func generateLargeText(size int) string {
	sentence := "The architecture uses a layered approach with clear separation of concerns. "
	var result string
	for len(result) < size {
		result += sentence
	}
	return result[:size]
}
