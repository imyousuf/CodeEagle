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
			"lines_of_code":         15,
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
			"lines_of_code":         15,
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

// setupArchTestGraph creates a graph with architectural metadata (models, patterns,
// roles, layers) for testing the new context builder methods.
func setupArchTestGraph(t *testing.T) graph.Store {
	t.Helper()

	store, err := embedded.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	ctx := context.Background()

	nodes := []*graph.Node{
		{
			ID:       graph.NewNodeID("DBModel", "src/models/user.go", "UserModel"),
			Type:     graph.NodeDBModel,
			Name:     "UserModel",
			FilePath: "src/models/user.go",
			Package:  "myservice",
			Language: "go",
			Exported: true,
			Properties: map[string]string{
				"fields":                "id, name, email, created_at",
				graph.PropArchRole:      "repository",
				graph.PropLayerTag:      "data_access",
				graph.PropDesignPattern: "repository",
			},
		},
		{
			ID:       graph.NewNodeID("DBModel", "src/models/order.go", "OrderModel"),
			Type:     graph.NodeDBModel,
			Name:     "OrderModel",
			FilePath: "src/models/order.go",
			Package:  "myservice",
			Language: "go",
			Exported: true,
			Properties: map[string]string{
				"fields":           "id, user_id, total, status",
				graph.PropLayerTag: "data_access",
			},
		},
		{
			ID:       graph.NewNodeID("DomainModel", "src/domain/user.go", "UserAggregate"),
			Type:     graph.NodeDomainModel,
			Name:     "UserAggregate",
			FilePath: "src/domain/user.go",
			Package:  "myservice",
			Language: "go",
			Exported: true,
			Properties: map[string]string{
				"fields":           "user, orders, preferences",
				graph.PropLayerTag: "business",
			},
		},
		{
			ID:       graph.NewNodeID("DTO", "src/dto/user.go", "UserResponse"),
			Type:     graph.NodeDTO,
			Name:     "UserResponse",
			FilePath: "src/dto/user.go",
			Package:  "myservice",
			Language: "go",
			Exported: true,
			Properties: map[string]string{
				"fields":           "id, name, email",
				graph.PropLayerTag: "presentation",
			},
		},
		{
			ID:       graph.NewNodeID("ViewModel", "src/views/order.go", "OrderView"),
			Type:     graph.NodeViewModel,
			Name:     "OrderView",
			FilePath: "src/views/order.go",
			Package:  "myservice",
			Language: "go",
			Exported: true,
			Properties: map[string]string{
				"fields":           "order_id, total, status_display",
				graph.PropLayerTag: "presentation",
			},
		},
		{
			ID:       graph.NewNodeID("Function", "src/controllers/user.go", "UserController"),
			Type:     graph.NodeFunction,
			Name:     "UserController",
			FilePath: "src/controllers/user.go",
			Package:  "myservice",
			Language: "go",
			Exported: true,
			Properties: map[string]string{
				graph.PropArchRole:      "controller",
				graph.PropLayerTag:      "presentation",
				graph.PropDesignPattern: "mvc",
			},
		},
		{
			ID:       graph.NewNodeID("Function", "src/services/user_svc.go", "UserService"),
			Type:     graph.NodeFunction,
			Name:     "UserService",
			FilePath: "src/services/user_svc.go",
			Package:  "myservice",
			Language: "go",
			Exported: true,
			Properties: map[string]string{
				graph.PropArchRole:      "service",
				graph.PropLayerTag:      "business",
				graph.PropDesignPattern: "repository,singleton",
			},
		},
		{
			ID:       graph.NewNodeID("Function", "src/repos/user_repo.go", "UserRepository"),
			Type:     graph.NodeFunction,
			Name:     "UserRepository",
			FilePath: "src/repos/user_repo.go",
			Package:  "myservice",
			Language: "go",
			Exported: true,
			Properties: map[string]string{
				graph.PropArchRole:      "repository",
				graph.PropLayerTag:      "data_access",
				graph.PropDesignPattern: "repository",
			},
		},
	}

	for _, n := range nodes {
		if err := store.AddNode(ctx, n); err != nil {
			t.Fatalf("add node %s: %v", n.Name, err)
		}
	}

	// Edges: UserController -> UserService -> UserRepository -> UserModel
	edges := []*graph.Edge{
		{
			ID:       "edge-ctrl-svc",
			Type:     graph.EdgeCalls,
			SourceID: nodes[5].ID, // UserController
			TargetID: nodes[6].ID, // UserService
		},
		{
			ID:       "edge-svc-repo",
			Type:     graph.EdgeCalls,
			SourceID: nodes[6].ID, // UserService
			TargetID: nodes[7].ID, // UserRepository
		},
		{
			ID:       "edge-repo-model",
			Type:     graph.EdgeDependsOn,
			SourceID: nodes[7].ID, // UserRepository
			TargetID: nodes[0].ID, // UserModel
		},
		{
			ID:       "edge-dto-model",
			Type:     graph.EdgeDependsOn,
			SourceID: nodes[3].ID, // UserResponse DTO
			TargetID: nodes[0].ID, // UserModel
		},
	}

	for _, e := range edges {
		if err := store.AddEdge(ctx, e); err != nil {
			t.Fatalf("add edge %s: %v", e.ID, err)
		}
	}

	return store
}

