package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/llm"
)

func TestProviderRegistration(t *testing.T) {
	if !llm.IsProviderRegistered("anthropic") {
		t.Fatal("expected 'anthropic' provider to be registered via init()")
	}
}

func TestNewClientValidation(t *testing.T) {
	_, err := llm.NewClient(llm.Config{
		Provider: "anthropic",
		// No API key
	})
	if err == nil {
		t.Fatal("expected error when API key is missing")
	}
	expected := "API key is required for Anthropic provider"
	if err.Error() != expected {
		t.Fatalf("expected error %q, got %q", expected, err.Error())
	}
}

func TestNewClientDefaults(t *testing.T) {
	client, err := llm.NewClient(llm.Config{
		Provider: "anthropic",
		APIKey:   "test-key",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer client.Close()

	if client.Model() != "claude-sonnet-4-5-20250929" {
		t.Errorf("expected default model %q, got %q", "claude-sonnet-4-5-20250929", client.Model())
	}

	if client.Provider() != "anthropic" {
		t.Errorf("expected provider %q, got %q", "anthropic", client.Provider())
	}
}

func TestNewClientCustomModel(t *testing.T) {
	client, err := llm.NewClient(llm.Config{
		Provider: "anthropic",
		APIKey:   "test-key",
		Model:    "claude-haiku-4-5-20251001",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer client.Close()

	if client.Model() != "claude-haiku-4-5-20251001" {
		t.Errorf("expected model %q, got %q", "claude-haiku-4-5-20251001", client.Model())
	}
}

func TestNewClientCustomBaseURL(t *testing.T) {
	client, err := newAnthropicClient(llm.Config{
		APIKey:  "test-key",
		BaseURL: "https://custom.api.example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ac := client.(*anthropicClient)
	if ac.baseURL != "https://custom.api.example.com" {
		t.Errorf("expected base URL %q, got %q", "https://custom.api.example.com", ac.baseURL)
	}
}

func TestUnknownProvider(t *testing.T) {
	_, err := llm.NewClient(llm.Config{
		Provider: "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestEmptyProvider(t *testing.T) {
	_, err := llm.NewClient(llm.Config{})
	if err == nil {
		t.Fatal("expected error when provider is empty")
	}
	expected := "provider is required"
	if err.Error() != expected {
		t.Fatalf("expected error %q, got %q", expected, err.Error())
	}
}

func TestAnthropicSupportsTools(t *testing.T) {
	client, err := newAnthropicClient(llm.Config{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !llm.SupportsTools(client) {
		t.Error("expected anthropicClient to support tools")
	}
}

func TestAnthropicChatWithToolsEndTurn(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request format.
		var req anthropicToolRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			w.WriteHeader(500)
			return
		}
		if len(req.Tools) != 1 {
			t.Errorf("expected 1 tool, got %d", len(req.Tools))
		}
		if req.Tools[0].Name != "search" {
			t.Errorf("expected tool name 'search', got %q", req.Tools[0].Name)
		}

		// Return a text-only response (end_turn).
		resp := anthropicToolResponse{
			Content: []anthropicToolResponseBlock{
				{Type: "text", Text: "Here is the answer."},
			},
			StopReason: "end_turn",
			Usage:      anthropicUsage{InputTokens: 100, OutputTokens: 50},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &anthropicClient{
		apiKey:  "test-key",
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
	if resp.FinishReason != "end_turn" {
		t.Errorf("expected finish_reason 'end_turn', got %q", resp.FinishReason)
	}
	if resp.Usage.InputTokens != 100 {
		t.Errorf("expected 100 input tokens, got %d", resp.Usage.InputTokens)
	}
}

func TestAnthropicChatWithToolsToolUse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := anthropicToolResponse{
			Content: []anthropicToolResponseBlock{
				{Type: "text", Text: "Let me search for that."},
				{
					Type:  "tool_use",
					ID:    "toolu_123",
					Name:  "search",
					Input: map[string]any{"query": "test"},
				},
			},
			StopReason: "tool_use",
			Usage:      anthropicUsage{InputTokens: 80, OutputTokens: 30},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &anthropicClient{
		apiKey:  "test-key",
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
	if resp.Content != "Let me search for that." {
		t.Errorf("expected text content, got %q", resp.Content)
	}
	if !resp.HasToolCalls() {
		t.Fatal("expected tool calls")
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "toolu_123" {
		t.Errorf("expected tool call ID 'toolu_123', got %q", tc.ID)
	}
	if tc.Name != "search" {
		t.Errorf("expected tool name 'search', got %q", tc.Name)
	}
	if tc.Arguments["query"] != "test" {
		t.Errorf("expected argument query='test', got %v", tc.Arguments)
	}
	if resp.FinishReason != "tool_use" {
		t.Errorf("expected finish_reason 'tool_use', got %q", resp.FinishReason)
	}
}

func TestConvertMessagesToToolFormat(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "Hello"},
		{Role: llm.RoleAssistant, Content: "Thinking", ToolCalls: []llm.ToolCall{
			{ID: "tc1", Name: "search", Arguments: map[string]any{"q": "test"}},
		}},
		{Role: llm.RoleTool, Content: "result1", ToolCallID: "tc1"},
		{Role: llm.RoleAssistant, Content: "Final answer"},
	}

	result := convertMessagesToToolFormat(messages)
	if len(result) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(result))
	}

	// First: user message.
	if result[0].Role != "user" {
		t.Errorf("msg 0: expected role 'user', got %q", result[0].Role)
	}

	// Second: assistant with tool_use blocks.
	if result[1].Role != "assistant" {
		t.Errorf("msg 1: expected role 'assistant', got %q", result[1].Role)
	}
	blocks, ok := result[1].Content.([]anthropicContentBlock)
	if !ok {
		t.Fatalf("msg 1: expected content to be []anthropicContentBlock, got %T", result[1].Content)
	}
	if len(blocks) != 2 { // text + tool_use
		t.Fatalf("msg 1: expected 2 blocks, got %d", len(blocks))
	}
	if blocks[0].Type != "text" {
		t.Errorf("msg 1 block 0: expected type 'text', got %q", blocks[0].Type)
	}
	if blocks[1].Type != "tool_use" {
		t.Errorf("msg 1 block 1: expected type 'tool_use', got %q", blocks[1].Type)
	}

	// Third: tool result batched as user message.
	if result[2].Role != "user" {
		t.Errorf("msg 2: expected role 'user', got %q", result[2].Role)
	}
	toolBlocks, ok := result[2].Content.([]anthropicContentBlock)
	if !ok {
		t.Fatalf("msg 2: expected content to be []anthropicContentBlock, got %T", result[2].Content)
	}
	if len(toolBlocks) != 1 {
		t.Fatalf("msg 2: expected 1 tool_result block, got %d", len(toolBlocks))
	}
	if toolBlocks[0].Type != "tool_result" {
		t.Errorf("msg 2 block 0: expected type 'tool_result', got %q", toolBlocks[0].Type)
	}
	if toolBlocks[0].ToolUseID != "tc1" {
		t.Errorf("msg 2 block 0: expected tool_use_id 'tc1', got %q", toolBlocks[0].ToolUseID)
	}

	// Fourth: assistant text.
	if result[3].Role != "assistant" {
		t.Errorf("msg 3: expected role 'assistant', got %q", result[3].Role)
	}
}

func TestConvertMessagesToolResultBatching(t *testing.T) {
	// Multiple consecutive tool results should be batched into one user message.
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "query"},
		{Role: llm.RoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
			{ID: "tc1", Name: "a"},
			{ID: "tc2", Name: "b"},
		}},
		{Role: llm.RoleTool, Content: "r1", ToolCallID: "tc1"},
		{Role: llm.RoleTool, Content: "r2", ToolCallID: "tc2"},
	}

	result := convertMessagesToToolFormat(messages)
	if len(result) != 3 {
		t.Fatalf("expected 3 messages (user, assistant, batched tool results), got %d", len(result))
	}

	// Third message should contain both tool results.
	toolBlocks, ok := result[2].Content.([]anthropicContentBlock)
	if !ok {
		t.Fatalf("expected []anthropicContentBlock, got %T", result[2].Content)
	}
	if len(toolBlocks) != 2 {
		t.Fatalf("expected 2 tool_result blocks, got %d", len(toolBlocks))
	}
}

func TestConvertToolDefinitions(t *testing.T) {
	tools := []llm.Tool{
		{
			Name:        "search",
			Description: "Search things",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}},
		},
	}

	defs := convertToolDefinitions(tools)
	if len(defs) != 1 {
		t.Fatalf("expected 1 definition, got %d", len(defs))
	}
	if defs[0].Name != "search" {
		t.Errorf("expected name 'search', got %q", defs[0].Name)
	}
	if defs[0].InputSchema == nil {
		t.Error("expected non-nil input_schema")
	}
}
