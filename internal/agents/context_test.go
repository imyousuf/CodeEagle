package agents

import (
	"context"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

// setupTestGraph creates an embedded store with a small test graph:
//
//	File: src/handler.go
//	  - Function: HandleRequest (exported, line 10-25)
//	  - Struct: RequestConfig (exported, line 30-45)
//
//	File: src/service.go
//	  - Function: ProcessOrder (exported, line 5-20)
//	  - Function: validateInput (unexported, line 22-30)
//
//	Edges:
//	  ProcessOrder --Calls--> HandleRequest
//	  service.go --Imports--> handler.go (via package-level import node)
func setupTestGraph(t *testing.T) graph.Store {
	t.Helper()

	store, err := embedded.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	ctx := context.Background()

	// File 1: handler.go
	handlerFile := &graph.Node{
		ID:       graph.NewNodeID("File", "src/handler.go", "handler.go"),
		Type:     graph.NodeFile,
		Name:     "handler.go",
		FilePath: "src/handler.go",
		Package:  "handler",
		Language: "go",
	}
	handleReq := &graph.Node{
		ID:            graph.NewNodeID("Function", "src/handler.go", "HandleRequest"),
		Type:          graph.NodeFunction,
		Name:          "HandleRequest",
		QualifiedName: "handler.HandleRequest",
		FilePath:      "src/handler.go",
		Line:          10,
		EndLine:       25,
		Package:       "handler",
		Language:      "go",
		Exported:      true,
		Signature:     "func HandleRequest(ctx context.Context, req *Request) error",
		Metrics: map[string]float64{
			"cyclomatic_complexity": 5,
			"lines_of_code":        15,
		},
	}
	reqConfig := &graph.Node{
		ID:            graph.NewNodeID("Struct", "src/handler.go", "RequestConfig"),
		Type:          graph.NodeStruct,
		Name:          "RequestConfig",
		QualifiedName: "handler.RequestConfig",
		FilePath:      "src/handler.go",
		Line:          30,
		EndLine:       45,
		Package:       "handler",
		Language:      "go",
		Exported:      true,
		Metrics: map[string]float64{
			"fields": 4,
		},
	}

	// File 2: service.go
	serviceFile := &graph.Node{
		ID:       graph.NewNodeID("File", "src/service.go", "service.go"),
		Type:     graph.NodeFile,
		Name:     "service.go",
		FilePath: "src/service.go",
		Package:  "service",
		Language: "go",
	}
	processOrder := &graph.Node{
		ID:            graph.NewNodeID("Function", "src/service.go", "ProcessOrder"),
		Type:          graph.NodeFunction,
		Name:          "ProcessOrder",
		QualifiedName: "service.ProcessOrder",
		FilePath:      "src/service.go",
		Line:          5,
		EndLine:       20,
		Package:       "service",
		Language:      "go",
		Exported:      true,
		Metrics: map[string]float64{
			"cyclomatic_complexity": 8,
			"lines_of_code":        15,
		},
	}
	validateInput := &graph.Node{
		ID:            graph.NewNodeID("Function", "src/service.go", "validateInput"),
		Type:          graph.NodeFunction,
		Name:          "validateInput",
		QualifiedName: "service.validateInput",
		FilePath:      "src/service.go",
		Line:          22,
		EndLine:       30,
		Package:       "service",
		Language:      "go",
		Exported:      false,
	}

	// Add all nodes.
	for _, n := range []*graph.Node{handlerFile, handleReq, reqConfig, serviceFile, processOrder, validateInput} {
		if err := store.AddNode(ctx, n); err != nil {
			t.Fatalf("add node %s: %v", n.Name, err)
		}
	}

	// Edge: ProcessOrder calls HandleRequest.
	callEdge := &graph.Edge{
		ID:       "edge-call-1",
		Type:     graph.EdgeCalls,
		SourceID: processOrder.ID,
		TargetID: handleReq.ID,
	}
	// Edge: service.go imports handler.go (represented as ProcessOrder imports HandleRequest's package).
	importEdge := &graph.Edge{
		ID:       "edge-import-1",
		Type:     graph.EdgeImports,
		SourceID: processOrder.ID,
		TargetID: handleReq.ID,
	}
	// Edge: handler.go file contains HandleRequest.
	containsEdge := &graph.Edge{
		ID:       "edge-contains-1",
		Type:     graph.EdgeContains,
		SourceID: handlerFile.ID,
		TargetID: handleReq.ID,
	}

	for _, e := range []*graph.Edge{callEdge, importEdge, containsEdge} {
		if err := store.AddEdge(ctx, e); err != nil {
			t.Fatalf("add edge %s: %v", e.ID, err)
		}
	}

	return store
}

func TestBuildFileContext(t *testing.T) {
	store := setupTestGraph(t)
	defer store.Close()

	cb := NewContextBuilder(store)
	ctx := context.Background()

	t.Run("file with symbols", func(t *testing.T) {
		result, err := cb.BuildFileContext(ctx, "src/handler.go")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Verify structure.
		if !strings.Contains(result, "## File: src/handler.go") {
			t.Error("missing file header")
		}
		if !strings.Contains(result, "Language: go") {
			t.Error("missing language")
		}
		if !strings.Contains(result, "HandleRequest") {
			t.Error("missing HandleRequest symbol")
		}
		if !strings.Contains(result, "RequestConfig") {
			t.Error("missing RequestConfig symbol")
		}
		if !strings.Contains(result, "[Function]") {
			t.Error("missing Function type annotation")
		}
		if !strings.Contains(result, "[Struct]") {
			t.Error("missing Struct type annotation")
		}
		if !strings.Contains(result, "exported") {
			t.Error("missing exported indicator")
		}
		if !strings.Contains(result, "(line 10-25)") {
			t.Error("missing line range for HandleRequest")
		}
		// HandleRequest has an incoming call from ProcessOrder (in service.go).
		if !strings.Contains(result, "### Dependents") {
			t.Error("missing dependents section")
		}
		if !strings.Contains(result, "src/service.go") {
			t.Error("missing dependent file src/service.go")
		}
	})

	t.Run("nonexistent file", func(t *testing.T) {
		result, err := cb.BuildFileContext(ctx, "src/nonexistent.go")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "No indexed symbols found") {
			t.Error("expected 'no indexed symbols' message")
		}
	})
}

