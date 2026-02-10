package linker

import (
	"context"
	"fmt"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/pkg/llm"
)

func newTestStore(t *testing.T) graph.Store {
	t.Helper()
	store, err := embedded.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func addNodes(t *testing.T, store graph.Store, nodes ...*graph.Node) {
	t.Helper()
	ctx := context.Background()
	for _, n := range nodes {
		if err := store.AddNode(ctx, n); err != nil {
			t.Fatalf("add node %s: %v", n.Name, err)
		}
	}
}

func TestTopDir(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"hypatia/src/main.py", "hypatia"},
		{"frontend/src/App.tsx", "frontend"},
		{"main.py", "(root)"},
		{"", ""},
		{"backend/api/routes.go", "backend"},
	}
	for _, tt := range tests {
		got := topDir(tt.path)
		if got != tt.want {
			t.Errorf("topDir(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestNormalizeURLPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/api/v1/instances/{id}", "/api/v1/instances/*"},
		{"/api/v1/users/:user_id/posts", "/api/v1/users/*/posts"},
		{"/api/v1/items/<item_id>", "/api/v1/items/*"},
		{"/API/V1/Users/", "/api/v1/users"},
		{"api/v1/data", "/api/v1/data"},
		{"/simple", "/simple"},
	}
	for _, tt := range tests {
		got := normalizeURLPath(tt.input)
		if got != tt.want {
			t.Errorf("normalizeURLPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/api//v1///data", "/api/v1/data"},
		{"api/v1", "/api/v1"},
		{"/clean/path", "/clean/path"},
	}
	for _, tt := range tests {
		got := normalizePath(tt.input)
		if got != tt.want {
			t.Errorf("normalizePath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestMatchSegments(t *testing.T) {
	tests := []struct {
		a, b []string
		want bool
	}{
		{[]string{"", "api", "v1", "*"}, []string{"", "api", "v1", "*"}, true},
		{[]string{"", "api", "v1", "users"}, []string{"", "api", "v1", "*"}, true},
		{[]string{"", "api", "v1", "*"}, []string{"", "api", "v1", "data"}, true},
		{[]string{"", "api", "v1"}, []string{"", "api", "v2"}, false},
		{[]string{"", "api"}, []string{"", "api", "v1"}, false},
	}
	for _, tt := range tests {
		got := matchSegments(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("matchSegments(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestLinkServices(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Add file nodes in two different top-level directories.
	addNodes(t, store,
		&graph.Node{
			ID:   graph.NewNodeID("File", "hypatia/src/main.py", "main.py"),
			Type: graph.NodeFile, Name: "main.py",
			FilePath: "hypatia/src/main.py",
		},
		&graph.Node{
			ID:   graph.NewNodeID("File", "hypatia/src/routes.py", "routes.py"),
			Type: graph.NodeFile, Name: "routes.py",
			FilePath: "hypatia/src/routes.py",
		},
		&graph.Node{
			ID:   graph.NewNodeID("File", "frontend/src/App.tsx", "App.tsx"),
			Type: graph.NodeFile, Name: "App.tsx",
			FilePath: "frontend/src/App.tsx",
		},
	)

	linker := NewLinker(store, nil, nil, false)
	count, err := linker.linkServices(ctx)
	if err != nil {
		t.Fatalf("linkServices: %v", err)
	}
	if count != 2 {
		t.Errorf("linkServices returned %d, want 2", count)
	}

	// Verify service nodes were created.
	services, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeService})
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 2 {
		t.Errorf("got %d services, want 2", len(services))
	}

	// Verify services are auto-detected.
	for _, svc := range services {
		if svc.Properties["kind"] != "auto_detected" {
			t.Errorf("service %s has kind %q, want auto_detected", svc.Name, svc.Properties["kind"])
		}
	}
}

func TestLinkServicesWithExisting(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Add an existing service from manifest parsing.
	svcID := graph.NewNodeID("Service", "hypatia/pyproject.toml", "hypatia")
	addNodes(t, store,
		&graph.Node{
			ID: svcID, Type: graph.NodeService, Name: "hypatia",
			FilePath:   "hypatia/pyproject.toml",
			Properties: map[string]string{"kind": "service", "ecosystem": "python"},
		},
		&graph.Node{
			ID:   graph.NewNodeID("File", "hypatia/src/main.py", "main.py"),
			Type: graph.NodeFile, Name: "main.py",
			FilePath: "hypatia/src/main.py",
		},
	)

	linker := NewLinker(store, nil, nil, false)
	count, err := linker.linkServices(ctx)
	if err != nil {
		t.Fatalf("linkServices: %v", err)
	}
	if count != 1 {
		t.Errorf("linkServices returned %d, want 1", count)
	}

	// Verify no duplicate service was created.
	services, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeService})
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 {
		t.Errorf("got %d services, want 1 (existing)", len(services))
	}
	if services[0].Properties["kind"] != "service" {
		t.Error("existing service was overwritten")
	}
}

func TestLinkEndpoints(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create a service and an endpoint in the same top-level dir.
	svcID := graph.NewNodeID("Service", "backend", "backend")
	epID := graph.NewNodeID("APIEndpoint", "backend/routes.go", "GET /api/v1/users")

	addNodes(t, store,
		&graph.Node{
			ID: svcID, Type: graph.NodeService, Name: "backend",
			Properties: map[string]string{"kind": "auto_detected"},
		},
		&graph.Node{
			ID: epID, Type: graph.NodeAPIEndpoint, Name: "GET /api/v1/users",
			FilePath: "backend/routes.go",
			Properties: map[string]string{
				"http_method": "GET",
				"path":        "/api/v1/users",
				"framework":   "gin",
			},
		},
	)

	linker := NewLinker(store, nil, nil, false)
	count, err := linker.linkEndpoints(ctx)
	if err != nil {
		t.Fatalf("linkEndpoints: %v", err)
	}
	if count != 1 {
		t.Errorf("linkEndpoints returned %d, want 1", count)
	}

	// Verify EdgeExposes was created.
	edges, err := store.GetEdges(ctx, svcID, graph.EdgeExposes)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Errorf("got %d EdgeExposes, want 1", len(edges))
	}
	if edges[0].TargetID != epID {
		t.Errorf("EdgeExposes target = %s, want %s", edges[0].TargetID, epID)
	}
}

func TestLinkEndpointsWithRouterPrefix(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	svcID := graph.NewNodeID("Service", "hypatia", "hypatia")
	epID := graph.NewNodeID("APIEndpoint", "hypatia/routes/instances.py", "GET /instances/{id}")
	mountID := graph.NewNodeID("Variable", "hypatia/main.py", "router_mount")

	addNodes(t, store,
		&graph.Node{
			ID: svcID, Type: graph.NodeService, Name: "hypatia",
			Properties: map[string]string{"kind": "service"},
		},
		&graph.Node{
			ID: epID, Type: graph.NodeAPIEndpoint, Name: "GET /instances/{id}",
			FilePath: "hypatia/routes/instances.py",
			Properties: map[string]string{
				"http_method": "GET",
				"path":        "/instances/{id}",
				"framework":   "fastapi",
			},
		},
		&graph.Node{
			ID: mountID, Type: graph.NodeVariable, Name: "router_mount",
			FilePath: "hypatia/main.py",
			Properties: map[string]string{
				"kind":   "router_mount",
				"prefix": "/api/v1",
			},
		},
	)

	linker := NewLinker(store, nil, nil, false)
	count, err := linker.linkEndpoints(ctx)
	if err != nil {
		t.Fatalf("linkEndpoints: %v", err)
	}
	if count != 1 {
		t.Errorf("linkEndpoints returned %d, want 1", count)
	}

	// Verify full_path was set.
	ep, err := store.GetNode(ctx, epID)
	if err != nil {
		t.Fatal(err)
	}
	want := "/api/v1/instances/{id}"
	if ep.Properties["full_path"] != want {
		t.Errorf("full_path = %q, want %q", ep.Properties["full_path"], want)
	}
}

func TestLinkAPICalls(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create two services.
	backendSvcID := graph.NewNodeID("Service", "backend", "backend")
	frontendSvcID := graph.NewNodeID("Service", "frontend", "frontend")

	// Create an endpoint in backend.
	epID := graph.NewNodeID("APIEndpoint", "backend/routes.go", "GET /api/v1/users")

	// Create an API call in frontend.
	callID := graph.NewNodeID("Dependency", "frontend/src/api.ts", "fetch /api/v1/users")

	addNodes(t, store,
		&graph.Node{
			ID: backendSvcID, Type: graph.NodeService, Name: "backend",
			Properties: map[string]string{"kind": "auto_detected"},
		},
		&graph.Node{
			ID: frontendSvcID, Type: graph.NodeService, Name: "frontend",
			Properties: map[string]string{"kind": "auto_detected"},
		},
		&graph.Node{
			ID: epID, Type: graph.NodeAPIEndpoint, Name: "GET /api/v1/users",
			FilePath: "backend/routes.go",
			Properties: map[string]string{
				"http_method": "GET",
				"path":        "/api/v1/users",
				"framework":   "gin",
			},
		},
		&graph.Node{
			ID: callID, Type: graph.NodeDependency, Name: "fetch /api/v1/users",
			FilePath: "frontend/src/api.ts",
			Properties: map[string]string{
				"kind":        "api_call",
				"http_method": "GET",
				"path":        "/api/v1/users",
				"framework":   "fetch",
			},
		},
	)

	linker := NewLinker(store, nil, nil, false)
	count, err := linker.linkAPICalls(ctx)
	if err != nil {
		t.Fatalf("linkAPICalls: %v", err)
	}
	if count != 1 {
		t.Errorf("linkAPICalls returned %d, want 1", count)
	}

	// Verify EdgeConsumes was created.
	edges, err := store.GetEdges(ctx, callID, graph.EdgeConsumes)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Errorf("got %d EdgeConsumes, want 1", len(edges))
	}

	// Verify service-level EdgeDependsOn was created.
	depEdges, err := store.GetEdges(ctx, frontendSvcID, graph.EdgeDependsOn)
	if err != nil {
		t.Fatal(err)
	}
	if len(depEdges) != 1 {
		t.Errorf("got %d service EdgeDependsOn, want 1", len(depEdges))
	}
}

func TestLinkAPICallsWithPathParams(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	epID := graph.NewNodeID("APIEndpoint", "backend/routes.go", "GET /api/v1/users/{id}")
	callID := graph.NewNodeID("Dependency", "frontend/src/api.ts", "fetch /api/v1/users/123")

	addNodes(t, store,
		&graph.Node{
			ID: epID, Type: graph.NodeAPIEndpoint, Name: "GET /api/v1/users/{id}",
			FilePath: "backend/routes.go",
			Properties: map[string]string{
				"http_method": "GET",
				"path":        "/api/v1/users/{id}",
			},
		},
		&graph.Node{
			ID: callID, Type: graph.NodeDependency, Name: "fetch /api/v1/users/*",
			FilePath: "frontend/src/api.ts",
			Properties: map[string]string{
				"kind":        "api_call",
				"http_method": "GET",
				"path":        "/api/v1/users/*",
			},
		},
	)

	linker := NewLinker(store, nil, nil, false)
	count, err := linker.linkAPICalls(ctx)
	if err != nil {
		t.Fatalf("linkAPICalls: %v", err)
	}
	if count != 1 {
		t.Errorf("linkAPICalls returned %d, want 1 (wildcard match)", count)
	}
}

func TestLinkDependencies(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Service A depends on package "llm-framework" which is provided by Service B.
	svcAID := graph.NewNodeID("Service", "hypatia/pyproject.toml", "hypatia")
	svcBID := graph.NewNodeID("Service", "llm-library/pyproject.toml", "llm-framework")

	depID := graph.NewNodeID("Dependency", "hypatia/pyproject.toml", "llm-framework")

	addNodes(t, store,
		&graph.Node{
			ID: svcAID, Type: graph.NodeService, Name: "hypatia",
			FilePath:   "hypatia/pyproject.toml",
			Properties: map[string]string{"kind": "service", "ecosystem": "python"},
		},
		&graph.Node{
			ID: svcBID, Type: graph.NodeService, Name: "llm-framework",
			FilePath:   "llm-library/pyproject.toml",
			Properties: map[string]string{"kind": "library", "ecosystem": "python"},
		},
		&graph.Node{
			ID: depID, Type: graph.NodeDependency, Name: "llm-framework",
			FilePath: "hypatia/pyproject.toml",
			Properties: map[string]string{
				"kind":      "manifest_dep",
				"version":   "==0.2.46",
				"ecosystem": "python",
			},
		},
	)

	linker := NewLinker(store, nil, nil, false)
	count, err := linker.linkDependencies(ctx)
	if err != nil {
		t.Fatalf("linkDependencies: %v", err)
	}
	if count != 1 {
		t.Errorf("linkDependencies returned %d, want 1", count)
	}

	// Verify EdgeDependsOn was created.
	edges, err := store.GetEdges(ctx, svcAID, graph.EdgeDependsOn)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Errorf("got %d EdgeDependsOn, want 1", len(edges))
	}
	if edges[0].Properties["dep"] != "llm-framework" {
		t.Errorf("dep property = %q, want llm-framework", edges[0].Properties["dep"])
	}
}

