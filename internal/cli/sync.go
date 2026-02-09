package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/indexer"
	"github.com/imyousuf/CodeEagle/pkg/llm"

	// Register LLM providers so their init() functions run.
	_ "github.com/imyousuf/CodeEagle/internal/llm"
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
	var branch string

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync the knowledge graph with file changes",
		Long: `Perform an on-demand sync of the knowledge graph.

By default, syncs incrementally using git diffs (or file modification times
for non-git directories). Use --full for a complete re-index.

Use --export to export the current branch's graph to a portable file, and
--import to import a previously exported graph. Use --branch to specify the
target branch for import.`,
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
				return handleExportImport(cfg, exportGraph, branch, cmd.OutOrStdout())
			}

			// Normal sync.
			store, currentBranch, err := openBranchStore(cfg)
			if err != nil {
				return err
			}
			defer store.Close()

			logFn := func(format string, args ...any) {
				fmt.Fprintf(out, format+"\n", args...)
			}

			// Auto-import if .CodeEagle.conf is available.
			if cfg.ProjectConf != nil && cfg.ProjectConfDir != "" {
				exportFilePath := config.ExportFilePath(cfg.ProjectConfDir, cfg.ProjectConf)
				statePath := cfg.ConfigDir + "/" + "sync.state"
				state, err := indexer.LoadSyncState(statePath)
				if err == nil {
					if err := indexer.AutoImportIfNeeded(ctx(cmd), store, exportFilePath, state, logFn); err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "Warning: auto-import failed: %v\n", err)
					} else {
						// Save state if auto-import updated LastImportTime.
						_ = state.Save(statePath)
					}
				}
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

			// Create LLM client if auto-summarize is enabled.
			var llmClient llm.Client
			if cfg.Agents.AutoSummarize {
				c, err := createLLMClient(cfg)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: auto-summarize enabled but LLM client creation failed: %v\n", err)
				} else {
					llmClient = c
					defer llmClient.Close()
				}
			}

			// Create indexer.
			idx := indexer.NewIndexer(indexer.IndexerConfig{
				GraphStore:     store,
				ParserRegistry: registry,
				WatcherConfig:  wcfg,
				RepoRoots:      paths,
				Verbose:        verbose,
				Logger:         logFn,
				LLMClient:      llmClient,
				AutoSummarize:  cfg.Agents.AutoSummarize,
			})

			mode := "incremental"
			if full {
				mode = "full"
			}
			fmt.Fprintf(out, "Syncing (%s) on branch %q...\n", mode, currentBranch)

			if err := indexer.SyncFiles(ctx(cmd), idx, paths, cfg.ConfigDir, full, currentBranch); err != nil {
				return fmt.Errorf("sync: %w", err)
			}

			// Run LLM summarization if enabled.
			idx.RunSummarization(ctx(cmd))

			// Cleanup stale branches.
			if len(cfg.Repositories) > 0 {
				statePath := cfg.ConfigDir + "/" + "sync.state"
				state, err := indexer.LoadSyncState(statePath)
				if err == nil {
					if err := indexer.CleanupStaleBranches(ctx(cmd), store, cfg.Repositories[0].Path, state, logFn); err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "Warning: branch cleanup failed: %v\n", err)
					}
					_ = state.Save(statePath)
				}
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
	cmd.Flags().BoolVar(&exportGraph, "export", false, "export current branch graph to a file")
	cmd.Flags().BoolVar(&importGraph, "import", false, "import a graph export file")
	cmd.Flags().StringVar(&branch, "branch", "", "target branch for import (auto-detected if empty)")

	return cmd
}

// ctx returns the command's context or a background context.
func ctx(cmd *cobra.Command) context.Context {
	if c := cmd.Context(); c != nil {
		return c
	}
	return context.Background()
}

func handleExportImport(cfg *config.Config, isExport bool, targetBranch string, out io.Writer) error {
	if cfg.ConfigDir == "" {
		return fmt.Errorf("no config directory found; run 'codeeagle init' first")
	}

	// Determine export path: from .CodeEagle.conf if available, else default.
	exportPath := cfg.ConfigDir + "/graph.export"
	if cfg.ProjectConf != nil && cfg.ProjectConfDir != "" {
		exportPath = config.ExportFilePath(cfg.ProjectConfDir, cfg.ProjectConf)
	}

	store, currentBranch, err := openBranchStore(cfg)
	if err != nil {
		return err
	}
	defer store.Close()

	ctx := context.Background()

	if isExport {
		f, err := os.Create(exportPath)
		if err != nil {
			return fmt.Errorf("create export file: %w", err)
		}
		defer f.Close()

		if err := store.ExportBranch(ctx, f, currentBranch); err != nil {
			return fmt.Errorf("export: %w", err)
		}

		fmt.Fprintf(out, "Exported branch %q to %s\n", currentBranch, exportPath)
	} else {
		f, err := os.Open(exportPath)
		if err != nil {
			return fmt.Errorf("open export file: %w", err)
		}

		// If no target branch specified, read it from the export.
		if targetBranch == "" {
			exportBranch, err := embedded.ReadExportBranch(f)
			f.Close()
			if err != nil {
				return fmt.Errorf("read export branch: %w", err)
			}
			if exportBranch == "" {
				targetBranch = "main" // legacy export
			} else {
				targetBranch = exportBranch
			}

			// Re-open for actual import.
			f, err = os.Open(exportPath)
			if err != nil {
				return fmt.Errorf("re-open export file: %w", err)
			}
		}
		defer f.Close()

		sourceBranch, err := store.ImportIntoBranch(ctx, f, targetBranch)
		if err != nil {
			return fmt.Errorf("import: %w", err)
		}

		stats, _ := store.Stats(ctx)
		if sourceBranch != "" {
			fmt.Fprintf(out, "Imported graph (source: %q) into branch %q: %d nodes, %d edges\n",
				sourceBranch, targetBranch, stats.NodeCount, stats.EdgeCount)
		} else {
			fmt.Fprintf(out, "Imported graph into branch %q: %d nodes, %d edges\n",
				targetBranch, stats.NodeCount, stats.EdgeCount)
		}
	}

	return nil
}
