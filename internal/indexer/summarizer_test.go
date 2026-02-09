package indexer

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/pkg/llm"
)

// mockLLMClient is a test double that returns canned responses.
type mockLLMClient struct {
	responses []string
	callIndex int
	calls     []mockCall
}

type mockCall struct {
	SystemPrompt string
	Messages     []llm.Message
}

func (m *mockLLMClient) Chat(_ context.Context, systemPrompt string, messages []llm.Message) (*llm.Response, error) {
	m.calls = append(m.calls, mockCall{SystemPrompt: systemPrompt, Messages: messages})
	if m.callIndex >= len(m.responses) {
		return &llm.Response{Content: "default response"}, nil
	}
	resp := m.responses[m.callIndex]
	m.callIndex++
	return &llm.Response{Content: resp}, nil
}

func (m *mockLLMClient) Model() string    { return "mock-model" }
func (m *mockLLMClient) Provider() string  { return "mock" }
func (m *mockLLMClient) Close() error      { return nil }

func setupSummarizerTest(t *testing.T) (graph.Store, *mockLLMClient) {
	t.Helper()
	store, err := embedded.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	mock := &mockLLMClient{}
	return store, mock
}

func TestSummarizeService(t *testing.T) {
	store, mock := setupSummarizerTest(t)
	mock.responses = []string{"This service handles HTTP requests and authentication using JWT tokens. It follows a handler-middleware pattern with Go's net/http."}

	ctx := context.Background()

	nodes := []*graph.Node{
		{ID: "n1", Type: graph.NodeFile, Name: "handler.go", FilePath: "svc/handler.go", Language: "go"},
		{ID: "n2", Type: graph.NodeFunction, Name: "HandleRequest", FilePath: "svc/handler.go", Language: "go", Exported: true},
		{ID: "n3", Type: graph.NodeStruct, Name: "AuthConfig", FilePath: "svc/auth.go", Language: "go", Exported: true},
		{ID: "n4", Type: graph.NodeMethod, Name: "Validate", FilePath: "svc/auth.go", Language: "go", Exported: true},
	}

	summarizer := NewSummarizer(mock, store, nil, false)
	if err := summarizer.SummarizeService(ctx, "auth-service", nodes); err != nil {
		t.Fatalf("SummarizeService failed: %v", err)
	}

	// Verify LLM was called.
	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 LLM call, got %d", len(mock.calls))
	}

	// Verify prompt contains service name and node info.
	prompt := mock.calls[0].Messages[0].Content
	if got := prompt; got == "" {
		t.Fatal("expected non-empty prompt")
	}
	for _, expected := range []string{"auth-service", "Function", "Struct", "HandleRequest"} {
		if !contains(prompt, expected) {
			t.Errorf("prompt missing %q", expected)
		}
	}

	// Verify a Document node was stored.
	expectedID := graph.NewNodeID("Document", "generated", "summary:auth-service")
	node, err := store.GetNode(ctx, expectedID)
	if err != nil {
		t.Fatalf("get summary node: %v", err)
	}
	if node.Type != graph.NodeDocument {
		t.Errorf("expected Document node, got %s", node.Type)
	}
	if node.Properties["generated"] != "true" {
		t.Error("expected generated=true property")
	}
	if node.Properties["kind"] != "summary" {
		t.Errorf("expected kind=summary, got %s", node.Properties["kind"])
	}
	if node.Properties["service"] != "auth-service" {
		t.Errorf("expected service=auth-service, got %s", node.Properties["service"])
	}
	if node.DocComment == "" {
		t.Error("expected non-empty DocComment containing the summary")
	}
}

func TestSummarizeServiceEmpty(t *testing.T) {
	store, mock := setupSummarizerTest(t)
	summarizer := NewSummarizer(mock, store, nil, false)

	// Empty nodes should be a no-op.
	if err := summarizer.SummarizeService(context.Background(), "empty", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.calls) != 0 {
		t.Error("expected no LLM calls for empty node list")
	}
}

