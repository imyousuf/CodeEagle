package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const (
	hookMarkerBegin = "# BEGIN codeeagle hook"
	hookMarkerEnd   = "# END codeeagle hook"
	hookContent     = `# BEGIN codeeagle hook
codeeagle sync 2>/dev/null &
# END codeeagle hook
`
	hookShebang = "#!/bin/sh\n"
)

func newHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Manage git hooks for automatic sync",
	}

	cmd.AddCommand(newHookInstallCmd())
	cmd.AddCommand(newHookRemoveCmd())

	return cmd
}

func newHookInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install a post-commit hook that runs codeeagle sync",
		RunE: func(cmd *cobra.Command, args []string) error {
			gitDir, err := findGitDir()
			if err != nil {
				return err
			}

			hookPath := filepath.Join(gitDir, "hooks", "post-commit")
			out := cmd.OutOrStdout()

			// Ensure hooks directory exists.
			if err := os.MkdirAll(filepath.Join(gitDir, "hooks"), 0755); err != nil {
				return fmt.Errorf("create hooks directory: %w", err)
			}

			// Read existing hook file if present.
			existing, err := os.ReadFile(hookPath)
			if err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("read hook file: %w", err)
			}

			content := string(existing)

			// Check if already installed.
			if strings.Contains(content, hookMarkerBegin) {
				fmt.Fprintln(out, "CodeEagle hook is already installed.")
				return nil
			}

			// Build new content.
			var newContent string
			if content == "" {
				newContent = hookShebang + "\n" + hookContent
			} else {
				// Append to existing hook.
				if !strings.HasSuffix(content, "\n") {
					content += "\n"
				}
				newContent = content + "\n" + hookContent
			}

			if err := os.WriteFile(hookPath, []byte(newContent), 0755); err != nil {
				return fmt.Errorf("write hook file: %w", err)
			}

			fmt.Fprintf(out, "Installed post-commit hook at %s\n", hookPath)
			return nil
		},
	}
}

func newHookRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove",
		Short: "Remove the codeeagle post-commit hook",
		RunE: func(cmd *cobra.Command, args []string) error {
			gitDir, err := findGitDir()
			if err != nil {
				return err
			}

			hookPath := filepath.Join(gitDir, "hooks", "post-commit")
			out := cmd.OutOrStdout()

			data, err := os.ReadFile(hookPath)
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Fprintln(out, "No post-commit hook found.")
					return nil
				}
				return fmt.Errorf("read hook file: %w", err)
			}

			content := string(data)
			if !strings.Contains(content, hookMarkerBegin) {
				fmt.Fprintln(out, "No CodeEagle hook found in post-commit.")
				return nil
			}

			// Strip the codeeagle section.
			cleaned := stripHookSection(content)

			// If only shebang (and whitespace) remains, delete the file.
			trimmed := strings.TrimSpace(cleaned)
			if trimmed == "" || trimmed == strings.TrimSpace(hookShebang) {
				if err := os.Remove(hookPath); err != nil {
					return fmt.Errorf("remove hook file: %w", err)
				}
				fmt.Fprintf(out, "Removed post-commit hook at %s\n", hookPath)
			} else {
				if err := os.WriteFile(hookPath, []byte(cleaned), 0755); err != nil {
					return fmt.Errorf("write hook file: %w", err)
				}
				fmt.Fprintf(out, "Removed CodeEagle section from %s\n", hookPath)
			}

			return nil
		},
	}
}

// findGitDir walks up from CWD looking for a .git directory.
func findGitDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	for {
		candidate := filepath.Join(dir, ".git")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("not inside a git repository")
}

// stripHookSection removes the codeeagle hook section from the content.
func stripHookSection(content string) string {
	lines := strings.Split(content, "\n")
	var result []string
	inSection := false
	for _, line := range lines {
		if strings.TrimSpace(line) == hookMarkerBegin {
			inSection = true
			continue
		}
		if strings.TrimSpace(line) == hookMarkerEnd {
			inSection = false
			continue
		}
		if !inSection {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}