func TestLinkDependenciesNoSelfRef(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Service that depends on itself (e.g., from internal dev requirements).
	svcID := graph.NewNodeID("Service", "mylib/pyproject.toml", "mylib")
	depID := graph.NewNodeID("Dependency", "mylib/pyproject.toml", "mylib")

	addNodes(t, store,
		&graph.Node{
			ID: svcID, Type: graph.NodeService, Name: "mylib",
			FilePath:   "mylib/pyproject.toml",
			Properties: map[string]string{"kind": "library"},
		},
		&graph.Node{
			ID: depID, Type: graph.NodeDependency, Name: "mylib",
			FilePath:   "mylib/pyproject.toml",
			Properties: map[string]string{"kind": "manifest_dep"},
		},
	)

	linker := NewLinker(store, nil, nil, false)
	count, err := linker.linkDependencies(ctx)
	if err != nil {
		t.Fatalf("linkDependencies: %v", err)
	}
	if count != 0 {
		t.Errorf("linkDependencies returned %d, want 0 (no self-ref)", count)
	}
}

func TestVersionConflictDetection(t *testing.T) {
	var logs []string
	logFn := func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}

	deps := []*graph.Node{
		{
			Name:     "requests",
			FilePath: "svc-a/requirements.txt",
			Properties: map[string]string{
				"kind":    "manifest_dep",
				"version": "==2.28.0",
			},
		},
		{
			Name:     "requests",
			FilePath: "svc-b/requirements.txt",
			Properties: map[string]string{
				"kind":    "manifest_dep",
				"version": "==2.31.0",
			},
		},
	}

	linker := &Linker{log: logFn, verbose: true}
	linker.detectVersionConflicts(deps)

	found := false
	for _, msg := range logs {
		if contains(msg, "Version conflict") && contains(msg, "requests") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected version conflict warning for 'requests'")
	}
}

