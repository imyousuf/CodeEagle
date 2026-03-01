package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/llm"
)

func TestOllamaProviderRegistration(t *testing.T) {
	if !llm.IsProviderRegistered("ollama") {
		t.Fatal("expected 'ollama' provider to be registered via init()")
	}
}

func TestOllamaNewClientValidation(t *testing.T) {
	_, err := llm.NewClient(llm.Config{
		Provider: "ollama",
		// No model
	})
	if err == nil {
		t.Fatal("expected error when model is missing")
	}
	expected := "model is required for Ollama provider"
	if err.Error() != expected {
		t.Fatalf("expected error %q, got %q", expected, err.Error())
	}
}

func TestOllamaNewClientDefaults(t *testing.T) {
	client, err := newOllamaClient(llm.Config{Model: "test-model"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	oc := client.(*ollamaClient)
	if oc.baseURL != "http://localhost:11434" {
		t.Errorf("expected default base URL %q, got %q", "http://localhost:11434", oc.baseURL)
	}
	if oc.model != "test-model" {
		t.Errorf("expected model %q, got %q", "test-model", oc.model)
	}
	if client.Provider() != "ollama" {
		t.Errorf("expected provider %q, got %q", "ollama", client.Provider())
	}
	if client.Model() != "test-model" {
		t.Errorf("expected model %q, got %q", "test-model", client.Model())
	}
}

func TestOllamaNewClientCustomBaseURL(t *testing.T) {
	client, err := newOllamaClient(llm.Config{
		Model:   "test-model",
		BaseURL: "http://custom:9999",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	oc := client.(*ollamaClient)
	if oc.baseURL != "http://custom:9999" {
		t.Errorf("expected base URL %q, got %q", "http://custom:9999", oc.baseURL)
	}
}

func TestOllamaSupportsTools(t *testing.T) {
	client, err := newOllamaClient(llm.Config{Model: "test-model"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !llm.SupportsTools(client) {
		t.Error("expected ollamaClient to support tools")
	}
}

func TestOllamaChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("expected path /api/chat, got %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var req ollamaChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			w.WriteHeader(500)
			return
		}

		if req.Model != "test-model" {
			t.Errorf("expected model 'test-model', got %q", req.Model)
		}
		if req.Stream {
			t.Error("expected stream to be false")
		}
		// Should have system + user messages.
		if len(req.Messages) != 2 {
			t.Errorf("expected 2 messages, got %d", len(req.Messages))
		}
		if req.Messages[0].Role != "system" {
			t.Errorf("expected first message role 'system', got %q", req.Messages[0].Role)
		}
		if req.Messages[1].Role != "user" {
			t.Errorf("expected second message role 'user', got %q", req.Messages[1].Role)
		}

		resp := ollamaChatResponse{
			Model: "test-model",
			Message: ollamaMessage{
				Role:    "assistant",
				Content: "The answer is 4.",
			},
			Done:            true,
			DoneReason:      "stop",
			PromptEvalCount: 50,
			EvalCount:       10,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &ollamaClient{
		baseURL: server.URL,
		model:   "test-model",
		client:  server.Client(),
	}

	resp, err := client.Chat(context.Background(), "You are helpful.", []llm.Message{
		{Role: llm.RoleUser, Content: "What is 2+2?"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "The answer is 4." {
		t.Errorf("expected content 'The answer is 4.', got %q", resp.Content)
	}
	if resp.Usage.InputTokens != 50 {
		t.Errorf("expected 50 input tokens, got %d", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 10 {
		t.Errorf("expected 10 output tokens, got %d", resp.Usage.OutputTokens)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("expected finish_reason 'stop', got %q", resp.FinishReason)
	}
}

func TestOllamaChatWithToolsEndTurn(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ollamaChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			w.WriteHeader(500)
			return
		}

		if len(req.Tools) != 1 {
			t.Errorf("expected 1 tool, got %d", len(req.Tools))
		}
		if req.Tools[0].Type != "function" {
			t.Errorf("expected tool type 'function', got %q", req.Tools[0].Type)
		}
		if req.Tools[0].Function.Name != "search" {
			t.Errorf("expected tool name 'search', got %q", req.Tools[0].Function.Name)
		}

		resp := ollamaChatResponse{
			Model: "test-model",
			Message: ollamaMessage{
				Role:    "assistant",
				Content: "Here is the answer.",
			},
			Done:            true,
			DoneReason:      "stop",
			PromptEvalCount: 100,
			EvalCount:       50,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &ollamaClient{
		baseURL: server.URL,
		model:   "test-model",
		client:  server.Client(),
	}

	tools := []llm.Tool{
		{Name: "search", Description: "Search", Parameters: map[string]any{"type": "object"}},
	}
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "Hello"},
	}

	resp, err := client.ChatWithTools(context.Background(), "system", messages, tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "Here is the answer." {
		t.Errorf("expected content 'Here is the answer.', got %q", resp.Content)
	}
	if resp.HasToolCalls() {
		t.Error("expected no tool calls")
	}
	if resp.FinishReason != "stop" {
		t.Errorf("expected finish_reason 'stop', got %q", resp.FinishReason)
	}
}

func TestOllamaChatWithToolsToolUse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ollamaChatResponse{
			Model: "test-model",
			Message: ollamaMessage{
				Role:    "assistant",
				Content: "",
				ToolCalls: []ollamaToolCall{
					{
						Function: ollamaToolFunction{
							Name:      "search",
							Arguments: map[string]any{"query": "test"},
						},
					},
				},
			},
			Done:            true,
			DoneReason:      "stop",
			PromptEvalCount: 80,
			EvalCount:       30,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &ollamaClient{
		baseURL: server.URL,
		model:   "test-model",
		client:  server.Client(),
	}

	tools := []llm.Tool{
		{Name: "search", Description: "Search", Parameters: map[string]any{"type": "object"}},
	}
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "Find something"},
	}

	resp, err := client.ChatWithTools(context.Background(), "system", messages, tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.HasToolCalls() {
		t.Fatal("expected tool calls")
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_0" {
		t.Errorf("expected tool call ID 'call_0', got %q", tc.ID)
	}
	if tc.Name != "search" {
		t.Errorf("expected tool name 'search', got %q", tc.Name)
	}
	if tc.Arguments["query"] != "test" {
		t.Errorf("expected argument query='test', got %v", tc.Arguments)
	}
}

func TestOllamaConvertMessages(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "Hello"},
		{Role: llm.RoleAssistant, Content: "Hi there"},
	}

	result := convertToOllamaMessages("You are helpful.", messages)
	if len(result) != 3 {
		t.Fatalf("expected 3 messages (system + 2), got %d", len(result))
	}
	if result[0].Role != "system" {
		t.Errorf("msg 0: expected role 'system', got %q", result[0].Role)
	}
	if result[0].Content != "You are helpful." {
		t.Errorf("msg 0: expected system content, got %q", result[0].Content)
	}
	if result[1].Role != "user" {
		t.Errorf("msg 1: expected role 'user', got %q", result[1].Role)
	}
	if result[2].Role != "assistant" {
		t.Errorf("msg 2: expected role 'assistant', got %q", result[2].Role)
	}
}

func TestOllamaConvertMessagesNoSystemPrompt(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "Hello"},
	}

	result := convertToOllamaMessages("", messages)
	if len(result) != 1 {
		t.Fatalf("expected 1 message (no system), got %d", len(result))
	}
	if result[0].Role != "user" {
		t.Errorf("msg 0: expected role 'user', got %q", result[0].Role)
	}
}

func TestOllamaConvertToolMessages(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "Hello"},
		{Role: llm.RoleAssistant, Content: "Thinking", ToolCalls: []llm.ToolCall{
			{ID: "call_0", Name: "search", Arguments: map[string]any{"q": "test"}},
		}},
		{Role: llm.RoleTool, Content: "result1", ToolCallID: "call_0"},
		{Role: llm.RoleAssistant, Content: "Final answer"},
	}

	result := convertToOllamaToolMessages("system prompt", messages)
	if len(result) != 5 { // system + user + assistant(tool_call) + tool + assistant
		t.Fatalf("expected 5 messages, got %d", len(result))
	}

	// System message.
	if result[0].Role != "system" {
		t.Errorf("msg 0: expected role 'system', got %q", result[0].Role)
	}

	// User message.
	if result[1].Role != "user" {
		t.Errorf("msg 1: expected role 'user', got %q", result[1].Role)
	}

	// Assistant with tool calls.
	if result[2].Role != "assistant" {
		t.Errorf("msg 2: expected role 'assistant', got %q", result[2].Role)
	}
	if len(result[2].ToolCalls) != 1 {
		t.Fatalf("msg 2: expected 1 tool call, got %d", len(result[2].ToolCalls))
	}
	if result[2].ToolCalls[0].Function.Name != "search" {
		t.Errorf("msg 2 tool call: expected name 'search', got %q", result[2].ToolCalls[0].Function.Name)
	}

	// Tool result.
	if result[3].Role != "tool" {
		t.Errorf("msg 3: expected role 'tool', got %q", result[3].Role)
	}
	if result[3].Content != "result1" {
		t.Errorf("msg 3: expected content 'result1', got %q", result[3].Content)
	}

	// Final assistant.
	if result[4].Role != "assistant" {
		t.Errorf("msg 4: expected role 'assistant', got %q", result[4].Role)
	}
}

func TestOllamaConvertTools(t *testing.T) {
	tools := []llm.Tool{
		{
			Name:        "search",
			Description: "Search things",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}},
		},
	}

	defs := convertToOllamaTools(tools)
	if len(defs) != 1 {
		t.Fatalf("expected 1 definition, got %d", len(defs))
	}
	if defs[0].Type != "function" {
		t.Errorf("expected type 'function', got %q", defs[0].Type)
	}
	if defs[0].Function.Name != "search" {
		t.Errorf("expected name 'search', got %q", defs[0].Function.Name)
	}
	if defs[0].Function.Parameters == nil {
		t.Error("expected non-nil parameters")
	}
}

func TestOllamaConvertToolsEmpty(t *testing.T) {
	defs := convertToOllamaTools(nil)
	if defs != nil {
		t.Errorf("expected nil for empty tools, got %v", defs)
	}
}

func TestOllamaChatAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ollamaErrorResponse{Error: "model not found"})
	}))
	defer server.Close()

	client := &ollamaClient{
		baseURL: server.URL,
		model:   "nonexistent",
		client:  server.Client(),
	}

	_, err := client.Chat(context.Background(), "", []llm.Message{
		{Role: llm.RoleUser, Content: "Hello"},
	})
	if err == nil {
		t.Fatal("expected error for API error response")
	}
	if !contains(err.Error(), "model not found") {
		t.Errorf("expected error to contain 'model not found', got %q", err.Error())
	}
}

func TestOllamaClose(t *testing.T) {
	client, _ := newOllamaClient(llm.Config{Model: "test"})
	if err := client.Close(); err != nil {
		t.Errorf("expected nil error from Close, got %v", err)
	}
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
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
