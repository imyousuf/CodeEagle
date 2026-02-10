package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	internalllm "github.com/imyousuf/CodeEagle/internal/llm"
)

func newInitCmd() *cobra.Command {
	var interactive bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a .CodeEagle/ project directory",
		Long: `Initialize a CodeEagle project in the current directory.

Creates a .CodeEagle/ directory containing:
  config.yaml    Project configuration
  .env           Credentials template (add your API keys here)

The project is also registered in ~/.codeeagle.conf for cross-project access.

Use --interactive (-i) for a guided setup wizard with language auto-detection.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}

			projectDir := filepath.Join(cwd, config.ProjectDirName)

			// Check if .CodeEagle/ already exists.
			if _, err := os.Stat(projectDir); err == nil {
				return fmt.Errorf("%s already exists; project is already initialized", projectDir)
			}

			// Interactive mode: delegate to the TUI wizard.
			if interactive {
				return runInteractiveInit(cmd, cwd)
			}

			// Non-interactive mode (default, unchanged behavior).

			// Create .CodeEagle/ directory.
			if err := os.MkdirAll(projectDir, 0755); err != nil {
				return fmt.Errorf("create project directory: %w", err)
			}

			out := cmd.OutOrStdout()

			// Detect LLM provider from environment.
			provider, providerHint := detectLLMProvider()

			// Write config.yaml.
			projectName := filepath.Base(cwd)
			configContent := generateConfigYAML(projectName, cwd, provider)
			configPath := filepath.Join(projectDir, config.ProjectConfigFile)
			if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
				return fmt.Errorf("write config file: %w", err)
			}
			fmt.Fprintf(out, "Created %s\n", configPath)

			// Write .env template.
			envContent := generateEnvTemplate()
			envPath := filepath.Join(projectDir, ".env")
			if err := os.WriteFile(envPath, []byte(envContent), 0644); err != nil {
				return fmt.Errorf("write .env file: %w", err)
			}
			fmt.Fprintf(out, "Created %s\n", envPath)

			// Register in global registry.
			if err := config.RegisterProject(projectName, cwd, projectDir); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to register project in %s: %v\n", config.RegistryPath(), err)
			} else {
				fmt.Fprintf(out, "Registered project %q in %s\n", projectName, config.RegistryPath())
			}

			// Create .CodeEagle.conf at project root (for git-committed config).
			confPath := filepath.Join(cwd, config.ProjectConfFile)
			confContent := "export_file: codeeagle-graph.export\n"
			if err := os.WriteFile(confPath, []byte(confContent), 0644); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not create %s: %v\n", confPath, err)
			} else {
				fmt.Fprintf(out, "Created %s\n", confPath)
			}

			// Print next steps.
			fmt.Fprintln(out)
			fmt.Fprintln(out, "Next steps:")
			fmt.Fprintln(out, "  1. Add your API keys to .CodeEagle/.env")
			if providerHint != "" {
				fmt.Fprintf(out, "     (detected %s)\n", providerHint)
			}
			fmt.Fprintln(out, "  2. Edit .CodeEagle/config.yaml to configure repositories and languages")
			fmt.Fprintln(out, "  3. Add .CodeEagle/ to .gitignore")
			fmt.Fprintln(out, "  4. Commit .CodeEagle.conf to git")
			fmt.Fprintln(out, "  5. Run 'codeeagle sync' to index the codebase")
			fmt.Fprintln(out, "  6. Run 'codeeagle hook install' to auto-sync on commits")

			return nil
		},
	}

	cmd.Flags().BoolVarP(&interactive, "interactive", "i", false, "use interactive setup wizard")

	return cmd
}

// detectLLMProvider checks environment variables to auto-detect the LLM provider.
func detectLLMProvider() (provider, hint string) {
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return "anthropic", "ANTHROPIC_API_KEY set"
	}
	if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") != "" || os.Getenv("GOOGLE_CLOUD_PROJECT") != "" {
		return "vertex-ai", "Google Cloud credentials detected"
	}
	if internalllm.FindClaudeCLI() != "" {
		return "claude-cli", "Claude Code CLI detected"
	}
	return "anthropic", ""
}

func generateConfigYAML(projectName, projectRoot, provider string) string {
	model := "claude-sonnet-4-5-20250929"

	return fmt.Sprintf(`project:
  name: %q

repositories:
  - path: %s
    type: single

watch:
  exclude:
    - "**/node_modules/**"
    - "**/.git/**"
    - "**/vendor/**"
    - "**/__pycache__/**"
    - "**/dist/**"
    - "**/build/**"

languages:
  - go
  - python
  - typescript
  - javascript
  - java
  - html
  - markdown

graph:
  storage: embedded

agents:
  llm_provider: %s
  model: %s
`, projectName, projectRoot, provider, model)
}

func generateEnvTemplate() string {
	return `# CodeEagle credentials
# Uncomment and fill in the appropriate values for your LLM provider.

# Anthropic (direct API)
# ANTHROPIC_API_KEY=sk-ant-...

# Google Cloud / Vertex AI
# GOOGLE_APPLICATION_CREDENTIALS=/path/to/service-account.json
# GOOGLE_CLOUD_PROJECT=my-gcp-project
`
}