func TestRunAll(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create a minimal graph with a file, endpoint, and API call.
	addNodes(t, store,
		// Backend service files + endpoint.
		&graph.Node{
			ID:   graph.NewNodeID("File", "backend/main.go", "main.go"),
			Type: graph.NodeFile, Name: "main.go",
			FilePath: "backend/main.go",
		},
		&graph.Node{
			ID:   graph.NewNodeID("APIEndpoint", "backend/routes.go", "GET /api/users"),
			Type: graph.NodeAPIEndpoint, Name: "GET /api/users",
			FilePath: "backend/routes.go",
			Properties: map[string]string{
				"http_method": "GET",
				"path":        "/api/users",
			},
		},
		// Frontend service files + API call.
		&graph.Node{
			ID:   graph.NewNodeID("File", "frontend/src/App.tsx", "App.tsx"),
			Type: graph.NodeFile, Name: "App.tsx",
			FilePath: "frontend/src/App.tsx",
		},
		&graph.Node{
			ID:   graph.NewNodeID("Dependency", "frontend/src/api.ts", "fetch /api/users"),
			Type: graph.NodeDependency, Name: "fetch /api/users",
			FilePath: "frontend/src/api.ts",
			Properties: map[string]string{
				"kind":        "api_call",
				"http_method": "GET",
				"path":        "/api/users",
			},
		},
	)

	var logs []string
	logFn := func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}

	linker := NewLinker(store, nil, logFn, true)
	err := linker.RunAll(ctx)
	if err != nil {
		t.Fatalf("RunAll: %v", err)
	}

	// Verify services were created.
	services, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeService})
	if err != nil {
		t.Fatal(err)
	}
	if len(services) < 2 {
		t.Errorf("got %d services, want >= 2", len(services))
	}

	// Verify verbose logging happened.
	if len(logs) == 0 {
		t.Error("expected verbose log output")
	}
}