func TestBuildModelContext(t *testing.T) {
	store := setupArchTestGraph(t)
	defer store.Close()

	cb := NewContextBuilder(store)
	ctx := context.Background()

	t.Run("service with models", func(t *testing.T) {
		result, err := cb.BuildModelContext(ctx, "myservice")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "## Data Models: myservice") {
			t.Error("missing header")
		}
		if !strings.Contains(result, "### DB Models") {
			t.Error("missing DB Models section")
		}
		if !strings.Contains(result, "UserModel") {
			t.Error("missing UserModel")
		}
		if !strings.Contains(result, "id, name, email, created_at") {
			t.Error("missing UserModel fields")
		}
		if !strings.Contains(result, "OrderModel") {
			t.Error("missing OrderModel")
		}
		if !strings.Contains(result, "### Domain Models") {
			t.Error("missing Domain Models section")
		}
		if !strings.Contains(result, "UserAggregate") {
			t.Error("missing UserAggregate")
		}
		if !strings.Contains(result, "### View Models / DTOs") {
			t.Error("missing View Models / DTOs section")
		}
		if !strings.Contains(result, "UserResponse") {
			t.Error("missing UserResponse DTO")
		}
		if !strings.Contains(result, "OrderView") {
			t.Error("missing OrderView")
		}
	})

	t.Run("nonexistent service", func(t *testing.T) {
		result, err := cb.BuildModelContext(ctx, "nosuchservice")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "No data models found") {
			t.Error("expected 'no data models found' message")
		}
	})
}

func TestBuildArchitectureContext(t *testing.T) {
	store := setupArchTestGraph(t)
	defer store.Close()

	cb := NewContextBuilder(store)
	ctx := context.Background()

	result, err := cb.BuildArchitectureContext(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "## Architecture Overview") {
		t.Error("missing header")
	}
	if !strings.Contains(result, "### Detected Design Patterns") {
		t.Error("missing design patterns section")
	}
	if !strings.Contains(result, "repository") {
		t.Error("missing repository pattern")
	}
	if !strings.Contains(result, "mvc") {
		t.Error("missing mvc pattern")
	}
	if !strings.Contains(result, "singleton") {
		t.Error("missing singleton pattern")
	}
	if !strings.Contains(result, "### Layer Distribution") {
		t.Error("missing layer distribution section")
	}
	if !strings.Contains(result, "presentation") {
		t.Error("missing presentation layer")
	}
	if !strings.Contains(result, "business") {
		t.Error("missing business layer")
	}
	if !strings.Contains(result, "data_access") {
		t.Error("missing data_access layer")
	}
	if !strings.Contains(result, "### Architectural Roles") {
		t.Error("missing architectural roles section")
	}
	if !strings.Contains(result, "controller") {
		t.Error("missing controller role")
	}
	if !strings.Contains(result, "service") {
		t.Error("missing service role")
	}
}

func TestBuildArchitectureContextEmpty(t *testing.T) {
	store := setupTestGraph(t) // original graph with no arch metadata
	defer store.Close()

	cb := NewContextBuilder(store)
	ctx := context.Background()

	result, err := cb.BuildArchitectureContext(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "No architectural metadata detected") {
		t.Error("expected 'no architectural metadata' message")
	}
}

