// Package cli implements the command-line interface for CodeEagle.
package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func newCompletionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion",
		Short: "Generate or install shell completion scripts",
		Long: `Generate or install shell completion scripts for CodeEagle.

Subcommands:
  bash      Print bash completion script to stdout
  zsh       Print zsh completion script to stdout
  install   Auto-detect shell and install completion script`,
	}

	cmd.AddCommand(newCompletionBashCmd())
	cmd.AddCommand(newCompletionZshCmd())
	cmd.AddCommand(newCompletionInstallCmd())

	return cmd
}

func newCompletionBashCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bash",
		Short: "Generate bash completion script",
		Long: `Generate bash completion script for CodeEagle.

To load completions in your current shell session:
  source <(codeeagle completion bash)

To install permanently, use:
  codeeagle completion install`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmd.Root().GenBashCompletionV2(os.Stdout, true); err != nil {
				return fmt.Errorf("failed to generate bash completion: %w", err)
			}
			fmt.Fprintln(os.Stderr, "\n# To load in current session: source <(codeeagle completion bash)")
			fmt.Fprintln(os.Stderr, "# To install permanently: codeeagle completion install")
			return nil
		},
	}
}

func newCompletionZshCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "zsh",
		Short: "Generate zsh completion script",
		Long: `Generate zsh completion script for CodeEagle.

To load completions in your current shell session:
  source <(codeeagle completion zsh)

To install permanently, use:
  codeeagle completion install`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmd.Root().GenZshCompletion(os.Stdout); err != nil {
				return fmt.Errorf("failed to generate zsh completion: %w", err)
			}
			fmt.Fprintln(os.Stderr, "\n# To load in current session: source <(codeeagle completion zsh)")
			fmt.Fprintln(os.Stderr, "# To install permanently: codeeagle completion install")
			return nil
		},
	}
}

func newCompletionInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Auto-detect shell and install completion script",
		Long: `Auto-detect your shell and install the completion script.

If running with sudo/root, installs system-wide:
  - Bash: /etc/bash_completion.d/codeeagle
  - Zsh: /usr/local/share/zsh/site-functions/_codeeagle

Otherwise, installs for current user:
  - Bash: ~/.bash_completion.d/codeeagle (sources from ~/.bashrc)
  - Zsh: ~/.zsh/completions/_codeeagle (add to fpath in ~/.zshrc)`,
		RunE: runCompletionInstall,
	}
}

func runCompletionInstall(cmd *cobra.Command, args []string) error {
	shell := detectShell()
	if shell == "" {
		return fmt.Errorf("could not detect shell (SHELL env not set or unsupported shell)")
	}

	isRoot := isRunningAsRoot()

	var completionPath string
	var completionContent bytes.Buffer
	var postInstallMsg string

	switch shell {
	case "bash":
		if err := cmd.Root().GenBashCompletionV2(&completionContent, true); err != nil {
			return fmt.Errorf("failed to generate bash completion: %w", err)
		}
		if isRoot {
			completionPath = "/etc/bash_completion.d/codeeagle"
			postInstallMsg = "Completion will be available in new shells."
		} else {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("failed to get home directory: %w", err)
			}
			completionPath = filepath.Join(homeDir, ".bash_completion.d", "codeeagle")
			postInstallMsg = `Add to your ~/.bashrc if not already present:
  for f in ~/.bash_completion.d/*; do source "$f"; done

Then reload: source ~/.bashrc`
		}

	case "zsh":
		if err := cmd.Root().GenZshCompletion(&completionContent); err != nil {
			return fmt.Errorf("failed to generate zsh completion: %w", err)
		}
		if isRoot {
			completionPath = "/usr/local/share/zsh/site-functions/_codeeagle"
			postInstallMsg = "Completion will be available in new shells."
		} else {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("failed to get home directory: %w", err)
			}
			completionPath = filepath.Join(homeDir, ".zsh", "completions", "_codeeagle")
			postInstallMsg = `Add to your ~/.zshrc if not already present:
  fpath=(~/.zsh/completions $fpath)
  autoload -U compinit && compinit

Then reload: source ~/.zshrc`
		}

	default:
		return fmt.Errorf("unsupported shell: %s (only bash and zsh are supported)", shell)
	}

	// Create parent directory if needed
	parentDir := filepath.Dir(completionPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", parentDir, err)
	}

	// Write completion file
	if err := os.WriteFile(completionPath, completionContent.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write completion file: %w", err)
	}

	fmt.Printf("Installed %s completion to: %s\n\n%s\n", shell, completionPath, postInstallMsg)
	return nil
}

func detectShell() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return ""
	}

	base := filepath.Base(shell)
	switch {
	case strings.Contains(base, "bash"):
		return "bash"
	case strings.Contains(base, "zsh"):
		return "zsh"
	default:
		return base
	}
}

func isRunningAsRoot() bool {
	if os.Geteuid() == 0 {
		return true
	}

	if os.Getenv("SUDO_USER") != "" {
		return true
	}

	currentUser, err := user.Current()
	if err != nil {
		return false
	}

	return currentUser.Uid == "0"
}
