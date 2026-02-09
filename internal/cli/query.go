package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph"
)

func newQueryCmd() *cobra.Command {
	var (
		nodeType    string
		namePattern string
		pkg         string
		filePath    string
		language    string
	)

	cmd := &cobra.Command{
		Use:   "query",
		Short: "Query the knowledge graph directly",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			store, _, err := openBranchStore(cfg)
			if err != nil {
				return err
			}
			defer store.Close()

			filter := graph.NodeFilter{
				Type:        graph.NodeType(nodeType),
				NamePattern: namePattern,
				Package:     pkg,
				FilePath:    filePath,
				Language:    language,
			}

			nodes, err := store.QueryNodes(context.Background(), filter)
			if err != nil {
				return fmt.Errorf("query nodes: %w", err)
			}

			out := cmd.OutOrStdout()
			if len(nodes) == 0 {
				fmt.Fprintln(out, "No results found.")
				return nil
			}

			fmt.Fprintf(out, "%-24s  %-14s  %-30s  %s\n", "ID", "Type", "Name", "Location")
			fmt.Fprintf(out, "%-24s  %-14s  %-30s  %s\n", "------------------------", "--------------", "------------------------------", "--------")
			for _, n := range nodes {
				loc := ""
				if n.FilePath != "" {
					loc = n.FilePath
					if n.Line > 0 {
						loc = fmt.Sprintf("%s:%d", n.FilePath, n.Line)
					}
				}
				fmt.Fprintf(out, "%-24s  %-14s  %-30s  %s\n", n.ID, n.Type, n.Name, loc)
			}
			fmt.Fprintf(out, "\n%d result(s)\n", len(nodes))

			return nil
		},
	}

	cmd.Flags().StringVar(&nodeType, "type", "", "filter by node type (e.g. Function, Struct, Interface)")
	cmd.Flags().StringVar(&namePattern, "name", "", "filter by name pattern (glob)")
	cmd.Flags().StringVar(&pkg, "package", "", "filter by package name")
	cmd.Flags().StringVar(&filePath, "file", "", "filter by file path")
	cmd.Flags().StringVar(&language, "language", "", "filter by language")

	cmd.AddCommand(newQuerySymbolsCmd())
	cmd.AddCommand(newQueryInterfaceCmd())
	cmd.AddCommand(newQueryEdgesCmd())

	return cmd
}

