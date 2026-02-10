package agents

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// --- query_file_symbols ---

type queryFileSymbolsTool struct {
	store graph.Store
}

func (t *queryFileSymbolsTool) Name() string { return "query_file_symbols" }

func (t *queryFileSymbolsTool) Description() string {
	return "List all symbols (functions, structs, interfaces, etc.) defined in a specific file, sorted by line number. Returns a markdown table with type, name, line range, and export status."
}

func (t *queryFileSymbolsTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "The file path to list symbols for (e.g., 'internal/graph/graph.go').",
			},
		},
		"required": []string{"file_path"},
	}
}

func (t *queryFileSymbolsTool) Execute(ctx context.Context, args map[string]any) (string, bool) {
	filePath, _ := args["file_path"].(string)
	if filePath == "" {
		return "Error: file_path is required", false
	}

	nodes, err := t.store.QueryNodes(ctx, graph.NodeFilter{FilePath: filePath})
	if err != nil {
		return fmt.Sprintf("Error querying nodes: %v", err), false
	}
	if len(nodes) == 0 {
		return fmt.Sprintf("No symbols found in file %q.", filePath), false
	}

	// Filter out file-level nodes and sort by line number.
	var symbols []*graph.Node
	for _, n := range nodes {
		if n.Type == graph.NodeFile {
			continue
		}
		symbols = append(symbols, n)
	}
	if len(symbols) == 0 {
		return fmt.Sprintf("No symbols found in file %q (only a file-level node exists).", filePath), false
	}

	sort.Slice(symbols, func(i, j int) bool {
		return symbols[i].Line < symbols[j].Line
	})

	var b strings.Builder
	fmt.Fprintf(&b, "Symbols in %s (%d):\n\n", filePath, len(symbols))
	b.WriteString("| Type | Name | Lines | Exported |\n")
	b.WriteString("|---|---|---|---|\n")
	for _, n := range symbols {
		lines := ""
		if n.Line > 0 {
			lines = fmt.Sprintf("%d", n.Line)
			if n.EndLine > 0 && n.EndLine != n.Line {
				lines = fmt.Sprintf("%d-%d", n.Line, n.EndLine)
			}
		}
		exported := "no"
		if n.Exported {
			exported = "yes"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", n.Type, n.Name, lines, exported)
	}

	return b.String(), true
}

// --- query_interface_implementors ---

type queryInterfaceImplTool struct {
	store graph.Store
}

func (t *queryInterfaceImplTool) Name() string { return "query_interface_implementors" }

func (t *queryInterfaceImplTool) Description() string {
	return "Find an interface by name and list all types that implement it. Uses the Implements edge in the knowledge graph."
}

func (t *queryInterfaceImplTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "The interface name to search for (e.g., 'Store', 'Parser', 'Handler').",
			},
		},
		"required": []string{"name"},
	}
}

func (t *queryInterfaceImplTool) Execute(ctx context.Context, args map[string]any) (string, bool) {
	name, _ := args["name"].(string)
	if name == "" {
		return "Error: name is required", false
	}

	// Search for interface nodes matching the name.
	nodes, err := t.store.QueryNodes(ctx, graph.NodeFilter{
		Type:        graph.NodeInterface,
		NamePattern: name,
	})
	if err != nil {
		return fmt.Sprintf("Error querying interfaces: %v", err), false
	}
	if len(nodes) == 0 {
		return fmt.Sprintf("No interface found matching %q.", name), false
	}

	var b strings.Builder

	for _, iface := range nodes {
		fmt.Fprintf(&b, "## Interface: %s\n", iface.Name)
		if iface.FilePath != "" {
			fmt.Fprintf(&b, "File: %s", iface.FilePath)
			if iface.Line > 0 {
				fmt.Fprintf(&b, " (line %d)", iface.Line)
			}
			b.WriteString("\n")
		}
		if iface.Package != "" {
			fmt.Fprintf(&b, "Package: %s\n", iface.Package)
		}

		// Find implementors via incoming Implements edges.
		implementors, err := t.store.GetNeighbors(ctx, iface.ID, graph.EdgeImplements, graph.Incoming)
		if err != nil {
			fmt.Fprintf(&b, "\nError finding implementors: %v\n", err)
			continue
		}

		if len(implementors) == 0 {
			b.WriteString("\nNo implementors found.\n")
		} else {
			fmt.Fprintf(&b, "\nImplementors (%d):\n", len(implementors))
			b.WriteString("| Type | Name | File | Package |\n")
			b.WriteString("|---|---|---|---|\n")
			for _, impl := range implementors {
				fmt.Fprintf(&b, "| %s | %s | %s | %s |\n",
					impl.Type, impl.Name, impl.FilePath, impl.Package)
			}
		}
		b.WriteString("\n")
	}

	return b.String(), true
}

