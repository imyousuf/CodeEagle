package vectorstore

import (
	"context"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph/memstore"
)

func TestVectorStoreNeedsReindex(t *testing.T) {
	vs, _, _ := setupTestVectorStore(t)

	// No meta → needs reindex.
	if !vs.NeedsReindex() {
		t.Error("NeedsReindex should be true when no meta")
	}

	// Set meta matching current embedder and text.
	vs.meta = &VectorIndexMeta{
		Provider:    "mock",
		Model:       "mock-embed",
		TextVersion: testTextVersion,
	}
	if vs.NeedsReindex() {
		t.Error("NeedsReindex should be false when provider/model match")
	}

	// A version other than the current one is a text change; an index that
	// recorded none cannot say what text it was built from, and is treated
	// the same way.
	for _, v := range []int{testTextVersion - 1, 0} {
		vs.meta.TextVersion = v
		if !vs.NeedsReindex() {
			t.Errorf("NeedsReindex should be true at text version %d", v)
		}
		if r := vs.ReindexReason(); !strings.Contains(r, "text version") {
			t.Errorf("ReindexReason at %d = %q, want it to name the text version", v, r)
		}
	}
	vs.meta.TextVersion = testTextVersion

	// Change provider in meta.
	vs.meta.Provider = "other"
	if !vs.NeedsReindex() {
		t.Error("NeedsReindex should be true when provider differs")
	}
}

func TestParseChunkKey(t *testing.T) {
	tests := []struct {
		key     string
		wantID  string
		wantIdx int
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

// TestIndexNodesReportsProgress covers being able to tell slow from stuck.
func TestIndexNodesReportsProgress(t *testing.T) {
	embedder := &countingEmbedder{}
	vs := newTestStore(t, memstore.New(), embedder)

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