func newQuerySymbolsCmd() *cobra.Command {
	var (
		filePath string
		jsonOut  bool
	)

	cmd := &cobra.Command{
		Use:   "symbols",
		Short: "List all symbols in a file with signatures and line numbers",
		RunE: func(cmd *cobra.Command, args []string) error {
			if filePath == "" {
				return fmt.Errorf("--file is required")
			}

			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			store, _, err := openBranchStore(cfg)
			if err != nil {
				return err
			}
			defer store.Close()

			ctx := context.Background()
			nodes, err := store.QueryNodes(ctx, graph.NodeFilter{FilePath: filePath})
			if err != nil {
				return fmt.Errorf("query nodes: %w", err)
			}

			// Sort by line number.
			sort.Slice(nodes, func(i, j int) bool {
				return nodes[i].Line < nodes[j].Line
			})

			// Determine file metadata from the first non-File node.
			var lang, pkg string
			for _, n := range nodes {
				if n.Type == graph.NodeFile {
					continue
				}
				if lang == "" && n.Language != "" {
					lang = n.Language
				}
				if pkg == "" && n.Package != "" {
					pkg = n.Package
				}
				if lang != "" && pkg != "" {
					break
				}
			}

			// Filter out File nodes for display.
			symbols := make([]*graph.Node, 0, len(nodes))
			for _, n := range nodes {
				if n.Type != graph.NodeFile {
					symbols = append(symbols, n)
				}
			}

			out := cmd.OutOrStdout()

			if jsonOut {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(symbols)
			}

			if len(symbols) == 0 {
				fmt.Fprintf(out, "No symbols found in %s\n", filePath)
				return nil
			}

			// Header.
			header := fmt.Sprintf("Symbols in %s", filePath)
			if lang != "" || pkg != "" {
				parts := []string{}
				if lang != "" {
					parts = append(parts, lang)
				}
				if pkg != "" {
					parts = append(parts, "package: "+pkg)
				}
				header += " (" + strings.Join(parts, ", ") + ")"
			}
			fmt.Fprintln(out, header)
			fmt.Fprintln(out)

			for _, n := range symbols {
				// Type column.
				typeStr := fmt.Sprintf("%-12s", n.Type)

				// Name + signature.
				nameStr := n.Name
				if n.Signature != "" {
					nameStr = n.Signature
				}

				// Line range.
				lineStr := ""
				if n.Line > 0 {
					if n.EndLine > 0 && n.EndLine != n.Line {
						lineStr = fmt.Sprintf("line %d-%d", n.Line, n.EndLine)
					} else {
						lineStr = fmt.Sprintf("line %d", n.Line)
					}
				}

				// Exported status.
				exportStr := ""
				if n.Exported {
					exportStr = "exported"
				}

				fmt.Fprintf(out, "  %s  %-60s  %-14s  %s\n", typeStr, nameStr, lineStr, exportStr)
			}

			fmt.Fprintf(out, "\n%d symbols\n", len(symbols))
			return nil
		},
	}

	cmd.Flags().StringVar(&filePath, "file", "", "file path to list symbols for (required)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "output as JSON")

	return cmd
}

// interfaceResult holds the structured output for the interface subcommand.
type interfaceResult struct {
	Name      string                `json:"name"`
	FilePath  string                `json:"file_path"`
	Line      int                   `json:"line"`
	Package   string                `json:"package"`
	Signature string                `json:"signature,omitempty"`
	Implementors []interfaceImplEntry `json:"implementors"`
}

// interfaceImplEntry holds info about a type that implements an interface.
type interfaceImplEntry struct {
	Type     graph.NodeType `json:"type"`
	Name     string         `json:"name"`
	FilePath string         `json:"file_path"`
	Line     int            `json:"line"`
	Package  string         `json:"package"`
}

func newQueryInterfaceCmd() *cobra.Command {
	var (
		namePattern string
		jsonOut     bool
	)

	cmd := &cobra.Command{
		Use:   "interface",
		Short: "Show interface definitions and their implementors",
		RunE: func(cmd *cobra.Command, args []string) error {
			if namePattern == "" {
				return fmt.Errorf("--name is required")
			}

			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			store, _, err := openBranchStore(cfg)
			if err != nil {
				return err
			}
			defer store.Close()

			ctx := context.Background()
			interfaces, err := store.QueryNodes(ctx, graph.NodeFilter{
				Type:        graph.NodeInterface,
				NamePattern: namePattern,
			})
			if err != nil {
				return fmt.Errorf("query interfaces: %w", err)
			}

			out := cmd.OutOrStdout()

			if len(interfaces) == 0 {
				fmt.Fprintf(out, "No interfaces matching %q found.\n", namePattern)
				return nil
			}

			var results []interfaceResult
			for _, iface := range interfaces {
				result := interfaceResult{
					Name:      iface.Name,
					FilePath:  iface.FilePath,
					Line:      iface.Line,
					Package:   iface.Package,
					Signature: iface.Signature,
				}

				// Find implementors: nodes that have EdgeImplements pointing TO this interface.
				implementors, err := store.GetNeighbors(ctx, iface.ID, graph.EdgeImplements, graph.Incoming)
				if err != nil {
					return fmt.Errorf("get implementors for %s: %w", iface.Name, err)
				}

				for _, impl := range implementors {
					result.Implementors = append(result.Implementors, interfaceImplEntry{
						Type:     impl.Type,
						Name:     impl.Name,
						FilePath: impl.FilePath,
						Line:     impl.Line,
						Package:  impl.Package,
					})
				}

				results = append(results, result)
			}

			if jsonOut {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(results)
			}

			for i, r := range results {
				if i > 0 {
					fmt.Fprintln(out)
				}
				loc := r.FilePath
				if r.Line > 0 {
					loc = fmt.Sprintf("%s:%d", r.FilePath, r.Line)
				}
				fmt.Fprintf(out, "Interface: %s (%s, package: %s)\n", r.Name, loc, r.Package)
				if r.Signature != "" {
					fmt.Fprintf(out, "  Signature: %s\n", r.Signature)
				}

				if len(r.Implementors) == 0 {
					fmt.Fprintln(out, "\n  No implementors found.")
				} else {
					fmt.Fprintln(out, "\n  Implemented by:")
					for _, impl := range r.Implementors {
						implLoc := impl.FilePath
						if impl.Line > 0 {
							implLoc = fmt.Sprintf("%s:%d", impl.FilePath, impl.Line)
						}
						fmt.Fprintf(out, "    %-10s %-30s %s  (package: %s)\n",
							impl.Type, impl.Name, implLoc, impl.Package)
					}
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&namePattern, "name", "", "interface name or glob pattern (required)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "output as JSON")

	return cmd
}

// edgeEntry holds a resolved edge for display.
type edgeEntry struct {
	EdgeType   graph.EdgeType `json:"edge_type"`
	NodeID     string         `json:"node_id"`
	NodeType   graph.NodeType `json:"node_type"`
	NodeName   string         `json:"node_name"`
	FilePath   string         `json:"file_path,omitempty"`
	Line       int            `json:"line,omitempty"`
	Package    string         `json:"package,omitempty"`
	Properties map[string]string `json:"properties,omitempty"`
}

// edgesResult holds the structured output for the edges subcommand.
type edgesResult struct {
	Node     *graph.Node `json:"node"`
	Outgoing []edgeEntry `json:"outgoing"`
	Incoming []edgeEntry `json:"incoming"`
}

func newQueryEdgesCmd() *cobra.Command {
	var (
		nodeArg   string
		edgeType  string
		direction string
		jsonOut   bool
	)

	cmd := &cobra.Command{
		Use:   "edges",
		Short: "Show all edges (relationships) for a node",
		RunE: func(cmd *cobra.Command, args []string) error {
			if nodeArg == "" {
				return fmt.Errorf("--node is required")
			}

			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			store, _, err := openBranchStore(cfg)
			if err != nil {
				return err
			}
			defer store.Close()

			ctx := context.Background()

			// Try to find the node by ID first, then by name.
			node, err := store.GetNode(ctx, nodeArg)
			if err != nil || node == nil {
				// Try name search.
				candidates, qErr := store.QueryNodes(ctx, graph.NodeFilter{NamePattern: nodeArg})
				if qErr != nil {
					return fmt.Errorf("query nodes: %w", qErr)
				}
				if len(candidates) == 0 {
					return fmt.Errorf("no node found matching %q", nodeArg)
				}
				node = candidates[0]
			}

			// Fetch all edges for this node.
			edges, err := store.GetEdges(ctx, node.ID, graph.EdgeType(edgeType))
			if err != nil {
				return fmt.Errorf("get edges: %w", err)
			}

			// Resolve other nodes and split into outgoing/incoming.
			var outgoing, incoming []edgeEntry
			for _, e := range edges {
				var otherID string
				var isOutgoing bool
				if e.SourceID == node.ID {
					otherID = e.TargetID
					isOutgoing = true
				} else {
					otherID = e.SourceID
					isOutgoing = false
				}

				other, _ := store.GetNode(ctx, otherID)
				entry := edgeEntry{
					EdgeType:   e.Type,
					NodeID:     otherID,
					Properties: e.Properties,
				}
				if other != nil {
					entry.NodeType = other.Type
					entry.NodeName = other.Name
					entry.FilePath = other.FilePath
					entry.Line = other.Line
					entry.Package = other.Package
				} else {
					entry.NodeName = otherID
				}

				if isOutgoing {
					outgoing = append(outgoing, entry)
				} else {
					incoming = append(incoming, entry)
				}
			}

			// Apply direction filter.
			showOutgoing := direction == "" || direction == "both" || direction == "out"
			showIncoming := direction == "" || direction == "both" || direction == "in"

			if !showOutgoing {
				outgoing = nil
			}
			if !showIncoming {
				incoming = nil
			}

			out := cmd.OutOrStdout()

			if jsonOut {
				result := edgesResult{
					Node:     node,
					Outgoing: outgoing,
					Incoming: incoming,
				}
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			}

			// Text output.
			loc := node.FilePath
			if node.Line > 0 {
				loc = fmt.Sprintf("%s:%d", node.FilePath, node.Line)
			}
			fmt.Fprintf(out, "Edges for: %s (%s, %s)\n", node.Name, node.Type, loc)

			if showOutgoing {
				fmt.Fprintln(out, "\n  Outgoing:")
				if len(outgoing) == 0 {
					fmt.Fprintln(out, "    (none)")
				}
				for _, e := range outgoing {
					detail := formatEdgeNodeDetail(e)
					fmt.Fprintf(out, "    %-14s -> %s\n", e.EdgeType, detail)
				}
			}

			if showIncoming {
				fmt.Fprintln(out, "\n  Incoming:")
				if len(incoming) == 0 {
					fmt.Fprintln(out, "    (none)")
				}
				for _, e := range incoming {
					detail := formatEdgeNodeDetail(e)
					fmt.Fprintf(out, "    %-14s <- %s\n", e.EdgeType, detail)
				}
			}

			fmt.Fprintf(out, "\n%d outgoing, %d incoming\n", len(outgoing), len(incoming))
			return nil
		},
	}

	cmd.Flags().StringVar(&nodeArg, "node", "", "node name or ID (required)")
	cmd.Flags().StringVar(&edgeType, "type", "", "filter by edge type (e.g. Calls, Implements)")
	cmd.Flags().StringVar(&direction, "direction", "both", "edge direction: in, out, or both")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "output as JSON")

	return cmd
}

// formatEdgeNodeDetail formats a resolved edge entry for text display.
func formatEdgeNodeDetail(e edgeEntry) string {
	parts := []string{e.NodeName}
	if e.NodeType != "" {
		parts = append(parts, fmt.Sprintf("(%s", e.NodeType))
		if e.FilePath != "" {
			loc := e.FilePath
			if e.Line > 0 {
				loc = fmt.Sprintf("%s:%d", e.FilePath, e.Line)
			}
			parts[len(parts)-1] += ", " + loc + ")"
		} else {
			parts[len(parts)-1] += ")"
		}
	}
	return strings.Join(parts, " ")
}
