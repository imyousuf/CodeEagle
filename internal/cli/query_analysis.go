package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph"
)

// unusedEntry represents a potentially unused function or method.
type unusedEntry struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Type     graph.NodeType `json:"type"`
	FilePath string         `json:"file_path"`
	Line     int            `json:"line"`
	Package  string         `json:"package"`
	Language string         `json:"language"`
}

func newQueryUnusedCmd() *cobra.Command {
	var (
		nodeType        string
		pkg             string
		language        string
		includeExported bool
		jsonOut         bool
	)

	cmd := &cobra.Command{
		Use:   "unused",
		Short: "Find functions and methods with no incoming Calls edges",
		Long: `Identify potentially unused functions and methods by checking whether
any other code calls them. By default, exported functions are excluded since
they may be called from outside the indexed codebase. Use --include-exported
to include them.

Test functions, init(), and main() are always excluded.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			store, _, err := openBranchStore(cfg)
			if err != nil {
				return err
			}
			defer store.Close()

			ctx := context.Background()

			// Collect candidate node types.
			types := []graph.NodeType{graph.NodeFunction, graph.NodeMethod}
			if nodeType != "" {
				nt := graph.NodeType(nodeType)
				if nt != graph.NodeFunction && nt != graph.NodeMethod {
					return fmt.Errorf("--type must be Function or Method")
				}
				types = []graph.NodeType{nt}
			}

			var candidates []*graph.Node
			for _, t := range types {
				filter := graph.NodeFilter{
					Type:     t,
					Package:  pkg,
					Language: language,
				}
				nodes, err := store.QueryNodes(ctx, filter)
				if err != nil {
					return fmt.Errorf("query %s nodes: %w", t, err)
				}
				candidates = append(candidates, nodes...)
			}

			// Filter and check for incoming Calls edges.
			var unused []unusedEntry
			for _, n := range candidates {
				if shouldSkipForUnused(n, includeExported) {
					continue
				}

				edges, err := store.GetEdges(ctx, n.ID, graph.EdgeCalls)
				if err != nil {
					return fmt.Errorf("get edges for %s: %w", n.Name, err)
				}

				hasIncoming := false
				for _, e := range edges {
					if e.TargetID == n.ID {
						hasIncoming = true
						break
					}
				}

				if !hasIncoming {
					unused = append(unused, unusedEntry{
						ID:       n.ID,
						Name:     n.Name,
						Type:     n.Type,
						FilePath: n.FilePath,
						Line:     n.Line,
						Package:  n.Package,
						Language: n.Language,
					})
				}
			}

			// Sort by file path then line.
			sort.Slice(unused, func(i, j int) bool {
				if unused[i].FilePath != unused[j].FilePath {
					return unused[i].FilePath < unused[j].FilePath
				}
				return unused[i].Line < unused[j].Line
			})

			out := cmd.OutOrStdout()

			if jsonOut {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(unused)
			}

			if len(unused) == 0 {
				fmt.Fprintln(out, "No unused functions or methods found.")
				return nil
			}

			fmt.Fprintf(out, "%-10s  %-40s  %s\n", "Type", "Name", "Location")
			fmt.Fprintf(out, "%-10s  %-40s  %s\n", "----------", "----------------------------------------", "--------")
			for _, u := range unused {
				loc := u.FilePath
				if u.Line > 0 {
					loc = fmt.Sprintf("%s:%d", u.FilePath, u.Line)
				}
				fmt.Fprintf(out, "%-10s  %-40s  %s\n", u.Type, u.Name, loc)
			}
			fmt.Fprintf(out, "\n%d potentially unused function(s)\n", len(unused))

			return nil
		},
	}

	cmd.Flags().StringVar(&nodeType, "type", "", "filter by node type: Function or Method")
	cmd.Flags().StringVar(&pkg, "package", "", "filter by package name")
	cmd.Flags().StringVar(&language, "language", "", "filter by language")
	cmd.Flags().BoolVar(&includeExported, "include-exported", false, "include exported functions (may be called externally)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "output as JSON")

	return cmd
}

// shouldSkipForUnused returns true if the node should be excluded from unused analysis.
func shouldSkipForUnused(n *graph.Node, includeExported bool) bool {
	// Skip test functions.
	if n.Type == graph.NodeTestFunction {
		return true
	}
	name := n.Name
	lang := n.Language
	if lang == "" {
		lang = inferLangFromPath(n.FilePath)
	}
	if isTestFuncByName(name, lang, n.FilePath) {
		return true
	}

	// Skip init() and main().
	if name == "init" || name == "main" {
		return true
	}

	// Skip exported unless explicitly included.
	if !includeExported && n.Exported {
		return true
	}

	return false
}

// coverageEntry represents a file or function with its test coverage status.
type coverageEntry struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Type     graph.NodeType `json:"type"`
	FilePath string         `json:"file_path"`
	Line     int            `json:"line,omitempty"`
	Package  string         `json:"package"`
	Language string         `json:"language"`
	Covered  bool           `json:"covered"`
}

// packageCoverage holds per-package test coverage statistics.
type packageCoverage struct {
	Package  string  `json:"package"`
	Total    int     `json:"total"`
	Covered  int     `json:"covered"`
	Percent  float64 `json:"percent"`
	Language string  `json:"language,omitempty"`
}

func newQueryCoverageCmd() *cobra.Command {
	var (
		level    string
		pkg      string
		language string
		jsonOut  bool
	)

	cmd := &cobra.Command{
		Use:   "coverage",
		Short: "Show test coverage gaps for files and functions",
		Long: `Analyze which files or functions have test coverage by checking for
EdgeTests relationships in the knowledge graph. Shows uncovered items
and per-package coverage percentages.

Levels:
  file      Show file-level coverage (default)
  function  Show function-level coverage`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if level != "file" && level != "function" {
				return fmt.Errorf("--level must be 'file' or 'function'")
			}

			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			store, _, err := openBranchStore(cfg)
			if err != nil {
				return err
			}
			defer store.Close()

			ctx := context.Background()

			if level == "file" {
				return runFileCoverage(ctx, cmd, store, pkg, language, jsonOut)
			}
			return runFunctionCoverage(ctx, cmd, store, pkg, language, jsonOut)
		},
	}

	cmd.Flags().StringVar(&level, "level", "file", "coverage level: file or function")
	cmd.Flags().StringVar(&pkg, "package", "", "filter by package name")
	cmd.Flags().StringVar(&language, "language", "", "filter by language")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "output as JSON")

	return cmd
}

func runFileCoverage(ctx context.Context, cmd *cobra.Command, store graph.Store, pkg, language string, jsonOut bool) error {
	// Query all File nodes.
	files, err := store.QueryNodes(ctx, graph.NodeFilter{
		Type:     graph.NodeFile,
		Package:  pkg,
		Language: language,
	})
	if err != nil {
		return fmt.Errorf("query file nodes: %w", err)
	}

	// Also include TestFile nodes to filter those out.
	testFiles, err := store.QueryNodes(ctx, graph.NodeFilter{
		Type:     graph.NodeTestFile,
		Package:  pkg,
		Language: language,
	})
	if err != nil {
		return fmt.Errorf("query test file nodes: %w", err)
	}

	// Build a set of test file paths.
	testFilePaths := make(map[string]bool, len(testFiles))
	for _, tf := range testFiles {
		testFilePaths[tf.FilePath] = true
	}

	// Filter to source files only (not test files).
	var sourceFiles []*graph.Node
	for _, f := range files {
		if testFilePaths[f.FilePath] {
			continue
		}
		lang := f.Language
		if lang == "" {
			lang = inferLangFromPath(f.FilePath)
		}
		if isTestFileByPath(f.FilePath, lang) {
			continue
		}
		sourceFiles = append(sourceFiles, f)
	}

	// Check coverage for each source file.
	var entries []coverageEntry
	for _, f := range sourceFiles {
		edges, err := store.GetEdges(ctx, f.ID, graph.EdgeTests)
		if err != nil {
			return fmt.Errorf("get edges for %s: %w", f.FilePath, err)
		}

		covered := false
		for _, e := range edges {
			if e.TargetID == f.ID {
				covered = true
				break
			}
		}

		entries = append(entries, coverageEntry{
			ID:       f.ID,
			Name:     f.Name,
			Type:     f.Type,
			FilePath: f.FilePath,
			Package:  f.Package,
			Language: f.Language,
			Covered:  covered,
		})
	}

	// Sort by file path.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].FilePath < entries[j].FilePath
	})

	// Compute per-package coverage.
	pkgStats := computePackageCoverage(entries)

	out := cmd.OutOrStdout()

	if jsonOut {
		result := struct {
			Files    []coverageEntry   `json:"files"`
			Packages []packageCoverage `json:"packages"`
		}{
			Files:    entries,
			Packages: pkgStats,
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	// Print uncovered files.
	var uncovered []coverageEntry
	for _, e := range entries {
		if !e.Covered {
			uncovered = append(uncovered, e)
		}
	}

	if len(uncovered) == 0 {
		fmt.Fprintln(out, "All source files have test coverage.")
	} else {
		fmt.Fprintf(out, "Uncovered files (%d of %d):\n\n", len(uncovered), len(entries))
		fmt.Fprintf(out, "  %-50s  %-20s  %s\n", "File", "Package", "Language")
		fmt.Fprintf(out, "  %-50s  %-20s  %s\n", "--------------------------------------------------", "--------------------", "--------")
		for _, u := range uncovered {
			fmt.Fprintf(out, "  %-50s  %-20s  %s\n", u.FilePath, u.Package, u.Language)
		}
	}

	// Print package summary.
	if len(pkgStats) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Coverage by package:")
		fmt.Fprintf(out, "  %-40s  %8s  %8s  %8s\n", "Package", "Total", "Covered", "Percent")
		fmt.Fprintf(out, "  %-40s  %8s  %8s  %8s\n", "----------------------------------------", "--------", "--------", "--------")
		for _, p := range pkgStats {
			fmt.Fprintf(out, "  %-40s  %8d  %8d  %7.1f%%\n", p.Package, p.Total, p.Covered, p.Percent)
		}
	}

	return nil
}

func runFunctionCoverage(ctx context.Context, cmd *cobra.Command, store graph.Store, pkg, language string, jsonOut bool) error {
	// Query Function and Method nodes.
	types := []graph.NodeType{graph.NodeFunction, graph.NodeMethod}
	var allFuncs []*graph.Node
	for _, t := range types {
		nodes, err := store.QueryNodes(ctx, graph.NodeFilter{
			Type:     t,
			Package:  pkg,
			Language: language,
		})
		if err != nil {
			return fmt.Errorf("query %s nodes: %w", t, err)
		}
		allFuncs = append(allFuncs, nodes...)
	}

	// Filter out test functions.
	var sourceFuncs []*graph.Node
	for _, f := range allFuncs {
		if f.Type == graph.NodeTestFunction {
			continue
		}
		lang := f.Language
		if lang == "" {
			lang = inferLangFromPath(f.FilePath)
		}
		if isTestFuncByName(f.Name, lang, f.FilePath) {
			continue
		}
		if isTestFileByPath(f.FilePath, lang) {
			continue
		}
		sourceFuncs = append(sourceFuncs, f)
	}

	// Check coverage for each function.
	var entries []coverageEntry
	for _, f := range sourceFuncs {
		edges, err := store.GetEdges(ctx, f.ID, graph.EdgeTests)
		if err != nil {
			return fmt.Errorf("get edges for %s: %w", f.Name, err)
		}

		covered := false
		for _, e := range edges {
			if e.TargetID == f.ID {
				covered = true
				break
			}
		}

		entries = append(entries, coverageEntry{
			ID:       f.ID,
			Name:     f.Name,
			Type:     f.Type,
			FilePath: f.FilePath,
			Line:     f.Line,
			Package:  f.Package,
			Language: f.Language,
			Covered:  covered,
		})
	}

	// Sort by file path then line.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].FilePath != entries[j].FilePath {
			return entries[i].FilePath < entries[j].FilePath
		}
		return entries[i].Line < entries[j].Line
	})

	// Compute per-package coverage.
	pkgStats := computePackageCoverage(entries)

	out := cmd.OutOrStdout()

	if jsonOut {
		result := struct {
			Functions []coverageEntry   `json:"functions"`
			Packages  []packageCoverage `json:"packages"`
		}{
			Functions: entries,
			Packages:  pkgStats,
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	// Print uncovered functions.
	var uncovered []coverageEntry
	for _, e := range entries {
		if !e.Covered {
			uncovered = append(uncovered, e)
		}
	}

	if len(uncovered) == 0 {
		fmt.Fprintln(out, "All functions have test coverage.")
	} else {
		fmt.Fprintf(out, "Uncovered functions (%d of %d):\n\n", len(uncovered), len(entries))
		fmt.Fprintf(out, "  %-10s  %-40s  %s\n", "Type", "Name", "Location")
		fmt.Fprintf(out, "  %-10s  %-40s  %s\n", "----------", "----------------------------------------", "--------")
		for _, u := range uncovered {
			loc := u.FilePath
			if u.Line > 0 {
				loc = fmt.Sprintf("%s:%d", u.FilePath, u.Line)
			}
			fmt.Fprintf(out, "  %-10s  %-40s  %s\n", u.Type, u.Name, loc)
		}
	}

	// Print package summary.
	if len(pkgStats) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Coverage by package:")
		fmt.Fprintf(out, "  %-40s  %8s  %8s  %8s\n", "Package", "Total", "Covered", "Percent")
		fmt.Fprintf(out, "  %-40s  %8s  %8s  %8s\n", "----------------------------------------", "--------", "--------", "--------")
		for _, p := range pkgStats {
			fmt.Fprintf(out, "  %-40s  %8d  %8d  %7.1f%%\n", p.Package, p.Total, p.Covered, p.Percent)
		}
	}

	return nil
}

// computePackageCoverage aggregates coverage statistics per package.
func computePackageCoverage(entries []coverageEntry) []packageCoverage {
	type pkgAccum struct {
		total    int
		covered  int
		language string
	}
	byPkg := make(map[string]*pkgAccum)
	for _, e := range entries {
		pkg := e.Package
		if pkg == "" {
			pkg = "(unknown)"
		}
		acc, ok := byPkg[pkg]
		if !ok {
			acc = &pkgAccum{language: e.Language}
			byPkg[pkg] = acc
		}
		acc.total++
		if e.Covered {
			acc.covered++
		}
	}

	result := make([]packageCoverage, 0, len(byPkg))
	for pkg, acc := range byPkg {
		pct := 0.0
		if acc.total > 0 {
			pct = float64(acc.covered) / float64(acc.total) * 100.0
		}
		result = append(result, packageCoverage{
			Package:  pkg,
			Total:    acc.total,
			Covered:  acc.covered,
			Percent:  pct,
			Language: acc.language,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Package < result[j].Package
	})

	return result
}

// --- Test file/function detection helpers ---
// These duplicate the heuristics from internal/linker/tests.go to avoid
// pulling in the linker package (which depends on pkg/llm).

// isTestFileByPath returns true if the file path matches test file naming conventions.
func isTestFileByPath(filePath, language string) bool {
	base := filepath.Base(filePath)
	if language == "" {
		language = inferLangFromPath(filePath)
	}
	switch language {
	case "go":
		return strings.HasSuffix(base, "_test.go")
	case "python":
		return (strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py")) ||
			strings.HasSuffix(base, "_test.py")
	case "typescript":
		return strings.HasSuffix(base, ".test.ts") || strings.HasSuffix(base, ".spec.ts") ||
			strings.HasSuffix(base, ".test.tsx") || strings.HasSuffix(base, ".spec.tsx")
	case "javascript":
		return strings.HasSuffix(base, ".test.js") || strings.HasSuffix(base, ".spec.js") ||
			strings.HasSuffix(base, ".test.jsx") || strings.HasSuffix(base, ".spec.jsx")
	case "java":
		name := strings.TrimSuffix(base, ".java")
		return strings.HasSuffix(base, ".java") &&
			(strings.HasSuffix(name, "Test") || strings.HasSuffix(name, "Tests") ||
				strings.HasPrefix(name, "Test") || strings.HasSuffix(name, "IT"))
	}
	return false
}

// isTestFuncByName returns true if the function name matches test function naming conventions.
func isTestFuncByName(name, language, filePath string) bool {
	if language == "" {
		language = inferLangFromPath(filePath)
	}
	switch language {
	case "go":
		return strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Benchmark") ||
			strings.HasPrefix(name, "Example") || strings.HasPrefix(name, "Fuzz")
	case "python":
		return strings.HasPrefix(name, "test_") && isTestFileByPath(filePath, language)
	case "typescript", "javascript":
		return isTestFileByPath(filePath, language)
	case "java":
		return strings.HasPrefix(name, "test")
	}
	return false
}

// inferLangFromPath guesses the language from the file extension.
func inferLangFromPath(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx":
		return "javascript"
	case ".java":
		return "java"
	}
	return ""
}
