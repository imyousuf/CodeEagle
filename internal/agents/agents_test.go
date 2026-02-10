package agents

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/pkg/llm"
)

// mockClient implements llm.Client for testing (no tool support).
type mockClient struct {
	lastSystemPrompt string
	lastMessages     []llm.Message
	response         string
	err              error
}

func (m *mockClient) Chat(_ context.Context, systemPrompt string, messages []llm.Message) (*llm.Response, error) {
	m.lastSystemPrompt = systemPrompt
	m.lastMessages = messages
	if m.err != nil {
		return nil, m.err
	}
	return &llm.Response{
		Content: m.response,
		Usage:   llm.TokenUsage{InputTokens: 100, OutputTokens: 50},
	}, nil
}

func (m *mockClient) Model() string    { return "test-model" }
func (m *mockClient) Provider() string { return "test" }
func (m *mockClient) Close() error     { return nil }

// mockToolClient implements llm.ToolCapableClient for testing.
type mockToolClient struct {
	mockClient
	responses []llm.Response // sequence of responses (consumed in order)
	callIdx   int
}

func (m *mockToolClient) ChatWithTools(_ context.Context, systemPrompt string, messages []llm.Message, tools []llm.Tool) (*llm.Response, error) {
	m.lastSystemPrompt = systemPrompt
	m.lastMessages = messages
	if m.err != nil {
		return nil, m.err
	}
	if m.callIdx < len(m.responses) {
		resp := m.responses[m.callIdx]
		m.callIdx++
		return &resp, nil
	}
	return &llm.Response{Content: m.response}, nil
}

// setupTestStore creates a temporary graph store populated with test data.
func setupTestStore(t *testing.T) (graph.Store, func()) {
	t.Helper()
	dbPath := t.TempDir()
	store, err := embedded.NewStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}

	ctx := context.Background()

	// Add some test nodes.
	fileNode := &graph.Node{
		ID:       "file1",
		Type:     graph.NodeFile,
		Name:     "main.go",
		FilePath: "cmd/main.go",
		Package:  "main",
		Language: "go",
	}
	funcNode := &graph.Node{
		ID:            "func1",
		Type:          graph.NodeFunction,
		Name:          "HandleRequest",
		QualifiedName: "main.HandleRequest",
		FilePath:      "cmd/main.go",
		Package:       "main",
		Language:      "go",
		Exported:      true,
		Line:          10,
		EndLine:       25,
		Metrics:       map[string]float64{"cyclomatic_complexity": 5.0, "lines_of_code": 15.0},
	}
	svcNode := &graph.Node{
		ID:       "svc1",
		Type:     graph.NodeService,
		Name:     "AuthService",
		Package:  "auth",
		Language: "go",
	}
	depNode := &graph.Node{
		ID:       "func2",
		Type:     graph.NodeFunction,
		Name:     "Login",
		FilePath: "internal/auth/login.go",
		Package:  "auth",
		Language: "go",
		Exported: true,
		Line:     5,
	}

	for _, n := range []*graph.Node{fileNode, funcNode, svcNode, depNode} {
		if err := store.AddNode(ctx, n); err != nil {
			t.Fatalf("failed to add node: %v", err)
		}
	}

	// Add an edge: Login calls HandleRequest.
	edge := &graph.Edge{
		ID:       "edge1",
		Type:     graph.EdgeCalls,
		SourceID: "func2",
		TargetID: "func1",
	}
	if err := store.AddEdge(ctx, edge); err != nil {
		t.Fatalf("failed to add edge: %v", err)
	}

	return store, func() { store.Close() }
}

func TestBaseAgentName(t *testing.T) {
	ba := &BaseAgent{name: "test-agent"}
	if ba.Name() != "test-agent" {
		t.Errorf("expected name 'test-agent', got %q", ba.Name())
	}
}

func TestBaseAgentAsk(t *testing.T) {
	mock := &mockClient{response: "test response"}
	ba := &BaseAgent{
		name:         "test",
		llmClient:    mock,
		systemPrompt: "system prompt",
	}

	resp, err := ba.ask(context.Background(), "context text", "my question")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "test response" {
		t.Errorf("expected 'test response', got %q", resp)
	}
	if mock.lastSystemPrompt != "system prompt" {
		t.Errorf("expected system prompt 'system prompt', got %q", mock.lastSystemPrompt)
	}
	// Should have 3 messages: context, acknowledgement, and query.
	if len(mock.lastMessages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(mock.lastMessages))
	}
	if !strings.Contains(mock.lastMessages[0].Content, "context text") {
		t.Errorf("expected context in first message, got %q", mock.lastMessages[0].Content)
	}
	if mock.lastMessages[2].Content != "my question" {
		t.Errorf("expected query in last message, got %q", mock.lastMessages[2].Content)
	}
}

