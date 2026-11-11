package vectorstore_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/kit/embedding"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph/memstore"
	"github.com/imyousuf/CodeEagle/pkg/kit/vectorstore"
)

// letters embeds a text as its letter counts, so texts sharing words are
// near each other and a query finds what it names.
type letters struct{}

func (letters) Embed(_ context.Context, texts []string) ([][]float32, error) {
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
			v[j] /= float32(math.Sqrt(n) + 1e-9)
		}
		out[i] = v
	}
	return out, nil
}
func (l letters) EmbedQuery(ctx context.Context, q string) ([]float32, error) {
	v, err := l.Embed(ctx, []string{q})
	return v[0], err
}
func (letters) Dimensions() int   { return 26 }
func (letters) Name() string      { return "letters" }
func (letters) ModelName() string { return "letters-1" }

func facts(t *testing.T) *memstore.Store {
	t.Helper()
	g := memstore.New()
	for id, text := range map[string]string{
		"f1": "Never send email on my behalf.",
		"f2": "Prefers metric units.",
		"f3": "Works with Kevin on the rollout.",
	} {
		if err := g.AddNode(context.Background(), &graph.Node{ID: id, Type: "Fact", DocComment: text}); err != nil {
			t.Fatal(err)
		}
	}
	return g
}

func TestAStoreInMemorySavesAndLoadsWhatItIndexed(t *testing.T) {
	ctx := context.Background()
	g := facts(t)
	p := vectorstore.NewMemoryPersistence()
	vs := vectorstore.New(g, letters{}, p, vectorstore.WithEmbeddable("Fact"))
	// Only a rebuild records the metadata a reopened store checks.
	if err := vs.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	if added, _, err := vs.Reconcile(ctx, 0); err != nil || added != 0 {
		t.Fatalf("Reconcile after a rebuild = %d, %v; want nothing to add", added, err)
	}
	if err := vs.Save(); err != nil {
		t.Fatal(err)
	}

	again := vectorstore.New(g, letters{}, p, vectorstore.WithEmbeddable("Fact"))
	if ok, err := again.Load(); !ok || err != nil {
		t.Fatalf("Load = %v, %v", ok, err)
	}
	if again.Len() != 3 || again.NeedsReindex() {
		t.Errorf("reloaded: %d vectors, needs reindex %v", again.Len(), again.NeedsReindex())
	}
	res, err := again.Search(ctx, "send email", 1)
	if err != nil || len(res) != 1 || res[0].Node.ID != "f1" || res[0].ChunkText != "Never send email on my behalf." {
		t.Fatalf("search = %+v, %v", res, err)
	}
	if stale, missing, _ := again.Staleness(ctx); stale != 0 || missing != 0 {
		t.Errorf("staleness after reload = %d, %d", stale, missing)
	}
}

func TestAStoreWithNothingSavedLoadsNothing(t *testing.T) {
	vs := vectorstore.New(memstore.New(), letters{}, vectorstore.NewMemoryPersistence())
	if ok, err := vs.Load(); ok || err != nil {
		t.Errorf("Load of an empty persistence = %v, %v; want false and no error", ok, err)
	}
}

func TestChangedInstructionsCallForAReindex(t *testing.T) {
	ctx := context.Background()
	g := facts(t)
	p := vectorstore.NewMemoryPersistence()
	open := func(doc, query string) *vectorstore.Store {
		emb := embedding.WithInstructions(letters{}, doc, query)
		vs := vectorstore.New(g, emb, p, vectorstore.WithEmbeddable("Fact"))
		if _, err := vs.Load(); err != nil {
			t.Fatal(err)
		}
		return vs
	}
	vs := open("title: none | text: ", "task: search result | query: ")
	if err := vs.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	if err := vs.Save(); err != nil {
		t.Fatal(err)
	}
	if vs.Meta().EmbedConfig == "" {
		t.Fatal("the instructions' fingerprint was not recorded")
	}
	if same := open("title: none | text: ", "task: search result | query: "); same.NeedsReindex() {
		t.Errorf("same instructions need a reindex: %s", same.ReindexReason())
	}
	other := open("title: none | text: ", "task: question answering | query: ")
	if !other.NeedsReindex() || !strings.Contains(other.ReindexReason(), "instructions") {
		t.Errorf("changed instructions: needs reindex %v, reason %q", other.NeedsReindex(), other.ReindexReason())
	}
}

func TestAProviderWithoutInstructionsRecordsNoFingerprint(t *testing.T) {
	vs := vectorstore.New(facts(t), letters{}, vectorstore.NewMemoryPersistence(), vectorstore.WithEmbeddable("Fact"))
	if err := vs.Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := vs.Meta().EmbedConfig; got != "" {
		t.Errorf("EmbedConfig = %q, want empty so existing metadata is unchanged", got)
	}
}

func TestADetachedStoreSearchesButRefusesChanges(t *testing.T) {
	ctx := context.Background()
	g := facts(t)
	vs := vectorstore.New(g, letters{}, vectorstore.NewMemoryPersistence(), vectorstore.WithEmbeddable("Fact"))
	if _, _, err := vs.Reconcile(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if err := vs.Detach(); err != nil {
		t.Fatal(err)
	}
	res, err := vs.Search(ctx, "metric units", 1)
	if err != nil || len(res) != 1 || res[0].Node.ID != "f2" || res[0].ChunkText != "" {
		t.Errorf("detached search = %+v, %v; want the node without its text", res, err)
	}
	for name, err := range map[string]error{
		"Save":       vs.Save(),
		"RemoveNode": vs.RemoveNode(ctx, "f1"),
		"Rebuild":    vs.Rebuild(ctx),
		"IndexNodes": vs.IndexNodes(ctx, nil),
	} {
		if !errors.Is(err, vectorstore.ErrDetached) {
			t.Errorf("%s on a detached store = %v, want ErrDetached", name, err)
		}
	}
	if vs.Len() != 3 {
		t.Errorf("a refused change altered the index: %d vectors", vs.Len())
	}
	if err := vs.Close(); err != nil {
		t.Errorf("Close after Detach = %v", err)
	}
}
