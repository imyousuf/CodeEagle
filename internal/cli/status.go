package cli

import (
	"context"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

func newStatusCmd() *cobra.Command {
	var dbPath string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show indexing status and graph stats",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := embedded.NewStore(dbPath)
			if err != nil {
				return fmt.Errorf("open graph store: %w", err)
			}
			defer store.Close()

			stats, err := store.Stats(context.Background())
			if err != nil {
				return fmt.Errorf("get stats: %w", err)
			}

			out := cmd.OutOrStdout()

			fmt.Fprintf(out, "Knowledge Graph Status\n")
			fmt.Fprintf(out, "======================\n\n")
			fmt.Fprintf(out, "  Total nodes: %d\n", stats.NodeCount)
			fmt.Fprintf(out, "  Total edges: %d\n\n", stats.EdgeCount)

			if len(stats.NodesByType) > 0 {
				fmt.Fprintf(out, "  Nodes by type:\n")
				nodeTypes := sortedNodeTypes(stats.NodesByType)
				for _, nt := range nodeTypes {
					fmt.Fprintf(out, "    %-20s %d\n", nt, stats.NodesByType[nt])
				}
				fmt.Fprintln(out)
			}

			if len(stats.EdgesByType) > 0 {
				fmt.Fprintf(out, "  Edges by type:\n")
				edgeTypes := sortedEdgeTypes(stats.EdgesByType)
				for _, et := range edgeTypes {
					fmt.Fprintf(out, "    %-20s %d\n", et, stats.EdgesByType[et])
				}
				fmt.Fprintln(out)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&dbPath, "db-path", ".codeeagle/graph.db", "path for the graph database")

	return cmd
}

func sortedNodeTypes(m map[graph.NodeType]int64) []graph.NodeType {
	keys := make([]graph.NodeType, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func sortedEdgeTypes(m map[graph.EdgeType]int64) []graph.EdgeType {
	keys := make([]graph.EdgeType, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