func TestSummarizePatterns(t *testing.T) {
	store, mock := setupSummarizerTest(t)
	mock.responses = []string{"The codebase uses a microservice architecture with Go for backend services and Python for data processing. Key patterns include dependency injection and clean architecture."}

	ctx := context.Background()

	nodes := []*graph.Node{
		{ID: "n1", Type: graph.NodeFunction, Name: "main", Package: "cmd", Language: "go"},
		{ID: "n2", Type: graph.NodeStruct, Name: "Server", Package: "server", Language: "go"},
		{ID: "n3", Type: graph.NodeInterface, Name: "Repository", Package: "store", Language: "go"},
		{ID: "n4", Type: graph.NodeClass, Name: "DataProcessor", Package: "pipeline", Language: "python"},
		{ID: "n5", Type: graph.NodeFunction, Name: "process", Package: "pipeline", Language: "python"},
	}

	summarizer := NewSummarizer(mock, store, nil, false)
	if err := summarizer.SummarizePatterns(ctx, nodes); err != nil {
		t.Fatalf("SummarizePatterns failed: %v", err)
	}

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 LLM call, got %d", len(mock.calls))
	}

	// Verify prompt contains language and type info.
	prompt := mock.calls[0].Messages[0].Content
	for _, expected := range []string{"go", "python", "Function", "Struct", "Interface", "Class"} {
		if !contains(prompt, expected) {
			t.Errorf("prompt missing %q", expected)
		}
	}

	// Verify patterns Document node was stored.
	expectedID := graph.NewNodeID("Document", "generated", "patterns")
	node, err := store.GetNode(ctx, expectedID)
	if err != nil {
		t.Fatalf("get patterns node: %v", err)
	}
	if node.Type != graph.NodeDocument {
		t.Errorf("expected Document node, got %s", node.Type)
	}
	if node.Properties["generated"] != "true" {
		t.Error("expected generated=true property")
	}
	if node.Properties["kind"] != "patterns" {
		t.Errorf("expected kind=patterns, got %s", node.Properties["kind"])
	}
	if node.DocComment == "" {
		t.Error("expected non-empty DocComment containing patterns")
	}
}

func TestSummarizePatternsEmpty(t *testing.T) {
	store, mock := setupSummarizerTest(t)
	summarizer := NewSummarizer(mock, store, nil, false)

	if err := summarizer.SummarizePatterns(context.Background(), nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.calls) != 0 {
		t.Error("expected no LLM calls for empty node list")
	}
}

func TestSummarizeServiceIdempotent(t *testing.T) {
	store, mock := setupSummarizerTest(t)
	mock.responses = []string{"First summary.", "Updated summary."}

	ctx := context.Background()
	nodes := []*graph.Node{
		{ID: "n1", Type: graph.NodeFunction, Name: "Foo", Language: "go", Exported: true},
	}

	summarizer := NewSummarizer(mock, store, nil, false)

	// First call.
	if err := summarizer.SummarizeService(ctx, "svc", nodes); err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	// Second call should replace the existing node.
	if err := summarizer.SummarizeService(ctx, "svc", nodes); err != nil {
		t.Fatalf("second call failed: %v", err)
	}

	expectedID := graph.NewNodeID("Document", "generated", "summary:svc")
	node, err := store.GetNode(ctx, expectedID)
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if node.DocComment != "Updated summary." {
		t.Errorf("expected updated content, got %q", node.DocComment)
	}
}

