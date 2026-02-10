package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/agents"
	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/mcp"
)

func newMCPCmd() *cobra.Command {
	mcpCmd := &cobra.Command{
		Use:   "mcp",
		Short: "MCP server commands",
		Long:  `Commands for running the MCP (Model Context Protocol) server.`,
	}

	mcpCmd.AddCommand(newMCPServeCmd())
	return mcpCmd
}

func newMCPServeCmd() *cobra.Command {
	var logFile string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the MCP server over stdio",
		Long: `Start a JSON-RPC 2.0 MCP server over stdin/stdout.

The server exposes CodeEagle knowledge graph tools to MCP clients
(e.g., Claude CLI). It reads requests from stdin and writes responses
to stdout, one JSON object per line.

This command is typically invoked automatically by the Claude CLI via
--mcp-config, not run directly by users.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			store, _, err := openBranchStore(cfg)
			if err != nil {
				return err
			}
			defer store.Close()

			var repoPaths []string
			for _, repo := range cfg.Repositories {
				repoPaths = append(repoPaths, repo.Path)
			}
			ctxBuilder := agents.NewContextBuilder(store, repoPaths...)

			registry := agents.NewRegistry()
			for _, tool := range agents.NewPlannerTools(ctxBuilder) {
				registry.Register(tool)
			}

			// Set up tool call logging: to log file if --log is set, else to stderr if -v.
			if logFile != "" {
				f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
				if err != nil {
					return fmt.Errorf("open log file %s: %w", logFile, err)
				}
				defer f.Close()
				registry.SetLogger(func(format string, args ...any) {
					fmt.Fprintf(f, format+"\n", args...)
					f.Sync()
				})
			} else if verbose {
				registry.SetLogger(func(format string, args ...any) {
					fmt.Fprintf(os.Stderr, format+"\n", args...)
				})
			}

			server := mcp.NewServer(registry)

			// Handle signals for graceful shutdown.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigCh
				cancel()
			}()

			// Redirect any fmt.Fprintf to stderr so stdout is clean for JSON-RPC.
			fmt.Fprintln(os.Stderr, "codeeagle MCP server started")

			if err := server.Run(ctx); err != nil && ctx.Err() == nil {
				return fmt.Errorf("MCP server error: %w", err)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&logFile, "log", "", "path to write tool call logs (used by Claude CLI verbose mode)")

	return cmd
}