// mockLLMClient implements llm.Client for testing LLM-assisted linking.
type mockLLMClient struct {
	response string
	err      error
}

func (m *mockLLMClient) Chat(_ context.Context, _ string, _ []llm.Message) (*llm.Response, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &llm.Response{Content: m.response}, nil
}

func (m *mockLLMClient) Model() string    { return "test-model" }
func (m *mockLLMClient) Provider() string { return "test" }
func (m *mockLLMClient) Close() error     { return nil }

func TestLLMAnalyzeUnresolvedCalls(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create an endpoint and an unresolved API call.
	epID := graph.NewNodeID("APIEndpoint", "backend/routes.go", "POST /api/v1/execute")
	callID := graph.NewNodeID("Dependency", "frontend/src/agent.ts", "fetch /execute")

	addNodes(t, store,
		&graph.Node{
			ID: epID, Type: graph.NodeAPIEndpoint, Name: "POST /api/v1/execute",
			FilePath: "backend/routes.go",
			Properties: map[string]string{
				"http_method": "POST",
				"path":        "/api/v1/execute",
				"framework":   "gin",
			},
		},
		&graph.Node{
			ID: callID, Type: graph.NodeDependency, Name: "fetch /execute",
			FilePath: "frontend/src/agent.ts",
			Properties: map[string]string{
				"kind":        "api_call",
				"http_method": "POST",
				"path":        "/execute",
				"framework":   "fetch",
			},
		},
		// Add services.
		&graph.Node{
			ID:   graph.NewNodeID("Service", "backend", "backend"),
			Type: graph.NodeService, Name: "backend",
			Properties: map[string]string{"kind": "auto_detected"},
		},
		&graph.Node{
			ID:   graph.NewNodeID("Service", "frontend", "frontend"),
			Type: graph.NodeService, Name: "frontend",
			Properties: map[string]string{"kind": "auto_detected"},
		},
	)

	mockClient := &mockLLMClient{
		response: `[{"endpoint_path": "/api/v1/execute", "confidence": "high", "reason": "Function name suggests agent execution"}]`,
	}

	linker := NewLinker(store, mockClient, nil, false)
	count, err := linker.llmAnalyzeUnresolvedCalls(ctx)
	if err != nil {
		t.Fatalf("llmAnalyzeUnresolvedCalls: %v", err)
	}
	if count != 1 {
		t.Errorf("llmAnalyzeUnresolvedCalls returned %d, want 1", count)
	}

	// Verify EdgeConsumes was created with LLM metadata.
	edges, err := store.GetEdges(ctx, callID, graph.EdgeConsumes)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Errorf("got %d EdgeConsumes, want 1", len(edges))
	}
	if len(edges) > 0 && edges[0].Properties["inferred"] != "true" {
		t.Error("expected inferred=true on LLM edge")
	}
}

