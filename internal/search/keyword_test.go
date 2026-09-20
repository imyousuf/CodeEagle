package search

import (
	"context"
	"math"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/vectorstore"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"AGI feasibility debate", []string{"agi", "feasibility", "debate"}},
		{"NewHTTPClient", []string{"new", "http", "client"}},
		{"handleRequest2Fast", []string{"handle", "request2", "fast"}},
		{"Okta CIAM / per-MAU cost!", []string{"okta", "ciam", "per", "mau", "cost"}},
		{"", nil},
		{"---", nil},
	}
	for _, tc := range tests {
		if got := Tokenize(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Tokenize(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestQueryTermsDropsNoiseAndDuplicates(t *testing.T) {
	got := QueryTerms("What did we decide about the AGI debate, per the AGI notes?")
	want := []string{"decide", "agi", "debate", "notes"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("QueryTerms = %v, want %v", got, want)
	}
}

// TestTermCreditMatchesWholeWords covers the failure that buried an acronym:
// "agi" used to match "messaging" and "packaging" by substring, and never
// matched "AGI" because the match was case-sensitive.
func TestTermCreditMatchesWholeWords(t *testing.T) {
	tests := []struct {
		term string
		name string
		want float64
	}{
		{"agi", "AGI feasibility debate", 1},
		{"agi", "Instant Messaging", 0},
		{"agi", "packaging", 0},
		{"agi", "agile methodology", 0}, // three letters: no prefix matching
		{"auth", "authentication middleware", PrefixCredit},
		{"auth", "Auth", 1},
		{"client", "NewHTTPClient", 1},
		{"per", "person", 0},
	}
	for _, tc := range tests {
		if got := TermCredit(tc.term, Tokenize(tc.name)); got != tc.want {
			t.Errorf("TermCredit(%q, %q) = %v, want %v", tc.term, tc.name, got, tc.want)
		}
	}
}

func TestTermGroupsJoinsAnInitialismWithItsExpansion(t *testing.T) {
	tests := []struct {
		terms []string
		want  [][]int
	}{
		{[]string{"agi", "artificial", "general", "intelligence"}, [][]int{{0, 1, 2, 3}}},
		// The initialism leads its group wherever it sits in the query.
		{[]string{"retrieval", "augmented", "generation", "rag", "eval"}, [][]int{{3, 0, 1, 2}, {4}}},
		{[]string{"agi", "feasibility", "debate"}, [][]int{{0}, {1}, {2}}},
		{[]string{"okta", "ciam"}, [][]int{{0}, {1}}},
		{nil, nil},
	}
	for _, tc := range tests {
		got := TermGroups(tc.terms)
		if len(got) == 0 && len(tc.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("TermGroups(%v) = %v, want %v", tc.terms, got, tc.want)
		}
	}
}

// TestShareOfWeighsGroupsByTheirRarestTerm: a node matching only the
// initialism covers its whole group; one matching two of three expansion
// words covers two thirds of it.
func TestShareOfWeighsGroupsByTheirRarestTerm(t *testing.T) {
	weights := []float64{9, 7, 5, 7} // agi, artificial, general, intelligence
	groups := [][]int{{0, 1, 2, 3}}

	if got := ShareOf([]float64{1, 0, 0, 0}, weights, groups); math.Abs(got-1) > 1e-9 {
		t.Errorf("initialism alone = %v, want 1", got)
	}
	if got := ShareOf([]float64{0, 1, 0, 1}, weights, groups); math.Abs(got-2.0/3) > 1e-9 {
		t.Errorf("two of three expansion words = %v, want 2/3", got)
	}

	// Ungrouped: the rare term carries more of the share than the common one.
	single := [][]int{{0}, {1}}
	rare := ShareOf([]float64{1, 0}, []float64{9, 3}, single)
	common := ShareOf([]float64{0, 1}, []float64{9, 3}, single)
	if rare <= common {
		t.Errorf("rare term share %v should exceed common term share %v", rare, common)
	}
	if got := ShareOf(nil, nil, nil); got != 0 {
		t.Errorf("empty = %v, want 0", got)
	}
}

func keywordStore(t *testing.T) graph.Store {
	t.Helper()
	store, err := embedded.NewStore(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()
	for _, n := range []*graph.Node{
		{ID: "agi", Type: graph.NodeTopic, Name: "AGI feasibility debate"},
		{ID: "im", Type: graph.NodeTopic, Name: "Instant Messaging"},
		{ID: "pkg", Type: graph.NodeTopic, Name: "packaging"},
		{ID: "ai", Type: graph.NodeTopic, Name: "artificial intelligence"},
		{ID: "client", Type: graph.NodeFunction, Name: "NewHTTPClient", Package: "llm"},
		{ID: "generic", Type: graph.NodeFunction, Name: "New", Package: "agi"},
		{ID: "file", Type: graph.NodeFile, Name: "agi.go"}, // not embeddable: never a hit
	} {
		if err := store.AddNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func TestKeywordSearchFindsAcronymsAndNotSubstrings(t *testing.T) {
	store := keywordStore(t)
	kw := KeywordSearch(context.Background(), store, "AGI")

	if _, ok := kw.Hits["agi"]; !ok {
		t.Fatalf("AGI did not find the topic named AGI; hits: %v", names(kw))
	}
	for _, id := range []string{"im", "pkg", "file"} {
		if _, ok := kw.Hits[id]; ok {
			t.Errorf("AGI matched %q, which merely contains the letters", id)
		}
	}
	// A package named for the term counts as a whole-word hit.
	if hit, ok := kw.Hits["generic"]; !ok || hit.Share != 1 {
		t.Errorf("package match = %+v, want a full-share hit", hit)
	}
}

func TestKeywordSearchSplitsIdentifiers(t *testing.T) {
	store := keywordStore(t)
	kw := KeywordSearch(context.Background(), store, "http client")
	hit, ok := kw.Hits["client"]
	if !ok {
		t.Fatalf("NewHTTPClient not found; hits: %v", names(kw))
	}
	if hit.MatchCount != 2 || math.Abs(hit.Share-1) > 1e-9 {
		t.Errorf("NewHTTPClient match = %+v, want both words and a full share", hit)
	}
}

// TestKeywordSearchGroupsInitialism: the exact "AGI ..." node must not lose
// to "artificial intelligence" when the query spells the acronym out.
func TestKeywordSearchGroupsInitialism(t *testing.T) {
	store := keywordStore(t)
	kw := KeywordSearch(context.Background(), store, "AGI artificial general intelligence")
	if len(kw.Groups) != 1 {
		t.Fatalf("groups = %v, want the four terms as one", kw.Groups)
	}
	agi, ai := kw.Hits["agi"], kw.Hits["ai"]
	if agi == nil || ai == nil {
		t.Fatalf("hits = %v, want both topics", names(kw))
	}
	if agi.Share <= ai.Share {
		t.Errorf("AGI share %v should beat artificial-intelligence share %v", agi.Share, ai.Share)
	}
}

func TestKeywordSearchEmptyQuery(t *testing.T) {
	store := keywordStore(t)
	kw := KeywordSearch(context.Background(), store, "the and for")
	if len(kw.Terms) != 0 || len(kw.Hits) != 0 {
		t.Errorf("stop words only: terms=%v hits=%d", kw.Terms, len(kw.Hits))
	}
}

// TestRerankDoesNotLetAHubOutrankAnExactMatch: on a linear centrality scale a
// taxonomy hub with forty edges beat the node named exactly what was asked.
func TestRerankDoesNotLetAHubOutrankAnExactMatch(t *testing.T) {
	store := keywordStore(t)
	ctx := context.Background()
	for i := range 40 {
		e := &graph.Edge{ID: graph.NewNodeID("e", "ai", string(rune('a'+i))), Type: graph.EdgeContains, SourceID: "ai", TargetID: "pkg"}
		if err := store.AddEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	kw := KeywordSearch(ctx, store, "AGI artificial general intelligence")
	topics := map[graph.NodeType]bool{graph.NodeTopic: true}
	results, shares := InjectKeywordResults(nil, kw, topics, false, "", "")
	results = RerankResults(ctx, store, results, shares)
	if len(results) == 0 || results[0].Node.ID != "agi" {
		t.Fatalf("top result = %+v, want the AGI topic", first(results))
	}
}

func TestInjectKeywordResultsHonoursFilters(t *testing.T) {
	store := keywordStore(t)
	kw := KeywordSearch(context.Background(), store, "AGI client")
	results, _ := InjectKeywordResults(nil, kw, map[graph.NodeType]bool{graph.NodeFunction: true}, false, "", "")
	for _, r := range results {
		if r.Node.Type != graph.NodeFunction {
			t.Errorf("type filter let through %s %q", r.Node.Type, r.Node.Name)
		}
	}
	results, _ = InjectKeywordResults(nil, kw, nil, false, "llm", "")
	if len(results) != 1 || results[0].Node.ID != "client" {
		t.Errorf("package filter = %v, want only NewHTTPClient", first(results))
	}
}

func names(kw *Keywords) []string {
	var out []string
	for _, h := range kw.Hits {
		out = append(out, h.Node.Name)
	}
	return out
}

func first(results []vectorstore.SearchResult) any {
	if len(results) == 0 {
		return "no results"
	}
	return results[0].Node.Name
}
