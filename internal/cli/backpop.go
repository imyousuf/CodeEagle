package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

func newBackpopPathsCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "backpop-paths",
		Short: "Migrate absolute file paths to relative paths in the graph DB",
		Long: `Backpop-paths scans all nodes and edges in the graph database and converts
absolute file paths to paths relative to the configured repository roots.

Node IDs are recomputed (since they are derived from file paths) and all
edge references are remapped accordingly. Secondary indexes are rebuilt.

Use --dry-run to preview the migration without writing changes.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			resolvedDBPath := cfg.ResolveDBPath(dbPath)
			if resolvedDBPath == "" {
				return fmt.Errorf("no graph database path; run 'codeeagle init' or use --db-path")
			}

			// Collect repo roots.
			var repoRoots []string
			for _, repo := range cfg.Repositories {
				repoRoots = append(repoRoots, repo.Path)
			}
			if len(repoRoots) == 0 {
				return fmt.Errorf("no repositories configured; nothing to migrate")
			}

			// Open store directly (use "default" branch for write — migration
			// operates on all branches via the raw DB).
			store, err := embedded.NewBranchStore(resolvedDBPath, "default", []string{"default"})
			if err != nil {
				return fmt.Errorf("open graph store: %w", err)
			}
			defer store.Close()

			out := cmd.OutOrStdout()

			if dryRun {
				fmt.Fprintln(out, "DRY RUN: no changes will be written")
			}

			fmt.Fprintf(out, "Migrating paths relative to: %v\n", repoRoots)

			result, err := store.MigrateAbsToRelPaths(context.Background(), repoRoots, dryRun)
			if err != nil {
				return fmt.Errorf("migration failed: %w", err)
			}

			fmt.Fprintf(out, "\nMigration %s:\n", statusLabel(dryRun))
			fmt.Fprintf(out, "  Branches found:  %d (%v)\n", len(result.BranchesFound), result.BranchesFound)
			fmt.Fprintf(out, "  Nodes scanned:   %d\n", result.NodesScanned)
			fmt.Fprintf(out, "  Nodes migrated:  %d\n", result.NodesMigrated)
			fmt.Fprintf(out, "  Edges scanned:   %d\n", result.EdgesScanned)
			fmt.Fprintf(out, "  Edges remapped:  %d\n", result.EdgesRemapped)

			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without writing")

	return cmd
}

func statusLabel(dryRun bool) string {
	if dryRun {
		return "preview"
	}
	return "complete"
}
