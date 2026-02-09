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
)

// Message represents a single message in a conversation.
type Message struct {
	// Role indicates who sent this message.
	Role Role `json:"role"`
	// Content is the text content of the message.
	Content string `json:"content"`
}

// Response represents a response from the LLM.
type Response struct {
	// Content is the text content of the response.
	Content string `json:"content"`
	// Usage contains token usage information.
	Usage TokenUsage `json:"usage"`
}

// TokenUsage contains token usage information for a request.
type TokenUsage struct {
	// InputTokens is the number of tokens in the prompt.
	InputTokens int `json:"input_tokens"`
	// OutputTokens is the number of tokens in the completion.
	OutputTokens int `json:"output_tokens"`
}
