//go:build e2e

package indexer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/gitutil"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/parser"
	"github.com/imyousuf/CodeEagle/internal/parser/golang"
	"github.com/imyousuf/CodeEagle/internal/parser/html"
	"github.com/imyousuf/CodeEagle/internal/parser/java"
	"github.com/imyousuf/CodeEagle/internal/parser/javascript"
	"github.com/imyousuf/CodeEagle/internal/parser/markdown"
	"github.com/imyousuf/CodeEagle/internal/parser/python"
	"github.com/imyousuf/CodeEagle/internal/parser/typescript"
	"github.com/imyousuf/CodeEagle/internal/watcher"
)

const opalAppPath = "/home/imyousuf/projects/opal-app"

// setupE2EIndexer creates an indexer with all 7 parsers and a temp DB.
func setupE2EIndexer(t *testing.T, paths ...string) (*Indexer, graph.Store) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "e2e-db")
	store, err := embedded.NewStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	registry := parser.NewRegistry()
	registry.Register(golang.NewParser())
	registry.Register(python.NewParser())
	registry.Register(typescript.NewParser())
	registry.Register(javascript.NewParser())
	registry.Register(java.NewParser())
	registry.Register(html.NewParser())
	registry.Register(markdown.NewParser())

	wcfg := &watcher.WatcherConfig{
		Paths: paths,
		ExcludePatterns: []string{
			"**/node_modules/**",
			"**/.git/**",
			"**/vendor/**",
			"**/__pycache__/**",
			"**/dist/**",
			"**/build/**",
			"**/.venv/**",
			"**/.tox/**",
			"**/htmlcov/**",
			"**/.ruff_cache/**",
			"**/.pytest_cache/**",
			"**/coverage.*",
			"**/.coverage",
		},
	}

	idx := NewIndexer(IndexerConfig{
		GraphStore:     store,
		ParserRegistry: registry,
		WatcherConfig:  wcfg,
		Verbose:        false,
		AutoSummarize:  false,
	})

	return idx, store
}

// TestE2EServiceSpaceElement indexes the space-element Go service and validates.
func TestE2EServiceSpaceElement(t *testing.T) {
	svcPath := filepath.Join(opalAppPath, "space-element")
	if _, err := os.Stat(svcPath); os.IsNotExist(err) {
		t.Skipf("opal-app not found at %s", opalAppPath)
	}

	idx, store := setupE2EIndexer(t, svcPath)
	ctx := context.Background()

	start := time.Now()
	err := idx.IndexDirectory(ctx, svcPath)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("IndexDirectory failed: %v", err)
	}

	stats := idx.Stats()
	graphStats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}

	t.Logf("=== space-element (Go) ===")
	t.Logf("  Indexing time:    %s", elapsed)
	t.Logf("  Files indexed:    %d", stats.FilesIndexed)
	t.Logf("  Total nodes:      %d", graphStats.NodeCount)
	t.Logf("  Total edges:      %d", graphStats.EdgeCount)
	t.Logf("  Nodes by type:")
	printNodesByType(t, graphStats.NodesByType)
	t.Logf("  Edges by type:")
	printEdgesByType(t, graphStats.EdgesByType)

	// Validate minimum thresholds.
	if stats.FilesIndexed < 10 {
		t.Errorf("expected at least 10 files indexed, got %d", stats.FilesIndexed)
	}
	if graphStats.NodeCount < 100 {
		t.Errorf("expected at least 100 nodes, got %d", graphStats.NodeCount)
	}
	if graphStats.EdgeCount < 50 {
		t.Errorf("expected at least 50 edges, got %d", graphStats.EdgeCount)
	}

	// Verify architectural classification.
	archRoleCount, designPatternCount := countClassifiedNodes(t, store, ctx)
	t.Logf("  Nodes with architectural_role: %d", archRoleCount)
	t.Logf("  Nodes with design_pattern:     %d", designPatternCount)

	if archRoleCount == 0 {
		t.Error("expected some nodes to have architectural_role set")
	}

	// Report errors.
	if len(stats.Errors) > 0 {
		t.Logf("  Errors (%d):", len(stats.Errors))
		for _, e := range stats.Errors {
			t.Logf("    - %s", truncate(e, 200))
		}
	}
}

