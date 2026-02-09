package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
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

func newWatchCmd() *cobra.Command {
	var dbPath string

	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Start watching and building/updating the knowledge graph",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if err := cfg.Validate(); err != nil {
				return fmt.Errorf("invalid config: %w", err)
			}

			// Open graph store.
			store, err := embedded.NewStore(dbPath)
			if err != nil {
				return fmt.Errorf("open graph store: %w", err)
			}
			defer store.Close()

			// Build parser registry.
			registry := parser.NewRegistry()
			registry.Register(golang.NewParser())
			registry.Register(python.NewParser())
			registry.Register(typescript.NewParser())
			registry.Register(javascript.NewParser())
			registry.Register(java.NewParser())
			registry.Register(htmlparser.NewParser())
			registry.Register(markdown.NewParser())

			// Build watcher config from project config.
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
			})

			// Set up signal handling.
			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigCh
				fmt.Fprintln(cmd.OutOrStdout(), "\nShutting down...")
				cancel()
			}()

			fmt.Fprintf(cmd.OutOrStdout(), "Watching %d repositories...\n", len(cfg.Repositories))
			for _, repo := range cfg.Repositories {
				fmt.Fprintf(cmd.OutOrStdout(), "  %s (%s)\n", repo.Path, repo.Type)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Graph database: %s\n", dbPath)

			if err := idx.Start(ctx); err != nil {
				return fmt.Errorf("indexer: %w", err)
			}

			// Print final stats.
			stats := idx.Stats()
			fmt.Fprintf(cmd.OutOrStdout(), "\nFinal stats:\n")
			fmt.Fprintf(cmd.OutOrStdout(), "  Files indexed: %d\n", stats.FilesIndexed)
			fmt.Fprintf(cmd.OutOrStdout(), "  Nodes:         %d\n", stats.NodesTotal)
			fmt.Fprintf(cmd.OutOrStdout(), "  Edges:         %d\n", stats.EdgesTotal)
			if len(stats.Errors) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "  Errors:        %d\n", len(stats.Errors))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&dbPath, "db-path", ".codeeagle/graph.db", "path for the graph database")

	return cmd
}
