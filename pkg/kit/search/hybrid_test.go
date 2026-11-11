package search_test

import (
	"context"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph/memstore"
	"github.com/imyousuf/CodeEagle/pkg/kit/search"
)

// ownVectors is a caller's own vector index: it returns fixed hits, as an
// index over snapshot vectors would.
type ownVectors struct{ hits []search.Hit }

func (v ownVectors) Search(context.Context, string, int) ([]search.Hit, error) {
	return append([]search.Hit(nil), v.hits...), nil
}
func (ownVectors) StaleInLastSearch() int { return 0 }

// countingStore counts GetEdges calls, which centrality makes once per
// result.
type countingStore struct {
	*memstore.Store
	edges int
}

func (s *countingStore) GetEdges(ctx context.Context, id string, t graph.EdgeType) ([]*graph.Edge, error) {
	s.edges++
	return s.Store.GetEdges(ctx, id, t)
}

func notes(t *testing.T) *countingStore {
	t.Helper()
	s := &countingStore{Store: memstore.New()}
	for _, n := range []*graph.Node{
		{ID: "a", Type: "Fact", Name: "Never send email", FilePath: "memories/FACTS.md"},
		{ID: "b", Type: "Fact", Name: "Email drafts go to chat first", FilePath: "work/memories/FACTS.md"},
		{ID: "c", Type: "Section", Name: "Vendor email thread", FilePath: "work/notes.md"},
		{ID: "d", Type: graph.NodeFunction, Name: "SendEmail", FilePath: "code/mail.go"},
	} {
		if err := s.AddNode(context.Background(), n); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func ids(hits []search.Hit) string {
	var out []string
	for _, h := range hits {
		out = append(out, h.Node.ID)
	}
	return strings.Join(out, ",")
}

func TestHybridWithTheKitsDefaults(t *testing.T) {
	store := notes(t)
	vec := ownVectors{hits: []search.Hit{{Node: &graph.Node{ID: "c", Type: "Section", Name: "Vendor email thread"}, Score: 0.9}}}
	res, err := search.Hybrid(context.Background(), store, vec, "email", search.HybridOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	// Every type is eligible for keywords (no Embeddable), nothing is
	// boosted (no Boost), so the vector hit leads and the keyword-only hits
	// follow.
	if got := ids(res.Results); len(res.Results) != 4 || !strings.HasPrefix(got, "c,") {
		t.Errorf("results = %s, want c first and all four nodes", got)
	}
	for _, h := range res.Results {
		if h.Node.ID == "d" && h.Score > res.Results[0].Score {
			t.Error("a code entity was boosted without Boost")
		}
	}
}

func TestHybridKeepScopesVectorAndKeywordMatches(t *testing.T) {
	store := notes(t)
	vec := ownVectors{hits: []search.Hit{
		{Node: &graph.Node{ID: "c", Type: "Section", FilePath: "work/notes.md"}, Score: 0.9},
		{Node: &graph.Node{ID: "a", Type: "Fact", FilePath: "memories/FACTS.md"}, Score: 0.5},
	}}
	inWork := func(n *graph.Node) bool { return strings.HasPrefix(n.FilePath, "work/") }
	res, err := search.Hybrid(context.Background(), store, vec, "email", search.HybridOptions{Limit: 10, Keep: inWork})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range res.Results {
		if !inWork(h.Node) {
			t.Errorf("%s (%s) is outside the scope", h.Node.ID, h.Node.FilePath)
		}
	}
	if got := ids(res.Results); !strings.Contains(got, "b") || !strings.Contains(got, "c") {
		t.Errorf("results = %s, want both nodes under work/", got)
	}
}

func TestHybridEmbeddableLimitsKeywordHits(t *testing.T) {
	store := notes(t)
	onlyFacts := func(t graph.NodeType) bool { return t == "Fact" }
	res, err := search.Hybrid(context.Background(), store, ownVectors{}, "email", search.HybridOptions{Limit: 10, Embeddable: onlyFacts})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(res.Results); got != "a,b" && got != "b,a" {
		t.Errorf("results = %s, want only the facts", got)
	}
}

func TestZeroCentralitySkipsEdgeReads(t *testing.T) {
	store := notes(t)
	w := search.Weights{Vector: 0.55, Keyword: 0.20}
	if _, err := search.Hybrid(context.Background(), store, ownVectors{}, "email", search.HybridOptions{Weights: &w}); err != nil {
		t.Fatal(err)
	}
	if store.edges != 0 {
		t.Errorf("read edges %d times with no centrality weight", store.edges)
	}
	if _, err := search.Hybrid(context.Background(), store, ownVectors{}, "email", search.HybridOptions{}); err != nil {
		t.Fatal(err)
	}
	if store.edges == 0 {
		t.Error("the default weights did not read edges for centrality")
	}
}

func TestRerankAddsTheWeightedParts(t *testing.T) {
	store := memstore.New()
	hits := []search.Hit{
		{Node: &graph.Node{ID: "x", Type: graph.NodeFunction}, Score: 0.8},
		{Node: &graph.Node{ID: "y", Type: "Fact"}, Score: 0},
	}
	w := search.Weights{Vector: 0.5, Keyword: 0.3, CodeType: 0.1}
	got := search.Rerank(context.Background(), store, hits, map[string]float64{"y": 1}, w, search.CodeNodeTypes)
	// x: 0.5*0.8 + 0.1 boost = 0.5; y: keyword-only, vector counts half its
	// share: 0.5*0.5 + 0.3*1 = 0.55.
	if got[0].Node.ID != "y" || got[0].Score != 0.5*0.5+0.3*1 || got[1].Score != 0.5*0.8+0.1 {
		t.Errorf("reranked = %+v", got)
	}
}