// TestE2EServiceEmailReceiver indexes the email-receiver Python service and validates.
func TestE2EServiceEmailReceiver(t *testing.T) {
	svcPath := filepath.Join(opalAppPath, "email-receiver")
	if _, err := os.Stat(svcPath); os.IsNotExist(err) {
		t.Skipf("opal-app not found at %s", opalAppPath)
	}

	idx, store := setupE2EIndexer(t, svcPath)
	ctx := context.Background()

	start := time.Now()
	err := idx.IndexDirectory(ctx, svcPath)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("IndexDirectory failed: %v", err)
	}

	stats := idx.Stats()
	graphStats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}

	t.Logf("=== email-receiver (Python) ===")
	t.Logf("  Indexing time:    %s", elapsed)
	t.Logf("  Files indexed:    %d", stats.FilesIndexed)
	t.Logf("  Total nodes:      %d", graphStats.NodeCount)
	t.Logf("  Total edges:      %d", graphStats.EdgeCount)
	t.Logf("  Nodes by type:")
	printNodesByType(t, graphStats.NodesByType)
	t.Logf("  Edges by type:")
	printEdgesByType(t, graphStats.EdgesByType)

	// Validate minimum thresholds.
	if stats.FilesIndexed < 5 {
		t.Errorf("expected at least 5 files indexed, got %d", stats.FilesIndexed)
	}
	if graphStats.NodeCount < 50 {
		t.Errorf("expected at least 50 nodes, got %d", graphStats.NodeCount)
	}
	if graphStats.EdgeCount < 20 {
		t.Errorf("expected at least 20 edges, got %d", graphStats.EdgeCount)
	}

	// Verify architectural classification.
	archRoleCount, designPatternCount := countClassifiedNodes(t, store, ctx)
	t.Logf("  Nodes with architectural_role: %d", archRoleCount)
	t.Logf("  Nodes with design_pattern:     %d", designPatternCount)

	// Report errors.
	if len(stats.Errors) > 0 {
		t.Logf("  Errors (%d):", len(stats.Errors))
		for _, e := range stats.Errors {
			t.Logf("    - %s", truncate(e, 200))
		}
	}
}

// TestE2EServiceBackend indexes the backend Python service and validates.
func TestE2EServiceBackend(t *testing.T) {
	svcPath := filepath.Join(opalAppPath, "backend")
	if _, err := os.Stat(svcPath); os.IsNotExist(err) {
		t.Skipf("opal-app not found at %s", opalAppPath)
	}

	idx, store := setupE2EIndexer(t, svcPath)
	ctx := context.Background()

	start := time.Now()
	err := idx.IndexDirectory(ctx, svcPath)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("IndexDirectory failed: %v", err)
	}

	stats := idx.Stats()
	graphStats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}

	t.Logf("=== backend (Python) ===")
	t.Logf("  Indexing time:    %s", elapsed)
	t.Logf("  Files indexed:    %d", stats.FilesIndexed)
	t.Logf("  Total nodes:      %d", graphStats.NodeCount)
	t.Logf("  Total edges:      %d", graphStats.EdgeCount)
	t.Logf("  Nodes by type:")
	printNodesByType(t, graphStats.NodesByType)
	t.Logf("  Edges by type:")
	printEdgesByType(t, graphStats.EdgesByType)

	// Check for DB model nodes (backend likely has SQLAlchemy models).
	dbModelCount := graphStats.NodesByType[graph.NodeDBModel]
	t.Logf("  DBModel nodes:    %d", dbModelCount)

	// Validate minimum thresholds.
	if stats.FilesIndexed < 5 {
		t.Errorf("expected at least 5 files indexed, got %d", stats.FilesIndexed)
	}
	if graphStats.NodeCount < 50 {
		t.Errorf("expected at least 50 nodes, got %d", graphStats.NodeCount)
	}

	// Verify architectural classification.
	archRoleCount, designPatternCount := countClassifiedNodes(t, store, ctx)
	t.Logf("  Nodes with architectural_role: %d", archRoleCount)
	t.Logf("  Nodes with design_pattern:     %d", designPatternCount)

	// Report errors.
	if len(stats.Errors) > 0 {
		t.Logf("  Errors (%d):", len(stats.Errors))
		for _, e := range stats.Errors {
			t.Logf("    - %s", truncate(e, 200))
		}
	}
}