func TestSummarizeArchitecture(t *testing.T) {
	store, mock := setupSummarizerTest(t)
	mock.responses = []string{"This service uses Repository pattern for data access with a clean separation between controllers, services, and data layers. Factory pattern is used for creating service instances."}

	ctx := context.Background()

	nodes := []*graph.Node{
		{
			ID: "n1", Type: graph.NodeInterface, Name: "UserRepository",
			FilePath: "svc/repo.go", Language: "go", Exported: true,
			Properties: map[string]string{
				graph.PropArchRole:      "repository",
				graph.PropDesignPattern: "repository",
				graph.PropLayerTag:      "data_access",
			},
		},
		{
			ID: "n2", Type: graph.NodeStruct, Name: "UserService",
			FilePath: "svc/service.go", Language: "go", Exported: true,
			Properties: map[string]string{
				graph.PropArchRole: "service",
				graph.PropLayerTag: "business",
			},
		},
		{
			ID: "n3", Type: graph.NodeStruct, Name: "UserController",
			FilePath: "svc/controller.go", Language: "go", Exported: true,
			Properties: map[string]string{
				graph.PropArchRole:      "controller",
				graph.PropDesignPattern: "factory",
				graph.PropLayerTag:      "presentation",
			},
		},
		{
			ID: "n4", Type: graph.NodeDBModel, Name: "User",
			FilePath: "svc/models.go", Language: "go", Exported: true,
			Properties: map[string]string{
				graph.PropLayerTag: "data_access",
			},
		},
		{
			ID: "n5", Type: graph.NodeClass, Name: "UserHandler",
			FilePath: "svc/handler.py", Language: "python", Exported: true,
			Properties: map[string]string{
				graph.PropArchRole:      "controller",
				graph.PropDesignPattern: "observer",
				graph.PropLayerTag:      "presentation",
			},
		},
	}

	summarizer := NewSummarizer(mock, store, nil, false)
	if err := summarizer.SummarizeArchitecture(ctx, "user-service", nodes); err != nil {
		t.Fatalf("SummarizeArchitecture failed: %v", err)
	}

	// Verify LLM was called.
	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 LLM call, got %d", len(mock.calls))
	}

	// Verify prompt includes architectural information.
	prompt := mock.calls[0].Messages[0].Content
	for _, expected := range []string{
		"user-service",
		"Architectural role distribution",
		"repository",
		"service",
		"controller",
		"Design patterns detected",
		"factory",
		"observer",
		"Layer distribution",
		"data_access",
		"business",
		"presentation",
		"Interfaces",
		"UserRepository",
		"Database models",
		"User",
		"DDD patterns",
		"Clean Architecture",
	} {
		if !contains(prompt, expected) {
			t.Errorf("prompt missing %q", expected)
		}
	}

	// Verify system prompt is architecture-focused.
	if !contains(mock.calls[0].SystemPrompt, "architecture") {
		t.Error("system prompt should mention architecture")
	}

	// Verify the Document node was stored with kind=architecture_analysis.
	expectedID := graph.NewNodeID("Document", "generated", "architecture:user-service")
	node, err := store.GetNode(ctx, expectedID)
	if err != nil {
		t.Fatalf("get architecture node: %v", err)
	}
	if node.Type != graph.NodeDocument {
		t.Errorf("expected Document node, got %s", node.Type)
	}
	if node.Properties["generated"] != "true" {
		t.Error("expected generated=true property")
	}
	if node.Properties["kind"] != "architecture_analysis" {
		t.Errorf("expected kind=architecture_analysis, got %s", node.Properties["kind"])
	}
	if node.Properties["service"] != "user-service" {
		t.Errorf("expected service=user-service, got %s", node.Properties["service"])
	}
	if node.DocComment == "" {
		t.Error("expected non-empty DocComment containing architecture analysis")
	}
}

func TestSummarizeArchitectureEmpty(t *testing.T) {
	store, mock := setupSummarizerTest(t)
	summarizer := NewSummarizer(mock, store, nil, false)

	if err := summarizer.SummarizeArchitecture(context.Background(), "empty", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.calls) != 0 {
		t.Error("expected no LLM calls for empty node list")
	}
}

