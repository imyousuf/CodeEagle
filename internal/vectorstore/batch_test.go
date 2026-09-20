package vectorstore

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

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

// newTestStore builds a store backed by the given embedder.
func newTestStore(t *testing.T, embedder *countingEmbedder) *VectorStore {
	t.Helper()
	dir := t.TempDir()
	vs, err := New(
		newMockGraphStore(), embedder, "test",
		filepath.Join(dir, "vec.idx"),
		filepath.Join(dir, "vec.db"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { vs.Close() })
	return vs
}

// TestIndexNodesBatches covers grouping chunks into one request.
//
// Indexing used to send one request per node. Almost all of the time went on
// round-trip overhead rather than work — a full rebuild of this corpus spent
// eighty-four minutes of wall clock on ninety seconds of CPU — so the fix is
// to ask for many at once.
func TestIndexNodesBatches(t *testing.T) {
	const nodeCount = 150

	embedder := &countingEmbedder{}
	vs := newTestStore(t, embedder)

	nodes := make([]*graph.Node, 0, nodeCount)
	for i := range nodeCount {
		nodes = append(nodes, &graph.Node{
			ID:            fmt.Sprintf("node-%03d", i),
			Type:          graph.NodeDocument,
			Name:          fmt.Sprintf("doc-%03d.md", i),
			QualifiedName: fmt.Sprintf("doc-%03d.md", i),
			DocComment:    fmt.Sprintf("Notes about the retention job, part %d.", i),
		})
	}

	if err := vs.IndexNodes(context.Background(), nodes); err != nil {
		t.Fatalf("IndexNodes: %v", err)
	}

	if embedder.chunks < nodeCount {
		t.Fatalf("embedded %d chunks for %d nodes", embedder.chunks, nodeCount)
	}
	// The point of the change: far fewer requests than nodes.
	if embedder.requests >= nodeCount {
		t.Errorf("made %d requests for %d nodes; they are not being batched",
			embedder.requests, nodeCount)
	}
	if embedder.largest < 2 {
		t.Errorf("largest request carried %d chunks; nothing was grouped", embedder.largest)
	}
	if embedder.largest > EmbedBatchSize {
		t.Errorf("a request carried %d chunks, over the %d cap", embedder.largest, EmbedBatchSize)
	}
	if got := vs.Len(); got != embedder.chunks {
		t.Errorf("index holds %d vectors, want %d", got, embedder.chunks)
	}
}

// TestIndexNodesReportsProgress covers being able to tell slow from stuck.
func TestIndexNodesReportsProgress(t *testing.T) {
	embedder := &countingEmbedder{}
	vs := newTestStore(t, embedder)

	var lastDone, lastTotal int
	var calls int
	vs.WithProgress(func(done, total int) {
		calls++
		lastDone, lastTotal = done, total
	})

	nodes := []*graph.Node{
		{ID: "a", Type: graph.NodeDocument, Name: "a.md", DocComment: "first"},
		{ID: "b", Type: graph.NodeDocument, Name: "b.md", DocComment: "second"},
		{ID: "c", Type: graph.NodeDocument, Name: "c.md", DocComment: "third"},
	}
	if err := vs.indexNodes(context.Background(), nodes, vs.progress); err != nil {
		t.Fatalf("indexNodes: %v", err)
	}

	if calls != len(nodes) {
		t.Errorf("progress reported %d times for %d nodes", calls, len(nodes))
	}
	if lastDone != len(nodes) || lastTotal != len(nodes) {
		t.Errorf("finished at %d/%d, want %d/%d", lastDone, lastTotal, len(nodes), len(nodes))
	}
}

// TestIndexNodesHonoursCancellation covers giving up when the caller does,
// which matters for a rebuild long enough that somebody will interrupt it.
func TestIndexNodesHonoursCancellation(t *testing.T) {
	embedder := &countingEmbedder{}
	vs := newTestStore(t, embedder)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	nodes := []*graph.Node{{ID: "a", Type: graph.NodeDocument, Name: "a.md", DocComment: "x"}}
	if err := vs.IndexNodes(ctx, nodes); err == nil {
		t.Error("IndexNodes ignored a cancelled context")
	}
}
