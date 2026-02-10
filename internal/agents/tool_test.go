package agents

import (
	"context"
	"fmt"
	"testing"
)

// mockTool implements Tool for testing.
type mockTool struct {
	name        string
	description string
	params      map[string]any
	result      string
	success     bool
}

func (m *mockTool) Name() string               { return m.name }
func (m *mockTool) Description() string        { return m.description }
func (m *mockTool) Parameters() map[string]any { return m.params }
func (m *mockTool) Execute(_ context.Context, _ map[string]any) (string, bool) {
	return m.result, m.success
}

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()

	tool := &mockTool{name: "test_tool", description: "A test tool", result: "ok", success: true}
	r.Register(tool)

	got, ok := r.Get("test_tool")
	if !ok {
		t.Fatal("expected tool to be found")
	}
	if got.Name() != "test_tool" {
		t.Errorf("expected name 'test_tool', got %q", got.Name())
	}

	_, ok = r.Get("nonexistent")
	if ok {
		t.Error("expected nonexistent tool to not be found")
	}
}

func TestRegistryRegisterOverwrite(t *testing.T) {
	r := NewRegistry()

	tool1 := &mockTool{name: "tool", description: "v1", result: "v1", success: true}
	tool2 := &mockTool{name: "tool", description: "v2", result: "v2", success: true}

	r.Register(tool1)
	r.Register(tool2)

	got, ok := r.Get("tool")
	if !ok {
		t.Fatal("expected tool to be found")
	}
	if got.Description() != "v2" {
		t.Errorf("expected description 'v2', got %q", got.Description())
	}

	// Should not duplicate in order.
	defs := r.Definitions()
	if len(defs) != 1 {
		t.Errorf("expected 1 definition, got %d", len(defs))
	}
}

func TestRegistryExecute(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{name: "greet", result: "hello", success: true})

	result, success, err := r.Execute(context.Background(), "greet", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hello" {
		t.Errorf("expected 'hello', got %q", result)
	}
	if !success {
		t.Error("expected success=true")
	}
}

func TestRegistryExecuteUnknown(t *testing.T) {
	r := NewRegistry()

	_, _, err := r.Execute(context.Background(), "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
	if err.Error() != "unknown tool: nonexistent" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRegistryDefinitions(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{
		name:        "tool_a",
		description: "Tool A",
		params:      map[string]any{"type": "object"},
	})
	r.Register(&mockTool{
		name:        "tool_b",
		description: "Tool B",
		params:      map[string]any{"type": "object"},
	})

	defs := r.Definitions()
	if len(defs) != 2 {
		t.Fatalf("expected 2 definitions, got %d", len(defs))
	}
	// Should be in registration order.
	if defs[0].Name != "tool_a" {
		t.Errorf("expected first tool 'tool_a', got %q", defs[0].Name)
	}
	if defs[1].Name != "tool_b" {
		t.Errorf("expected second tool 'tool_b', got %q", defs[1].Name)
	}
}

func TestToLLMTool(t *testing.T) {
	tool := &mockTool{
		name:        "search",
		description: "Search something",
		params:      map[string]any{"type": "object", "properties": map[string]any{}},
	}

	lt := ToLLMTool(tool)
	if lt.Name != "search" {
		t.Errorf("expected name 'search', got %q", lt.Name)
	}
	if lt.Description != "Search something" {
		t.Errorf("expected description 'Search something', got %q", lt.Description)
	}
}

func TestToLLMTools(t *testing.T) {
	tools := []Tool{
		&mockTool{name: "a"},
		&mockTool{name: "b"},
	}

	lts := ToLLMTools(tools)
	if len(lts) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(lts))
	}
	if lts[0].Name != "a" || lts[1].Name != "b" {
		t.Errorf("unexpected tool names: %q, %q", lts[0].Name, lts[1].Name)
	}
}

func TestRegistryLogging(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{name: "search_nodes", result: "found 3", success: true})

	var logs []string
	r.SetLogger(func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	})

	_, _, err := r.Execute(context.Background(), "search_nodes", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have logged tool start and completion.
	if len(logs) != 2 {
		t.Fatalf("expected 2 log entries, got %d: %v", len(logs), logs)
	}
	if logs[0] != "  -> tool: search_nodes" {
		t.Errorf("expected start log, got %q", logs[0])
	}
	if logs[1] != "  <- tool search_nodes (ok)" {
		t.Errorf("expected completion log, got %q", logs[1])
	}
}

func TestRegistryLoggingFailedTool(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{name: "failing_tool", result: "error", success: false})

	var logs []string
	r.SetLogger(func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	})

	_, success, err := r.Execute(context.Background(), "failing_tool", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if success {
		t.Error("expected success=false")
	}

	if len(logs) != 2 {
		t.Fatalf("expected 2 log entries, got %d: %v", len(logs), logs)
	}
	if logs[1] != "  <- tool failing_tool (failed)" {
		t.Errorf("expected failure log, got %q", logs[1])
	}
}

func TestRegistryNoLogger(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{name: "quiet_tool", result: "ok", success: true})

	// No logger set — Execute should not panic.
	result, success, err := r.Execute(context.Background(), "quiet_tool", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !success || result != "ok" {
		t.Errorf("unexpected result: %q, success=%v", result, success)
	}
}
