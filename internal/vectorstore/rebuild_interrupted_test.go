package vectorstore

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// seedEmbeddable fills a graph store with nodes a rebuild will pick up.
func seedEmbeddable(t *testing.T, store *mockGraphStore, n int) {
	t.Helper()
	for i := range n {
		if err := store.AddNode(context.Background(), &graph.Node{
			ID:            fmt.Sprintf("node-%03d", i),
			Type:          graph.NodeDocument,
			Name:          fmt.Sprintf("doc-%03d.md", i),
			QualifiedName: fmt.Sprintf("doc-%03d.md", i),
			DocComment:    fmt.Sprintf("Notes about the retention job, part %d.", i),
		}); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}
}

// failingEmbedder embeds a few batches and then refuses, standing in for an
// embedding server that restarts or a quota that runs out partway.
type failingEmbedder struct {
	countingEmbedder
	failAfter int
}

func (e *failingEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	e.mu.Lock()
	done := e.requests
	e.mu.Unlock()
	if done >= e.failAfter {
		return nil, errors.New("embedding backend went away")
	}
	return e.countingEmbedder.Embed(ctx, texts)
}

// TestInterruptedRebuildIsRecorded is the guard on the worst failure this
// store can have.
//
// A rebuild deletes every stored chunk before embedding the first
// replacement, and writes the graph of vectors back only at the end. If the
// embedder fails in between, the two halves disagree: the file on disk still
// describes a full index while the chunks it names are gone. Nothing about
// the node count, the file, or a later search says so — searches simply
// answer from whatever fraction survived, and a following incremental sync
// can save the stale graph back over the gutted store, making it permanent.
//
// The rebuild must therefore leave a mark that outlives the process.
func TestInterruptedRebuildIsRecorded(t *testing.T) {
	embedder := &failingEmbedder{failAfter: 1}
	store := newMockGraphStore()
	seedEmbeddable(t, store, 150)
	dir := t.TempDir()
	vs, err := New(
		store, embedder, "test",
		filepath.Join(dir, "vec.idx"),
		filepath.Join(dir, "vec.db"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer vs.Close()

	if err := vs.Rebuild(context.Background()); err == nil {
		t.Fatal("expected the rebuild to fail")
	}

	if !vs.Rebuilding() {
		t.Error("an interrupted rebuild was not recorded; the index looks healthy " +
			"while its vectors are gone")
	}
	if m := vs.Meta(); m != nil && !m.Rebuilding {
		t.Error("the marker did not reach the metadata a later process reads")
	}
}

// TestCompletedRebuildClearsTheMark checks the other direction: a rebuild
// that finishes must not leave the warning behind, or it becomes noise that
// gets ignored when it matters.
func TestCompletedRebuildClearsTheMark(t *testing.T) {
	store := newMockGraphStore()
	seedEmbeddable(t, store, 20)
	dir := t.TempDir()
	vs, err := New(
		store, &countingEmbedder{}, "test",
		filepath.Join(dir, "vec.idx"),
		filepath.Join(dir, "vec.db"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer vs.Close()

	if err := vs.Rebuild(context.Background()); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if vs.Rebuilding() {
		t.Error("a completed rebuild is still marked as interrupted")
	}
	if m := vs.Meta(); m == nil || m.Rebuilding {
		t.Error("the metadata still records a rebuild in progress")
	}
}
