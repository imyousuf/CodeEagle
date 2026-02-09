package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/gitutil"
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
	"github.com/imyousuf/CodeEagle/pkg/llm"

	// Register LLM providers so their init() functions run.
	_ "github.com/imyousuf/CodeEagle/internal/llm"
)

func newWatchCmd() *cobra.Command {
	var pidFile string
	var logFile string

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

			// Set up log file output if requested.
			var output io.Writer = cmd.OutOrStdout()
			if logFile != "" {
				f, err := os.Create(logFile)
				if err != nil {
					return fmt.Errorf("create log file: %w", err)
				}
				defer f.Close()
				output = f
				cmd.SetOut(f)
				cmd.SetErr(f)
			}

			// Write PID file if requested.
			if pidFile != "" {
				if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0644); err != nil {
					return fmt.Errorf("write pid file: %w", err)
				}
				defer os.Remove(pidFile)
			}

			// Build a logger function that writes to the chosen output.
			logFn := func(format string, args ...any) {
				fmt.Fprintf(output, format+"\n", args...)
			}

			// Open graph store.
			store, currentBranch, err := openBranchStore(cfg)
			if err != nil {
				return err
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
				Verbose:        verbose,
				Logger:         logFn,
				LLMClient:      llmClient,
				AutoSummarize:  cfg.Agents.AutoSummarize,
			})

			// Set up signal handling.
			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigCh
				fmt.Fprintln(output, "\nShutting down...")
				cancel()
			}()

			fmt.Fprintf(output, "Watching %d repositories (branch: %s)...\n", len(cfg.Repositories), currentBranch)
			for _, repo := range cfg.Repositories {
				fmt.Fprintf(output, "  %s (%s)\n", repo.Path, repo.Type)
				if verbose {
					info, err := gitutil.GetBranchInfo(repo.Path)
					if err == nil {
						fmt.Fprintf(output, "    Branch: %s", info.CurrentBranch)
						if info.IsFeatureBranch {
							fmt.Fprintf(output, " (%d ahead, %d behind %s)", info.Ahead, info.Behind, info.DefaultBranch)
						}
						fmt.Fprintln(output)
					}
				}
			}

			if err := idx.Start(ctx); err != nil {
				return fmt.Errorf("indexer: %w", err)
			}

			// Print final stats.
			stats := idx.Stats()
			fmt.Fprintf(output, "\nFinal stats:\n")
			fmt.Fprintf(output, "  Files indexed: %d\n", stats.FilesIndexed)
			fmt.Fprintf(output, "  Nodes:         %d\n", stats.NodesTotal)
			fmt.Fprintf(output, "  Edges:         %d\n", stats.EdgesTotal)
			if len(stats.Errors) > 0 {
				fmt.Fprintf(output, "  Errors:        %d\n", len(stats.Errors))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&pidFile, "pid-file", "", "write process PID to this file")
	cmd.Flags().StringVar(&logFile, "log-file", "", "redirect all output to this file")

	return cmd
}