func TestBuildModelImpactContext(t *testing.T) {
	store := setupArchTestGraph(t)
	defer store.Close()

	cb := NewContextBuilder(store)
	ctx := context.Background()

	// UserModel is depended on by UserRepository and UserResponse DTO.
	userModelID := graph.NewNodeID("DBModel", "src/models/user.go", "UserModel")
	result, err := cb.BuildModelImpactContext(ctx, userModelID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "## Model Impact Analysis: UserModel") {
		t.Error("missing header")
	}
	// UserRepository depends on UserModel.
	if !strings.Contains(result, "UserRepository") {
		t.Error("missing UserRepository as consumer")
	}
	// UserResponse DTO depends on UserModel.
	if !strings.Contains(result, "UserResponse") {
		t.Error("missing UserResponse as consumer")
	}
}

func TestBuildModelImpactContextNoConsumers(t *testing.T) {
	store := setupArchTestGraph(t)
	defer store.Close()

	cb := NewContextBuilder(store)
	ctx := context.Background()

	// UserController has no incoming model-related edges.
	controllerID := graph.NewNodeID("Function", "src/controllers/user.go", "UserController")
	result, err := cb.BuildModelImpactContext(ctx, controllerID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "No consumers found") {
		t.Error("expected 'no consumers found' message")
	}
}

func TestPropertyBasedFiltering(t *testing.T) {
	store := setupArchTestGraph(t)
	defer store.Close()

	ctx := context.Background()

	t.Run("filter by architectural role", func(t *testing.T) {
		nodes, err := store.QueryNodes(ctx, graph.NodeFilter{
			Properties: map[string]string{graph.PropArchRole: "controller"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(nodes) != 1 {
			t.Fatalf("expected 1 controller node, got %d", len(nodes))
		}
		if nodes[0].Name != "UserController" {
			t.Errorf("expected UserController, got %s", nodes[0].Name)
		}
	})

	t.Run("filter by layer", func(t *testing.T) {
		nodes, err := store.QueryNodes(ctx, graph.NodeFilter{
			Properties: map[string]string{graph.PropLayerTag: "presentation"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(nodes) != 3 {
			t.Fatalf("expected 3 presentation-layer nodes, got %d", len(nodes))
		}
	})

	t.Run("filter by design pattern substring", func(t *testing.T) {
		nodes, err := store.QueryNodes(ctx, graph.NodeFilter{
			Properties: map[string]string{graph.PropDesignPattern: "repository"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// UserModel, UserRepository, UserService ("repository,singleton" contains "repository")
		if len(nodes) != 3 {
			t.Fatalf("expected 3 nodes with repository pattern, got %d", len(nodes))
		}
	})

	t.Run("filter by multiple properties", func(t *testing.T) {
		nodes, err := store.QueryNodes(ctx, graph.NodeFilter{
			Properties: map[string]string{
				graph.PropArchRole: "service",
				graph.PropLayerTag: "business",
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(nodes) != 1 {
			t.Fatalf("expected 1 node, got %d", len(nodes))
		}
		if nodes[0].Name != "UserService" {
			t.Errorf("expected UserService, got %s", nodes[0].Name)
		}
	})
}

func TestBuildOverviewContextWithArchData(t *testing.T) {
	store := setupArchTestGraph(t)
	defer store.Close()

	cb := NewContextBuilder(store)
	ctx := context.Background()

	result, err := cb.BuildOverviewContext(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "### Architectural Patterns") {
		t.Error("missing architectural patterns in overview")
	}
	if !strings.Contains(result, "### Layer Distribution") {
		t.Error("missing layer distribution in overview")
	}
}

func TestBuildDiffContextWithArchImpact(t *testing.T) {
	store := setupArchTestGraph(t)
	defer store.Close()

	cb := NewContextBuilder(store)
	ctx := context.Background()

	result, err := cb.BuildDiffContext(ctx, []string{"src/models/user.go"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Architectural impact") {
		t.Error("missing architectural impact section")
	}
	if !strings.Contains(result, "WARNING") {
		t.Error("missing model change warning")
	}
	if !strings.Contains(result, "data model") {
		t.Error("missing data model warning text")
	}
}
