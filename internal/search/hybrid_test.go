package search

import (
	"context"
	"errors"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/vectorstore"
)

// fakeVectors returns fixed results, standing in for an embedding model.
type fakeVectors struct {
	results []vectorstore.SearchResult
	stale   int
	asked   int
}

func (f *fakeVectors) Search(_ context.Context, _ string, topK int) ([]vectorstore.SearchResult, error) {
	f.asked = topK
	return append([]vectorstore.SearchResult(nil), f.results...), nil
}
func (f *fakeVectors) StaleInLastSearch() int { return f.stale }

func hybridFixture(t *testing.T) (graph.Store, map[string]*graph.Node) {
	t.Helper()
	store, err := embedded.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	nodes := map[string]*graph.Node{
		"fn":   {ID: "fn", Type: graph.NodeFunction, Name: "RetryWithBackoff", Package: "client", Language: "go"},
		"doc":  {ID: "doc", Type: graph.NodeDocument, Name: "Retry policy", Package: "docs"},
		"py":   {ID: "py", Type: graph.NodeFunction, Name: "backoff_delay", Package: "worker", Language: "python"},
		"spec": {ID: "spec", Type: graph.NodeDocument, Name: "Idempotency keys", Package: "docs"},
	}
	for _, n := range nodes {
		if err := store.AddNode(context.Background(), n); err != nil {
			t.Fatal(err)
		}
	}
	return store, nodes
}

func ids(results []vectorstore.SearchResult) map[string]bool {
	out := make(map[string]bool)
	for _, r := range results {
		out[r.Node.ID] = true
	}
	return out
}

func TestHybridFiltersAndInjectsKeywordMatches(t *testing.T) {
	store, nodes := hybridFixture(t)
	vs := &fakeVectors{results: []vectorstore.SearchResult{
		{Node: nodes["doc"], Score: 0.9},
		{Node: nodes["py"], Score: 0.8},
	}}
	ctx := context.Background()

	res, err := Hybrid(ctx, store, vs, "retry backoff", HybridOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	// RetryWithBackoff was not among the nearest vectors but is named what
	// was asked for; keyword search must bring it in.
	if !ids(res.Results)["fn"] {
		t.Error("a node named for the query was lost because vector search missed it")
	}

	res, _ = Hybrid(ctx, store, vs, "retry backoff", HybridOptions{Limit: 10, NoDocs: true})
	if ids(res.Results)["doc"] {
		t.Error("NoDocs kept a document")
	}
	res, _ = Hybrid(ctx, store, vs, "retry backoff", HybridOptions{Limit: 10, Language: "PYTHON"})
	if got := ids(res.Results); !got["py"] || got["fn"] {
		t.Errorf("language filter: %v", got)
	}
	res, _ = Hybrid(ctx, store, vs, "retry backoff",
		HybridOptions{Limit: 10, Types: map[graph.NodeType]bool{graph.NodeDocument: true}})
	if got := ids(res.Results); got["fn"] || got["py"] {
		t.Errorf("type filter: %v", got)
	}
}

func TestHybridReportsRankedBeyondTheLimit(t *testing.T) {
	store, nodes := hybridFixture(t)
	vs := &fakeVectors{results: []vectorstore.SearchResult{
		{Node: nodes["doc"], Score: 0.9}, {Node: nodes["py"], Score: 0.8}, {Node: nodes["spec"], Score: 0.7},
	}, stale: 4}

	res, err := Hybrid(context.Background(), store, vs, "retry backoff", HybridOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 1 || res.Ranked < 2 {
		t.Errorf("got %d results of %d ranked; want 1 shown and more ranked", len(res.Results), res.Ranked)
	}
	if res.Stale != 4 || res.Examined != 7 || res.Fetched != vs.asked {
		t.Errorf("stale %d examined %d fetched %d (asked %d)", res.Stale, res.Examined, res.Fetched, vs.asked)
	}
}

func TestHybridMinScoreAppliesAfterReranking(t *testing.T) {
	store, nodes := hybridFixture(t)
	vs := &fakeVectors{results: []vectorstore.SearchResult{{Node: nodes["spec"], Score: 0.9}}}
	res, err := Hybrid(context.Background(), store, vs, "retry backoff", HybridOptions{Limit: 10, MinScore: 0.99})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Results {
		if r.Score < 0.99 {
			t.Errorf("%s scored %.2f, under the minimum", r.Node.ID, r.Score)
		}
	}
}

// failingVectors stands in for an embedding service that is down.
type failingVectors struct{}

func (failingVectors) Search(context.Context, string, int) ([]vectorstore.SearchResult, error) {
	return nil, errors.New("embed query: connection refused")
}
func (failingVectors) StaleInLastSearch() int { return 0 }

// TestHybridFallsBackToWordsWhenVectorsFail covers the embedding service
// failing under load: the words still match.
func TestHybridFallsBackToWordsWhenVectorsFail(t *testing.T) {
	store, _ := hybridFixture(t)
	res, err := Hybrid(context.Background(), store, failingVectors{}, "retry policy", HybridOptions{Limit: 5})
	if err != nil {
		t.Fatalf("Hybrid failed outright: %v", err)
	}
	if res.VectorErr == nil || len(res.Results) == 0 {
		t.Errorf("VectorErr %v, %d results; want the error noted and keyword results", res.VectorErr, len(res.Results))
	}
}