func TestBuildServicePromptIncludesArchInfo(t *testing.T) {
	nodes := []*graph.Node{
		{
			ID: "n1", Type: graph.NodeFunction, Name: "Handle", Language: "go", Exported: true,
			Properties: map[string]string{
				graph.PropArchRole:      "controller",
				graph.PropDesignPattern: "factory",
				graph.PropLayerTag:      "presentation",
			},
		},
		{
			ID: "n2", Type: graph.NodeDBModel, Name: "Order", Language: "go", Exported: true,
			Properties: map[string]string{
				graph.PropLayerTag: "data_access",
			},
		},
	}

	prompt := buildServicePrompt("order-svc", nodes)
	for _, expected := range []string{
		"Architectural roles",
		"controller",
		"Design patterns",
		"factory",
		"Layers",
		"presentation",
		"data_access",
		"DB models",
	} {
		if !contains(prompt, expected) {
			t.Errorf("buildServicePrompt missing %q", expected)
		}
	}
}

func TestBuildPatternsPromptIncludesCrossServiceInfo(t *testing.T) {
	nodes := []*graph.Node{
		{
			ID: "n1", Type: graph.NodeStruct, Name: "Svc1", Package: "a", Language: "go",
			Properties: map[string]string{
				graph.PropArchRole:      "service",
				graph.PropDesignPattern: "repository",
				graph.PropLayerTag:      "business",
			},
		},
		{
			ID: "n2", Type: graph.NodeStruct, Name: "Svc2", Package: "b", Language: "go",
			Properties: map[string]string{
				graph.PropArchRole:      "service",
				graph.PropDesignPattern: "repository,factory",
				graph.PropLayerTag:      "business",
			},
		},
	}

	prompt := buildPatternsPrompt(nodes)
	for _, expected := range []string{
		"Cross-service design patterns",
		"repository",
		"factory",
		"Architectural role distribution across codebase",
		"service",
		"Layer consistency across codebase",
		"business",
		"cross-service pattern consistency",
	} {
		if !contains(prompt, expected) {
			t.Errorf("buildPatternsPrompt missing %q", expected)
		}
	}
}

func TestGroupNodesByTopDir(t *testing.T) {
	base := "/project"
	nodes := []*graph.Node{
		{ID: "1", FilePath: filepath.Join(base, "services", "auth", "main.go")},
		{ID: "2", FilePath: filepath.Join(base, "services", "auth", "handler.go")},
		{ID: "3", FilePath: filepath.Join(base, "lib", "utils.go")},
		{ID: "4", FilePath: ""},
	}

	groups := GroupNodesByTopDir(nodes, []string{base})

	if len(groups["services"]) != 2 {
		t.Errorf("expected 2 nodes in 'services', got %d", len(groups["services"]))
	}
	if len(groups["lib"]) != 1 {
		t.Errorf("expected 1 node in 'lib', got %d", len(groups["lib"]))
	}
	if len(groups["(root)"]) != 1 {
		t.Errorf("expected 1 node in '(root)', got %d", len(groups["(root)"]))
	}
}

func TestGroupNodesByTopDirRelativePaths(t *testing.T) {
	// With relative paths (the new default), basePaths is unused.
	nodes := []*graph.Node{
		{ID: "1", FilePath: "services/auth/main.go"},
		{ID: "2", FilePath: "services/auth/handler.go"},
		{ID: "3", FilePath: "lib/utils.go"},
		{ID: "4", FilePath: ""},
	}

	groups := GroupNodesByTopDir(nodes, nil)

	if len(groups["services"]) != 2 {
		t.Errorf("expected 2 nodes in 'services', got %d", len(groups["services"]))
	}
	if len(groups["lib"]) != 1 {
		t.Errorf("expected 1 node in 'lib', got %d", len(groups["lib"]))
	}
	if len(groups["(root)"]) != 1 {
		t.Errorf("expected 1 node in '(root)', got %d", len(groups["(root)"]))
	}
}

func contains(s, substr string) bool {
	return fmt.Sprintf("%s", s) != "" && len(s) > 0 && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
