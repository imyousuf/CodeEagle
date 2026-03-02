package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/vectorstore"

	// Register embedding providers so their init() functions run.
	_ "github.com/imyousuf/CodeEagle/internal/embedding"
)

// ragJSONEdge represents an edge in JSON output.
type ragJSONEdge struct {
	Direction string `json:"direction"`
	EdgeType  string `json:"edge_type"`
	NodeName  string `json:"node_name"`
	NodeType  string `json:"node_type"`
}

// ragJSONResult represents a single search result in JSON output.
type ragJSONResult struct {
	Rank      int            `json:"rank"`
	Score     float64        `json:"score"`
	Relevance int            `json:"relevance"`
	Type      graph.NodeType `json:"type"`
	Name      string         `json:"name"`
	FilePath  string         `json:"file_path,omitempty"`
	Line      int            `json:"line,omitempty"`
	Package   string         `json:"package,omitempty"`
	Language  string         `json:"language,omitempty"`
	Signature string         `json:"signature,omitempty"`
	ChunkText string         `json:"chunk_text,omitempty"`
	Edges     []ragJSONEdge  `json:"edges,omitempty"`
}

// docNodeTypes are node types excluded by --no-docs.
var docNodeTypes = map[graph.NodeType]bool{
	graph.NodeDocument:    true,
	graph.NodeAIGuideline: true,
}

