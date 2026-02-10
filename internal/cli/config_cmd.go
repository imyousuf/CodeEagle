package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
)

// Style definitions for config view.
var (
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#5A56E0", Dark: "#7571F9"})
	labelStyle = lipgloss.NewStyle().
			Faint(true).
			Width(18)
	valueStyle = lipgloss.NewStyle()
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "View or edit project configuration",
		Long: `View or edit CodeEagle project configuration.

By default, displays the current configuration in a pretty-printed format.
Use 'config edit' to edit configuration interactively.`,
		RunE: runConfigView,
	}

	cmd.AddCommand(newConfigEditCmd())

	return cmd
}

func runConfigView(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintln(out)

	// Title
	fmt.Fprintln(out, headerStyle.Render("CodeEagle Configuration"))
	fmt.Fprintln(out, headerStyle.Render(strings.Repeat("=", 26)))
	fmt.Fprintln(out)

	// Project
	printSection(out, "Project")
	printKV(out, "Name", cfg.Project.Name)
	if cfg.ConfigDir != "" {
		printKV(out, "Config dir", cfg.ConfigDir)
	}
	fmt.Fprintln(out)

	// Repositories
	printSection(out, "Repositories")
	for _, repo := range cfg.Repositories {
		repoType := repo.Type
		if repoType == "" {
			repoType = "single"
		}
		fmt.Fprintf(out, "    %s  (%s)\n", repo.Path, repoType)
	}
	fmt.Fprintln(out)

	// Languages
	printSection(out, "Languages")
	if len(cfg.Languages) > 0 {
		fmt.Fprintf(out, "    %s\n", strings.Join(cfg.Languages, ", "))
	} else {
		fmt.Fprintln(out, "    (none)")
	}
	fmt.Fprintln(out)

	// Graph Storage
	printSection(out, "Graph Storage")
	storage := cfg.Graph.Storage
	if storage == "" {
		storage = "embedded"
	}
	printKV(out, "Backend", storage)
	dbPath := cfg.ResolveDBPath("")
	if dbPath != "" {
		printKV(out, "DB Path", dbPath)
	}
	fmt.Fprintln(out)

	// LLM Configuration
	printSection(out, "LLM Configuration")
	printKV(out, "Provider", cfg.Agents.LLMProvider)
	printKV(out, "Model", cfg.Agents.Model)
	if cfg.Agents.LLMProvider == "vertex-ai" {
		printKV(out, "GCP Project", cfg.Agents.Project)
		printKV(out, "GCP Region", cfg.Agents.Location)
	}
	printKV(out, "Auto-summarize", boolYesNo(cfg.Agents.AutoSummarize))
	printKV(out, "Auto-link", boolYesNo(cfg.Agents.AutoLink))
	fmt.Fprintln(out)

	// Watch Exclusions
	printSection(out, "Watch Exclusions")
	for _, pattern := range cfg.Watch.Exclude {
		fmt.Fprintf(out, "    %s\n", pattern)
	}
	fmt.Fprintln(out)

	return nil
}

func printSection(out io.Writer, title string) {
	fmt.Fprintf(out, "  %s\n", headerStyle.Render(title))
}

func printKV(out io.Writer, label, value string) {
	fmt.Fprintf(out, "    %s%s\n", labelStyle.Render(label+":"), valueStyle.Render(value))
}

func boolYesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func newConfigEditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "edit",
		Short: "Edit project configuration interactively",
		Long:  `Edit CodeEagle project configuration using an interactive wizard.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigEdit(cmd)
		},
	}

	return cmd
}

func runConfigEdit(cmd *cobra.Command) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if cfg.ConfigDir == "" {
		return fmt.Errorf("no project config found; run 'codeeagle init' first")
	}

	out := cmd.OutOrStdout()

	// Pre-fill form variables from existing config
	projectName := cfg.Project.Name
	repoType := "single"
	if len(cfg.Repositories) > 0 {
		repoType = cfg.Repositories[0].Type
		if repoType == "" {
			repoType = "single"
		}
	}
	languages := make([]string, len(cfg.Languages))
	copy(languages, cfg.Languages)
	llmProvider := cfg.Agents.LLMProvider
	gcpProject := cfg.Agents.Project
	gcpRegion := cfg.Agents.Location
	if gcpRegion == "" {
		gcpRegion = "us-central1"
	}
	autoLink := cfg.Agents.AutoLink
	autoSummarize := cfg.Agents.AutoSummarize
	var confirm bool

	// Build language options with currently selected ones pre-selected
	selectedSet := make(map[string]bool, len(languages))
	for _, l := range languages {
		selectedSet[l] = true
	}
	langOptions := make([]huh.Option[string], len(allLanguages))
	for i, lang := range allLanguages {
		opt := huh.NewOption(lang, lang)
		if selectedSet[lang] {
			opt = opt.Selected(true)
		}
		langOptions[i] = opt
	}

	providerOptions := []huh.Option[string]{
		huh.NewOption("Claude Code CLI", "claude-cli"),
		huh.NewOption("Anthropic API", "anthropic"),
		huh.NewOption("Vertex AI (GCP)", "vertex-ai"),
	}

	repoTypeOptions := []huh.Option[string]{
		huh.NewOption("Single repository", "single"),
		huh.NewOption("Monorepo", "monorepo"),
	}

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

		// Group 3b: Vertex AI Config
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
				Title("Save changes?").
				Value(&confirm).
				Affirmative("Save").
				Negative("Cancel"),
		).Title("Confirm"),
	).WithTheme(huh.ThemeCharm())

	if err := form.Run(); err != nil {
		if err == huh.ErrUserAborted {
			fmt.Fprintln(out, "Cancelled.")
			return nil
		}
		return fmt.Errorf("interactive config edit: %w", err)
	}

	if !confirm {
		fmt.Fprintln(out, "Cancelled.")
		return nil
	}

	// Update config from form values
	cfg.Project.Name = projectName
	if len(cfg.Repositories) > 0 {
		cfg.Repositories[0].Type = repoType
	}
	cfg.Languages = languages
	cfg.Agents.LLMProvider = llmProvider
	cfg.Agents.AutoLink = autoLink
	cfg.Agents.AutoSummarize = autoSummarize

	if llmProvider == "vertex-ai" {
		cfg.Agents.Project = gcpProject
		cfg.Agents.Location = gcpRegion
	} else {
		cfg.Agents.Project = ""
		cfg.Agents.Location = ""
	}

	// Write updated config
	configPath := filepath.Join(cfg.ConfigDir, config.ProjectConfigFile)
	if err := config.WriteConfig(cfg, configPath); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	fmt.Fprintf(out, "Configuration saved to %s\n", configPath)
	return nil
}
