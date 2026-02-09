package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/pkg/llm"

	// Register LLM providers so their init() functions run.
	_ "github.com/imyousuf/CodeEagle/internal/llm"
)

func newLLMTestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "llm-test",
		Short: "Verify LLM configuration and connectivity",
		Long: `Test the LLM setup by loading the configuration, creating an LLM client,
and sending a simple test prompt. Prints the provider, model, response,
and whether the connection was successful.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Provider: %s\n", cfg.Agents.LLMProvider)
			fmt.Fprintf(cmd.OutOrStdout(), "Model:    %s\n", cfg.Agents.Model)

			client, err := createLLMClient(cfg)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "\nFailed to create LLM client: %v\n", err)
				fmt.Fprintln(cmd.ErrOrStderr(), "\nDebugging tips:")
				fmt.Fprintln(cmd.ErrOrStderr(), "  - Check that your LLM provider is correctly configured in .codeeagle.yaml")
				fmt.Fprintln(cmd.ErrOrStderr(), "  - For Anthropic: ensure ANTHROPIC_API_KEY environment variable is set")
				fmt.Fprintln(cmd.ErrOrStderr(), "  - For Vertex AI: ensure GOOGLE_APPLICATION_CREDENTIALS is set or credentials_file is configured")
				fmt.Fprintln(cmd.ErrOrStderr(), "  - For Vertex AI: ensure project and location are set in the config")
				fmt.Fprintln(cmd.ErrOrStderr(), "  - Verify the model name is valid for your provider")
				return fmt.Errorf("LLM client creation failed")
			}
			defer client.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			fmt.Fprintln(cmd.OutOrStdout(), "\nSending test prompt...")

			resp, err := client.Chat(ctx, "You are a helpful assistant.", []llm.Message{
				{Role: llm.RoleUser, Content: "Say hello in one word"},
			})
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "\nLLM chat failed: %v\n", err)
				fmt.Fprintln(cmd.ErrOrStderr(), "\nDebugging tips:")
				fmt.Fprintln(cmd.ErrOrStderr(), "  - Check your network connectivity")
				fmt.Fprintln(cmd.ErrOrStderr(), "  - Verify API credentials are valid and not expired")
				fmt.Fprintln(cmd.ErrOrStderr(), "  - Ensure the model name is correct for your provider")
				fmt.Fprintln(cmd.ErrOrStderr(), "  - For Vertex AI: verify the GCP project has the required APIs enabled")
				return fmt.Errorf("LLM test failed")
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Response: %s\n", resp.Content)
			fmt.Fprintf(cmd.OutOrStdout(), "\nLLM connection verified successfully!\n")
			return nil
		},
	}

	return cmd
}
