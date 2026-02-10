package agents

import (
	"context"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// setupQueryToolTestStore creates a store with richer data for query tool tests:
// an interface, a struct implementing it, function nodes, and edges.
func setupQueryToolTestStore(t *testing.T) (graph.Store, func()) {
	t.Helper()
	store, cleanup := setupTestStore(t) // inherits file1, func1 (HandleRequest), svc1, func2 (Login), edge1 (Login->HandleRequest Calls)

	ctx := context.Background()

	// Add an interface node.
	ifaceNode := &graph.Node{
		ID:            "iface1",
		Type:          graph.NodeInterface,
		Name:          "Store",
		QualifiedName: "graph.Store",
		FilePath:      "internal/graph/graph.go",
		Package:       "graph",
		Language:      "go",
		Exported:      true,
		Line:          28,
		EndLine:       67,
	}

	// Add a struct implementing the interface.
	structNode := &graph.Node{
		ID:            "struct1",
		Type:          graph.NodeStruct,
		Name:          "EmbeddedStore",
		QualifiedName: "embedded.EmbeddedStore",
		FilePath:      "internal/graph/embedded/store.go",
		Package:       "embedded",
		Language:      "go",
		Exported:      true,
		Line:          15,
		EndLine:       30,
	}

	// Add a method node.
	methodNode := &graph.Node{
		ID:            "method1",
		Type:          graph.NodeMethod,
		Name:          "AddNode",
		QualifiedName: "embedded.EmbeddedStore.AddNode",
		FilePath:      "internal/graph/embedded/store.go",
		Package:       "embedded",
		Language:      "go",
		Exported:      true,
		Line:          32,
		EndLine:       45,
	}

	for _, n := range []*graph.Node{ifaceNode, structNode, methodNode} {
		if err := store.AddNode(ctx, n); err != nil {
			t.Fatalf("failed to add node: %v", err)
		}
	}

	// Add Implements edge: EmbeddedStore -> Store.
	implEdge := &graph.Edge{
		ID:       "edge_impl1",
		Type:     graph.EdgeImplements,
		SourceID: "struct1",
		TargetID: "iface1",
	}
	// Add Contains edge: EmbeddedStore contains AddNode.
	containsEdge := &graph.Edge{
		ID:       "edge_contains1",
		Type:     graph.EdgeContains,
		SourceID: "struct1",
		TargetID: "method1",
	}

	for _, e := range []*graph.Edge{implEdge, containsEdge} {
		if err := store.AddEdge(ctx, e); err != nil {
			t.Fatalf("failed to add edge: %v", err)
		}
	}

	return store, cleanup
}

// --- query_file_symbols tests ---

func TestQueryFileSymbolsTool(t *testing.T) {
	store, cleanup := setupQueryToolTestStore(t)
	defer cleanup()

	tool := &queryFileSymbolsTool{store: store}

	result, ok := tool.Execute(context.Background(), map[string]any{"file_path": "cmd/main.go"})
	if !ok {
		t.Fatalf("expected success, got error: %s", result)
	}
	if !strings.Contains(result, "HandleRequest") {
		t.Errorf("expected HandleRequest in result, got %q", result)
	}
	if !strings.Contains(result, "Function") {
		t.Errorf("expected Function type in result, got %q", result)
	}
	if !strings.Contains(result, "10-25") {
		t.Errorf("expected line range 10-25 in result, got %q", result)
	}
	if !strings.Contains(result, "yes") {
		t.Errorf("expected exported=yes in result, got %q", result)
	}
}

func TestQueryFileSymbolsToolMultipleSymbols(t *testing.T) {
	store, cleanup := setupQueryToolTestStore(t)
	defer cleanup()

	tool := &queryFileSymbolsTool{store: store}

	result, ok := tool.Execute(context.Background(), map[string]any{"file_path": "internal/graph/embedded/store.go"})
	if !ok {
		t.Fatalf("expected success, got error: %s", result)
	}
	// Should contain both EmbeddedStore and AddNode, sorted by line.
	if !strings.Contains(result, "EmbeddedStore") {
		t.Errorf("expected EmbeddedStore in result, got %q", result)
	}
	if !strings.Contains(result, "AddNode") {
		t.Errorf("expected AddNode in result, got %q", result)
	}
	// EmbeddedStore (line 15) should come before AddNode (line 32).
	storeIdx := strings.Index(result, "EmbeddedStore")
	addIdx := strings.Index(result, "AddNode")
	if storeIdx > addIdx {
		t.Errorf("expected EmbeddedStore before AddNode in sorted output")
	}
}

func TestQueryFileSymbolsToolNoFile(t *testing.T) {
	store, cleanup := setupQueryToolTestStore(t)
	defer cleanup()

	tool := &queryFileSymbolsTool{store: store}

	result, ok := tool.Execute(context.Background(), map[string]any{"file_path": "nonexistent.go"})
	if ok {
		t.Error("expected failure for nonexistent file")
	}
	if !strings.Contains(result, "No symbols found") {
		t.Errorf("expected 'No symbols found' message, got %q", result)
	}
}

func TestQueryFileSymbolsToolMissingParam(t *testing.T) {
	store, cleanup := setupQueryToolTestStore(t)
	defer cleanup()

	tool := &queryFileSymbolsTool{store: store}

	result, ok := tool.Execute(context.Background(), map[string]any{})
	if ok {
		t.Error("expected failure for missing file_path")
	}
	if !strings.Contains(result, "file_path is required") {
		t.Errorf("expected error message, got %q", result)
	}
}

// --- query_interface_implementors tests ---

func TestQueryInterfaceImplTool(t *testing.T) {
	store, cleanup := setupQueryToolTestStore(t)
	defer cleanup()

	tool := &queryInterfaceImplTool{store: store}

	result, ok := tool.Execute(context.Background(), map[string]any{"name": "Store"})
	if !ok {
		t.Fatalf("expected success, got error: %s", result)
	}
	if !strings.Contains(result, "Interface: Store") {
		t.Errorf("expected interface header, got %q", result)
	}
	if !strings.Contains(result, "EmbeddedStore") {
		t.Errorf("expected EmbeddedStore as implementor, got %q", result)
	}
	if !strings.Contains(result, "Implementors (1)") {
		t.Errorf("expected implementor count, got %q", result)
	}
}

func TestQueryInterfaceImplToolNoMatch(t *testing.T) {
	store, cleanup := setupQueryToolTestStore(t)
	defer cleanup()

	tool := &queryInterfaceImplTool{store: store}

	result, ok := tool.Execute(context.Background(), map[string]any{"name": "NonExistent"})
	if ok {
		t.Error("expected failure for no matching interface")
	}
	if !strings.Contains(result, "No interface found") {
		t.Errorf("expected 'No interface found' message, got %q", result)
	}
}

func TestQueryInterfaceImplToolMissingParam(t *testing.T) {
	store, cleanup := setupQueryToolTestStore(t)
	defer cleanup()

	tool := &queryInterfaceImplTool{store: store}

	result, ok := tool.Execute(context.Background(), map[string]any{})
	if ok {
		t.Error("expected failure for missing name")
	}
	if !strings.Contains(result, "name is required") {
		t.Errorf("expected error message, got %q", result)
	}
}

// --- query_node_edges tests ---

func TestQueryNodeEdgesTool(t *testing.T) {
	store, cleanup := setupQueryToolTestStore(t)
	defer cleanup()

	tool := &queryNodeEdgesTool{store: store}

	// HandleRequest has an incoming Calls edge from Login.
	result, ok := tool.Execute(context.Background(), map[string]any{"node": "HandleRequest"})
	if !ok {
		t.Fatalf("expected success, got error: %s", result)
	}
	if !strings.Contains(result, "Edges for: HandleRequest") {
		t.Errorf("expected header, got %q", result)
	}
	if !strings.Contains(result, "Calls") {
		t.Errorf("expected Calls edge type, got %q", result)
	}
	if !strings.Contains(result, "Login") {
		t.Errorf("expected Login as peer, got %q", result)
	}
	if !strings.Contains(result, "incoming") {
		t.Errorf("expected incoming direction, got %q", result)
	}
}

func TestQueryNodeEdgesToolDirectionFilter(t *testing.T) {
	store, cleanup := setupQueryToolTestStore(t)
	defer cleanup()

	tool := &queryNodeEdgesTool{store: store}

	// EmbeddedStore has outgoing Implements and Contains edges.
	result, ok := tool.Execute(context.Background(), map[string]any{
		"node":      "EmbeddedStore",
		"direction": "out",
	})
	if !ok {
		t.Fatalf("expected success, got error: %s", result)
	}
	if !strings.Contains(result, "Implements") {
		t.Errorf("expected Implements edge, got %q", result)
	}
	if !strings.Contains(result, "Store") {
		t.Errorf("expected Store as peer, got %q", result)
	}

	// Filter incoming only - EmbeddedStore has no incoming edges.
	result, ok = tool.Execute(context.Background(), map[string]any{
		"node":      "EmbeddedStore",
		"direction": "in",
	})
	if !ok {
		t.Fatalf("expected success, got error: %s", result)
	}
	if !strings.Contains(result, "No edges match") {
		t.Errorf("expected 'No edges match' message, got %q", result)
	}
}

func TestQueryNodeEdgesToolEdgeTypeFilter(t *testing.T) {
	store, cleanup := setupQueryToolTestStore(t)
	defer cleanup()

	tool := &queryNodeEdgesTool{store: store}

	// Filter by Contains edge type for EmbeddedStore.
	result, ok := tool.Execute(context.Background(), map[string]any{
		"node":      "EmbeddedStore",
		"edge_type": "Contains",
	})
	if !ok {
		t.Fatalf("expected success, got error: %s", result)
	}
	if !strings.Contains(result, "Contains") {
		t.Errorf("expected Contains edge, got %q", result)
	}
	if !strings.Contains(result, "AddNode") {
		t.Errorf("expected AddNode as peer, got %q", result)
	}
	// Should not contain Implements edge.
	if strings.Contains(result, "Implements") {
		t.Errorf("expected no Implements edge when filtered by Contains, got %q", result)
	}
}

func TestQueryNodeEdgesToolNoMatch(t *testing.T) {
	store, cleanup := setupQueryToolTestStore(t)
	defer cleanup()

	tool := &queryNodeEdgesTool{store: store}

	result, ok := tool.Execute(context.Background(), map[string]any{"node": "ZZZNonExistent"})
	if ok {
		t.Error("expected failure for no matching node")
	}
	if !strings.Contains(result, "No node found") {
		t.Errorf("expected 'No node found' message, got %q", result)
	}
}

func TestQueryNodeEdgesToolMissingParam(t *testing.T) {
	store, cleanup := setupQueryToolTestStore(t)
	defer cleanup()

	tool := &queryNodeEdgesTool{store: store}

	result, ok := tool.Execute(context.Background(), map[string]any{})
	if ok {
		t.Error("expected failure for missing node")
	}
	if !strings.Contains(result, "node is required") {
		t.Errorf("expected error message, got %q", result)
	}
}

func TestQueryNodeEdgesToolNoEdges(t *testing.T) {
	store, cleanup := setupQueryToolTestStore(t)
	defer cleanup()

	tool := &queryNodeEdgesTool{store: store}

	// AuthService (svc1) has no edges in the test data.
	result, ok := tool.Execute(context.Background(), map[string]any{"node": "AuthService"})
	if !ok {
		t.Fatalf("expected success, got error: %s", result)
	}
	if !strings.Contains(result, "No edges found") {
		t.Errorf("expected 'No edges found' message, got %q", result)
	}
}
