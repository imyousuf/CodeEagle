package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/agents"
	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/pkg/llm"

	// Register LLM providers so their init() functions run.
	internalllm "github.com/imyousuf/CodeEagle/internal/llm"
)

func newAgentCmd() *cobra.Command {
	agentCmd := &cobra.Command{
		Use:   "agent",
		Short: "Interact with AI agents (plan, design, review, ask)",
		Long: `Interact with AI agents grounded in the codebase knowledge graph.

Available agents:
  plan     Planning agent for impact analysis, dependency mapping, and scope estimation
  design   Design agent for architecture review and pattern recognition
  review   Code review agent for diff review and convention checking
  ask      General-purpose Q&A agent for freeform questions about the codebase`,
	}

	agentCmd.AddCommand(newAgentPlanCmd())
	agentCmd.AddCommand(newAgentDesignCmd())
	agentCmd.AddCommand(newAgentReviewCmd())
	agentCmd.AddCommand(newAgentAskCmd())

	return agentCmd
}

// createLLMClient creates an LLM client from the config and environment.
func createLLMClient(cfg *config.Config) (llm.Client, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")

	provider := cfg.Agents.LLMProvider
	if provider == "" {
		provider = "anthropic"
	}

	// Auto-detect Claude CLI when Anthropic is configured but no API key is set.
	if (provider == "" || provider == "anthropic") && apiKey == "" {
		if path := internalllm.FindClaudeCLI(); path != "" {
			provider = "claude-cli"
		}
	}

	model := cfg.Agents.Model

	project := cfg.Agents.Project
	if project == "" {
		project = os.Getenv("GOOGLE_CLOUD_PROJECT")
	}

	location := cfg.Agents.Location

	client, err := llm.NewClient(llm.Config{
		Provider:        provider,
		Model:           model,
		APIKey:          apiKey,
		Project:         project,
		Location:        location,
		CredentialsFile: cfg.Agents.CredentialsFile,
	})
	if err != nil {
		return nil, fmt.Errorf("create LLM client: %w", err)
	}

	return client, nil
}

func newAgentPlanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan [query]",
		Short: "Ask the planning agent a question",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			resolvedDBPath := cfg.ResolveDBPath(dbPath)
			if resolvedDBPath == "" {
				return fmt.Errorf("no graph database path; run 'codeeagle init' or use --db-path")
			}

			client, err := createLLMClient(cfg)
			if err != nil {
				return err
			}
			defer client.Close()

			store, err := embedded.NewStore(resolvedDBPath)
			if err != nil {
				return fmt.Errorf("open graph store: %w", err)
			}
			defer store.Close()

			ctxBuilder := agents.NewContextBuilder(store)

			var repoPaths []string
			for _, repo := range cfg.Repositories {
				repoPaths = append(repoPaths, repo.Path)
			}
			planner := agents.NewPlanner(client, ctxBuilder, repoPaths...)

			query := strings.Join(args, " ")
			resp, err := planner.Ask(context.Background(), query)
			if err != nil {
				return fmt.Errorf("planner query failed: %w", err)
			}

			fmt.Fprintln(cmd.OutOrStdout(), resp)
			return nil
		},
	}

	return cmd
}

func newAgentDesignCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "design [query]",
		Short: "Ask the design agent a question",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			resolvedDBPath := cfg.ResolveDBPath(dbPath)
			if resolvedDBPath == "" {
				return fmt.Errorf("no graph database path; run 'codeeagle init' or use --db-path")
			}

			client, err := createLLMClient(cfg)
			if err != nil {
				return err
			}
			defer client.Close()

			store, err := embedded.NewStore(resolvedDBPath)
			if err != nil {
				return fmt.Errorf("open graph store: %w", err)
			}
			defer store.Close()

			ctxBuilder := agents.NewContextBuilder(store)
			designer := agents.NewDesigner(client, ctxBuilder)

			query := strings.Join(args, " ")
			resp, err := designer.Ask(context.Background(), query)
			if err != nil {
				return fmt.Errorf("designer query failed: %w", err)
			}

			fmt.Fprintln(cmd.OutOrStdout(), resp)
			return nil
		},
	}

	return cmd
}

func newAgentReviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review [query]",
		Short: "Ask the code review agent a question",
		Args:  cobra.MinimumNArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			resolvedDBPath := cfg.ResolveDBPath(dbPath)
			if resolvedDBPath == "" {
				return fmt.Errorf("no graph database path; run 'codeeagle init' or use --db-path")
			}

			client, err := createLLMClient(cfg)
			if err != nil {
				return err
			}
			defer client.Close()

			store, err := embedded.NewStore(resolvedDBPath)
			if err != nil {
				return fmt.Errorf("open graph store: %w", err)
			}
			defer store.Close()

			ctxBuilder := agents.NewContextBuilder(store)
			reviewer := agents.NewReviewer(client, ctxBuilder)

			var repoPaths []string
			for _, repo := range cfg.Repositories {
				repoPaths = append(repoPaths, repo.Path)
			}

			diff, _ := cmd.Flags().GetString("diff")
			if diff != "" {
				resp, err := reviewer.ReviewDiff(context.Background(), diff, repoPaths...)
				if err != nil {
					return fmt.Errorf("review diff failed: %w", err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), resp)
				return nil
			}

			if len(args) == 0 {
				return fmt.Errorf("provide a query or use --diff <ref>")
			}

			query := strings.Join(args, " ")
			resp, err := reviewer.Ask(context.Background(), query)
			if err != nil {
				return fmt.Errorf("reviewer query failed: %w", err)
			}

			fmt.Fprintln(cmd.OutOrStdout(), resp)
			return nil
		},
	}

	cmd.Flags().String("diff", "", "review changes in a git diff/PR reference")
	return cmd
}

func newAgentAskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ask [query]",
		Short: "Ask a question about the indexed codebase",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			resolvedDBPath := cfg.ResolveDBPath(dbPath)
			if resolvedDBPath == "" {
				return fmt.Errorf("no graph database path; run 'codeeagle init' or use --db-path")
			}

			client, err := createLLMClient(cfg)
			if err != nil {
				return err
			}
			defer client.Close()

			store, err := embedded.NewStore(resolvedDBPath)
			if err != nil {
				return fmt.Errorf("open graph store: %w", err)
			}
			defer store.Close()

			ctxBuilder := agents.NewContextBuilder(store)

			var repoPaths []string
			for _, repo := range cfg.Repositories {
				repoPaths = append(repoPaths, repo.Path)
			}
			asker := agents.NewAsker(client, ctxBuilder, repoPaths...)

			query := strings.Join(args, " ")
			resp, err := asker.Ask(context.Background(), query)
			if err != nil {
				return fmt.Errorf("ask query failed: %w", err)
			}

			fmt.Fprintln(cmd.OutOrStdout(), resp)
			return nil
		},
	}

	return cmd
}
