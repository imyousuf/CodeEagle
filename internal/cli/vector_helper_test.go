package cli

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/vectorstore"
)

// letterEmbedder embeds text as normalised letter counts: enough to tell one
// wording from another, with no network.
type letterEmbedder struct{}

func (letterEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, 26)
		for _, r := range strings.ToLower(t) {
			if r >= 'a' && r <= 'z' {
				v[r-'a']++
			}
		}
		var n float64
		for _, x := range v {
			n += float64(x * x)
		}
		for j := range v {
			if n > 0 {
				v[j] /= float32(math.Sqrt(n))
			}
		}
		out[i] = v
	}
	return out, nil
}

func (e letterEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	v, err := e.Embed(ctx, []string{text})
	return v[0], err
}
func (letterEmbedder) Dimensions() int   { return 26 }
func (letterEmbedder) Name() string      { return "letters" }
func (letterEmbedder) ModelName() string { return "letters-v1" }

func newTestVectors(t *testing.T, store graph.Store) *vectorstore.VectorStore {
	t.Helper()
	dir := t.TempDir()
	vs, err := vectorstore.New(store, letterEmbedder{}, "main",
		filepath.Join(dir, "vec.idx"), filepath.Join(dir, "vec.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { vs.Close() })
	if err := vs.Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := vs.Save(); err != nil {
		t.Fatal(err)
	}
	return vs
}

func vectorOf(t *testing.T, n *graph.Node) []float32 {
	t.Helper()
	v, err := letterEmbedder{}.Embed(context.Background(), []string{vectorstore.EmbeddableText(n)})
	if err != nil {
		t.Fatal(err)
	}
	return v[0]
}

func sameVector(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Abs(float64(a[i]-b[i])) > 1e-6 {
			return false
		}
	}
	return true
}

// TestIncrementalSyncReembedsChangedFiles covers a file edited after the
// index was built. Its node keeps its ID, so without re-embedding its vector
// would go on describing the old text.
func TestIncrementalSyncReembedsChangedFiles(t *testing.T) {
	store, err := embedded.NewBranchStore(t.TempDir(), "main", []string{"main"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	node := &graph.Node{ID: "s1", Type: graph.NodeDocument, Name: "Limits", FilePath: "docs/retry.md",
		DocComment: "alpha alpha alpha"}
	if err := store.AddNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	vs := newTestVectors(t, store)

	node.DocComment = "zebra zebra zebra"
	if err := store.UpdateNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	if err := syncVectorIndex(vs, cfg, false, store, []string{"docs/retry.md"}, func(string, ...any) {}); err != nil {
		t.Fatalf("syncVectorIndex: %v", err)
	}

	got, ok := vs.Vector("s1")
	if !ok {
		t.Fatal("the changed node has no vector")
	}
	if !sameVector(got, vectorOf(t, node)) {
		t.Error("the changed node's vector still describes its old text")
	}
}

// TestCodeSyncKeepsMeetingVectors covers the view the vector index is kept
// against. A code sync's store reads only branches; without the meetings
// scope in view, every meeting vector looks like a deleted node's and the
// reconcile drops them all.
func TestCodeSyncKeepsMeetingVectors(t *testing.T) {
	dbDir := t.TempDir()
	ctx := context.Background()

	meetings, err := embedded.NewBranchStore(dbDir, embedded.MeetingScope, []string{embedded.MeetingScope})
	if err != nil {
		t.Fatal(err)
	}
	if err := meetings.AddNode(ctx, &graph.Node{ID: "m1", Type: graph.NodeMeeting, Name: "Platform sync",
		DocComment: "raised the retry cap"}); err != nil {
		t.Fatal(err)
	}
	meetings.Close()

	store, err := embedded.NewBranchStore(dbDir, "main", []string{"main"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	view := vectorGraphView(store)
	vs := newTestVectors(t, view)
	if _, ok := vs.Vector("m1"); !ok {
		t.Fatal("setup: the meeting was not embedded through the view")
	}

	if err := syncVectorIndex(vs, &config.Config{}, false, view, nil, func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	if _, ok := vs.Vector("m1"); !ok {
		t.Error("a code sync dropped a meeting's vector")
	}
}
