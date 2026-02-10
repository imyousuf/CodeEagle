package agents

import (
	"context"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

func setupToolTestStore(t *testing.T) (graph.Store, *ContextBuilder, func()) {
	t.Helper()
	store, cleanup := setupTestStore(t)
	ctxBuilder := NewContextBuilder(store)
	return store, ctxBuilder, cleanup
}

func TestNewPlannerTools(t *testing.T) {
	_, ctxBuilder, cleanup := setupToolTestStore(t)
	defer cleanup()

	tools := NewPlannerTools(ctxBuilder)
	if len(tools) != 11 {
		t.Fatalf("expected 11 tools, got %d", len(tools))
	}

	expectedNames := []string{
		"get_graph_overview",
		"get_architecture_overview",
		"get_service_info",
		"get_file_info",
		"get_impact_analysis",
		"search_nodes",
		"get_model_info",
		"get_project_guidelines",
		"query_file_symbols",
		"query_interface_implementors",
		"query_node_edges",
	}
	for i, tool := range tools {
		if tool.Name() != expectedNames[i] {
			t.Errorf("tool %d: expected name %q, got %q", i, expectedNames[i], tool.Name())
		}
		if tool.Description() == "" {
			t.Errorf("tool %q has empty description", tool.Name())
		}
		if tool.Parameters() == nil {
			t.Errorf("tool %q has nil parameters", tool.Name())
		}
	}
}

func TestGraphOverviewTool(t *testing.T) {
	_, ctxBuilder, cleanup := setupToolTestStore(t)
	defer cleanup()

	tool := &graphOverviewTool{ctxBuilder: ctxBuilder}
	result, ok := tool.Execute(context.Background(), nil)
	if !ok {
		t.Fatalf("expected success, got error: %s", result)
	}
	if !strings.Contains(result, "Knowledge Graph Overview") {
		t.Errorf("expected overview content, got %q", result)
	}
	if !strings.Contains(result, "Total nodes:") {
		t.Errorf("expected node count in result")
	}
}

func TestArchitectureOverviewTool(t *testing.T) {
	_, ctxBuilder, cleanup := setupToolTestStore(t)
	defer cleanup()

	tool := &architectureOverviewTool{ctxBuilder: ctxBuilder}
	result, ok := tool.Execute(context.Background(), nil)
	if !ok {
		t.Fatalf("expected success, got error: %s", result)
	}
	if !strings.Contains(result, "Architecture Overview") {
		t.Errorf("expected architecture content, got %q", result)
	}
}

func TestServiceInfoTool(t *testing.T) {
	_, ctxBuilder, cleanup := setupToolTestStore(t)
	defer cleanup()

	tool := &serviceInfoTool{ctxBuilder: ctxBuilder}

	// Test with valid service name.
	result, ok := tool.Execute(context.Background(), map[string]any{"service_name": "auth"})
	if !ok {
		t.Fatalf("expected success, got error: %s", result)
	}
	if !strings.Contains(result, "Service: auth") {
		t.Errorf("expected service context, got %q", result)
	}

	// Test with missing service_name.
	result, ok = tool.Execute(context.Background(), map[string]any{})
	if ok {
		t.Error("expected failure for missing service_name")
	}
	if !strings.Contains(result, "service_name is required") {
		t.Errorf("expected error message, got %q", result)
	}
}

func TestFileInfoTool(t *testing.T) {
	_, ctxBuilder, cleanup := setupToolTestStore(t)
	defer cleanup()

	tool := &fileInfoTool{ctxBuilder: ctxBuilder}

	// Test with valid file path.
	result, ok := tool.Execute(context.Background(), map[string]any{"file_path": "cmd/main.go"})
	if !ok {
		t.Fatalf("expected success, got error: %s", result)
	}
	if !strings.Contains(result, "File: cmd/main.go") {
		t.Errorf("expected file context, got %q", result)
	}

	// Test with missing file_path.
	result, ok = tool.Execute(context.Background(), map[string]any{})
	if ok {
		t.Error("expected failure for missing file_path")
	}
	if !strings.Contains(result, "file_path is required") {
		t.Errorf("expected error message, got %q", result)
	}
}

func TestImpactAnalysisTool(t *testing.T) {
	_, ctxBuilder, cleanup := setupToolTestStore(t)
	defer cleanup()

	tool := &impactAnalysisTool{ctxBuilder: ctxBuilder}

	// Test with a matching entity name.
	result, ok := tool.Execute(context.Background(), map[string]any{"entity_name": "HandleRequest"})
	if !ok {
		t.Fatalf("expected success, got error: %s", result)
	}
	if !strings.Contains(result, "Impact Analysis") {
		t.Errorf("expected impact analysis, got %q", result)
	}

	// Test with non-matching entity.
	result, ok = tool.Execute(context.Background(), map[string]any{"entity_name": "NonExistentEntity"})
	if ok {
		t.Error("expected failure for non-matching entity")
	}
	if !strings.Contains(result, "No entity found") {
		t.Errorf("expected 'No entity found' message, got %q", result)
	}

	// Test with missing entity_name.
	result, ok = tool.Execute(context.Background(), map[string]any{})
	if ok {
		t.Error("expected failure for missing entity_name")
	}
	if !strings.Contains(result, "entity_name is required") {
		t.Errorf("expected error message, got %q", result)
	}
}

func TestSearchNodesTool(t *testing.T) {
	store, _, cleanup := setupToolTestStore(t)
	defer cleanup()

	tool := &searchNodesTool{store: store}

	// Search by name pattern.
	result, ok := tool.Execute(context.Background(), map[string]any{"name_pattern": "Handle*"})
	if !ok {
		t.Fatalf("expected success, got error: %s", result)
	}
	if !strings.Contains(result, "HandleRequest") {
		t.Errorf("expected HandleRequest in results, got %q", result)
	}

	// Search by node type.
	result, ok = tool.Execute(context.Background(), map[string]any{"node_type": "Function"})
	if !ok {
		t.Fatalf("expected success, got error: %s", result)
	}
	if !strings.Contains(result, "Function") {
		t.Errorf("expected Function type in results, got %q", result)
	}

	// Search by package.
	result, ok = tool.Execute(context.Background(), map[string]any{"package": "auth"})
	if !ok {
		t.Fatalf("expected success, got error: %s", result)
	}
	if !strings.Contains(result, "auth") {
		t.Errorf("expected auth package in results, got %q", result)
	}

	// Search by language.
	result, ok = tool.Execute(context.Background(), map[string]any{"language": "go"})
	if !ok {
		t.Fatalf("expected success, got error: %s", result)
	}
	if !strings.Contains(result, "go") {
		t.Errorf("expected go language in results")
	}

	// Search with no matches.
	result, ok = tool.Execute(context.Background(), map[string]any{"name_pattern": "ZZZNonExistent"})
	if ok {
		t.Error("expected failure for no matches")
	}
	if !strings.Contains(result, "No nodes found") {
		t.Errorf("expected 'No nodes found' message, got %q", result)
	}
}

func TestModelInfoTool(t *testing.T) {
	_, ctxBuilder, cleanup := setupToolTestStore(t)
	defer cleanup()

	tool := &modelInfoTool{ctxBuilder: ctxBuilder}

	// Test with empty service name (all models).
	result, ok := tool.Execute(context.Background(), map[string]any{})
	if !ok {
		t.Fatalf("expected success, got error: %s", result)
	}
	if !strings.Contains(result, "Data Models") {
		t.Errorf("expected model context, got %q", result)
	}
}

func TestProjectGuidelinesTool(t *testing.T) {
	_, ctxBuilder, cleanup := setupToolTestStore(t)
	defer cleanup()

	tool := &projectGuidelinesTool{ctxBuilder: ctxBuilder}

	// The test store has no guideline nodes, so we expect the fallback message.
	result, ok := tool.Execute(context.Background(), nil)
	if !ok {
		t.Fatalf("expected success, got error: %s", result)
	}
	if !strings.Contains(result, "No project guidelines") {
		t.Errorf("expected 'No project guidelines' message, got %q", result)
	}
}

func TestProjectGuidelinesToolWithData(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	// Add a guideline node.
	guidelineNode := &graph.Node{
		ID:       "guideline1",
		Type:     graph.NodeAIGuideline,
		Name:     "CLAUDE.md",
		FilePath: "/nonexistent/CLAUDE.md", // won't resolve, but tests the path
	}
	if err := store.AddNode(ctx, guidelineNode); err != nil {
		t.Fatalf("failed to add guideline node: %v", err)
	}

	ctxBuilder := NewContextBuilder(store)
	tool := &projectGuidelinesTool{ctxBuilder: ctxBuilder}

	result, ok := tool.Execute(ctx, nil)
	if !ok {
		// Even if the file can't be read, it should still return some output.
		t.Logf("tool returned with ok=false: %s", result)
	}
	// Should at least attempt to reference the guideline.
	if !strings.Contains(result, "Guidelines") && !strings.Contains(result, "CLAUDE.md") {
		t.Errorf("expected guidelines-related content, got %q", result)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 8, "hello..."},
		{"ab", 3, "ab"},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.maxLen)
		if got != tt.expected {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.expected)
		}
	}
}
