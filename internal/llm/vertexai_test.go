package llm

import (
	"testing"

	"google.golang.org/genai"

	"github.com/imyousuf/CodeEagle/pkg/llm"
)

func TestVertexAIProviderRegistration(t *testing.T) {
	if !llm.IsProviderRegistered("vertex-ai") {
		t.Fatal("expected 'vertex-ai' provider to be registered via init()")
	}
}

func TestVertexAIConfigValidation(t *testing.T) {
	_, err := llm.NewClient(llm.Config{
		Provider: "vertex-ai",
		// No project
	})
	if err == nil {
		t.Fatal("expected error when project is missing")
	}
	expected := "project is required for Vertex AI provider"
	if err.Error() != expected {
		t.Fatalf("expected error %q, got %q", expected, err.Error())
	}
}

func TestConvertToGenAITools(t *testing.T) {
	tools := []llm.Tool{
		{
			Name:        "search",
			Description: "Search nodes",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
				},
			},
		},
		{
			Name:        "get_info",
			Description: "Get info",
			Parameters:  map[string]any{"type": "object"},
		},
	}

	result := convertToGenAITools(tools)
	if len(result) != 1 {
		t.Fatalf("expected 1 genai.Tool (with multiple declarations), got %d", len(result))
	}
	if len(result[0].FunctionDeclarations) != 2 {
		t.Fatalf("expected 2 function declarations, got %d", len(result[0].FunctionDeclarations))
	}
	if result[0].FunctionDeclarations[0].Name != "search" {
		t.Errorf("expected name 'search', got %q", result[0].FunctionDeclarations[0].Name)
	}
	if result[0].FunctionDeclarations[1].Name != "get_info" {
		t.Errorf("expected name 'get_info', got %q", result[0].FunctionDeclarations[1].Name)
	}
}

func TestConvertToGenAIToolsEmpty(t *testing.T) {
	result := convertToGenAITools(nil)
	if result != nil {
		t.Errorf("expected nil for empty tools, got %v", result)
	}
}

func TestConvertMessagesWithTools(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "What is X?"},
		{Role: llm.RoleAssistant, Content: "Let me check.", ToolCalls: []llm.ToolCall{
			{ID: "fc1", Name: "search", Arguments: map[string]any{"q": "X"}},
		}},
		{Role: llm.RoleTool, Content: "X is a struct", ToolCallID: "fc1"},
		{Role: llm.RoleAssistant, Content: "X is a struct."},
	}

	contents := convertMessagesWithTools(messages)
	if len(contents) != 4 {
		t.Fatalf("expected 4 contents, got %d", len(contents))
	}

	// First: user
	if contents[0].Role != "user" {
		t.Errorf("content 0: expected role 'user', got %q", contents[0].Role)
	}

	// Second: model with function call
	if contents[1].Role != "model" {
		t.Errorf("content 1: expected role 'model', got %q", contents[1].Role)
	}
	if len(contents[1].Parts) != 2 {
		t.Fatalf("content 1: expected 2 parts (text + function_call), got %d", len(contents[1].Parts))
	}
	if contents[1].Parts[1].FunctionCall == nil {
		t.Fatal("content 1 part 1: expected FunctionCall")
	}
	if contents[1].Parts[1].FunctionCall.Name != "search" {
		t.Errorf("expected function name 'search', got %q", contents[1].Parts[1].FunctionCall.Name)
	}

	// Third: user with function response
	if contents[2].Role != "user" {
		t.Errorf("content 2: expected role 'user', got %q", contents[2].Role)
	}
	if len(contents[2].Parts) != 1 {
		t.Fatalf("content 2: expected 1 part, got %d", len(contents[2].Parts))
	}
	if contents[2].Parts[0].FunctionResponse == nil {
		t.Fatal("content 2 part 0: expected FunctionResponse")
	}

	// Fourth: model text
	if contents[3].Role != "model" {
		t.Errorf("content 3: expected role 'model', got %q", contents[3].Role)
	}
}

func TestConvertMessagesWithToolsBatching(t *testing.T) {
	// Multiple consecutive tool results should be batched.
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "query"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "fc1", Name: "a"}, {ID: "fc2", Name: "b"},
		}},
		{Role: llm.RoleTool, Content: "r1", ToolCallID: "fc1"},
		{Role: llm.RoleTool, Content: "r2", ToolCallID: "fc2"},
	}

	contents := convertMessagesWithTools(messages)
	if len(contents) != 3 {
		t.Fatalf("expected 3 contents (user, model, batched results), got %d", len(contents))
	}
	// Third should have 2 function response parts.
	if len(contents[2].Parts) != 2 {
		t.Fatalf("expected 2 parts in batched results, got %d", len(contents[2].Parts))
	}
}

func TestConvertResponseWithTools(t *testing.T) {
	// Response with function calls.
	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Parts: []*genai.Part{
						{Text: "Checking..."},
						{FunctionCall: &genai.FunctionCall{
							ID:   "fc1",
							Name: "search",
							Args: map[string]any{"q": "test"},
						}},
					},
				},
				FinishReason: "STOP",
			},
		},
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     100,
			CandidatesTokenCount: 50,
		},
	}

	result := convertResponseWithTools(resp)
	if result.Content != "Checking..." {
		t.Errorf("expected content 'Checking...', got %q", result.Content)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "search" {
		t.Errorf("expected tool name 'search', got %q", result.ToolCalls[0].Name)
	}
	if result.FinishReason != "STOP" {
		t.Errorf("expected finish reason 'STOP', got %q", result.FinishReason)
	}
	if result.Usage.InputTokens != 100 {
		t.Errorf("expected 100 input tokens, got %d", result.Usage.InputTokens)
	}
}

func TestConvertResponseWithToolsNil(t *testing.T) {
	result := convertResponseWithTools(nil)
	if result.Content != "" {
		t.Errorf("expected empty content, got %q", result.Content)
	}
	if result.HasToolCalls() {
		t.Error("expected no tool calls")
	}
}

func TestConvertResponseWithToolsNoCandidates(t *testing.T) {
	result := convertResponseWithTools(&genai.GenerateContentResponse{})
	if result.Content != "" {
		t.Errorf("expected empty content, got %q", result.Content)
	}
}
