package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
)

// detectLanguages delegates to config.DetectLanguages.
func detectLanguages(rootDir string) []string {
	return config.DetectLanguages(rootDir)
}

// runInteractiveInit runs the interactive TUI wizard for project initialization.
func runInteractiveInit(cmd *cobra.Command, cwd string) error {
	out := cmd.OutOrStdout()

	// Detect languages in the project directory.
	detected := detectLanguages(cwd)
	detectedSet := make(map[string]bool, len(detected))
	for _, l := range detected {
		detectedSet[l] = true
	}

	// Form variables
	var (
		projectName   = filepath.Base(cwd)
		repoType      string
		languages     []string
		llmProvider   string
		gcpProject    string
		gcpRegion     string
		autoLink      = true
		autoSummarize bool
		confirm       bool
	)

	// Detect default provider
	defaultProvider, _ := detectLLMProvider()

	// Build language options with detected ones pre-selected
	langOptions := make([]huh.Option[string], len(config.AllLanguages))
	for i, lang := range config.AllLanguages {
		opt := huh.NewOption(lang, lang)
		if detectedSet[lang] {
			opt = opt.Selected(true)
		}
		langOptions[i] = opt
	}

	// Provider options
	providerOptions := []huh.Option[string]{
		huh.NewOption("Claude Code CLI", "claude-cli"),
		huh.NewOption("Anthropic API", "anthropic"),
		huh.NewOption("Vertex AI (GCP)", "vertex-ai"),
	}

	// Repo type options
	repoTypeOptions := []huh.Option[string]{
		huh.NewOption("Single repository", "single"),
		huh.NewOption("Monorepo", "monorepo"),
	}

	// Set default provider
	repoType = "single"
	llmProvider = defaultProvider
	gcpRegion = "us-central1"

	form := huh.NewForm(
		// Group 1: Project Setup
		huh.NewGroup(
			huh.NewInput().
				Title("Project name").
				Value(&projectName).
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return fmt.Errorf("project name cannot be empty")
					}
					return nil
				}),
			huh.NewSelect[string]().
				Title("Repository type").
				Options(repoTypeOptions...).
				Value(&repoType),
		).Title("Project Setup"),

		// Group 2: Languages
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Languages to parse").
				Description("Auto-detected languages are pre-selected").
				Options(langOptions...).
				Value(&languages).
				Filterable(true).
				Height(16),
		).Title("Languages"),

		// Group 3a: LLM Provider
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("LLM provider").
				Options(providerOptions...).
				Value(&llmProvider),
		).Title("LLM Configuration"),

		// Group 3b: Vertex AI Config (hidden unless vertex-ai selected)
		huh.NewGroup(
			huh.NewInput().
				Title("GCP Project ID").
				Value(&gcpProject).
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return fmt.Errorf("GCP project ID is required for Vertex AI")
					}
					return nil
				}),
			huh.NewInput().
				Title("GCP Region").
				Value(&gcpRegion).
				Placeholder("us-central1").
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return fmt.Errorf("GCP region is required for Vertex AI")
					}
					return nil
				}),
		).Title("Vertex AI Configuration").
			WithHideFunc(func() bool { return llmProvider != "vertex-ai" }),

		// Group 3c: Anthropic Note (hidden unless anthropic selected)
		huh.NewGroup(
			huh.NewNote().
				Title("Anthropic API").
				Description("Set ANTHROPIC_API_KEY in .CodeEagle/.env after init."),
		).WithHideFunc(func() bool { return llmProvider != "anthropic" }),

		// Group 3d: Claude CLI Note (hidden unless claude-cli selected)
		huh.NewGroup(
			huh.NewNote().
				Title("Claude Code CLI").
				Description("Uses your installed Claude Code CLI. No API key needed."),
		).WithHideFunc(func() bool { return llmProvider != "claude-cli" }),

		// Group 4: Advanced Options
		huh.NewGroup(
			huh.NewConfirm().
				Title("Enable LLM cross-service linking?").
				Description("Resolves unmatched API calls and event-driven dependencies using LLM").
				Value(&autoLink).
				Affirmative("Yes").
				Negative("No"),
			huh.NewConfirm().
				Title("Enable LLM auto-summarization?").
				Description("Summarizes services and architectural patterns after indexing").
				Value(&autoSummarize).
				Affirmative("Yes").
				Negative("No"),
		).Title("Advanced Options"),

		// Group 5: Confirm
		huh.NewGroup(
			huh.NewNote().
				Title("Summary").
				DescriptionFunc(func() string {
					langStr := strings.Join(languages, ", ")
					if langStr == "" {
						langStr = "(none)"
					}
					providerLabel := llmProvider
					switch llmProvider {
					case "claude-cli":
						providerLabel = "Claude Code CLI"
					case "anthropic":
						providerLabel = "Anthropic API"
					case "vertex-ai":
						providerLabel = fmt.Sprintf("Vertex AI (%s / %s)", gcpProject, gcpRegion)
					}
					return fmt.Sprintf(
						"Project:     %s\n"+
							"Repo type:   %s\n"+
							"Languages:   %s\n"+
							"LLM:         %s\n"+
							"Auto-link:   %v\n"+
							"Summarize:   %v",
						projectName, repoType, langStr,
						providerLabel, autoLink, autoSummarize,
					)
				}, &languages),
			huh.NewConfirm().
				Title("Create project?").
				Value(&confirm).
				Affirmative("Create").
				Negative("Cancel"),
		).Title("Confirm"),
	).WithTheme(huh.ThemeCharm())

	if err := form.Run(); err != nil {
		if err == huh.ErrUserAborted {
			fmt.Fprintln(out, "Cancelled.")
			return nil
		}
		return fmt.Errorf("interactive init: %w", err)
	}

	if !confirm {
		fmt.Fprintln(out, "Cancelled.")
		return nil
	}

	// Build repository list — scope to user content dirs when in home directory.
	var repos []config.RepositoryConfig
	if isHomeDir(cwd) {
		contentDirs := userContentDirs(cwd)
		if len(contentDirs) > 0 {
			for _, dir := range contentDirs {
				repos = append(repos, config.RepositoryConfig{Path: dir, Type: "single"})
			}
		} else {
			repos = []config.RepositoryConfig{{Path: cwd, Type: repoType}}
		}
	} else {
		repos = []config.RepositoryConfig{{Path: cwd, Type: repoType}}
	}

	// Build config from wizard values
	cfg := &config.Config{
		Project:      config.ProjectConfig{Name: projectName},
		Repositories: repos,
		Watch: config.WatchConfig{
			Exclude: []string{
				"**/node_modules/**",
				"**/.git/**",
				"**/vendor/**",
				"**/__pycache__/**",
				"**/dist/**",
				"**/build/**",
			},
		},
		Languages: languages,
		Graph:     config.GraphConfig{Storage: "embedded"},
		Agents: config.AgentsConfig{
			LLMProvider:   llmProvider,
			AutoLink:      autoLink,
			AutoSummarize: autoSummarize,
		},
	}

	if llmProvider == "vertex-ai" {
		cfg.Agents.Project = gcpProject
		cfg.Agents.Location = gcpRegion
	}

	// Create .CodeEagle/ directory
	projectDir := filepath.Join(cwd, config.ProjectDirName)
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		return fmt.Errorf("create project directory: %w", err)
	}

	// Write config.yaml via WriteConfig
	configPath := filepath.Join(projectDir, config.ProjectConfigFile)
	if err := config.WriteConfig(cfg, configPath); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}
	fmt.Fprintf(out, "Created %s\n", configPath)

	// Write .env template
	envContent := generateEnvTemplate()
	envPath := filepath.Join(projectDir, ".env")
	if err := os.WriteFile(envPath, []byte(envContent), 0644); err != nil {
		return fmt.Errorf("write .env file: %w", err)
	}
	fmt.Fprintf(out, "Created %s\n", envPath)

	// Register in global registry
	if err := config.RegisterProject(projectName, cwd, projectDir); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to register project in %s: %v\n", config.RegistryPath(), err)
	} else {
		fmt.Fprintf(out, "Registered project %q in %s\n", projectName, config.RegistryPath())
	}

	// Create .CodeEagle.conf at project root
	confPath := filepath.Join(cwd, config.ProjectConfFile)
	confContent := "export_file: codeeagle-graph.export\n"
	if err := os.WriteFile(confPath, []byte(confContent), 0644); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not create %s: %v\n", confPath, err)
	} else {
		fmt.Fprintf(out, "Created %s\n", confPath)
	}

	// Print next steps
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Next steps:")
	if llmProvider == "anthropic" {
		fmt.Fprintln(out, "  1. Add ANTHROPIC_API_KEY to .CodeEagle/.env")
	} else {
		fmt.Fprintln(out, "  1. Review .CodeEagle/.env for credentials if needed")
	}
	fmt.Fprintln(out, "  2. Add .CodeEagle/ to .gitignore")
	fmt.Fprintln(out, "  3. Commit .CodeEagle.conf to git")
	fmt.Fprintln(out, "  4. Run 'codeeagle sync' to index the codebase")
	fmt.Fprintln(out, "  5. Run 'codeeagle hook install' to auto-sync on commits")

	return nil
}
