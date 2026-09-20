// Package search provides reusable search algorithms for CodeEagle's hybrid
// (vector + keyword) semantic search. Functions are extracted from the CLI rag
// command so both CLI and desktop app can share the same logic.
package search

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/vectorstore"
)

// DocNodeTypes are node types excluded by the "no docs" filter.
var DocNodeTypes = map[graph.NodeType]bool{
	graph.NodeDocument:    true,
	graph.NodeAIGuideline: true,
}

// CodeNodeTypes get a score boost during reranking.
var CodeNodeTypes = map[graph.NodeType]bool{
	graph.NodeFunction:     true,
	graph.NodeMethod:       true,
	graph.NodeStruct:       true,
	graph.NodeClass:        true,
	graph.NodeInterface:    true,
	graph.NodeEnum:         true,
	graph.NodeType_:        true,
	graph.NodeAPIEndpoint:  true,
	graph.NodeTestFunction: true,
}

// KeywordResult holds a keyword-matched node and how well it covers the query.
type KeywordResult struct {
	Node *graph.Node
	// MatchCount is how many distinct query keywords the node matched.
	MatchCount int
	// Share is the weighted fraction of the query this node covers, in
	// [0, 1]. Each keyword weighs by how rare it is across the graph, so a
	// node matching the one distinctive word of a query outscores one that
	// matches two of its commonplace ones.
	Share float64
}

// Keywords is the outcome of the keyword pass over the graph.
type Keywords struct {
	// Hits maps a node ID to its match.
	Hits map[string]*KeywordResult
	// Terms are the keywords that were searched, in query order.
	Terms []string
	// Groups partitions Terms into the units the query is scored by. An
	// initialism and the words it stands for form one group; every other
	// term is a group of its own.
	Groups [][]int
}

// EdgeInfo represents a relationship edge in structured form.
type EdgeInfo struct {
	Direction string `json:"direction"`
	EdgeType  string `json:"edge_type"`
	NodeName  string `json:"node_name"`
	NodeType  string `json:"node_type"`
}

// DeduplicateResults keeps only the highest-scoring chunk per node ID.
func DeduplicateResults(results []vectorstore.SearchResult) []vectorstore.SearchResult {
	seen := make(map[string]int) // nodeID -> index in deduped
	var deduped []vectorstore.SearchResult

	for _, r := range results {
		if r.Node == nil {
			continue
		}
		if idx, ok := seen[r.Node.ID]; ok {
			if r.Score > deduped[idx].Score {
				deduped[idx] = r
			}
			continue
		}
		seen[r.Node.ID] = len(deduped)
		deduped = append(deduped, r)
	}
	return deduped
}

// stopWords are query words that carry no signal on their own.
var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "how": true, "what": true,
	"does": true, "this": true, "that": true, "with": true, "from": true,
	"are": true, "was": true, "has": true, "not": true, "all": true,
	"about": true, "did": true, "when": true, "who": true, "where": true,
	"which": true, "our": true, "you": true, "into": true, "have": true,
	"were": true, "been": true, "per": true, "via": true, "any": true,
	// Two-letter function words. Two-letter terms are kept otherwise,
	// because "VT", "IM", "EU" and "PR" are how people name things.
	"of": true, "to": true, "in": true, "is": true, "we": true, "it": true,
	"be": true, "do": true, "as": true, "at": true, "by": true, "on": true,
	"or": true, "an": true, "if": true, "so": true, "no": true, "up": true,
	"us": true, "my": true, "me": true, "he": true, "am": true, "vs": true,
	"re": true, "ok": true, "hi": true, "oh": true, "its": true, "can": true,
}

// minTermLen is the shortest query word that is searched for. Anything
// shorter is a letter.
const minTermLen = 2

// prefixMinLen is the shortest keyword allowed to match a longer token by
// prefix. "auth" should find "authentication"; a three-letter word should
// not, or "per" finds "person" and "performance" and every acronym is buried
// under words that happen to contain it.
const prefixMinLen = 4

// PrefixCredit is what a prefix match is worth relative to a whole-word one.
const PrefixCredit = 0.7

// QueryTerms extracts the keywords of a query: lower-cased, two or more
// characters, and not a stop word.
func QueryTerms(query string) []string {
	var terms []string
	seen := make(map[string]bool)
	for _, w := range Tokenize(query) {
		if len(w) < minTermLen || stopWords[w] || seen[w] {
			continue
		}
		seen[w] = true
		terms = append(terms, w)
	}
	return terms
}