func TestBuildServiceContext(t *testing.T) {
	store := setupTestGraph(t)
	defer store.Close()

	cb := NewContextBuilder(store)
	ctx := context.Background()

	result, err := cb.BuildServiceContext(ctx, "handler")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "## Service: handler") {
		t.Error("missing service header")
	}
	if !strings.Contains(result, "Total symbols:") {
		t.Error("missing total symbols count")
	}
	// Should include key types section with RequestConfig.
	if !strings.Contains(result, "RequestConfig") {
		t.Error("missing RequestConfig in service context")
	}
}

func TestBuildImpactContext(t *testing.T) {
	store := setupTestGraph(t)
	defer store.Close()

	cb := NewContextBuilder(store)
	ctx := context.Background()

	// HandleRequest is called by ProcessOrder, so impact analysis should find ProcessOrder.
	handleReqID := graph.NewNodeID("Function", "src/handler.go", "HandleRequest")
	result, err := cb.BuildImpactContext(ctx, handleReqID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "## Impact Analysis: HandleRequest") {
		t.Error("missing impact analysis header")
	}
	if !strings.Contains(result, "Direct impact") {
		t.Error("missing direct impact section")
	}
	if !strings.Contains(result, "ProcessOrder") {
		t.Error("missing ProcessOrder as direct dependent")
	}
}

func TestBuildDiffContext(t *testing.T) {
	store := setupTestGraph(t)
	defer store.Close()

	cb := NewContextBuilder(store)
	ctx := context.Background()

	t.Run("changed file with dependents", func(t *testing.T) {
		result, err := cb.BuildDiffContext(ctx, []string{"src/handler.go"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "## Changed Files Impact") {
			t.Error("missing diff context header")
		}
		if !strings.Contains(result, "### src/handler.go") {
			t.Error("missing file section header")
		}
		if !strings.Contains(result, "HandleRequest") {
			t.Error("missing symbol in changed file")
		}
		// service.go depends on handler.go via the call/import edges.
		if !strings.Contains(result, "src/service.go") {
			t.Error("missing affected file src/service.go")
		}
	})

	t.Run("unknown file", func(t *testing.T) {
		result, err := cb.BuildDiffContext(ctx, []string{"src/unknown.go"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "No indexed symbols") {
			t.Error("expected 'no indexed symbols' for unknown file")
		}
	})

	t.Run("empty list", func(t *testing.T) {
		result, err := cb.BuildDiffContext(ctx, []string{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "No changed files") {
			t.Error("expected 'no changed files' message")
		}
	})
}

func TestBuildOverviewContext(t *testing.T) {
	store := setupTestGraph(t)
	defer store.Close()

	cb := NewContextBuilder(store)
	ctx := context.Background()

	result, err := cb.BuildOverviewContext(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "## Knowledge Graph Overview") {
		t.Error("missing overview header")
	}
	if !strings.Contains(result, "Total nodes:") {
		t.Error("missing total nodes count")
	}
	if !strings.Contains(result, "Total edges:") {
		t.Error("missing total edges count")
	}
	if !strings.Contains(result, "Nodes by type") {
		t.Error("missing nodes by type section")
	}
	if !strings.Contains(result, "Function") {
		t.Error("missing Function node type in breakdown")
	}
	if !strings.Contains(result, "Language distribution") {
		t.Error("missing language distribution section")
	}
	if !strings.Contains(result, "go:") {
		t.Error("missing Go language in distribution")
	}
}

func TestBuildMetricsContext(t *testing.T) {
	store := setupTestGraph(t)
	defer store.Close()

	cb := NewContextBuilder(store)
	ctx := context.Background()

	t.Run("file with metrics", func(t *testing.T) {
		result, err := cb.BuildMetricsContext(ctx, "src/handler.go")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "## Metrics: src/handler.go") {
			t.Error("missing metrics header")
		}
		if !strings.Contains(result, "HandleRequest") {
			t.Error("missing HandleRequest metrics")
		}
		if !strings.Contains(result, "cyclomatic_complexity") {
			t.Error("missing cyclomatic complexity metric")
		}
		if !strings.Contains(result, "5.00") {
			t.Error("missing expected metric value 5.00")
		}
		if !strings.Contains(result, "RequestConfig") {
			t.Error("missing RequestConfig metrics")
		}
		if !strings.Contains(result, "fields") {
			t.Error("missing fields metric for RequestConfig")
		}
	})

	t.Run("file without metrics", func(t *testing.T) {
		// service.go has validateInput with no metrics.
		// But ProcessOrder has metrics, so check a file where nothing has them.
		result, err := cb.BuildMetricsContext(ctx, "src/nonexistent.go")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "No indexed symbols found") {
			t.Error("expected 'no indexed symbols' message")
		}
	})
}
