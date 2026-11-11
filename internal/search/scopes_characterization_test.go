package search_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/search"
	"github.com/imyousuf/CodeEagle/internal/vectorstore"
)

// These tests pin exact scores over a store read the way the CLI reads one:
// the current branch, then the default branch, then the branch-independent
// meeting and relation scopes. Scores are compared through their shortest
// exact decimal form, so a difference in the last bit fails.

// Scopes of the fixture store, in read order.
const (
	scopeCurrent = "feature-x"
	scopeDefault = "main"
)

var readScopes = []string{scopeCurrent, scopeDefault, embedded.MeetingScope, embedded.RelationScope}

// stubVectors returns fixed results, standing in for an embedding model.
type stubVectors struct {
	results []vectorstore.SearchResult
	err     error
	stale   int
	topK    int
	query   string
}

func (s *stubVectors) Search(_ context.Context, query string, topK int) ([]vectorstore.SearchResult, error) {
	s.topK, s.query = topK, query
	if s.err != nil {
		return nil, s.err
	}
	return append([]vectorstore.SearchResult(nil), s.results...), nil
}

func (s *stubVectors) StaleInLastSearch() int { return s.stale }

// scopedNode is a node and the scope it is written to.
type scopedNode struct {
	scope string
	node  *graph.Node
}

type scopedEdge struct {
	scope string
	edge  *graph.Edge
}

// multiScopeFixture is a small graph spread over four scopes.
//
// Per scope:
//   - feature-x: RetryWithBackoff (function, the hub), RetryPolicy (struct),
//     and retry_backoff.go, a File whose name matches the query but which is
//     not an embeddable type.
//   - main: the Retry policy document, backoff_delay, computeBackoffs (a
//     prefix match for "backoff"), TestRetryPolicy (a TestFunction: a code
//     type, but not embeddable), Run (named for nothing, but in package
//     "backoff"), and ParseConfig (matches nothing).
//   - @meetings: a topic and a meeting.
//   - @relations: one relation.
//
// Edge counts as read through every scope: RetryWithBackoff 6 (one edge is
// stored in two scopes under one ID and counts once), backoff_delay 2, the
// document 2, the meeting 2, every other node 1 or 0.
var (
	fixtureNodes = []scopedNode{
		{scopeCurrent, &graph.Node{ID: "fn-retry", Type: graph.NodeFunction, Name: "RetryWithBackoff", Package: "client", Language: "go"}},
		{scopeCurrent, &graph.Node{ID: "struct-policy", Type: graph.NodeStruct, Name: "RetryPolicy", Package: "client", Language: "go"}},
		{scopeCurrent, &graph.Node{ID: "file-retry", Type: graph.NodeFile, Name: "retry_backoff.go", Package: "client", Language: "go"}},
		{scopeDefault, &graph.Node{ID: "doc-retry", Type: graph.NodeDocument, Name: "Retry policy", Package: "docs", Language: "markdown"}},
		{scopeDefault, &graph.Node{ID: "fn-backoff", Type: graph.NodeFunction, Name: "backoff_delay", Package: "worker", Language: "python"}},
		{scopeDefault, &graph.Node{ID: "fn-backoffs", Type: graph.NodeMethod, Name: "computeBackoffs", Package: "worker", Language: "python"}},
		{scopeDefault, &graph.Node{ID: "test-retry", Type: graph.NodeTestFunction, Name: "TestRetryPolicy", Package: "client", Language: "go"}},
		{scopeDefault, &graph.Node{ID: "pkg-run", Type: graph.NodeFunction, Name: "Run", Package: "backoff", Language: "go"}},
		{scopeDefault, &graph.Node{ID: "fn-other", Type: graph.NodeFunction, Name: "ParseConfig", Package: "config", Language: "go"}},
		{embedded.MeetingScope, &graph.Node{ID: "topic-retry", Type: graph.NodeTopic, Name: "Retry storms"}},
		{embedded.MeetingScope, &graph.Node{ID: "meeting", Type: graph.NodeMeeting, Name: "Incident review"}},
		{embedded.RelationScope, &graph.Node{ID: "rel", Type: graph.NodeRelation, Name: "relation: Retry policy"}},
	}
	fixtureEdges = []scopedEdge{
		{scopeCurrent, &graph.Edge{ID: "e1", Type: graph.EdgeContains, SourceID: "file-retry", TargetID: "fn-retry"}},
		{scopeCurrent, &graph.Edge{ID: "e2", Type: graph.EdgeContains, SourceID: "file-retry", TargetID: "struct-policy"}},
		{scopeCurrent, &graph.Edge{ID: "e3", Type: graph.EdgeCalls, SourceID: "fn-retry", TargetID: "fn-backoff"}},
		{scopeCurrent, &graph.Edge{ID: "e-dup", Type: graph.EdgeCalls, SourceID: "fn-retry", TargetID: "fn-backoffs"}},
		{scopeDefault, &graph.Edge{ID: "e-dup", Type: graph.EdgeCalls, SourceID: "fn-retry", TargetID: "fn-backoffs"}},
		{scopeDefault, &graph.Edge{ID: "e4", Type: graph.EdgeTests, SourceID: "test-retry", TargetID: "fn-retry"}},
		{scopeDefault, &graph.Edge{ID: "e5", Type: graph.EdgeDocuments, SourceID: "doc-retry", TargetID: "fn-retry"}},
		{scopeDefault, &graph.Edge{ID: "e6", Type: graph.EdgeCalls, SourceID: "pkg-run", TargetID: "fn-backoff"}},
		{embedded.MeetingScope, &graph.Edge{ID: "e7", Type: graph.EdgeMentions, SourceID: "meeting", TargetID: "fn-retry"}},
		{embedded.MeetingScope, &graph.Edge{ID: "e8", Type: graph.EdgeHasTopic, SourceID: "meeting", TargetID: "topic-retry"}},
		{embedded.RelationScope, &graph.Edge{ID: "e9", Type: graph.EdgeAbout, SourceID: "rel", TargetID: "doc-retry"}},
	}
)

