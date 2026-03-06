//go:build app

package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/imyousuf/CodeEagle/internal/app"
	"github.com/imyousuf/CodeEagle/internal/app/frontend"
	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/pkg/llm"

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
		Long: `Launch the CodeEagle desktop application with Search, Ask, and Settings.

The app provides a graphical interface for:
  - Search: RAG-powered semantic search over the knowledge graph
  - Ask: Conversational interface to AI agents (planner, designer, reviewer, asker)
  - Settings: Visual configuration editor with auto-detection and connection testing

The app launches immediately. Features that require a synced graph or vector
index will gracefully show their status and guide you to set them up.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if cfg.ConfigDir == "" {
				return fmt.Errorf("no config directory found; run 'codeeagle init' first")
			}

			// Build repo paths.
			var repoPaths []string
			for _, repo := range cfg.Repositories {
				repoPaths = append(repoPaths, repo.Path)
			}

			// Create the app with injected factories.
			application := app.NewApp(cfg, repoPaths, "",
				func(c *config.Config) (llm.Client, error) {
					return createLLMClient(c)
				},
				func(ctx context.Context, c *config.Config, rp []string, full bool, logFn func(string, ...any), warnFn func(string, ...any)) error {
					return RunSync(ctx, c, rp, full, true, logFn, warnFn)
				},
			)

			// Run Wails app.
			if err := wails.Run(&options.App{
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
			}); err != nil {
				return fmt.Errorf("wails app: %w", err)
			}

			return nil
		},
	}

	return cmd
}