func TestBaseAgentAskNoContext(t *testing.T) {
	mock := &mockClient{response: "no context response"}
	ba := &BaseAgent{
		name:         "test",
		llmClient:    mock,
		systemPrompt: "system prompt",
	}

	resp, err := ba.ask(context.Background(), "", "my question")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "no context response" {
		t.Errorf("expected 'no context response', got %q", resp)
	}
	// Should have 1 message (just the query) when no context.
	if len(mock.lastMessages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mock.lastMessages))
	}
}

func TestBaseAgentAskLLMError(t *testing.T) {
	mock := &mockClient{err: fmt.Errorf("API error")}
	ba := &BaseAgent{
		name:         "test",
		llmClient:    mock,
		systemPrompt: "system prompt",
	}

	_, err := ba.ask(context.Background(), "", "my question")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "API error") {
		t.Errorf("expected error to contain 'API error', got %q", err.Error())
	}
}

func TestPlannerAsk(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	mock := &mockClient{response: "impact analysis result"}
	ctxBuilder := NewContextBuilder(store)
	planner := NewPlanner(mock, ctxBuilder)

	if planner.Name() != "planner" {
		t.Errorf("expected name 'planner', got %q", planner.Name())
	}

	// Test with impact-related query.
	resp, err := planner.Ask(context.Background(), "What would be affected if I change HandleRequest?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "impact analysis result" {
		t.Errorf("expected 'impact analysis result', got %q", resp)
	}
	if mock.lastSystemPrompt != plannerSystemPrompt {
		t.Errorf("expected planner system prompt")
	}
}

func TestPlannerAskDependencyQuery(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	mock := &mockClient{response: "dependency result"}
	ctxBuilder := NewContextBuilder(store)
	planner := NewPlanner(mock, ctxBuilder)

	resp, err := planner.Ask(context.Background(), "What depends on auth service?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "dependency result" {
		t.Errorf("expected 'dependency result', got %q", resp)
	}
}

func TestPlannerAskDefaultQuery(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	mock := &mockClient{response: "overview result"}
	ctxBuilder := NewContextBuilder(store)
	planner := NewPlanner(mock, ctxBuilder)

	resp, err := planner.Ask(context.Background(), "tell me about this project")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "overview result" {
		t.Errorf("expected 'overview result', got %q", resp)
	}
	// Should have context with overview information.
	if len(mock.lastMessages) < 2 {
		t.Fatal("expected at least 2 messages with context")
	}
	if !strings.Contains(mock.lastMessages[0].Content, "Knowledge Graph Overview") {
		t.Errorf("expected overview context, got %q", mock.lastMessages[0].Content)
	}
}

func TestDesignerAsk(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	mock := &mockClient{response: "design analysis"}
	ctxBuilder := NewContextBuilder(store)
	designer := NewDesigner(mock, ctxBuilder)

	if designer.Name() != "designer" {
		t.Errorf("expected name 'designer', got %q", designer.Name())
	}

	resp, err := designer.Ask(context.Background(), "How is auth handled?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "design analysis" {
		t.Errorf("expected 'design analysis', got %q", resp)
	}
	// Designer should always include overview context.
	if len(mock.lastMessages) < 2 {
		t.Fatal("expected at least 2 messages with context")
	}
	if !strings.Contains(mock.lastMessages[0].Content, "Knowledge Graph Overview") {
		t.Errorf("expected overview context, got %q", mock.lastMessages[0].Content)
	}
}

func TestReviewerAsk(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	mock := &mockClient{response: "review result"}
	ctxBuilder := NewContextBuilder(store)
	reviewer := NewReviewer(mock, ctxBuilder)

	if reviewer.Name() != "reviewer" {
		t.Errorf("expected name 'reviewer', got %q", reviewer.Name())
	}

	// Test with a query mentioning a file path.
	resp, err := reviewer.Ask(context.Background(), "Review cmd/main.go for issues")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "review result" {
		t.Errorf("expected 'review result', got %q", resp)
	}
}

