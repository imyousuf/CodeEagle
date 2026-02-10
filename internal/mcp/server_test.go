package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/agents"
)

// mockToolForMCP implements agents.Tool for testing.
type mockToolForMCP struct {
	name        string
	description string
	params      map[string]any
	result      string
	success     bool
}

func (m *mockToolForMCP) Name() string               { return m.name }
func (m *mockToolForMCP) Description() string        { return m.description }
func (m *mockToolForMCP) Parameters() map[string]any { return m.params }
func (m *mockToolForMCP) Execute(_ context.Context, _ map[string]any) (string, bool) {
	return m.result, m.success
}

func setupTestRegistry() *agents.Registry {
	r := agents.NewRegistry()
	r.Register(&mockToolForMCP{
		name:        "test_tool",
		description: "A test tool",
		params:      map[string]any{"type": "object", "properties": map[string]any{}},
		result:      "test result",
		success:     true,
	})
	r.Register(&mockToolForMCP{
		name:        "search_nodes",
		description: "Search nodes",
		params:      map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}},
		result:      "found 5 nodes",
		success:     true,
	})
	return r
}

func sendAndReceive(t *testing.T, registry *agents.Registry, requests ...string) []jsonRPCResponse {
	t.Helper()
	input := strings.Join(requests, "\n") + "\n"
	reader := strings.NewReader(input)
	var output bytes.Buffer

	server := NewServerWithIO(registry, reader, &output)
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("server.Run error: %v", err)
	}

	var responses []jsonRPCResponse
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		if line == "" {
			continue
		}
		var resp jsonRPCResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("failed to parse response: %v\nline: %s", err, line)
		}
		responses = append(responses, resp)
	}
	return responses
}

func TestInitialize(t *testing.T) {
	registry := setupTestRegistry()
	responses := sendAndReceive(t, registry,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
	)

	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	resp := responses[0]
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	// Parse the result as initializeResult.
	resultBytes, _ := json.Marshal(resp.Result)
	var result initializeResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if result.ProtocolVersion != protocolVersion {
		t.Errorf("expected protocol version %q, got %q", protocolVersion, result.ProtocolVersion)
	}
	if result.ServerInfo.Name != "codeeagle" {
		t.Errorf("expected server name 'codeeagle', got %q", result.ServerInfo.Name)
	}
}

func TestInitializedNotification(t *testing.T) {
	registry := setupTestRegistry()
	// "initialized" is a notification (no id), should produce no response.
	responses := sendAndReceive(t, registry,
		`{"jsonrpc":"2.0","method":"initialized"}`,
	)
	if len(responses) != 0 {
		t.Errorf("expected no responses for notification, got %d", len(responses))
	}
}

func TestToolsList(t *testing.T) {
	registry := setupTestRegistry()
	responses := sendAndReceive(t, registry,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	)

	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	resp := responses[0]
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result map[string]any
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("expected tools array, got %T", result["tools"])
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}

	// Check first tool.
	tool0, _ := tools[0].(map[string]any)
	if tool0["name"] != "test_tool" {
		t.Errorf("expected first tool 'test_tool', got %q", tool0["name"])
	}
	if tool0["inputSchema"] == nil {
		t.Error("expected inputSchema to be present")
	}
}

func TestToolsCall(t *testing.T) {
	registry := setupTestRegistry()
	responses := sendAndReceive(t, registry,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"test_tool","arguments":{}}}`,
	)

	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	resp := responses[0]
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result toolCallResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if result.IsError {
		t.Error("expected isError=false")
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result.Content))
	}
	if result.Content[0].Text != "test result" {
		t.Errorf("expected 'test result', got %q", result.Content[0].Text)
	}
}

func TestToolsCallUnknown(t *testing.T) {
	registry := setupTestRegistry()
	responses := sendAndReceive(t, registry,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nonexistent","arguments":{}}}`,
	)

	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	resp := responses[0]
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %v", resp.Error)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result toolCallResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if !result.IsError {
		t.Error("expected isError=true for unknown tool")
	}
	if !strings.Contains(result.Content[0].Text, "unknown tool") {
		t.Errorf("expected error about unknown tool, got %q", result.Content[0].Text)
	}
}

func TestUnknownMethod(t *testing.T) {
	registry := setupTestRegistry()
	responses := sendAndReceive(t, registry,
		`{"jsonrpc":"2.0","id":5,"method":"unknown/method","params":{}}`,
	)

	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	resp := responses[0]
	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("expected error code -32601, got %d", resp.Error.Code)
	}
}

func TestInvalidJSON(t *testing.T) {
	registry := setupTestRegistry()
	responses := sendAndReceive(t, registry,
		`not valid json`,
	)

	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	resp := responses[0]
	if resp.Error == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if resp.Error.Code != -32700 {
		t.Errorf("expected error code -32700, got %d", resp.Error.Code)
	}
}

func TestMultipleRequests(t *testing.T) {
	registry := setupTestRegistry()
	responses := sendAndReceive(t, registry,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"test_tool","arguments":{}}}`,
	)

	// initialized is a notification (no response), so expect 3 responses.
	if len(responses) != 3 {
		t.Fatalf("expected 3 responses, got %d", len(responses))
	}
}

func TestToolsCallWithArguments(t *testing.T) {
	registry := setupTestRegistry()
	responses := sendAndReceive(t, registry,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_nodes","arguments":{"q":"test"}}}`,
	)

	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	resp := responses[0]
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result toolCallResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if result.Content[0].Text != "found 5 nodes" {
		t.Errorf("expected 'found 5 nodes', got %q", result.Content[0].Text)
	}
}
