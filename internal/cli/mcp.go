package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/agents"
	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
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

// leaseIdle is how long the MCP tools keep the graph open after their last
// use: long enough for one tool's burst of queries to share a handle, short
// against an agent's pause between tool calls.
const leaseIdle = 500 * time.Millisecond

func newMCPServeCmd() *cobra.Command {
	var (
		logFile string
		tools   string
	)

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
			if tools != "" {
				cfg.MCP.Tools = tools
			}
			if t := cfg.MCP.Tools; t != "" && t != "all" && t != "verbs" {
				return fmt.Errorf("mcp tools %q: want all or verbs", t)
			}

			registry := agents.NewRegistry()
			// No tool holds the graph open between calls: a read-only handle
			// keeps every writer out while it is open, so one kept for the
			// session would stop the worker syncing until the session ended.
			v := newVerbs(cfg)
			defer v.Close()
			for _, tool := range verbTools(v) {
				registry.Register(tool)
			}
			if cfg.MCP.Tools != "verbs" {
				store := embedded.NewLeased(func() (*embedded.BranchStore, error) {
					s, _, err := openReadOnlyBranchStore(cfg)
					return s, err
				}, leaseIdle)
				defer store.Close()
				var repoPaths []string
				for _, repo := range cfg.Repositories {
					repoPaths = append(repoPaths, repo.Path)
				}
				ctxBuilder := agents.NewContextBuilder(store, repoPaths...)
				for _, tool := range agents.NewPlannerTools(ctxBuilder) {
					registry.Register(tool)
				}
				// Meeting tools appear only when meetings are indexed: a tool whose
				// only possible answer is "nothing is indexed" wastes an agent's turns.
				for _, tool := range agents.NewMeetingTools(context.Background(), store) {
					registry.Register(tool)
				}
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
					_ = f.Sync()
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
	cmd.Flags().StringVar(&tools, "tools", "", "tools to expose: all, or verbs for only find, brief and show (default: mcp.tools)")

	return cmd
}