// codeNodeTypes get a score boost during reranking.
var codeNodeTypes = map[graph.NodeType]bool{
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

func newRagCmd() *cobra.Command {
	var (
		limit     int
		nodeType  string
		pkg       string
		language  string
		jsonOut   bool
		showEdges bool
		minScore  float64
		noDocs    bool
	)

	cmd := &cobra.Command{
		Use:   "rag [query]",
		Short: "Semantic search over the knowledge graph",
		Long: `Search the knowledge graph using natural language semantic similarity.

Finds functions, types, services, docs, and other code entities that are
semantically related to your query. Requires a vector index — run
'codeeagle sync' or 'codeeagle vectorindex' first.

Results are ranked using a hybrid approach: vector similarity is combined
with keyword matching from the graph and graph centrality (nodes with
more connections rank higher). Code entities (functions, structs, etc.)
receive a small boost over documentation nodes.

Examples:
  codeeagle rag "authentication middleware"
  codeeagle rag "database connection pooling" --limit 10
  codeeagle rag "error handling" --type Function,Struct
  codeeagle rag "API routing" --json --package api
  codeeagle rag "test helpers" --edges --language go
  codeeagle rag "parser" --no-docs`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if cfg.ConfigDir == "" {
				return fmt.Errorf("no config directory found; run 'codeeagle init' first")
			}

			store, currentBranch, err := openBranchStore(cfg)
			if err != nil {
				return err
			}
			defer store.Close()

			logFn := func(format string, args ...any) {
				if verbose {
					fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", args...)
				}
			}

			vs, err := openVectorStore(cfg, store, currentBranch, logFn)
			if err != nil {
				return err
			}
			if vs == nil {
				return fmt.Errorf("no embedding provider available; install Ollama with nomic-embed-text-v2-moe or configure Vertex AI")
			}
			defer vs.Close()

			loaded, err := vs.Load()
			if err != nil {
				return fmt.Errorf("load vector index: %w", err)
			}
			if !loaded {
				return fmt.Errorf("vector index not built; run 'codeeagle sync' or 'codeeagle vectorindex' first")
			}

			if limit > 30 {
				limit = 30
			}

			query := strings.Join(args, " ")

			// Parse type filter.
			var typeFilter map[graph.NodeType]bool
			if nodeType != "" {
				parts := strings.Split(nodeType, ",")
				typeFilter = make(map[graph.NodeType]bool, len(parts))
				for _, p := range parts {
					p = strings.TrimSpace(p)
					if p != "" {
						typeFilter[graph.NodeType(p)] = true
					}
				}
			}

			// Fetch extra results to account for filtering, dedup, and reranking.
			// When --type is specified, fetch much more since vector search results
			// are dominated by Document nodes (longer text = stronger embeddings),
			// and we need enough candidates of the requested type after filtering.
			fetchK := limit * 5
			if len(typeFilter) > 0 || noDocs {
				fetchK = limit * 10
			}
			if fetchK > 200 {
				fetchK = 200
			}

			results, err := vs.Search(context.Background(), query, fetchK)
			if err != nil {
				return fmt.Errorf("search failed: %w", err)
			}

			// Deduplicate by node ID (keep highest-scoring chunk per node).
			results = deduplicateResults(results)

			// Apply --no-docs filter.
			if noDocs {
				filtered := results[:0]
				for _, r := range results {
					if r.Node != nil && !docNodeTypes[r.Node.Type] {
						filtered = append(filtered, r)
					}
				}
				results = filtered
			}

			// Apply type filter.
			if len(typeFilter) > 0 {
				filtered := results[:0]
				for _, r := range results {
					if r.Node != nil && typeFilter[r.Node.Type] {
						filtered = append(filtered, r)
					}
				}
				results = filtered
			}

			// Apply package filter.
			if pkg != "" {
				filtered := results[:0]
				for _, r := range results {
					if r.Node != nil && strings.Contains(strings.ToLower(r.Node.Package), strings.ToLower(pkg)) {
						filtered = append(filtered, r)
					}
				}
				results = filtered
			}

			// Apply language filter.
			if language != "" {
				filtered := results[:0]
				for _, r := range results {
					if r.Node != nil && strings.EqualFold(r.Node.Language, language) {
						filtered = append(filtered, r)
					}
				}
				results = filtered
			}

			// Hybrid search: keyword-match nodes from the graph, inject any that
			// vector search missed, then rerank everything together.
			// Nodes matching more query keywords get a proportionally higher bonus.
			keywordNodes, totalKeywords := keywordSearch(context.Background(), store, query)
			results, keywordCounts := injectKeywordResults(results, keywordNodes, typeFilter, noDocs, pkg, language)
			results = rerankResults(context.Background(), store, results, keywordCounts, totalKeywords)

			// Apply min score filter (after reranking).
			if minScore > 0 {
				filtered := results[:0]
				for _, r := range results {
					if r.Score >= minScore {
						filtered = append(filtered, r)
					}
				}
				results = filtered
			}

			// Limit results.
			if len(results) > limit {
				results = results[:limit]
			}

			// Build repo root paths for relative path display.
			var repoPaths []string
			for _, repo := range cfg.Repositories {
				repoPaths = append(repoPaths, repo.Path)
			}

			out := cmd.OutOrStdout()

			if len(results) == 0 {
				fmt.Fprintln(out, "No results found.")
				return nil
			}

			// JSON output.
			if jsonOut {
				jResults := make([]ragJSONResult, 0, len(results))
				for i, r := range results {
					if r.Node == nil {
						continue
					}
					jr := ragJSONResult{
						Rank:      i + 1,
						Score:     r.Score,
						Relevance: int(math.Round(r.Score * 100)),
						Type:      r.Node.Type,
						Name:      r.Node.Name,
						FilePath:  relativePath(r.Node.FilePath, repoPaths),
						Line:      r.Node.Line,
						Package:   r.Node.Package,
						Language:  r.Node.Language,
						Signature: r.Node.Signature,
						ChunkText: r.ChunkText,
					}
					if showEdges {
						jr.Edges = ragOneHopEdges(context.Background(), store, r.Node.ID)
					}
					jResults = append(jResults, jr)
				}

				data, err := json.MarshalIndent(jResults, "", "  ")
				if err != nil {
					return fmt.Errorf("marshal JSON: %w", err)
				}
				fmt.Fprintln(out, string(data))
				return nil
			}

			// Text output.
			fmt.Fprintf(out, "Semantic search: %q (%d results)\n\n", query, len(results))

			for i, r := range results {
				if r.Node == nil {
					continue
				}
				n := r.Node
				fmt.Fprintf(out, "%2d. [%s] %s  (%d%%)\n", i+1, n.Type, n.Name, int(math.Round(r.Score*100)))
				relPath := relativePath(n.FilePath, repoPaths)
				if relPath != "" {
					fmt.Fprintf(out, "    File: %s", relPath)
					if n.Line > 0 {
						fmt.Fprintf(out, ":%d", n.Line)
					}
					fmt.Fprintln(out)
				}
				if n.Package != "" {
					fmt.Fprintf(out, "    Package: %s\n", n.Package)
				}
				if n.Signature != "" {
					fmt.Fprintf(out, "    %s\n", n.Signature)
				}

				// Show chunk text snippet (first 2 meaningful lines).
				if snippet := chunkSnippet(r.ChunkText, 2); snippet != "" {
					fmt.Fprintf(out, "    > %s\n", snippet)
				}

				if showEdges {
					edgeText := ragOneHopEdgesText(context.Background(), store, n.ID)
					if edgeText != "" {
						fmt.Fprint(out, edgeText)
					}
				}

				fmt.Fprintln(out)
			}

			meta := vs.Meta()
			if meta != nil {
				fmt.Fprintf(out, "%d results (embedding: %s/%s)\n", len(results), meta.Provider, meta.Model)
			}

			return nil
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 15, "maximum number of results (max 30)")
	cmd.Flags().StringVar(&nodeType, "type", "", "filter by node types (comma-separated, e.g., Function,Struct)")
	cmd.Flags().StringVar(&pkg, "package", "", "filter by package name (substring match)")
	cmd.Flags().StringVar(&language, "language", "", "filter by language (e.g., go, python)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "output results as JSON")
	cmd.Flags().BoolVar(&showEdges, "edges", false, "include 1-hop relationship edges in output")
	cmd.Flags().Float64Var(&minScore, "min-score", 0, "minimum similarity score (0-1)")
	cmd.Flags().BoolVar(&noDocs, "no-docs", false, "exclude Document and AIGuideline nodes from results")

	return cmd
}

// deduplicateResults keeps only the highest-scoring chunk per node ID.
func deduplicateResults(results []vectorstore.SearchResult) []vectorstore.SearchResult {
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

// keywordResult holds a keyword-matched node and how many distinct query keywords it matched.
type keywordResult struct {
	node       *graph.Node
	matchCount int
}

// keywordSearch extracts keywords from the query and finds matching nodes via graph glob search.
// It searches both node names (glob) and package names (exact match) so that queries like
// "LLM provider" find nodes in the "llm" package even when their names are generic.
// Returns per-node results with match counts and the total number of keywords searched.
// Nodes matching more keywords rank higher during reranking.
func keywordSearch(ctx context.Context, store graph.Store, query string) (map[string]*keywordResult, int) {
	hits := make(map[string]*keywordResult)

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
		// Go uses camelCase, so "user" should match "initUserAgent".
		// filepath.Match is case-sensitive, so we try both lowercase and
		// title-cased variants (e.g., *user* and *User*).
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
		// Catches e.g. "llm" → all nodes in package "llm".
		nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Package: w})
		if err == nil {
			addHits(nodes)
		}

		// Merge this keyword's hits into the overall map, incrementing match counts.
		for id, n := range kwHits {
			if existing, ok := hits[id]; ok {
				existing.matchCount++
			} else {
				hits[id] = &keywordResult{node: n, matchCount: 1}
			}
		}
	}
	return hits, len(keywords)
}