func TestReviewerAskGenericQuery(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	mock := &mockClient{response: "generic review"}
	ctxBuilder := NewContextBuilder(store)
	reviewer := NewReviewer(mock, ctxBuilder)

	resp, err := reviewer.Ask(context.Background(), "what are the code quality issues")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "generic review" {
		t.Errorf("expected 'generic review', got %q", resp)
	}
	// Should fallback to overview context.
	if len(mock.lastMessages) < 2 {
		t.Fatal("expected at least 2 messages with context")
	}
	if !strings.Contains(mock.lastMessages[0].Content, "Knowledge Graph Overview") {
		t.Errorf("expected overview context, got %q", mock.lastMessages[0].Content)
	}
}

func TestReviewerReviewDiff(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	mock := &mockClient{response: "diff review result"}
	ctxBuilder := NewContextBuilder(store)
	reviewer := NewReviewer(mock, ctxBuilder)

	// Replace the git diff runner with a mock.
	originalRunner := gitDiffRunner
	gitDiffRunner = func(_ context.Context, diffRef string) (string, error) {
		if diffRef != "HEAD~1" {
			return "", fmt.Errorf("unexpected diff ref: %s", diffRef)
		}
		return `diff --git a/cmd/main.go b/cmd/main.go
index abc1234..def5678 100644
--- a/cmd/main.go
+++ b/cmd/main.go
@@ -10,6 +10,8 @@ func HandleRequest() {
+    newLine1
+    newLine2
diff --git a/internal/auth/login.go b/internal/auth/login.go
index 111..222 100644
--- a/internal/auth/login.go
+++ b/internal/auth/login.go
@@ -5,3 +5,5 @@ func Login() {
+    added
`, nil
	}
	defer func() { gitDiffRunner = originalRunner }()

	resp, err := reviewer.ReviewDiff(context.Background(), "HEAD~1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "diff review result" {
		t.Errorf("expected 'diff review result', got %q", resp)
	}

	// Verify that the query contains diff information.
	lastMsg := mock.lastMessages[len(mock.lastMessages)-1]
	if !strings.Contains(lastMsg.Content, "HEAD~1") {
		t.Errorf("expected query to contain 'HEAD~1', got %q", lastMsg.Content)
	}
	if !strings.Contains(lastMsg.Content, "cmd/main.go") {
		t.Errorf("expected query to mention changed files, got %q", lastMsg.Content)
	}
}

func TestReviewerReviewDiffNoChanges(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	mock := &mockClient{response: "should not be called"}
	ctxBuilder := NewContextBuilder(store)
	reviewer := NewReviewer(mock, ctxBuilder)

	originalRunner := gitDiffRunner
	gitDiffRunner = func(_ context.Context, _ string) (string, error) {
		return "", nil
	}
	defer func() { gitDiffRunner = originalRunner }()

	resp, err := reviewer.ReviewDiff(context.Background(), "HEAD")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(resp, "No changed files") {
		t.Errorf("expected 'No changed files' message, got %q", resp)
	}
}

func TestReviewerReviewDiffGitError(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	mock := &mockClient{response: "should not be called"}
	ctxBuilder := NewContextBuilder(store)
	reviewer := NewReviewer(mock, ctxBuilder)

	originalRunner := gitDiffRunner
	gitDiffRunner = func(_ context.Context, _ string) (string, error) {
		return "", fmt.Errorf("git not found")
	}
	defer func() { gitDiffRunner = originalRunner }()

	_, err := reviewer.ReviewDiff(context.Background(), "HEAD~1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "git not found") {
		t.Errorf("expected error to contain 'git not found', got %q", err.Error())
	}
}

func TestParseDiffFiles(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name: "standard diff",
			input: `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,4 @@
diff --git a/bar.go b/bar.go
--- a/bar.go
+++ b/bar.go
@@ -1,3 +1,4 @@`,
			expected: []string{"foo.go", "bar.go"},
		},
		{
			name: "new file",
			input: `diff --git a/new.go b/new.go
--- /dev/null
+++ b/new.go
@@ -0,0 +1,5 @@`,
			expected: []string{"new.go"},
		},
		{
			name: "deleted file",
			input: `diff --git a/old.go b/old.go
--- a/old.go
+++ /dev/null
@@ -1,5 +0,0 @@`,
			expected: nil,
		},
		{
			name:     "empty diff",
			input:    "",
			expected: nil,
		},
		{
			name: "deduplication",
			input: `+++ b/same.go
+++ b/same.go`,
			expected: []string{"same.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseDiffFiles(tt.input)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d files, got %d: %v", len(tt.expected), len(result), result)
			}
			for i, f := range result {
				if f != tt.expected[i] {
					t.Errorf("file %d: expected %q, got %q", i, tt.expected[i], f)
				}
			}
		})
	}
}