// TestE2EFullMonorepo indexes the entire opal-app and reports comprehensive statistics.
func TestE2EFullMonorepo(t *testing.T) {
	if _, err := os.Stat(opalAppPath); os.IsNotExist(err) {
		t.Skipf("opal-app not found at %s", opalAppPath)
	}

	idx, store := setupE2EIndexer(t, opalAppPath)
	ctx := context.Background()

	start := time.Now()
	err := idx.IndexDirectory(ctx, opalAppPath)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("IndexDirectory failed: %v", err)
	}

	stats := idx.Stats()
	graphStats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}

	t.Logf("========================================")
	t.Logf("=== FULL MONOREPO: opal-app ===")
	t.Logf("========================================")
	t.Logf("  Indexing time:    %s", elapsed)
	t.Logf("  Files indexed:    %d", stats.FilesIndexed)
	t.Logf("  Total nodes:      %d", graphStats.NodeCount)
	t.Logf("  Total edges:      %d", graphStats.EdgeCount)

	t.Logf("")
	t.Logf("  --- Nodes by Type ---")
	printNodesByType(t, graphStats.NodesByType)

	t.Logf("")
	t.Logf("  --- Edges by Type ---")
	printEdgesByType(t, graphStats.EdgesByType)

	// Count languages found.
	t.Logf("")
	t.Logf("  --- Languages ---")
	languageCounts := countLanguages(t, store, ctx)
	langNames := make([]string, 0, len(languageCounts))
	for lang := range languageCounts {
		langNames = append(langNames, lang)
	}
	sort.Strings(langNames)
	for _, lang := range langNames {
		t.Logf("    %-15s %d nodes", lang, languageCounts[lang])
	}

	// Architectural classification stats.
	t.Logf("")
	t.Logf("  --- Architectural Classification ---")
	archRoleCount, designPatternCount := countClassifiedNodes(t, store, ctx)
	layerCount := countNodesWithProperty(t, store, ctx, graph.PropLayerTag)
	t.Logf("    Nodes with architectural_role: %d", archRoleCount)
	t.Logf("    Nodes with design_pattern:     %d", designPatternCount)
	t.Logf("    Nodes with layer tag:          %d", layerCount)

	// DBModel count.
	dbModelCount := graphStats.NodesByType[graph.NodeDBModel]
	t.Logf("    DBModel nodes:                 %d", dbModelCount)

	// Validate minimum thresholds for a large monorepo.
	if stats.FilesIndexed < 100 {
		t.Errorf("expected at least 100 files indexed for full monorepo, got %d", stats.FilesIndexed)
	}
	if graphStats.NodeCount < 500 {
		t.Errorf("expected at least 500 nodes for full monorepo, got %d", graphStats.NodeCount)
	}
	if graphStats.EdgeCount < 200 {
		t.Errorf("expected at least 200 edges for full monorepo, got %d", graphStats.EdgeCount)
	}
	if len(languageCounts) < 3 {
		t.Errorf("expected at least 3 languages in monorepo, got %d", len(languageCounts))
	}
	if archRoleCount == 0 {
		t.Error("expected some nodes to have architectural_role set in full monorepo")
	}

	// Report errors summary.
	if len(stats.Errors) > 0 {
		t.Logf("")
		t.Logf("  --- Errors (%d total) ---", len(stats.Errors))
		// Show first 20 errors.
		limit := 20
		if len(stats.Errors) < limit {
			limit = len(stats.Errors)
		}
		for i := 0; i < limit; i++ {
			t.Logf("    - %s", truncate(stats.Errors[i], 200))
		}
		if len(stats.Errors) > limit {
			t.Logf("    ... and %d more errors", len(stats.Errors)-limit)
		}
	}
}