// Tokenize splits text into lower-cased words. Boundaries are anything that
// is not a letter or digit, plus the case changes inside identifiers, so
// "NewHTTPClient" yields new, http, client and "AGI feasibility" yields agi,
// feasibility.
func Tokenize(s string) []string {
	var tokens []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			tokens = append(tokens, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	runes := []rune(s)
	for i, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if len(cur) > 0 && unicode.IsUpper(r) {
			prev := cur[len(cur)-1]
			// "aB": a new word starts. "ABc": the last capital of a run
			// begins a new word ("HTTPClient" -> http, client).
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return tokens
}

// TermGroups partitions query terms so that an initialism and its expansion
// count once. People write both — "AGI artificial general intelligence",
// "RAG retrieval augmented generation" — and scored as four separate words
// the acronym is worth a quarter of the query, so the one node named
// "AGI ..." loses to anything containing "artificial" and "intelligence".
// A term of two to six letters whose letters are the initials of a run of
// neighbouring terms is grouped with that run; each other term stands alone.
func TermGroups(terms []string) [][]int {
	grouped := make([]bool, len(terms))
	var groups [][]int
	for i, t := range terms {
		if grouped[i] || len(t) < 2 || len(t) > 6 {
			continue
		}
		if run := expansionOf(t, terms, i, grouped); run != nil {
			g := append([]int{i}, run...)
			for _, j := range g {
				grouped[j] = true
			}
			groups = append(groups, g)
		}
	}
	for i := range terms {
		if !grouped[i] {
			groups = append(groups, []int{i})
		}
	}
	sort.Slice(groups, func(a, b int) bool { return groups[a][0] < groups[b][0] })
	return groups
}

// expansionOf finds a run of terms, not including index skip and not already
// grouped, whose initials spell the candidate.
func expansionOf(candidate string, terms []string, skip int, grouped []bool) []int {
	n := len(candidate)
	for start := 0; start+n <= len(terms); start++ {
		run := make([]int, 0, n)
		ok := true
		for k := 0; k < n; k++ {
			j := start + k
			if j == skip || grouped[j] || terms[j][0] != candidate[k] {
				ok = false
				break
			}
			run = append(run, j)
		}
		if ok {
			return run
		}
	}
	return nil
}

// TermCredit reports how well one keyword matches a tokenized name: 1 for a
// whole word, PrefixCredit for the start of a longer word, 0 for neither.
func TermCredit(term string, tokens []string) float64 {
	best := 0.0
	for _, tok := range tokens {
		if tok == term {
			return 1
		}
		if len(term) >= prefixMinLen && strings.HasPrefix(tok, term) {
			best = PrefixCredit
		}
	}
	return best
}

// KeywordSearch finds nodes whose name or package contains the query's
// keywords, in one pass over the graph.
//
// Matching is on whole words, case-insensitively: "AGI" finds "AGI
// feasibility debate" and not "messaging". A keyword of four or more letters
// also matches the start of a longer word, so "auth" still finds
// "authentication". Package names are matched exactly, through the index, so
// a query like "LLM provider" reaches nodes in the "llm" package whose own
// names are generic.
func KeywordSearch(ctx context.Context, store graph.Store, query string) *Keywords {
	kw := &Keywords{Hits: make(map[string]*KeywordResult), Terms: QueryTerms(query)}
	if len(kw.Terms) == 0 {
		return kw
	}
	kw.Groups = TermGroups(kw.Terms)

	// One scan for every term, counting what it visits so rarity can be
	// judged against the graph rather than against the hits alone.
	scanned := 0
	nodes, err := store.QueryNodes(ctx, graph.NodeFilter{NameMatch: func(name string) bool {
		scanned++
		tokens := Tokenize(name)
		for _, term := range kw.Terms {
			if TermCredit(term, tokens) > 0 {
				return true
			}
		}
		return false
	}})
	if err != nil {
		return kw
	}

	// credit[nodeID][termIndex]
	credit := make(map[string][]float64)
	record := func(n *graph.Node, i int, c float64) {
		if n == nil || !vectorstore.IsEmbeddable(n.Type) {
			return
		}
		cs, ok := credit[n.ID]
		if !ok {
			cs = make([]float64, len(kw.Terms))
			credit[n.ID] = cs
			kw.Hits[n.ID] = &KeywordResult{Node: n}
		}
		if c > cs[i] {
			cs[i] = c
		}
	}

	for _, n := range nodes {
		tokens := Tokenize(n.Name)
		for i, term := range kw.Terms {
			if c := TermCredit(term, tokens); c > 0 {
				record(n, i, c)
			}
		}
	}
	for i, term := range kw.Terms {
		pkgNodes, err := store.QueryNodes(ctx, graph.NodeFilter{Package: term})
		if err != nil {
			continue
		}
		for _, n := range pkgNodes {
			record(n, i, 1)
		}
	}

	// A term's weight falls with how many nodes carry it. A term nobody
	// carries still counts in the denominator: a query the graph only half
	// answers is only half matched.
	df := make([]int, len(kw.Terms))
	for _, cs := range credit {
		for i, c := range cs {
			if c > 0 {
				df[i]++
			}
		}
	}
	weights := make([]float64, len(kw.Terms))
	for i := range kw.Terms {
		weights[i] = math.Log(1 + float64(max(scanned, 1))/float64(df[i]+1))
	}
	for id, cs := range credit {
		hit := kw.Hits[id]
		for _, c := range cs {
			if c > 0 {
				hit.MatchCount++
			}
		}
		hit.Share = ShareOf(cs, weights, kw.Groups)
	}
	return kw
}

// ShareOf scores one node's credits against the query's groups: each group
// weighs as much as its rarest term, and is covered as well as its
// initialism alone or its expansion on average, whichever is better.
func ShareOf(credits, weights []float64, groups [][]int) float64 {
	total, got := 0.0, 0.0
	for _, g := range groups {
		w := 0.0
		for _, i := range g {
			w = math.Max(w, weights[i])
		}
		total += w
		cover := credits[g[0]]
		if len(g) > 1 {
			sum := 0.0
			for _, i := range g[1:] {
				sum += credits[i]
			}
			cover = math.Max(cover, sum/float64(len(g)-1))
		}
		got += cover * w
	}
	if total == 0 {
		return 0
	}
	return got / total
}

// InjectKeywordResults adds keyword-matched nodes that vector search missed
// into the results, applying the same filters the vector results went
// through. It returns the updated results and every hit's share of the query,
// for reranking.
func InjectKeywordResults(
	results []vectorstore.SearchResult,
	kw *Keywords,
	typeFilter map[graph.NodeType]bool,
	noDocs bool,
	pkg, language string,
) ([]vectorstore.SearchResult, map[string]float64) {
	existing := make(map[string]bool, len(results))
	for _, r := range results {
		if r.Node != nil {
			existing[r.Node.ID] = true
		}
	}

	shares := make(map[string]float64)
	if kw == nil {
		return results, shares
	}
	for id, hit := range kw.Hits {
		shares[id] = hit.Share
	}

	// Injected in a fixed order so equal scores do not shuffle between runs.
	ids := make([]string, 0, len(kw.Hits))
	for id := range kw.Hits {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		if existing[id] {
			continue
		}
		n := kw.Hits[id].Node
		if noDocs && DocNodeTypes[n.Type] {
			continue
		}
		if len(typeFilter) > 0 && !typeFilter[n.Type] {
			continue
		}
		if pkg != "" && !strings.Contains(strings.ToLower(n.Package), strings.ToLower(pkg)) {
			continue
		}
		if language != "" && !strings.EqualFold(n.Language, language) {
			continue
		}
		results = append(results, vectorstore.SearchResult{Node: n, Score: 0})
	}
	return results, shares
}

// RerankResults combines vector similarity with keyword coverage, a code-type
// boost, and graph centrality.
//
// Centrality is on a log scale: a taxonomy hub with forty edges is better
// connected than a leaf with three, but not thirteen times better, and on a
// linear scale the hub outranked an exact match on its own name.
func RerankResults(ctx context.Context, store graph.Store, results []vectorstore.SearchResult, shares map[string]float64) []vectorstore.SearchResult {
	if len(results) == 0 {
		return results
	}

	const (
		vectorWeight     = 0.55
		keywordWeight    = 0.20
		codeTypeBonus    = 0.10
		centralityWeight = 0.15
	)

	type scored struct {
		idx      int
		combined float64
	}

	maxEdges := 1
	edgeCounts := make([]int, len(results))
	for i, r := range results {
		if r.Node == nil {
			continue
		}
		edges, err := store.GetEdges(ctx, r.Node.ID, "")
		if err == nil {
			edgeCounts[i] = len(edges)
			if len(edges) > maxEdges {
				maxEdges = len(edges)
			}
		}
	}

	scoredResults := make([]scored, len(results))
	for i, r := range results {
		if r.Node == nil {
			scoredResults[i] = scored{idx: i, combined: 0}
			continue
		}

		vectorScore := r.Score
		share := shares[r.Node.ID]
		if vectorScore == 0 && share > 0 {
			vectorScore = share * 0.5
		}

		combined := vectorWeight * vectorScore
		combined += keywordWeight * share

		if CodeNodeTypes[r.Node.Type] {
			combined += codeTypeBonus
		}

		centrality := math.Log1p(float64(edgeCounts[i])) / math.Log1p(float64(maxEdges))
		combined += centralityWeight * centrality

		scoredResults[i] = scored{idx: i, combined: combined}
	}

	sort.SliceStable(scoredResults, func(i, j int) bool {
		return scoredResults[i].combined > scoredResults[j].combined
	})

	reranked := make([]vectorstore.SearchResult, len(results))
	for i, s := range scoredResults {
		reranked[i] = results[s.idx]
		reranked[i].Score = s.combined
	}

	return reranked
}

// ChunkSnippet extracts the first N meaningful lines from chunk text.
func ChunkSnippet(text string, maxLines int) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	var meaningful []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "```" {
			continue
		}
		meaningful = append(meaningful, trimmed)
		if len(meaningful) >= maxLines {
			break
		}
	}
	if len(meaningful) == 0 {
		return ""
	}
	result := strings.Join(meaningful, " | ")
	if len(result) > 200 {
		result = result[:197] + "..."
	}
	return result
}