func TestLLMAnalyzeNoClient(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	linker := NewLinker(store, nil, nil, false)
	count, err := linker.llmAnalyzeUnresolvedCalls(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 when no LLM client, got %d", count)
	}
}

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{
			`Here are the matches: [{"endpoint_path": "/api/v1/users", "confidence": "high"}]`,
			`[{"endpoint_path": "/api/v1/users", "confidence": "high"}]`,
		},
		{
			`No matches found.`,
			"",
		},
		{
			`[{"a": 1}, {"b": 2}] and more text`,
			`[{"a": 1}, {"b": 2}]`,
		},
	}
	for _, tt := range tests {
		got := extractJSON(tt.input)
		if got != tt.want {
			t.Errorf("extractJSON(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestContainsAny(t *testing.T) {
	if !containsAny("publish_event", "publish", "emit") {
		t.Error("expected containsAny to match 'publish'")
	}
	if containsAny("get_data", "publish", "emit") {
		t.Error("expected containsAny to not match")
	}
}

func TestLinkImportsExactMatch(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Import "axios" in frontend matches manifest dep "axios" in frontend.
	impID := graph.NewNodeID("Dependency", "frontend/src/api.ts", "axios")
	manID := graph.NewNodeID("Dependency", "frontend/package.json", "axios")

	addNodes(t, store,
		&graph.Node{
			ID: impID, Type: graph.NodeDependency, Name: "axios",
			FilePath:   "frontend/src/api.ts",
			Properties: map[string]string{"kind": "import"},
		},
		&graph.Node{
			ID: manID, Type: graph.NodeDependency, Name: "axios",
			FilePath:   "frontend/package.json",
			Properties: map[string]string{"kind": "manifest_dep", "version": "^1.6.0"},
		},
	)

	linker := NewLinker(store, nil, nil, false)
	count, err := linker.linkImports(ctx)
	if err != nil {
		t.Fatalf("linkImports: %v", err)
	}
	if count != 1 {
		t.Errorf("linkImports returned %d, want 1", count)
	}

	// Verify EdgeDependsOn was created.
	edges, err := store.GetEdges(ctx, impID, graph.EdgeDependsOn)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Errorf("got %d EdgeDependsOn, want 1", len(edges))
	}
	if len(edges) > 0 && edges[0].TargetID != manID {
		t.Errorf("EdgeDependsOn target = %s, want %s", edges[0].TargetID, manID)
	}
	if len(edges) > 0 && edges[0].Properties["kind"] != "import_to_manifest" {
		t.Errorf("edge kind = %q, want import_to_manifest", edges[0].Properties["kind"])
	}
}

func TestLinkImportsGoPrefixMatch(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Go import "github.com/foo/bar/pkg/util" matches manifest dep "github.com/foo/bar".
	impID := graph.NewNodeID("Dependency", "backend/handler.go", "github.com/foo/bar/pkg/util")
	manID := graph.NewNodeID("Dependency", "backend/go.mod", "github.com/foo/bar")

	addNodes(t, store,
		&graph.Node{
			ID: impID, Type: graph.NodeDependency, Name: "github.com/foo/bar/pkg/util",
			FilePath:   "backend/handler.go",
			Properties: map[string]string{"kind": "import"},
		},
		&graph.Node{
			ID: manID, Type: graph.NodeDependency, Name: "github.com/foo/bar",
			FilePath:   "backend/go.mod",
			Properties: map[string]string{"kind": "manifest_dep", "version": "v1.2.3"},
		},
	)

	linker := NewLinker(store, nil, nil, false)
	count, err := linker.linkImports(ctx)
	if err != nil {
		t.Fatalf("linkImports: %v", err)
	}
	if count != 1 {
		t.Errorf("linkImports returned %d, want 1", count)
	}

	edges, err := store.GetEdges(ctx, impID, graph.EdgeDependsOn)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Errorf("got %d EdgeDependsOn, want 1", len(edges))
	}
}

