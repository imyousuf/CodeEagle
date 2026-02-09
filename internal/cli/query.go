package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
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

			resolvedDBPath := cfg.ResolveDBPath(dbPath)
			if resolvedDBPath == "" {
				return fmt.Errorf("no graph database path; run 'codeeagle init' or use --db-path")
			}

			store, err := embedded.NewStore(resolvedDBPath)
			if err != nil {
				return fmt.Errorf("open graph store: %w", err)
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

	return cmd
}
