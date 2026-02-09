package llm

import "testing"

func TestHasToolCalls(t *testing.T) {
	tests := []struct {
		name     string
		resp     *Response
		expected bool
	}{
		{
			name:     "no tool calls",
			resp:     &Response{Content: "hello"},
			expected: false,
		},
		{
			name:     "nil tool calls",
			resp:     &Response{Content: "hello", ToolCalls: nil},
			expected: false,
		},
		{
			name:     "empty tool calls",
			resp:     &Response{Content: "hello", ToolCalls: []ToolCall{}},
			expected: false,
		},
		{
			name: "with tool calls",
			resp: &Response{
				Content: "",
				ToolCalls: []ToolCall{
					{ID: "tc1", Name: "get_info", Arguments: map[string]any{"key": "value"}},
				},
			},
			expected: true,
		},
		{
			name: "multiple tool calls",
			resp: &Response{
				ToolCalls: []ToolCall{
					{ID: "tc1", Name: "get_info"},
					{ID: "tc2", Name: "search"},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.resp.HasToolCalls(); got != tt.expected {
				t.Errorf("HasToolCalls() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestRoleConstants(t *testing.T) {
	if RoleTool != "tool" {
		t.Errorf("RoleTool = %q, want %q", RoleTool, "tool")
	}
}
