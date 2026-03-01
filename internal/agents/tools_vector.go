package agents

import (
	"context"
	"fmt"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/vectorstore"
)

// semanticSearchTool provides vector-based semantic search over the knowledge graph.
type semanticSearchTool struct {
	vs    *vectorstore.VectorStore
	store graph.Store
}

func (t *semanticSearchTool) Name() string { return "semantic_search" }

func (t *semanticSearchTool) Description() string {
	return "Search the knowledge graph using semantic similarity. Returns the most relevant code entities (functions, types, services, docs) matching a natural language query. Use this to quickly find relevant code when you know *what* you're looking for but not the exact name."
}

func (t *semanticSearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Natural language query describing what you're looking for (e.g., 'authentication middleware', 'database connection pooling').",
			},
			"top_k": map[string]any{
				"type":        "integer",
				"description": "Number of results to return (default 15, max 30).",
			},
			"node_types": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional filter: only return nodes of these types (e.g., ['Function', 'Struct', 'Interface']).",
			},
		},
		"required": []string{"query"},
	}
}

func (t *semanticSearchTool) Execute(ctx context.Context, args map[string]any) (string, bool) {
	query, _ := args["query"].(string)
	if query == "" {
		return "Error: query is required", false
	}

	topK := 15
	if v, ok := args["top_k"].(float64); ok && v > 0 {
		topK = int(v)
	}
	if topK > 30 {
		topK = 30
	}

	// Node type filter.
	var typeFilter map[graph.NodeType]bool
	if types, ok := args["node_types"].([]any); ok && len(types) > 0 {
		typeFilter = make(map[graph.NodeType]bool, len(types))
		for _, t := range types {
			if s, ok := t.(string); ok {
				typeFilter[graph.NodeType(s)] = true
			}
		}
	}

	// Fetch more results than needed if filtering.
	fetchK := topK
	if len(typeFilter) > 0 {
		fetchK = min(topK*3, 60)
	}

	results, err := t.vs.Search(ctx, query, fetchK)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), false
	}
	if len(results) == 0 {
		return "No results found for the given query.", false
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

	// Limit to topK.
	if len(results) > topK {
		results = results[:topK]
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d relevant results:\n\n", len(results))

	for i, r := range results {
		if r.Node == nil {
			continue
		}
		n := r.Node
		fmt.Fprintf(&b, "### %d. [%s] %s (score: %.3f)\n", i+1, n.Type, n.Name, r.Score)
		if n.FilePath != "" {
			fmt.Fprintf(&b, "File: %s", n.FilePath)
			if n.Line > 0 {
				fmt.Fprintf(&b, ":%d", n.Line)
			}
			fmt.Fprintln(&b)
		}
		if n.Package != "" {
			fmt.Fprintf(&b, "Package: %s\n", n.Package)
		}

		// Show the matching chunk text (truncated if long).
		if r.ChunkText != "" {
			text := r.ChunkText
			if len(text) > 500 {
				text = text[:500] + "..."
			}
			fmt.Fprintf(&b, "```\n%s\n```\n", text)
		}

		// Include 1-hop edges for structural context.
		if t.store != nil {
			edges := getOneHopEdges(ctx, t.store, n.ID)
			if edges != "" {
				fmt.Fprintf(&b, "Relationships:\n%s", edges)
			}
		}

		fmt.Fprintln(&b)
	}

	return b.String(), true
}

// getOneHopEdges fetches key relationship edges for a node and formats them.
func getOneHopEdges(ctx context.Context, store graph.Store, nodeID string) string {
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
		// Limit edges shown per type.
		limit := min(5, len(edges))
		for _, e := range edges[:limit] {
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
			fmt.Fprintf(&b, "  %s %s %s (%s)\n", direction, et, peer.Name, peer.Type)
		}
	}
	return b.String()
}