// RelativePath tries to make filePath relative to one of the repo roots.
func RelativePath(filePath string, repoPaths []string) string {
	if filePath == "" {
		return ""
	}
	for _, root := range repoPaths {
		if rel, err := filepath.Rel(root, filePath); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return filePath
}

// OneHopEdges fetches key edges for a node as structured data.
func OneHopEdges(ctx context.Context, store graph.Store, nodeID string) []EdgeInfo {
	edgeTypes := []graph.EdgeType{
		graph.EdgeContains,
		graph.EdgeExposes,
		graph.EdgeCalls,
		graph.EdgeTests,
		graph.EdgeImplements,
		graph.EdgeImports,
	}

	var result []EdgeInfo
	for _, et := range edgeTypes {
		edges, err := store.GetEdges(ctx, nodeID, et)
		if err != nil || len(edges) == 0 {
			continue
		}
		edgeLimit := min(5, len(edges))
		for _, e := range edges[:edgeLimit] {
			peerID := e.TargetID
			direction := "out"
			if e.TargetID == nodeID {
				peerID = e.SourceID
				direction = "in"
			}
			peer, err := store.GetNode(ctx, peerID)
			if err != nil {
				continue
			}
			result = append(result, EdgeInfo{
				Direction: direction,
				EdgeType:  string(et),
				NodeName:  peer.Name,
				NodeType:  string(peer.Type),
			})
		}
	}
	return result
}

// OneHopEdgesText fetches key edges for a node and formats as indented text.
func OneHopEdgesText(ctx context.Context, store graph.Store, nodeID string) string {
	edgeTypes := []graph.EdgeType{
		graph.EdgeContains,
		graph.EdgeExposes,
		graph.EdgeCalls,
		graph.EdgeTests,
		graph.EdgeImplements,
		graph.EdgeImports,
	}

	var b strings.Builder
	for _, et := range edgeTypes {
		edges, err := store.GetEdges(ctx, nodeID, et)
		if err != nil || len(edges) == 0 {
			continue
		}
		edgeLimit := min(5, len(edges))
		for _, e := range edges[:edgeLimit] {
			peerID := e.TargetID
			direction := "→"
			if e.TargetID == nodeID {
				peerID = e.SourceID
				direction = "←"
			}
			peer, err := store.GetNode(ctx, peerID)
			if err != nil {
				continue
			}
			fmt.Fprintf(&b, "    %s %s %s (%s)\n", direction, et, peer.Name, peer.Type)
		}
	}
	return b.String()
}