// fixtureQuery has four terms after stop words: retry, backoff, policy and
// work. Nothing carries "work", so no node can cover more than three
// quarters of the query by count.
const fixtureQuery = "How does retry backoff policy work"

// openScopedStore opens a store that writes to the current branch and reads
// every scope, then writes nodes and edges into their scopes through views.
func openScopedStore(t *testing.T, nodes []scopedNode, edges []scopedEdge) (*embedded.BranchStore, map[string]*graph.Node) {
	t.Helper()
	store, err := embedded.NewBranchStore(filepath.Join(t.TempDir(), "graph.db"), scopeCurrent, readScopes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()
	byID := make(map[string]*graph.Node)
	for _, sn := range nodes {
		w := store.View(sn.scope, []string{sn.scope})
		n := *sn.node
		if err := w.AddNode(ctx, &n); err != nil {
			t.Fatal(err)
		}
		byID[n.ID] = sn.node
	}
	for _, se := range edges {
		w := store.View(se.scope, []string{se.scope})
		e := *se.edge
		if err := w.AddEdge(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}
	return store, byID
}

// fmtScore is the shortest decimal that reads back as exactly f.
func fmtScore(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

// ranked renders results as "id score" lines, in order.
func ranked(results []vectorstore.SearchResult) []string {
	out := make([]string, len(results))
	for i, r := range results {
		id := "<nil>"
		if r.Node != nil {
			id = r.Node.ID
		}
		out[i] = id + " " + fmtScore(r.Score)
	}
	return out
}

// hitLines renders keyword hits as "id matches share" lines, sorted by ID.
func hitLines(kw *search.Keywords) []string {
	var out []string
	for id, h := range kw.Hits {
		out = append(out, fmt.Sprintf("%s %d %s", id, h.MatchCount, fmtScore(h.Share)))
	}
	sort.Strings(out)
	return out
}

func diffLines(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s:\ngot:\n\t%s\nwant:\n\t%s", what, strings.Join(got, "\n\t"), strings.Join(want, "\n\t"))
	}
}

// TestCharacterizeKeywordSearchAcrossScopes pins every hit and its share.
//
// Rarity is judged against every node the name scan visits in every read
// scope, twelve here, whatever their type. The File and the TestFunction are
// visited and named for the query, but are not embeddable, so neither is a
// hit nor counts towards a term's document frequency. "Run" is a hit through
// its package alone, and "computeBackoffs" earns prefix credit.
func TestCharacterizeKeywordSearchAcrossScopes(t *testing.T) {
	store, _ := openScopedStore(t, fixtureNodes, fixtureEdges)
	kw := search.KeywordSearch(context.Background(), store, fixtureQuery)

	if got, want := strings.Join(kw.Terms, ","), "retry,backoff,policy,work"; got != want {
		t.Errorf("Terms = %s, want %s", got, want)
	}
	if got, want := fmt.Sprint(kw.Groups), "[[0] [1] [2] [3]]"; got != want {
		t.Errorf("Groups = %s, want %s", got, want)
	}
	diffLines(t, "keyword hits (id, matches, share)", hitLines(kw), []string{
		"doc-retry 2 0.39608744536560225",
		"fn-backoff 1 0.19506651666522168",
		"fn-backoffs 1 0.13654656166565515",
		"fn-retry 2 0.3701823645393998",
		"pkg-run 1 0.19506651666522168",
		"rel 2 0.39608744536560225",
		"struct-policy 2 0.39608744536560225",
		"topic-retry 1 0.17511584787417814",
	})
	for _, id := range []string{"file-retry", "test-retry", "fn-other", "meeting"} {
		if kw.Hits[id] != nil {
			t.Errorf("%s is a keyword hit; it should not be", id)
		}
	}
	if got := kw.Hits["doc-retry"].Node.Properties[graph.PropGraphSource]; got != scopeDefault {
		t.Errorf("doc-retry hit read from scope %q, want %q", got, scopeDefault)
	}
}

// TestCharacterizeNonEmbeddableNameMatchDoesNotChangeShares pins that a
// non-embeddable node named for the query weighs on rarity exactly as a node
// named for nothing does: it is one more node scanned, and no more.
func TestCharacterizeNonEmbeddableNameMatchDoesNotChangeShares(t *testing.T) {
	named, _ := openScopedStore(t, fixtureNodes, fixtureEdges)

	renamed := append([]scopedNode(nil), fixtureNodes...)
	for i, sn := range renamed {
		if sn.node.ID == "file-retry" {
			n := *sn.node
			n.Name = "main.go"
			renamed[i] = scopedNode{sn.scope, &n}
		}
	}
	plain, _ := openScopedStore(t, renamed, fixtureEdges)

	ctx := context.Background()
	diffLines(t, "shares with the File renamed",
		hitLines(search.KeywordSearch(ctx, plain, fixtureQuery)),
		hitLines(search.KeywordSearch(ctx, named, fixtureQuery)))
}

// fixtureVectors are nearest-vector results as an embedding model might
// return them: the document twice (two chunks), a nil node, a
// TestFunction, and nodes keyword search does not find.
func fixtureVectors(nodes map[string]*graph.Node) []vectorstore.SearchResult {
	return []vectorstore.SearchResult{
		{Node: nodes["doc-retry"], Score: 0.9},
		{Node: nodes["fn-backoff"], Score: 0.8},
		{Node: nil, Score: 0.75},
		{Node: nodes["test-retry"], Score: 0.6},
		{Node: nodes["topic-retry"], Score: 0.5},
		{Node: nodes["doc-retry"], Score: 0.4},
		{Node: nodes["fn-other"], Score: 0.3},
		{Node: nodes["meeting"], Score: 0.2},
	}
}

// TestCharacterizeHybridAcrossScopes pins the ranking and every score.
//
// What the scores show:
//   - fn-retry, struct-policy, rel, fn-backoffs and pkg-run came from
//     keyword search only, so their vector score is half their share.
//   - fn-retry has the most edges, so it alone gets full centrality.
//   - TestRetryPolicy is no keyword hit but still gets the code-type boost.
//   - The document's second, weaker chunk is dropped, and so is the nil
//     node.
func TestCharacterizeHybridAcrossScopes(t *testing.T) {
	store, nodes := openScopedStore(t, fixtureNodes, fixtureEdges)
	vs := &stubVectors{results: fixtureVectors(nodes), stale: 2}

	res, err := search.Hybrid(context.Background(), store, vs, fixtureQuery, search.HybridOptions{Limit: 8})
	if err != nil {
		t.Fatal(err)
	}
	if vs.query != fixtureQuery {
		t.Errorf("vector query = %q, want the query as given", vs.query)
	}
	if res.VectorErr != nil {
		t.Errorf("VectorErr = %v", res.VectorErr)
	}
	if got := fmt.Sprintf("fetched=%d examined=%d stale=%d ranked=%d topK=%d", res.Fetched, res.Examined, res.Stale, res.Ranked, vs.topK); got != "fetched=40 examined=10 stale=2 ranked=11 topK=40" {
		t.Errorf("counts: %s", got)
	}
	diffLines(t, "hybrid ranking", ranked(res.Results), []string{
		"fn-backoff 0.6636995584410813",
		"doc-retry 0.6589037441811575",
		"test-retry 0.4834310780662034",
		"fn-retry 0.42583662315621496",
		"topic-retry 0.363454247641039",
		"struct-policy 0.34157261461486443",
		"fn-other 0.265",
		"pkg-run 0.24608767348218363",
	})
}

// TestCharacterizeHybridFiltersAndMinScore pins the options over the same
// fixture: NoDocs fetches ten per result instead of five, filters apply to
// injected keyword hits too, and MinScore counts towards Ranked.
func TestCharacterizeHybridFiltersAndMinScore(t *testing.T) {
	store, nodes := openScopedStore(t, fixtureNodes, fixtureEdges)
	ctx := context.Background()

	tests := []struct {
		name   string
		opts   search.HybridOptions
		counts string
		want   []string
	}{
		{
			name:   "no docs",
			opts:   search.HybridOptions{Limit: 3, NoDocs: true},
			counts: "fetched=30 ranked=10",
			want: []string{
				"fn-backoff 0.6636995584410813",
				"test-retry 0.4834310780662034",
				"fn-retry 0.42583662315621496",
			},
		},
		{
			name:   "types",
			opts:   search.HybridOptions{Limit: 10, Types: map[graph.NodeType]bool{graph.NodeFunction: true, graph.NodeMethod: true}},
			counts: "fetched=100 ranked=5",
			want: []string{
				"fn-backoff 0.6636995584410813",
				"fn-retry 0.42583662315621496",
				"fn-other 0.265",
				"pkg-run 0.24608767348218363",
				"fn-backoffs 0.21829069485738956",
			},
		},
		{
			name:   "package and language",
			opts:   search.HybridOptions{Limit: 10, Package: "WORK", Language: "Python"},
			counts: "fetched=50 ranked=2",
			want: []string{
				"fn-backoff 0.7290133033330444",
				"fn-backoffs 0.25949907982690484",
			},
		},
		{
			name:   "min score",
			opts:   search.HybridOptions{Limit: 2, MinScore: 0.39},
			counts: "fetched=10 ranked=4",
			want: []string{
				"fn-backoff 0.6636995584410813",
				"doc-retry 0.6589037441811575",
			},
		},
		{
			name:   "default limit caps the fetch",
			opts:   search.HybridOptions{Limit: 0},
			counts: "fetched=75 ranked=11",
			want: []string{
				"fn-backoff 0.6636995584410813",
				"doc-retry 0.6589037441811575",
				"test-retry 0.4834310780662034",
				"fn-retry 0.42583662315621496",
				"topic-retry 0.363454247641039",
				"struct-policy 0.34157261461486443",
				"fn-other 0.265",
				"pkg-run 0.24608767348218363",
				"rel 0.2415726146148644",
				"fn-backoffs 0.21829069485738956",
				"meeting 0.19468625510803694",
			},
		},
		{
			name:   "fetch is capped at 200",
			opts:   search.HybridOptions{Limit: 30, NoDocs: true},
			counts: "fetched=200 ranked=10",
			want: []string{
				"fn-backoff 0.6636995584410813",
				"test-retry 0.4834310780662034",
				"fn-retry 0.42583662315621496",
				"topic-retry 0.363454247641039",
				"struct-policy 0.34157261461486443",
				"fn-other 0.265",
				"pkg-run 0.24608767348218363",
				"rel 0.2415726146148644",
				"fn-backoffs 0.21829069485738956",
				"meeting 0.19468625510803694",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs := &stubVectors{results: fixtureVectors(nodes)}
			res, err := search.Hybrid(ctx, store, vs, fixtureQuery, tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprintf("fetched=%d ranked=%d", res.Fetched, res.Ranked); got != tt.counts {
				t.Errorf("counts = %s, want %s", got, tt.counts)
			}
			diffLines(t, "ranking", ranked(res.Results), tt.want)
		})
	}
}

// TestCharacterizeHybridWithoutVectors pins the ranking when the embedding
// service fails: keyword hits only, each scored from its share alone.
func TestCharacterizeHybridWithoutVectors(t *testing.T) {
	store, _ := openScopedStore(t, fixtureNodes, fixtureEdges)
	vs := &stubVectors{err: errors.New("embedding service down"), stale: 7}

	res, err := search.Hybrid(context.Background(), store, vs, fixtureQuery, search.HybridOptions{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if res.VectorErr == nil || res.VectorErr.Error() != "embedding service down" {
		t.Errorf("VectorErr = %v", res.VectorErr)
	}
	if got := fmt.Sprintf("fetched=%d examined=%d stale=%d ranked=%d", res.Fetched, res.Examined, res.Stale, res.Ranked); got != "fetched=100 examined=0 stale=0 ranked=8" {
		t.Errorf("counts: %s", got)
	}
	diffLines(t, "keyword-only ranking", ranked(res.Results), []string{
		"fn-retry 0.42583662315621496",
		"struct-policy 0.34157261461486443",
		"fn-backoff 0.27734285052401725",
		"doc-retry 0.272827791656698",
		"pkg-run 0.24608767348218363",
		"rel 0.2415726146148644",
		"fn-backoffs 0.21829069485738956",
		"topic-retry 0.13661110580643793",
	})
}

// TestCharacterizeRerankResults pins the scoring formula on hand-made
// inputs: 0.55 x vector score (or half the share when the vector score is
// zero), 0.20 x share, 0.10 for a code type, and 0.15 x centrality on a log
// scale against the best-connected result. A nil node scores zero and keeps
// its place among equals; a node the store does not hold has no edges.
func TestCharacterizeRerankResults(t *testing.T) {
	store, nodes := openScopedStore(t, fixtureNodes, fixtureEdges)
	ctx := context.Background()

	if got := search.RerankResults(ctx, store, nil, nil); got != nil {
		t.Errorf("RerankResults(nil) = %v, want nil", got)
	}

	ghost := &graph.Node{ID: "ghost", Type: graph.NodeEnum, Name: "Ghost"}
	results := []vectorstore.SearchResult{
		{Node: nil, Score: 0.99},
		{Node: nodes["meeting"], Score: 0.7},
		{Node: nodes["fn-backoff"], Score: 0},
		{Node: nodes["struct-policy"], Score: 0.25},
		{Node: ghost, Score: 0.5},
		{Node: nodes["file-retry"], Score: 0},
		{Node: nodes["topic-retry"], Score: 0.1},
	}
	shares := map[string]float64{
		"fn-backoff":    0.6,
		"struct-policy": 0.25,
		"topic-retry":   1,
		"meeting":       0,
	}
	// The meeting and backoff_delay tie exactly (0.385 + 0.15 against
	// 0.165 + 0.12 + 0.10 + 0.15) and keep their input order.
	diffLines(t, "reranked", ranked(search.RerankResults(ctx, store, results, shares)), []string{
		"meeting 0.535",
		"fn-backoff 0.535",
		"struct-policy 0.3821394630357186",
		"ghost 0.375",
		"topic-retry 0.34963946303571863",
		"file-retry 0.15",
		"<nil> 0",
	})
}

// TestCharacterizeKeywordSearchShadowedScopes pins how a node ID present in
// two scopes is matched. The first scope's version wins for reads by ID,
// but the name scan only remembers an ID once its version has matched, so
// when the first scope's version does not match the query and a later
// scope's does, the later, shadowed version is the hit.
//
// The shares show what the scan counted towards rarity: four nodes. A
// shadowed version is visited, and counted, unless an earlier scope's
// version of the ID already matched: LegacyShim and RetryHelper count
// twice for one ID, RetryLoopOld not at all.
func TestCharacterizeKeywordSearchShadowedScopes(t *testing.T) {
	nodes := []scopedNode{
		{scopeCurrent, &graph.Node{ID: "fn-shadow", Type: graph.NodeFunction, Name: "LegacyShim", Package: "client"}},
		{scopeDefault, &graph.Node{ID: "fn-shadow", Type: graph.NodeFunction, Name: "RetryHelper", Package: "client"}},
		{scopeCurrent, &graph.Node{ID: "fn-both", Type: graph.NodeFunction, Name: "RetryLoop", Package: "client"}},
		{scopeDefault, &graph.Node{ID: "fn-both", Type: graph.NodeFunction, Name: "RetryLoopOld", Package: "client"}},
		{scopeDefault, &graph.Node{ID: "fn-plain", Type: graph.NodeFunction, Name: "Unrelated", Package: "client"}},
	}
	store, _ := openScopedStore(t, nodes, nil)
	ctx := context.Background()

	kw := search.KeywordSearch(ctx, store, "retry helper")
	// ln(1+4/3) / (ln(1+4/3) + ln(1+4/2)) for RetryLoop; all of the
	// query for RetryHelper.
	diffLines(t, "shadowed hits", hitLines(kw), []string{
		"fn-both 1 0.4354249659464204",
		"fn-shadow 2 1",
	})
	if got := kw.Hits["fn-shadow"]; got == nil || got.Node.Name != "RetryHelper" || got.Node.Properties[graph.PropGraphSource] != scopeDefault {
		t.Errorf("fn-shadow hit = %+v, want the shadowed RetryHelper from %s", got, scopeDefault)
	}
	if got := kw.Hits["fn-both"]; got == nil || got.Node.Name != "RetryLoop" || got.Node.Properties[graph.PropGraphSource] != scopeCurrent {
		t.Errorf("fn-both hit = %+v, want RetryLoop from %s", got, scopeCurrent)
	}
	n, err := store.GetNode(ctx, "fn-shadow")
	if err != nil {
		t.Fatal(err)
	}
	if n.Name != "LegacyShim" {
		t.Errorf("GetNode(fn-shadow).Name = %q, want the current scope's LegacyShim", n.Name)
	}
}
