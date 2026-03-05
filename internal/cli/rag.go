package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/search"

	// Register embedding providers so their init() functions run.
	_ "github.com/imyousuf/CodeEagle/internal/embedding"
)

// ragJSONResult represents a single search result in JSON output.
type ragJSONResult struct {
	Rank      int               `json:"rank"`
	Score     float64           `json:"score"`
	Relevance int               `json:"relevance"`
	Type      graph.NodeType    `json:"type"`
	Name      string            `json:"name"`
	FilePath  string            `json:"file_path,omitempty"`
	Line      int               `json:"line,omitempty"`
	Package   string            `json:"package,omitempty"`
	Language  string            `json:"language,omitempty"`
	Signature string            `json:"signature,omitempty"`
	ChunkText string            `json:"chunk_text,omitempty"`
	Edges     []search.EdgeInfo `json:"edges,omitempty"`
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
			results = search.DeduplicateResults(results)

			// Apply --no-docs filter.
			if noDocs {
				filtered := results[:0]
				for _, r := range results {
					if r.Node != nil && !search.DocNodeTypes[r.Node.Type] {
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
			keywordNodes, totalKeywords := search.KeywordSearch(context.Background(), store, query)
			results, keywordCounts := search.InjectKeywordResults(results, keywordNodes, typeFilter, noDocs, pkg, language)
			results = search.RerankResults(context.Background(), store, results, keywordCounts, totalKeywords)

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
						FilePath:  search.RelativePath(r.Node.FilePath, repoPaths),
						Line:      r.Node.Line,
						Package:   r.Node.Package,
						Language:  r.Node.Language,
						Signature: r.Node.Signature,
						ChunkText: r.ChunkText,
					}
					if showEdges {
						jr.Edges = search.OneHopEdges(context.Background(), store, r.Node.ID)
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
				relPath := search.RelativePath(n.FilePath, repoPaths)
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
				if snippet := search.ChunkSnippet(r.ChunkText, 2); snippet != "" {
					fmt.Fprintf(out, "    > %s\n", snippet)
				}

				if showEdges {
					edgeText := search.OneHopEdgesText(context.Background(), store, n.ID)
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