func TestContainsAny(t *testing.T) {
	if !containsAny("what would change if I modify this", "change", "modify") {
		t.Error("expected true for 'change' or 'modify'")
	}
	if containsAny("hello world", "change", "modify") {
		t.Error("expected false for 'hello world'")
	}
}

func TestExtractEntityName(t *testing.T) {
	tests := []struct {
		query    string
		expected string
	}{
		{"How is auth/handler.go structured?", "auth/handler.go"},
		{"Tell me about AuthService", "AuthService"},
		{"what are the issues", ""},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			result := extractEntityName(tt.query)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestIsStopWord(t *testing.T) {
	if !isStopWord("the") {
		t.Error("expected 'the' to be a stop word")
	}
	if isStopWord("HandleRequest") {
		t.Error("expected 'HandleRequest' to not be a stop word")
	}
}

// --- Agentic planner tests ---

func TestPlannerFallbackToSingleTurn(t *testing.T) {
	// When using a non-tool-capable client, should fallback to single-turn.
	store, cleanup := setupTestStore(t)
	defer cleanup()

	mock := &mockClient{response: "single turn response"}
	ctxBuilder := NewContextBuilder(store)
	planner := NewPlanner(mock, ctxBuilder)

	// Verify it doesn't implement ToolCapableClient.
	if llm.SupportsTools(mock) {
		t.Fatal("mockClient should not support tools")
	}

	resp, err := planner.Ask(context.Background(), "tell me about this project")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "single turn response" {
		t.Errorf("expected 'single turn response', got %q", resp)
	}
}

func TestPlannerAgenticNoToolCalls(t *testing.T) {
	// Tool-capable client that returns a response with no tool calls.
	store, cleanup := setupTestStore(t)
	defer cleanup()

	mock := &mockToolClient{
		mockClient: mockClient{response: "direct answer"},
	}
	ctxBuilder := NewContextBuilder(store)
	planner := NewPlanner(mock, ctxBuilder)

	if !llm.SupportsTools(mock) {
		t.Fatal("mockToolClient should support tools")
	}

	resp, err := planner.Ask(context.Background(), "What is this project?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "direct answer" {
		t.Errorf("expected 'direct answer', got %q", resp)
	}
}

func TestPlannerAgenticOneRound(t *testing.T) {
	// Tool-capable client: first call returns tool calls, second returns text.
	store, cleanup := setupTestStore(t)
	defer cleanup()

	mock := &mockToolClient{
		responses: []llm.Response{
			{
				Content: "Let me search for that.",
				ToolCalls: []llm.ToolCall{
					{ID: "tc1", Name: "get_graph_overview", Arguments: map[string]any{}},
				},
			},
			{
				Content: "The project has 4 nodes.",
			},
		},
	}
	ctxBuilder := NewContextBuilder(store)
	planner := NewPlanner(mock, ctxBuilder)

	resp, err := planner.Ask(context.Background(), "What is the project overview?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "The project has 4 nodes." {
		t.Errorf("expected 'The project has 4 nodes.', got %q", resp)
	}
	// Should have called ChatWithTools twice.
	if mock.callIdx != 2 {
		t.Errorf("expected 2 calls, got %d", mock.callIdx)
	}
}

func TestPlannerAgenticMaxIterations(t *testing.T) {
	// Tool-capable client that always returns tool calls -> should hit max iterations.
	store, cleanup := setupTestStore(t)
	defer cleanup()

	mock := &mockToolClient{
		responses: make([]llm.Response, 20), // more than maxIterations
	}
	// Fill all responses with tool calls.
	for i := range mock.responses {
		mock.responses[i] = llm.Response{
			ToolCalls: []llm.ToolCall{
				{ID: fmt.Sprintf("tc%d", i), Name: "get_graph_overview", Arguments: map[string]any{}},
			},
		}
	}

	ctxBuilder := NewContextBuilder(store)
	planner := NewPlanner(mock, ctxBuilder)
	planner.SetMaxIterations(3) // Low limit for test.

	_, err := planner.Ask(context.Background(), "query")
	if err == nil {
		t.Fatal("expected error for max iterations")
	}
	if !strings.Contains(err.Error(), "maximum iterations (3)") {
		t.Errorf("expected max iterations error, got %q", err.Error())
	}
}

func TestPlannerAgenticLLMError(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	mock := &mockToolClient{
		mockClient: mockClient{err: fmt.Errorf("API timeout")},
	}
	ctxBuilder := NewContextBuilder(store)
	planner := NewPlanner(mock, ctxBuilder)

	_, err := planner.Ask(context.Background(), "query")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "API timeout") {
		t.Errorf("expected API timeout error, got %q", err.Error())
	}
}

func TestPlannerSetMaxIterations(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	mock := &mockClient{response: "ok"}
	ctxBuilder := NewContextBuilder(store)
	planner := NewPlanner(mock, ctxBuilder)

	if planner.maxIterations != defaultMaxIterations {
		t.Errorf("expected default maxIterations=%d, got %d", defaultMaxIterations, planner.maxIterations)
	}

	planner.SetMaxIterations(10)
	if planner.maxIterations != 10 {
		t.Errorf("expected maxIterations=10, got %d", planner.maxIterations)
	}

	// Invalid value should be ignored.
	planner.SetMaxIterations(0)
	if planner.maxIterations != 10 {
		t.Errorf("expected maxIterations still 10, got %d", planner.maxIterations)
	}
}

func TestPlannerVerboseToolLogging(t *testing.T) {
	// Verify that verbose mode logs iteration numbers and tool names.
	store, cleanup := setupTestStore(t)
	defer cleanup()

	mock := &mockToolClient{
		responses: []llm.Response{
			{
				Content: "Let me check.",
				ToolCalls: []llm.ToolCall{
					{ID: "tc1", Name: "get_graph_overview", Arguments: map[string]any{}},
				},
			},
			{
				Content: "Here is the answer.",
			},
		},
	}
	ctxBuilder := NewContextBuilder(store)
	planner := NewPlanner(mock, ctxBuilder)

	var logs []string
	logger := func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}
	planner.SetVerbose(true, logger)
	planner.SetToolLogger(logger)

	resp, err := planner.Ask(context.Background(), "What is the project overview?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "Here is the answer." {
		t.Errorf("expected 'Here is the answer.', got %q", resp)
	}

	// Verify log entries contain expected messages.
	foundStartup := false
	foundIteration1 := false
	foundToolStart := false
	foundToolEnd := false
	for _, log := range logs {
		if strings.Contains(log, "Starting planner query") {
			foundStartup = true
		}
		if strings.Contains(log, "Planner iteration 1/") {
			foundIteration1 = true
		}
		if strings.Contains(log, "-> tool: get_graph_overview") {
			foundToolStart = true
		}
		if strings.Contains(log, "<- tool get_graph_overview (ok)") {
			foundToolEnd = true
		}
	}
	if !foundStartup {
		t.Error("expected startup log message")
	}
	if !foundIteration1 {
		t.Error("expected iteration 1 log message")
	}
	if !foundToolStart {
		t.Error("expected tool start log message")
	}
	if !foundToolEnd {
		t.Error("expected tool completion log message")
	}
}