func TestLinkImportsPythonDottedModule(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Python import "llm_framework.core" matches manifest dep "llm-framework".
	impID := graph.NewNodeID("Dependency", "hypatia/src/main.py", "llm_framework.core")
	manID := graph.NewNodeID("Dependency", "hypatia/pyproject.toml", "llm-framework")

	addNodes(t, store,
		&graph.Node{
			ID: impID, Type: graph.NodeDependency, Name: "llm_framework.core",
			FilePath:   "hypatia/src/main.py",
			Properties: map[string]string{"kind": "import"},
		},
		&graph.Node{
			ID: manID, Type: graph.NodeDependency, Name: "llm-framework",
			FilePath:   "hypatia/pyproject.toml",
			Properties: map[string]string{"kind": "manifest_dep", "version": "==0.2.46"},
		},
	)

	linker := NewLinker(store, nil, nil, false)
	count, err := linker.linkImports(ctx)
	if err != nil {
		t.Fatalf("linkImports: %v", err)
	}
	if count != 1 {
		t.Errorf("linkImports returned %d, want 1", count)
	}

	edges, err := store.GetEdges(ctx, impID, graph.EdgeDependsOn)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Errorf("got %d EdgeDependsOn, want 1", len(edges))
	}
}

func TestLinkImportsNoMatchReturnsZero(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Import that doesn't match any manifest dep.
	impID := graph.NewNodeID("Dependency", "backend/handler.go", "fmt")
	addNodes(t, store,
		&graph.Node{
			ID: impID, Type: graph.NodeDependency, Name: "fmt",
			FilePath:   "backend/handler.go",
			Properties: map[string]string{"kind": "import"},
		},
		&graph.Node{
			ID:   graph.NewNodeID("Dependency", "backend/go.mod", "github.com/gin-gonic/gin"),
			Type: graph.NodeDependency, Name: "github.com/gin-gonic/gin",
			FilePath:   "backend/go.mod",
			Properties: map[string]string{"kind": "manifest_dep", "version": "v1.9.0"},
		},
	)

	linker := NewLinker(store, nil, nil, false)
	count, err := linker.linkImports(ctx)
	if err != nil {
		t.Fatalf("linkImports: %v", err)
	}
	if count != 0 {
		t.Errorf("linkImports returned %d, want 0 (no match)", count)
	}
}

func TestLinkImportsGoPrefixLongestMatch(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Two manifest deps with overlapping prefixes; should match the longest.
	impID := graph.NewNodeID("Dependency", "backend/handler.go", "github.com/foo/bar/baz/sub")
	shortManID := graph.NewNodeID("Dependency", "backend/go.mod", "github.com/foo/bar")
	longManID := graph.NewNodeID("Dependency", "backend/go.mod", "github.com/foo/bar/baz")

	addNodes(t, store,
		&graph.Node{
			ID: impID, Type: graph.NodeDependency, Name: "github.com/foo/bar/baz/sub",
			FilePath:   "backend/handler.go",
			Properties: map[string]string{"kind": "import"},
		},
		&graph.Node{
			ID: shortManID, Type: graph.NodeDependency, Name: "github.com/foo/bar",
			FilePath:   "backend/go.mod",
			Properties: map[string]string{"kind": "manifest_dep", "version": "v1.0.0"},
		},
		&graph.Node{
			ID: longManID, Type: graph.NodeDependency, Name: "github.com/foo/bar/baz",
			FilePath:   "backend/go.mod",
			Properties: map[string]string{"kind": "manifest_dep", "version": "v2.0.0"},
		},
	)

	linker := NewLinker(store, nil, nil, false)
	count, err := linker.linkImports(ctx)
	if err != nil {
		t.Fatalf("linkImports: %v", err)
	}
	if count != 1 {
		t.Errorf("linkImports returned %d, want 1", count)
	}

	// Verify it matched the longer prefix.
	edges, err := store.GetEdges(ctx, impID, graph.EdgeDependsOn)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Fatalf("got %d EdgeDependsOn, want 1", len(edges))
	}
	if edges[0].TargetID != longManID {
		t.Errorf("matched %s, want longer prefix %s", edges[0].TargetID, longManID)
	}
}

