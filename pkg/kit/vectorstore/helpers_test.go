package vectorstore

import (
	"context"
	"math"
	"sync"
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/kit/embedding"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph/memstore"
)

// These tests reach into the store, so they live beside it. The ones that
// pin CodeEagle's own configuration of it, with Badger underneath, stay in
// CodeEagle (internal/vectorstore).

// testTextVersion is the version of testText.
const testTextVersion = 2

// testText embeds a node by its name and doc comment.
func testText(n *graph.Node) string {
	if n.DocComment == "" {
		return ""
	}
	return n.Name + "\n" + n.DocComment
}

// newTestStore builds a store over g, held in memory, embedding functions,
// documents and relations, and re-embedding relations that have no hash.
func newTestStore(t *testing.T, g graph.Store, embedder embedding.Provider) *Store {
	t.Helper()
	vs := New(g, embedder, NewMemoryPersistence(),
		WithEmbeddable(graph.NodeFunction, graph.NodeDocument, graph.NodeRelation),
		WithEmbedText(testText, testTextVersion),
		WithReembedWithoutHash(func(n *graph.Node) bool { return n.Type == graph.NodeRelation }),
		WithNamespace("test"))
	t.Cleanup(func() { vs.Close() })
	return vs
}

// setupTestVectorStore builds a store, its graph and a deterministic
// embedder.
func setupTestVectorStore(t *testing.T) (*Store, *memstore.Store, *mockEmbedder) {
	t.Helper()
	graphStore := memstore.New()
	embedder := &mockEmbedder{dims: 32}
	return newTestStore(t, graphStore, embedder), graphStore, embedder
}

// dropTextHash forgets a node's text hash, as an index written before
// hashes were recorded has none.
func dropTextHash(vs *Store, nodeID string) {
	m := vs.p.(*memory)
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.hashes, nodeID)
}

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

func (m *mockEmbedder) Dimensions() int   { return m.dims }
func (m *mockEmbedder) Name() string      { return "mock" }
func (m *mockEmbedder) ModelName() string { return "mock-embed" }

// countingEmbedder records how it was called, so a test can tell one request
// carrying many chunks from many requests carrying one.
type countingEmbedder struct {
	mu       sync.Mutex
	requests int
	chunks   int
	largest  int
}

func (e *countingEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	e.mu.Lock()
	e.requests++
	e.chunks += len(texts)
	if len(texts) > e.largest {
		e.largest = len(texts)
	}
	e.mu.Unlock()

	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{float32(i), 1, 0}
	}
	return out, nil
}

func (e *countingEmbedder) EmbedQuery(_ context.Context, _ string) ([]float32, error) {
	return []float32{0, 1, 0}, nil
}

func (e *countingEmbedder) Name() string      { return "counting" }
func (e *countingEmbedder) ModelName() string { return "counting-v1" }
func (e *countingEmbedder) Dimensions() int   { return 3 }
