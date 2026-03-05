// Package search provides reusable search algorithms for CodeEagle's hybrid
// (vector + keyword) semantic search. Functions are extracted from the CLI rag
// command so both CLI and desktop app can share the same logic.
package search

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

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

// KeywordResult holds a keyword-matched node and how many distinct query keywords it matched.
type KeywordResult struct {
	Node       *graph.Node
	MatchCount int
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

// KeywordSearch extracts keywords from the query and finds matching nodes via graph glob search.
// It searches both node names (glob) and package names (exact match) so that queries like
// "LLM provider" find nodes in the "llm" package even when their names are generic.
// Returns per-node results with match counts and the total number of keywords searched.
func KeywordSearch(ctx context.Context, store graph.Store, query string) (map[string]*KeywordResult, int) {
	hits := make(map[string]*KeywordResult)

	// Extract meaningful keywords (3+ chars, skip common words).
	words := strings.Fields(strings.ToLower(query))
	skipWords := map[string]bool{
		"the": true, "and": true, "for": true, "how": true, "what": true,
		"does": true, "this": true, "that": true, "with": true, "from": true,
		"are": true, "was": true, "has": true, "not": true, "all": true,
	}

	var keywords []string
	for _, w := range words {
		if len(w) >= 3 && !skipWords[w] {
			keywords = append(keywords, w)
		}
	}

	for _, w := range keywords {
		// Collect nodes matching this keyword (deduplicated within the keyword).
		kwHits := make(map[string]*graph.Node)

		addHits := func(nodes []*graph.Node) {
			for _, n := range nodes {
				if n != nil && vectorstore.IsEmbeddable(n.Type) {
					kwHits[n.ID] = n
				}
			}
		}

		// 1. Glob on node Name — case-insensitive via multiple patterns.
		patterns := []string{"*" + w + "*"}
		titled := strings.ToUpper(w[:1]) + w[1:]
		if titled != w {
			patterns = append(patterns, "*"+titled+"*")
		}
		for _, pattern := range patterns {
			nodes, err := store.QueryNodes(ctx, graph.NodeFilter{NamePattern: pattern})
			if err == nil {
				addHits(nodes)
			}
		}

		// 2. Exact match on Package name (uses idx:pkg: index — fast).
		nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Package: w})
		if err == nil {
			addHits(nodes)
		}

		// Merge this keyword's hits into the overall map, incrementing match counts.
		for id, n := range kwHits {
			if existing, ok := hits[id]; ok {
				existing.MatchCount++
			} else {
				hits[id] = &KeywordResult{Node: n, MatchCount: 1}
			}
		}
	}
	return hits, len(keywords)
}

// InjectKeywordResults adds keyword-matched nodes that vector search missed into the results.
// Returns the updated results and per-node keyword match counts (for proportional reranking).
func InjectKeywordResults(
	results []vectorstore.SearchResult,
	keywordNodes map[string]*KeywordResult,
	typeFilter map[graph.NodeType]bool,
	noDocs bool,
	pkg, language string,
) ([]vectorstore.SearchResult, map[string]int) {
	// Build set of node IDs already in results.
	existing := make(map[string]bool, len(results))
	for _, r := range results {
		if r.Node != nil {
			existing[r.Node.ID] = true
		}
	}

	// All keyword node match counts (for reranking, even if already in results).
	keywordCounts := make(map[string]int, len(keywordNodes))
	for id, kr := range keywordNodes {
		keywordCounts[id] = kr.MatchCount
	}

	// Inject keyword-only nodes (not in vector results) with zero vector score.
	for id, kr := range keywordNodes {
		if existing[id] {
			continue
		}
		n := kr.Node
		// Apply the same filters that were applied to vector results.
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
		results = append(results, vectorstore.SearchResult{
			Node:  n,
			Score: 0,
		})
	}

	return results, keywordCounts
}

// RerankResults combines vector similarity with keyword match bonus, code type boost, and graph centrality.
// The keyword bonus is scaled proportionally: a node matching 2/3 keywords scores higher than 1/3.
func RerankResults(ctx context.Context, store graph.Store, results []vectorstore.SearchResult, keywordCounts map[string]int, totalKeywords int) []vectorstore.SearchResult {
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
		matchCount := keywordCounts[r.Node.ID]
		keywordRatio := 0.0
		if totalKeywords > 0 && matchCount > 0 {
			keywordRatio = float64(matchCount) / float64(totalKeywords)
		}

		if vectorScore == 0 && keywordRatio > 0 {
			vectorScore = keywordRatio * 0.5
		}

		combined := vectorWeight * vectorScore
		combined += keywordWeight * keywordRatio

		if CodeNodeTypes[r.Node.Type] {
			combined += codeTypeBonus
		}

		centrality := float64(edgeCounts[i]) / float64(maxEdges)
		combined += centralityWeight * centrality

		scoredResults[i] = scored{idx: i, combined: combined}
	}

	sort.Slice(scoredResults, func(i, j int) bool {
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
