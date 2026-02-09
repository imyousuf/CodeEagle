package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/indexer"
	"github.com/imyousuf/CodeEagle/internal/parser"
	"github.com/imyousuf/CodeEagle/internal/parser/golang"
	htmlparser "github.com/imyousuf/CodeEagle/internal/parser/html"
	"github.com/imyousuf/CodeEagle/internal/parser/java"
	"github.com/imyousuf/CodeEagle/internal/parser/javascript"
	"github.com/imyousuf/CodeEagle/internal/parser/markdown"
	"github.com/imyousuf/CodeEagle/internal/parser/python"
	"github.com/imyousuf/CodeEagle/internal/parser/typescript"
	"github.com/imyousuf/CodeEagle/internal/watcher"
)

func newSyncCmd() *cobra.Command {
	var full bool
	var exportGraph bool
	var importGraph bool

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync the knowledge graph with file changes",
		Long: `Perform an on-demand sync of the knowledge graph.

By default, syncs incrementally using git diffs (or file modification times
for non-git directories). Use --full for a complete re-index.

Use --export to export the graph to a portable file, and --import to import
a previously exported graph into the main database.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if err := cfg.Validate(); err != nil {
				return fmt.Errorf("invalid config: %w", err)
			}

			out := cmd.OutOrStdout()

			if exportGraph && importGraph {
				return fmt.Errorf("cannot use --export and --import together")
			}

			// Handle export/import.
			if exportGraph || importGraph {
				return handleExportImport(cfg, exportGraph, cmd.OutOrStdout())
			}

			// Normal sync.
			mainDBPath, localDBPath := cfg.ResolveDBPaths(dbPath)
			if mainDBPath == "" {
				return fmt.Errorf("no graph database path; run 'codeeagle init' or use --db-path")
			}

			logFn := func(format string, args ...any) {
				fmt.Fprintf(out, format+"\n", args...)
			}

			// Open store(s).
			var store graph.Store
			if mainDBPath == localDBPath {
				// Single-DB mode.
				s, err := embedded.NewStore(mainDBPath)
				if err != nil {
					return fmt.Errorf("open graph store: %w", err)
				}
				defer s.Close()
				store = s
			} else {
				mainStore, err := embedded.NewStore(mainDBPath)
				if err != nil {
					return fmt.Errorf("open main graph store: %w", err)
				}
				defer mainStore.Close()

				localStore, err := embedded.NewStore(localDBPath)
				if err != nil {
					return fmt.Errorf("open local graph store: %w", err)
				}
				defer localStore.Close()

				store = graph.NewLayeredStore(mainStore, localStore)
			}

			// Build parser registry.
			registry := parser.NewRegistry()
			registry.Register(golang.NewParser())
			registry.Register(python.NewParser())
			registry.Register(typescript.NewParser())
			registry.Register(javascript.NewParser())
			registry.Register(java.NewParser())
			registry.Register(htmlparser.NewParser())
			registry.Register(markdown.NewParser())

			// Build watcher config for the matcher.
			var paths []string
			for _, repo := range cfg.Repositories {
				paths = append(paths, repo.Path)
			}
			wcfg := &watcher.WatcherConfig{
				Paths:           paths,
				ExcludePatterns: cfg.Watch.Exclude,
			}

			// Create indexer.
			idx := indexer.NewIndexer(indexer.IndexerConfig{
				GraphStore:     store,
				ParserRegistry: registry,
				WatcherConfig:  wcfg,
				Verbose:        verbose,
				Logger:         logFn,
			})

			ctx := context.Background()

			mode := "incremental"
			if full {
				mode = "full"
			}
			fmt.Fprintf(out, "Syncing (%s)...\n", mode)

			if err := indexer.SyncFiles(ctx, idx, paths, cfg.ConfigDir, full); err != nil {
				return fmt.Errorf("sync: %w", err)
			}

			// Print stats.
			stats := idx.Stats()
			fmt.Fprintf(out, "Sync complete: %d files indexed, %d nodes, %d edges\n",
				stats.FilesIndexed, stats.NodesTotal, stats.EdgesTotal)
			if len(stats.Errors) > 0 {
				fmt.Fprintf(out, "  Errors: %d\n", len(stats.Errors))
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&full, "full", false, "full re-index of all files")
	cmd.Flags().BoolVar(&exportGraph, "export", false, "export graph to .CodeEagle/graph.export")
	cmd.Flags().BoolVar(&importGraph, "import", false, "import .CodeEagle/graph.export into main DB")

	return cmd
}

func handleExportImport(cfg *config.Config, isExport bool, out io.Writer) error {
	if cfg.ConfigDir == "" {
		return fmt.Errorf("no config directory found; run 'codeeagle init' first")
	}

	exportPath := cfg.ConfigDir + "/graph.export"
	ctx := context.Background()

	mainDBPath, _ := cfg.ResolveDBPaths(dbPath)
	if mainDBPath == "" {
		return fmt.Errorf("no graph database path; run 'codeeagle init' or use --db-path")
	}

	store, err := embedded.NewStore(mainDBPath)
	if err != nil {
		return fmt.Errorf("open graph store: %w", err)
	}
	defer store.Close()

	if isExport {
		f, err := os.Create(exportPath)
		if err != nil {
			return fmt.Errorf("create export file: %w", err)
		}
		defer f.Close()

		if err := store.Export(ctx, f); err != nil {
			return fmt.Errorf("export: %w", err)
		}

		fmt.Fprintf(out, "Exported graph to %s\n", exportPath)
	} else {
		f, err := os.Open(exportPath)
		if err != nil {
			return fmt.Errorf("open export file: %w", err)
		}
		defer f.Close()

		if err := store.Import(ctx, f); err != nil {
			return fmt.Errorf("import: %w", err)
		}

		stats, _ := store.Stats(ctx)
		fmt.Fprintf(out, "Imported graph from %s: %d nodes, %d edges\n",
			exportPath, stats.NodeCount, stats.EdgeCount)
	}

	return nil
}