func TestBaseAgentSetVerbose(t *testing.T) {
	mock := &mockClient{response: "ok"}
	ba := &BaseAgent{
		name:         "test",
		llmClient:    mock,
		systemPrompt: "test",
	}

	var logs []string
	ba.SetVerbose(true, func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	})

	_, err := ba.ask(context.Background(), "", "query")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(logs) == 0 {
		t.Error("expected verbose log from ask()")
	}
	foundProvider := false
	for _, log := range logs {
		if strings.Contains(log, "Sending query to LLM") {
			foundProvider = true
		}
	}
	if !foundProvider {
		t.Error("expected provider log message in verbose mode")
	}
}

func TestPlannerAgenticToolError(t *testing.T) {
	// Tool-capable client that calls an unknown tool -> should include error in response.
	store, cleanup := setupTestStore(t)
	defer cleanup()

	mock := &mockToolClient{
		responses: []llm.Response{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "tc1", Name: "nonexistent_tool", Arguments: map[string]any{}},
				},
			},
			{
				Content: "I got an error, but here is my answer.",
			},
		},
	}
	ctxBuilder := NewContextBuilder(store)
	planner := NewPlanner(mock, ctxBuilder)

	resp, err := planner.Ask(context.Background(), "query")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "I got an error, but here is my answer." {
		t.Errorf("expected fallback answer, got %q", resp)
	}
}
