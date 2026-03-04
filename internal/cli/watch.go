package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/docs"
	"github.com/imyousuf/CodeEagle/internal/gitutil"
	"github.com/imyousuf/CodeEagle/internal/indexer"
	"github.com/imyousuf/CodeEagle/internal/linker"
	"github.com/imyousuf/CodeEagle/internal/parser"
	csharpparser "github.com/imyousuf/CodeEagle/internal/parser/csharp"
	genericparser "github.com/imyousuf/CodeEagle/internal/parser/generic"
	"github.com/imyousuf/CodeEagle/internal/parser/golang"
	htmlparser "github.com/imyousuf/CodeEagle/internal/parser/html"
	"github.com/imyousuf/CodeEagle/internal/parser/java"
	"github.com/imyousuf/CodeEagle/internal/parser/javascript"
	makefileparser "github.com/imyousuf/CodeEagle/internal/parser/makefile"
	"github.com/imyousuf/CodeEagle/internal/parser/manifest"
	"github.com/imyousuf/CodeEagle/internal/parser/markdown"
	"github.com/imyousuf/CodeEagle/internal/parser/python"
	rubyparser "github.com/imyousuf/CodeEagle/internal/parser/ruby"
	rustparser "github.com/imyousuf/CodeEagle/internal/parser/rust"
	"github.com/imyousuf/CodeEagle/internal/parser/shell"
	"github.com/imyousuf/CodeEagle/internal/parser/terraform"
	"github.com/imyousuf/CodeEagle/internal/parser/typescript"
	yamlparser "github.com/imyousuf/CodeEagle/internal/parser/yaml"
	"github.com/imyousuf/CodeEagle/internal/watcher"
	"github.com/imyousuf/CodeEagle/pkg/llm"

	// Register LLM and embedding providers so their init() functions run.
	_ "github.com/imyousuf/CodeEagle/internal/embedding"
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
			output := cmd.OutOrStdout()
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
			registry.Register(makefileparser.NewParser())
			registry.Register(shell.NewParser())
			registry.Register(terraform.NewParser())
			registry.Register(yamlparser.NewParser())
			registry.Register(rustparser.NewParser())
			registry.Register(rubyparser.NewParser())
			registry.Register(manifest.NewParser())
			registry.Register(csharpparser.NewParser())

			// Detect docs LLM provider for topic extraction.
			var docsProvider docs.Provider
			var docsCache *docs.Cache
			dp, dpErr := docs.DetectProvider(cfg)
			if dpErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: docs provider: %v\n", dpErr)
			}
			if dp != nil {
				docsProvider = dp
				logFn("[docs] Using %s (%s)", dp.Name(), dp.ModelName())
				cachePath := cfg.ConfigDir + "/docs.db"
				dc, dcErr := docs.OpenCache(cachePath)
				if dcErr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: docs cache: %v\n", dcErr)
				} else {
					docsCache = dc
					defer docsCache.Close()
				}
			}

			// Register generic fallback parser for non-code files.
			registry.SetFallback(genericparser.NewGenericParser(cfg.Docs.ExcludeExtensions, docsProvider, docsCache, cfg.Docs.MaxImageRes))
			registry.SetExcludeExtensions(cfg.Docs.ExcludeExtensions)

			// Build watcher config from project config.
			var paths []string
			for _, repo := range cfg.Repositories {
				paths = append(paths, repo.Path)
			}
			wcfg := &watcher.WatcherConfig{
				Paths:           paths,
				ExcludePatterns: cfg.Watch.Exclude,
			}

			// Create LLM client if auto-summarize or auto-link is enabled.
			var llmClient llm.Client
			if cfg.Agents.AutoSummarize || cfg.Agents.AutoLink {
				c, err := createLLMClient(cfg)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: LLM client creation failed: %v\n", err)
				} else {
					llmClient = c
					defer llmClient.Close()
				}
			}

			// Build linker as post-index hook.
			var linkerLLM llm.Client
			if cfg.Agents.AutoLink {
				linkerLLM = llmClient
			}
			lnk := linker.NewLinker(store, linkerLLM, logFn, verbose)

			// Open vector store if embedding provider is available.
			vs, vecErr := openVectorStore(cfg, store, currentBranch, logFn)
			if vecErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: vector store: %v\n", vecErr)
			}
			if vs != nil {
				defer vs.Close()
				// Load existing index (or build from scratch on first run).
				if err := syncVectorIndex(vs, cfg, false, logFn); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: initial vector index: %v\n", err)
				}
			}

			// Build post-index hook: linker + vector update.
			postIndexHook := func(hookCtx context.Context) error {
				if err := lnk.RunAll(hookCtx); err != nil {
					return err
				}
				if vs != nil && vs.Available() {
					// Save updated vectors after each index round.
					if err := vs.Save(); err != nil {
						logFn("Warning: save vector index: %v", err)
					}
				}
				return nil
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
				PostIndexHook:  postIndexHook,
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
