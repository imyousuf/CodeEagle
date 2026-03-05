//go:build app

package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/imyousuf/CodeEagle/internal/agents"
	"github.com/imyousuf/CodeEagle/internal/app"
	"github.com/imyousuf/CodeEagle/internal/app/frontend"
	"github.com/imyousuf/CodeEagle/internal/config"

	// Register embedding providers so their init() functions run.
	_ "github.com/imyousuf/CodeEagle/internal/embedding"
)

func init() {
	registerAppCmd = func(rootCmd *cobra.Command) {
		rootCmd.AddCommand(newAppCmd())
	}
}

func newAppCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "app",
		Short: "Launch the CodeEagle desktop app",
		Long: `Launch the CodeEagle desktop application with Search and Ask features.

The app provides a graphical interface for:
  - Search: RAG-powered semantic search over the knowledge graph
  - Ask: Conversational interface to AI agents (planner, designer, reviewer, asker)

Requires a synced knowledge graph ('codeeagle sync') and optionally a vector
index for search functionality.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if cfg.ConfigDir == "" {
				return fmt.Errorf("no config directory found; run 'codeeagle init' first")
			}

			// Open graph store.
			store, currentBranch, err := openBranchStore(cfg)
			if err != nil {
				return err
			}

			// Open vector store (nil if unavailable).
			logFn := func(format string, a ...any) {
				if verbose {
					fmt.Fprintf(os.Stderr, format+"\n", a...)
				}
			}
			vs, err := openVectorStore(cfg, store, currentBranch, logFn)
			if err != nil {
				logFn("Warning: vector store unavailable: %v", err)
			}
			if vs != nil {
				loaded, loadErr := vs.Load()
				if loadErr != nil || !loaded {
					logFn("Warning: vector index not built; search will be unavailable")
					vs.Close()
					vs = nil
				}
			}

			// Create LLM client (reuses the same logic as the CLI agent command).
			llmClient, llmErr := createLLMClient(cfg)
			if llmErr != nil {
				logFn("Warning: LLM unavailable: %v", llmErr)
			}

			// Build repo paths for context builder.
			var repoPaths []string
			for _, repo := range cfg.Repositories {
				repoPaths = append(repoPaths, repo.Path)
			}

			// Create context builder for agents.
			ctxBuilder := agents.NewContextBuilder(store, repoPaths...)

			// Create the app.
			application := app.NewApp(cfg, store, vs, llmClient, ctxBuilder, repoPaths, currentBranch)

			// Run Wails app.
			err = wails.Run(&options.App{
				Title:  "CodeEagle",
				Width:  1200,
				Height: 800,
				AssetServer: &assetserver.Options{
					Assets: frontend.Assets,
				},
				OnStartup:  application.Startup,
				OnShutdown: application.Shutdown,
				Bind: []interface{}{
					application,
				},
			})
			if err != nil {
				return fmt.Errorf("wails app: %w", err)
			}

			return nil
		},
	}

	return cmd
}