func TestNormalizePythonPkg(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"llm-framework", "llm_framework"},
		{"requests", "requests"},
		{"My-Package", "my_package"},
		{"UPPER-CASE", "upper_case"},
	}
	for _, tt := range tests {
		got := normalizePythonPkg(tt.input)
		if got != tt.want {
			t.Errorf("normalizePythonPkg(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestLinkGoImplements(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create a Go interface with methods.
	ifaceID := graph.NewNodeID("Interface", "pkg/graph/graph.go", "Store")
	addNodes(t, store,
		&graph.Node{
			ID: ifaceID, Type: graph.NodeInterface, Name: "Store",
			FilePath: "pkg/graph/graph.go",
			Language: "go",
			Properties: map[string]string{
				"methods": "AddNode,GetNode,Close",
			},
		},
	)

	// Create a Go struct with matching methods in a different file.
	structID := graph.NewNodeID("Struct", "internal/embedded/store.go", "EmbeddedStore")
	addNodes(t, store,
		&graph.Node{
			ID: structID, Type: graph.NodeStruct, Name: "EmbeddedStore",
			FilePath: "internal/embedded/store.go",
			Language: "go",
		},
		&graph.Node{
			ID:   graph.NewNodeID("Method", "internal/embedded/store.go", "EmbeddedStore.AddNode"),
			Type: graph.NodeMethod, Name: "AddNode",
			FilePath:   "internal/embedded/store.go",
			Language:   "go",
			Properties: map[string]string{"receiver": "EmbeddedStore"},
		},
		&graph.Node{
			ID:   graph.NewNodeID("Method", "internal/embedded/store.go", "EmbeddedStore.GetNode"),
			Type: graph.NodeMethod, Name: "GetNode",
			FilePath:   "internal/embedded/store.go",
			Language:   "go",
			Properties: map[string]string{"receiver": "EmbeddedStore"},
		},
		&graph.Node{
			ID:   graph.NewNodeID("Method", "internal/embedded/store.go", "EmbeddedStore.Close"),
			Type: graph.NodeMethod, Name: "Close",
			FilePath:   "internal/embedded/store.go",
			Language:   "go",
			Properties: map[string]string{"receiver": "EmbeddedStore"},
		},
	)

	linker := NewLinker(store, nil, nil, false)
	count, err := linker.linkImplements(ctx)
	if err != nil {
		t.Fatalf("linkImplements: %v", err)
	}
	if count != 1 {
		t.Errorf("linkImplements returned %d, want 1", count)
	}

	// Verify EdgeImplements was created.
	edges, err := store.GetEdges(ctx, structID, graph.EdgeImplements)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Errorf("got %d EdgeImplements, want 1", len(edges))
	}
	if len(edges) > 0 && edges[0].TargetID != ifaceID {
		t.Errorf("EdgeImplements target = %s, want %s", edges[0].TargetID, ifaceID)
	}
}

func TestLinkGoImplementsPartialMatch(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Interface with 3 methods, struct only has 2.
	ifaceID := graph.NewNodeID("Interface", "pkg/iface.go", "Full")
	addNodes(t, store,
		&graph.Node{
			ID: ifaceID, Type: graph.NodeInterface, Name: "Full",
			FilePath: "pkg/iface.go", Language: "go",
			Properties: map[string]string{"methods": "A,B,C"},
		},
		&graph.Node{
			ID:   graph.NewNodeID("Struct", "impl/s.go", "Partial"),
			Type: graph.NodeStruct, Name: "Partial",
			FilePath: "impl/s.go", Language: "go",
		},
		&graph.Node{
			ID:   graph.NewNodeID("Method", "impl/s.go", "Partial.A"),
			Type: graph.NodeMethod, Name: "A",
			FilePath: "impl/s.go", Language: "go",
			Properties: map[string]string{"receiver": "Partial"},
		},
		&graph.Node{
			ID:   graph.NewNodeID("Method", "impl/s.go", "Partial.B"),
			Type: graph.NodeMethod, Name: "B",
			FilePath: "impl/s.go", Language: "go",
			Properties: map[string]string{"receiver": "Partial"},
		},
	)

	linker := NewLinker(store, nil, nil, false)
	count, err := linker.linkImplements(ctx)
	if err != nil {
		t.Fatalf("linkImplements: %v", err)
	}
	if count != 0 {
		t.Errorf("linkImplements returned %d, want 0 (partial match)", count)
	}
}

func TestLinkPythonProtocol(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	protoID := graph.NewNodeID("Interface", "mylib/protocols.py", "Serializable")
	classID := graph.NewNodeID("Class", "mylib/impl.py", "JsonSerializer")

	addNodes(t, store,
		&graph.Node{
			ID: protoID, Type: graph.NodeInterface, Name: "Serializable",
			FilePath: "mylib/protocols.py", Language: "python",
			Properties: map[string]string{
				"protocol": "true",
				"methods":  "serialize,deserialize",
			},
		},
		&graph.Node{
			ID: classID, Type: graph.NodeClass, Name: "JsonSerializer",
			FilePath: "mylib/impl.py", Language: "python",
			Properties: map[string]string{
				"bases": "Serializable",
			},
		},
	)

	linker := NewLinker(store, nil, nil, false)
	count, err := linker.linkImplements(ctx)
	if err != nil {
		t.Fatalf("linkImplements: %v", err)
	}
	if count != 1 {
		t.Errorf("linkImplements returned %d, want 1", count)
	}

	edges, err := store.GetEdges(ctx, classID, graph.EdgeImplements)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Errorf("got %d EdgeImplements, want 1", len(edges))
	}
}