// --- query_node_edges ---

type queryNodeEdgesTool struct {
	store graph.Store
}

func (t *queryNodeEdgesTool) Name() string { return "query_node_edges" }

func (t *queryNodeEdgesTool) Description() string {
	return "Show all edges (relationships) for a node. Looks up the node by name, then returns connected nodes grouped by edge type and direction."
}

func (t *queryNodeEdgesTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"node": map[string]any{
				"type":        "string",
				"description": "The node name to look up (e.g., 'HandleRequest', 'Store', 'main.go').",
			},
			"edge_type": map[string]any{
				"type":        "string",
				"description": "Optional: filter by edge type (e.g., 'Calls', 'Imports', 'Implements', 'Contains', 'DependsOn', 'Tests').",
			},
			"direction": map[string]any{
				"type":        "string",
				"description": "Optional: filter by direction ('in', 'out', or 'both'). Default is 'both'.",
			},
		},
		"required": []string{"node"},
	}
}

func (t *queryNodeEdgesTool) Execute(ctx context.Context, args map[string]any) (string, bool) {
	nodeName, _ := args["node"].(string)
	if nodeName == "" {
		return "Error: node is required", false
	}

	edgeTypeFilter, _ := args["edge_type"].(string)
	dirFilter, _ := args["direction"].(string)
	if dirFilter == "" {
		dirFilter = "both"
	}

	// Find the node by name pattern.
	nodes, err := t.store.QueryNodes(ctx, graph.NodeFilter{NamePattern: nodeName})
	if err != nil {
		return fmt.Sprintf("Error querying nodes: %v", err), false
	}
	if len(nodes) == 0 {
		return fmt.Sprintf("No node found matching %q.", nodeName), false
	}

	// Use the first match.
	node := nodes[0]

	var b strings.Builder
	fmt.Fprintf(&b, "## Edges for: %s (%s)\n", node.Name, node.Type)
	if node.FilePath != "" {
		fmt.Fprintf(&b, "File: %s\n", node.FilePath)
	}

	// Get edges for this node.
	edges, err := t.store.GetEdges(ctx, node.ID, graph.EdgeType(edgeTypeFilter))
	if err != nil {
		return fmt.Sprintf("Error getting edges: %v", err), false
	}

	if len(edges) == 0 {
		b.WriteString("\nNo edges found.\n")
		return b.String(), true
	}

	// Group edges by type and direction.
	type edgeEntry struct {
		edgeType  graph.EdgeType
		direction string // "outgoing" or "incoming"
		peerName  string
		peerType  graph.NodeType
		peerFile  string
	}

	var entries []edgeEntry
	for _, e := range edges {
		isOutgoing := e.SourceID == node.ID
		dir := "outgoing"
		peerID := e.TargetID
		if !isOutgoing {
			dir = "incoming"
			peerID = e.SourceID
		}

		// Apply direction filter.
		if dirFilter == "in" && dir != "incoming" {
			continue
		}
		if dirFilter == "out" && dir != "outgoing" {
			continue
		}

		peer, err := t.store.GetNode(ctx, peerID)
		if err != nil {
			continue
		}

		entries = append(entries, edgeEntry{
			edgeType:  e.Type,
			direction: dir,
			peerName:  peer.Name,
			peerType:  peer.Type,
			peerFile:  peer.FilePath,
		})
	}

	if len(entries) == 0 {
		b.WriteString("\nNo edges match the given filters.\n")
		return b.String(), true
	}

	// Sort by edge type, then direction, then peer name.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].edgeType != entries[j].edgeType {
			return entries[i].edgeType < entries[j].edgeType
		}
		if entries[i].direction != entries[j].direction {
			return entries[i].direction < entries[j].direction
		}
		return entries[i].peerName < entries[j].peerName
	})

	fmt.Fprintf(&b, "\nEdges (%d):\n\n", len(entries))
	b.WriteString("| Edge Type | Direction | Peer Name | Peer Type | Peer File |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
			e.edgeType, e.direction, e.peerName, e.peerType, e.peerFile)
	}

	return b.String(), true
}