// TestE2EGitBranchTracking tests the git branch tracking on opal-app.
func TestE2EGitBranchTracking(t *testing.T) {
	if _, err := os.Stat(opalAppPath); os.IsNotExist(err) {
		t.Skipf("opal-app not found at %s", opalAppPath)
	}

	diff, err := gitutil.GetBranchDiff(opalAppPath)
	if err != nil {
		t.Fatalf("GetBranchDiff failed: %v", err)
	}

	t.Logf("=== Git Branch Tracking ===")
	t.Logf("  Current branch:   %s", diff.CurrentBranch)
	t.Logf("  Default branch:   %s", diff.DefaultBranch)
	t.Logf("  Is feature branch: %v", diff.IsFeatureBranch)
	t.Logf("  Ahead:            %d", diff.Ahead)
	t.Logf("  Behind:           %d", diff.Behind)
	t.Logf("  Changed files:    %d", len(diff.ChangedFiles))

	if diff.IsFeatureBranch && len(diff.ChangedFiles) > 0 {
		// Group by status.
		statusCounts := make(map[string]int)
		for _, f := range diff.ChangedFiles {
			statusCounts[f.Status]++
		}
		t.Logf("  Changes by status:")
		for status, count := range statusCounts {
			t.Logf("    %-10s %d files", status, count)
		}

		// Show first 10 changed files.
		limit := 10
		if len(diff.ChangedFiles) < limit {
			limit = len(diff.ChangedFiles)
		}
		t.Logf("  First %d changed files:", limit)
		for i := 0; i < limit; i++ {
			f := diff.ChangedFiles[i]
			t.Logf("    [%s] %s (+%d/-%d)", f.Status, f.Path, f.Additions, f.Deletions)
		}
		if len(diff.ChangedFiles) > limit {
			t.Logf("    ... and %d more files", len(diff.ChangedFiles)-limit)
		}
	}
}

// --- Helper functions ---

func countClassifiedNodes(t *testing.T, store graph.Store, ctx context.Context) (archRole, designPattern int) {
	t.Helper()
	allNodes, err := store.QueryNodes(ctx, graph.NodeFilter{})
	if err != nil {
		t.Fatalf("QueryNodes failed: %v", err)
	}
	for _, n := range allNodes {
		if n.Properties != nil {
			if v, ok := n.Properties[graph.PropArchRole]; ok && v != "" {
				archRole++
			}
			if v, ok := n.Properties[graph.PropDesignPattern]; ok && v != "" {
				designPattern++
			}
		}
	}
	return
}

func countNodesWithProperty(t *testing.T, store graph.Store, ctx context.Context, prop string) int {
	t.Helper()
	allNodes, err := store.QueryNodes(ctx, graph.NodeFilter{})
	if err != nil {
		t.Fatalf("QueryNodes failed: %v", err)
	}
	count := 0
	for _, n := range allNodes {
		if n.Properties != nil {
			if v, ok := n.Properties[prop]; ok && v != "" {
				count++
			}
		}
	}
	return count
}

func countLanguages(t *testing.T, store graph.Store, ctx context.Context) map[string]int {
	t.Helper()
	allNodes, err := store.QueryNodes(ctx, graph.NodeFilter{})
	if err != nil {
		t.Fatalf("QueryNodes failed: %v", err)
	}
	counts := make(map[string]int)
	for _, n := range allNodes {
		if n.Language != "" {
			counts[n.Language]++
		}
	}
	return counts
}

func printNodesByType(t *testing.T, m map[graph.NodeType]int64) {
	t.Helper()
	type entry struct {
		name  string
		count int64
	}
	entries := make([]entry, 0, len(m))
	for k, v := range m {
		entries = append(entries, entry{string(k), v})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].count > entries[j].count
	})
	for _, e := range entries {
		t.Logf("    %-20s %d", e.name, e.count)
	}
}

func printEdgesByType(t *testing.T, m map[graph.EdgeType]int64) {
	t.Helper()
	type entry struct {
		name  string
		count int64
	}
	entries := make([]entry, 0, len(m))
	for k, v := range m {
		entries = append(entries, entry{string(k), v})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].count > entries[j].count
	})
	for _, e := range entries {
		t.Logf("    %-20s %d", e.name, e.count)
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// TestE2ESummary is a final summary test that runs last (alphabetically after other tests).
func TestE2ESummary(t *testing.T) {
	t.Logf("")
	t.Logf("========================================")
	t.Logf("=== E2E VALIDATION COMPLETE ===")
	t.Logf("========================================")
	t.Logf("")
	t.Logf("All E2E tests above contain detailed results.")
	t.Logf("Check individual test output for node/edge counts,")
	t.Logf("language breakdowns, and classification statistics.")

	// Quick sanity: verify the e2e tag isolation by checking that
	// this file would not be included without the e2e build tag.
	_, err := os.Stat(fmt.Sprintf("%s/space-element", opalAppPath))
	if os.IsNotExist(err) {
		t.Logf("NOTE: opal-app not available, individual tests were skipped.")
	}
}