func TestLinkTestFiles(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	srcID := graph.NewNodeID("File", "pkg/handler.go", "pkg/handler.go")
	testID := graph.NewNodeID("TestFile", "pkg/handler_test.go", "pkg/handler_test.go")

	addNodes(t, store,
		&graph.Node{
			ID: srcID, Type: graph.NodeFile, Name: "pkg/handler.go",
			FilePath: "pkg/handler.go", Language: "go",
		},
		&graph.Node{
			ID: testID, Type: graph.NodeTestFile, Name: "pkg/handler_test.go",
			FilePath: "pkg/handler_test.go", Language: "go",
		},
	)

	linker := NewLinker(store, nil, nil, false)
	count, err := linker.linkTests(ctx)
	if err != nil {
		t.Fatalf("linkTests: %v", err)
	}
	if count < 1 {
		t.Errorf("linkTests returned %d, want >= 1", count)
	}

	edges, err := store.GetEdges(ctx, testID, graph.EdgeTests)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Errorf("got %d EdgeTests from test file, want 1", len(edges))
	}
}

func TestLinkTestFunctions(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	funcID := graph.NewNodeID("Function", "pkg/handler.go", "ParseFile")
	testFuncID := graph.NewNodeID("TestFunction", "pkg/handler_test.go", "TestParseFile")

	addNodes(t, store,
		&graph.Node{
			ID: funcID, Type: graph.NodeFunction, Name: "ParseFile",
			FilePath: "pkg/handler.go", Language: "go",
			Package: "handler",
		},
		&graph.Node{
			ID: testFuncID, Type: graph.NodeTestFunction, Name: "TestParseFile",
			FilePath: "pkg/handler_test.go", Language: "go",
			Package: "handler",
		},
	)

	linker := NewLinker(store, nil, nil, false)
	count, err := linker.linkTests(ctx)
	if err != nil {
		t.Fatalf("linkTests: %v", err)
	}
	if count < 1 {
		t.Errorf("linkTests returned %d, want >= 1", count)
	}
}

func TestDeriveSourceFilePaths(t *testing.T) {
	tests := []struct {
		testPath string
		language string
		want     []string
	}{
		{"pkg/handler_test.go", "go", []string{"pkg/handler.go"}},
		{"tests/test_handlers.py", "python", []string{"tests/handlers.py"}},
		{"tests/handlers_test.py", "python", []string{"tests/handlers.py"}},
		{"src/utils.test.ts", "typescript", []string{"src/utils.ts"}},
		{"src/utils.spec.ts", "typescript", []string{"src/utils.ts"}},
		{"src/utils.test.js", "javascript", []string{"src/utils.js"}},
		{"src/FooTest.java", "java", []string{"src/Foo.java"}},
		{"src/FooTests.java", "java", []string{"src/Foo.java"}},
		{"src/TestFoo.java", "java", []string{"src/Foo.java"}},
	}
	for _, tt := range tests {
		got := deriveSourceFilePaths(tt.testPath, tt.language)
		if len(got) == 0 {
			t.Errorf("deriveSourceFilePaths(%q, %q) returned empty", tt.testPath, tt.language)
			continue
		}
		if got[0] != tt.want[0] {
			t.Errorf("deriveSourceFilePaths(%q, %q)[0] = %q, want %q", tt.testPath, tt.language, got[0], tt.want[0])
		}
	}
}

func TestDeriveSourceFuncNames(t *testing.T) {
	tests := []struct {
		name     string
		language string
		want     string // first candidate
	}{
		{"TestParseFile", "go", "ParseFile"},
		{"TestFoo_Bar", "go", "Foo"},
		{"BenchmarkSort", "go", "Sort"},
		{"test_process_user", "python", "process_user"},
		{"testProcessUser", "java", "processUser"},
	}
	for _, tt := range tests {
		got := deriveSourceFuncNames(tt.name, tt.language)
		if len(got) == 0 {
			t.Errorf("deriveSourceFuncNames(%q, %q) returned empty", tt.name, tt.language)
			continue
		}
		if got[0] != tt.want {
			t.Errorf("deriveSourceFuncNames(%q, %q)[0] = %q, want %q", tt.name, tt.language, got[0], tt.want)
		}
	}
}

func TestRunPhases(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	linker := NewLinker(store, nil, nil, false)
	phases := linker.NewPhases()

	results, err := linker.RunPhases(ctx, phases)
	if err != nil {
		t.Fatalf("RunPhases: %v", err)
	}

	if _, ok := results["implements"]; !ok {
		t.Error("missing implements phase result")
	}
	if _, ok := results["tests"]; !ok {
		t.Error("missing tests phase result")
	}
}

func TestPhasesCount(t *testing.T) {
	store := newTestStore(t)
	linker := NewLinker(store, nil, nil, false)

	allPhases := linker.Phases()
	if len(allPhases) != 7 {
		t.Errorf("Phases() returned %d, want 7", len(allPhases))
	}

	newPhases := linker.NewPhases()
	if len(newPhases) != 2 {
		t.Errorf("NewPhases() returned %d, want 2", len(newPhases))
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
