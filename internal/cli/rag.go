package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph"

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

func newRagCmd() *cobra.Command {
	var (
		limit    int
		nodeType string
		jsonOut  bool
		showEdges bool
		minScore float64
	)

	cmd := &cobra.Command{
		Use:   "rag [query]",
		Short: "Semantic search over the knowledge graph",
		Long: `Search the knowledge graph using natural language semantic similarity.

Finds functions, types, services, docs, and other code entities that are
semantically related to your query. Requires a vector index — run
'codeeagle sync' or 'codeeagle vectorindex' first.

Examples:
  codeeagle rag "authentication middleware"
  codeeagle rag "database connection pooling" --limit 10
  codeeagle rag "error handling" --type Function,Struct
  codeeagle rag "API routing" --json
  codeeagle rag "test helpers" --edges`,
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

			// Fetch more results when filtering by type.
			fetchK := limit
			if len(typeFilter) > 0 {
				fetchK = min(limit*3, 60)
			}

			results, err := vs.Search(context.Background(), query, fetchK)
			if err != nil {
				return fmt.Errorf("search failed: %w", err)
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

			// Apply min score filter.
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
						Type:      r.Node.Type,
						Name:      r.Node.Name,
						FilePath:  r.Node.FilePath,
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
				fmt.Fprintf(out, "%2d. [%s] %s (%.3f)\n", i+1, n.Type, n.Name, r.Score)
				if n.FilePath != "" {
					fmt.Fprintf(out, "    File: %s", n.FilePath)
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
	cmd.Flags().BoolVar(&jsonOut, "json", false, "output results as JSON")
	cmd.Flags().BoolVar(&showEdges, "edges", false, "include 1-hop relationship edges in output")
	cmd.Flags().Float64Var(&minScore, "min-score", 0, "minimum similarity score (0-1)")

	return cmd
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
