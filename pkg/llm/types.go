// Package llm provides a unified interface for interacting with Large Language Models.
package llm

// Role represents the role of a message sender in a conversation.
type Role string

const (
	// RoleSystem represents a system message.
	RoleSystem Role = "system"
	// RoleUser represents a message from the user.
	RoleUser Role = "user"
	// RoleAssistant represents a message from the assistant/model.
	RoleAssistant Role = "assistant"
	// RoleTool represents a tool result message.
	RoleTool Role = "tool"
)

// ToolCall represents a tool invocation requested by the LLM.
type ToolCall struct {
	// ID is a unique identifier for this tool call, used to match results.
	ID string `json:"id"`
	// Name is the name of the tool to invoke.
	Name string `json:"name"`
	// Arguments are the tool's input parameters.
	Arguments map[string]any `json:"arguments"`
}

// Tool describes a tool that can be made available to the LLM.
type Tool struct {
	// Name is the tool's name.
	Name string `json:"name"`
	// Description explains what the tool does.
	Description string `json:"description"`
	// Parameters is the JSON Schema describing the tool's input.
	Parameters map[string]any `json:"parameters"`
}

// Message represents a single message in a conversation.
type Message struct {
	// Role indicates who sent this message.
	Role Role `json:"role"`
	// Content is the text content of the message.
	Content string `json:"content"`
	// ToolCallID is the ID of the tool call this message is a result for (role=tool).
	ToolCallID string `json:"tool_call_id,omitempty"`
	// ToolCalls contains tool invocations requested by the assistant.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// Response represents a response from the LLM.
type Response struct {
	// Content is the text content of the response.
	Content string `json:"content"`
	// ToolCalls contains tool invocations requested by the model.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// FinishReason indicates why the model stopped generating (e.g., "end_turn", "tool_use").
	FinishReason string `json:"finish_reason,omitempty"`
	// Usage contains token usage information.
	Usage TokenUsage `json:"usage"`
}

// HasToolCalls returns true if the response contains tool call requests.
func (r *Response) HasToolCalls() bool {
	return len(r.ToolCalls) > 0
}

// TokenUsage contains token usage information for a request.
type TokenUsage struct {
	// InputTokens is the number of tokens in the prompt.
	InputTokens int `json:"input_tokens"`
	// OutputTokens is the number of tokens in the completion.
	OutputTokens int `json:"output_tokens"`
}