// injectKeywordResults adds keyword-matched nodes that vector search missed into the results.
// Returns the updated results and per-node keyword match counts (for proportional reranking).
func injectKeywordResults(
	results []vectorstore.SearchResult,
	keywordNodes map[string]*keywordResult,
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
		keywordCounts[id] = kr.matchCount
	}

	// Inject keyword-only nodes (not in vector results) with zero vector score.
	// The reranker will assign them a score based on keyword + code type + centrality.
	for id, kr := range keywordNodes {
		if existing[id] {
			continue
		}
		n := kr.node
		// Apply the same filters that were applied to vector results.
		if noDocs && docNodeTypes[n.Type] {
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
			Score: 0, // no vector similarity — reranker will score via keyword + centrality + code boost
		})
	}

	return results, keywordCounts
}

// rerankResults combines vector similarity with keyword match bonus, code type boost, and graph centrality.
// The keyword bonus is scaled proportionally: a node matching 2/3 keywords scores higher than 1/3.
func rerankResults(ctx context.Context, store graph.Store, results []vectorstore.SearchResult, keywordCounts map[string]int, totalKeywords int) []vectorstore.SearchResult {
	if len(results) == 0 {
		return results
	}

	const (
		vectorWeight     = 0.55 // base vector similarity
		keywordWeight    = 0.20 // proportional keyword match (matchCount / totalKeywords)
		codeTypeBonus    = 0.10 // bonus for code entities (vs docs)
		centralityWeight = 0.15 // normalized graph centrality
	)

	// Compute centrality (edge count) for all result nodes.
	type scored struct {
		idx      int
		combined float64
	}

	maxEdges := 1 // avoid division by zero
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

	scored_results := make([]scored, len(results))
	for i, r := range results {
		if r.Node == nil {
			scored_results[i] = scored{idx: i, combined: 0}
			continue
		}

		vectorScore := r.Score
		matchCount := keywordCounts[r.Node.ID]
		keywordRatio := 0.0
		if totalKeywords > 0 && matchCount > 0 {
			keywordRatio = float64(matchCount) / float64(totalKeywords)
		}

		// For keyword-injected nodes (zero vector score), estimate a synthetic
		// vector score from their keyword match ratio. A node matching 2/3
		// keywords is likely more relevant than one matching 1/3, and should
		// be competitive with low-scoring vector results.
		if vectorScore == 0 && keywordRatio > 0 {
			vectorScore = keywordRatio * 0.5 // scale to ~half of a strong vector match
		}

		combined := vectorWeight * vectorScore

		// Proportional keyword match bonus: matchCount/totalKeywords.
		// A node matching all keywords gets the full bonus; matching 1/3 gets 1/3.
		combined += keywordWeight * keywordRatio

		// Code type boost.
		if codeNodeTypes[r.Node.Type] {
			combined += codeTypeBonus
		}

		// Centrality: normalized edge count.
		centrality := float64(edgeCounts[i]) / float64(maxEdges)
		combined += centralityWeight * centrality

		scored_results[i] = scored{idx: i, combined: combined}
	}

	// Sort by combined score descending.
	sort.Slice(scored_results, func(i, j int) bool {
		return scored_results[i].combined > scored_results[j].combined
	})

	// Rebuild results in new order with updated scores.
	reranked := make([]vectorstore.SearchResult, len(results))
	for i, s := range scored_results {
		reranked[i] = results[s.idx]
		reranked[i].Score = s.combined
	}

	return reranked
}

// relativePath tries to make filePath relative to one of the repo roots.
func relativePath(filePath string, repoPaths []string) string {
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

// chunkSnippet extracts the first N meaningful lines from chunk text.
func chunkSnippet(text string, maxLines int) string {
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

// ragOneHopEdgesText fetches key edges for a node and formats as indented text.
func ragOneHopEdgesText(ctx context.Context, store graph.Store, nodeID string) string {
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

// ragOneHopEdges fetches key edges for a node and returns structured data for JSON output.
func ragOneHopEdges(ctx context.Context, store graph.Store, nodeID string) []ragJSONEdge {
	edgeTypes := []graph.EdgeType{
		graph.EdgeContains,
		graph.EdgeExposes,
		graph.EdgeCalls,
		graph.EdgeTests,
		graph.EdgeImplements,
		graph.EdgeImports,
	}

	var result []ragJSONEdge
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
			result = append(result, ragJSONEdge{
				Direction: direction,
				EdgeType:  string(et),
				NodeName:  peer.Name,
				NodeType:  string(peer.Type),
			})
		}
	}
	return result
}
