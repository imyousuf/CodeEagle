package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const defaultConfigContent = `project:
  name: "my-project"

repositories:
  - path: .
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
  llm_provider: anthropic
  model: claude-sonnet-4-5-20250929
`

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize a .codeeagle.yaml config file in the current directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath := filepath.Join(".", ".codeeagle.yaml")

			if _, err := os.Stat(configPath); err == nil {
				return fmt.Errorf("config file %s already exists", configPath)
			}

			if err := os.WriteFile(configPath, []byte(defaultConfigContent), 0644); err != nil {
				return fmt.Errorf("failed to write config file: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Created %s\n", configPath)
			return nil
		},
	}
}
