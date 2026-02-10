package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/metrics"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

// extToLanguage maps file extensions to language names for metrics calculation.
var extToLanguage = map[string]string{
	".go":   "go",
	".py":   "python",
	".pyi":  "python",
	".ts":   "typescript",
	".tsx":  "typescript",
	".js":   "javascript",
	".jsx":  "javascript",
	".mjs":  "javascript",
	".cjs":  "javascript",
	".java": "java",
	".html": "html",
	".htm":  "html",
	".md":   "markdown",
	".mdx":  "markdown",
}

func newMetricsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "metrics <file>",
		Short: "Show code quality metrics",
		Long:  `Show code quality metrics for a source file.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath := args[0]

			content, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("read file: %w", err)
			}

			ext := filepath.Ext(filePath)
			language := detectLanguage(ext)

			calc := metrics.NewCompositeCalculator()
			result, err := calc.Calculate(filePath, content, language)
			if err != nil {
				return fmt.Errorf("calculate metrics: %w", err)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Metrics for %s (language: %s)\n", filePath, language)
			fmt.Fprintf(out, "%s\n\n", strings.Repeat("=", 40))

			// Sort metric names for stable output.
			keys := make([]string, 0, len(result))
			for k := range result {
				keys = append(keys, string(k))
			}
			sort.Strings(keys)

			for _, k := range keys {
				v := result[metrics.MetricType(k)]
				// Display integer values without decimal for cleaner output.
				if v == float64(int64(v)) {
					fmt.Fprintf(out, "  %-25s %d\n", k, int64(v))
				} else {
					fmt.Fprintf(out, "  %-25s %.2f\n", k, v)
				}
			}

			return nil
		},
	}
}

// detectLanguage maps a file extension to a language name.
// It first checks the static map, then falls back to the parser registry extensions.
func detectLanguage(ext string) string {
	if lang, ok := extToLanguage[ext]; ok {
		return lang
	}
	// Fallback: check parser.FileExtensions.
	for lang, exts := range parser.FileExtensions {
		for _, e := range exts {
			if e == ext {
				return string(lang)
			}
		}
	}
	return "unknown"
}
