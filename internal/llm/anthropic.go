// Package llm provides LLM client implementations.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/imyousuf/CodeEagle/pkg/llm"
)

const (
	defaultAnthropicBaseURL = "https://api.anthropic.com"
	defaultAnthropicModel   = "claude-sonnet-4-5-20250929"
	anthropicAPIVersion     = "2023-06-01"
	defaultMaxTokens        = 4096
)

func init() {
	llm.RegisterProvider("anthropic", newAnthropicClient)
}

// anthropicClient implements llm.Client using the Anthropic Messages API.
type anthropicClient struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
}

// newAnthropicClient creates a new Anthropic client.
func newAnthropicClient(cfg llm.Config) (llm.Client, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("API key is required for Anthropic provider")
	}

	model := cfg.Model
	if model == "" {
		model = defaultAnthropicModel
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultAnthropicBaseURL
	}

	return &anthropicClient{
		apiKey:  cfg.APIKey,
		baseURL: baseURL,
		model:   model,
		client:  &http.Client{},
	}, nil
}

// anthropicRequest is the request body for the Anthropic Messages API.
type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
}

// anthropicMessage is a message in the Anthropic API format.
type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// anthropicResponse is the response from the Anthropic Messages API.
type anthropicResponse struct {
	Content []anthropicContent `json:"content"`
	Usage   anthropicUsage     `json:"usage"`
}

// anthropicContent is a content block in the Anthropic response.
type anthropicContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// anthropicUsage contains token usage from the Anthropic response.
type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// anthropicError is the error response from the Anthropic API.
type anthropicError struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// --- Tool-use wire format types ---

// anthropicToolRequest extends the request body with tool definitions.
type anthropicToolRequest struct {
	Model     string                    `json:"model"`
	MaxTokens int                       `json:"max_tokens"`
	System    string                    `json:"system,omitempty"`
	Messages  []anthropicToolMessage    `json:"messages"`
	Tools     []anthropicToolDefinition `json:"tools,omitempty"`
}

// anthropicToolDefinition describes a tool for the Anthropic API.
type anthropicToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// anthropicToolMessage handles both simple text messages and content-block messages.
type anthropicToolMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []anthropicContentBlock
}

// anthropicContentBlock represents a content block in the Anthropic API.
type anthropicContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   any            `json:"content,omitempty"` // string for tool_result
}

// anthropicToolResponse is the response body from the Anthropic Messages API with tool support.
type anthropicToolResponse struct {
	Content    []anthropicToolResponseBlock `json:"content"`
	StopReason string                       `json:"stop_reason"`
	Usage      anthropicUsage               `json:"usage"`
}

// anthropicToolResponseBlock represents a content block in the tool response.
type anthropicToolResponseBlock struct {
	Type  string         `json:"type"`
	Text  string         `json:"text,omitempty"`
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`
}

// Chat sends a system prompt and messages to the Anthropic API and returns a response.
func (c *anthropicClient) Chat(ctx context.Context, systemPrompt string, messages []llm.Message) (*llm.Response, error) {
	apiMessages := make([]anthropicMessage, 0, len(messages))
	for _, msg := range messages {
		apiMessages = append(apiMessages, anthropicMessage{
			Role:    string(msg.Role),
			Content: msg.Content,
		})
	}

	reqBody := anthropicRequest{
		Model:     c.model,
		MaxTokens: defaultMaxTokens,
		System:    systemPrompt,
		Messages:  apiMessages,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := c.baseURL + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", anthropicAPIVersion)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr anthropicError
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Error.Message != "" {
			return nil, fmt.Errorf("Anthropic API error (HTTP %d): %s: %s", resp.StatusCode, apiErr.Error.Type, apiErr.Error.Message)
		}
		return nil, fmt.Errorf("Anthropic API error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var apiResp anthropicResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Extract text content from response
	var content string
	for _, block := range apiResp.Content {
		if block.Type == "text" {
			content += block.Text
		}
	}

	return &llm.Response{
		Content: content,
		Usage: llm.TokenUsage{
			InputTokens:  apiResp.Usage.InputTokens,
			OutputTokens: apiResp.Usage.OutputTokens,
		},
	}, nil
}

// ChatWithTools sends messages with tool definitions to the Anthropic API.
func (c *anthropicClient) ChatWithTools(ctx context.Context, systemPrompt string, messages []llm.Message, tools []llm.Tool) (*llm.Response, error) {
	apiMessages := convertMessagesToToolFormat(messages)
	apiTools := convertToolDefinitions(tools)

	reqBody := anthropicToolRequest{
		Model:     c.model,
		MaxTokens: defaultMaxTokens,
		System:    systemPrompt,
		Messages:  apiMessages,
		Tools:     apiTools,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := c.baseURL + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", anthropicAPIVersion)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr anthropicError
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Error.Message != "" {
			return nil, fmt.Errorf("Anthropic API error (HTTP %d): %s: %s", resp.StatusCode, apiErr.Error.Type, apiErr.Error.Message)
		}
		return nil, fmt.Errorf("Anthropic API error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var apiResp anthropicToolResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return parseToolResponse(apiResp), nil
}

// convertMessagesToToolFormat converts llm.Message to the Anthropic tool message format.
func convertMessagesToToolFormat(messages []llm.Message) []anthropicToolMessage {
	var result []anthropicToolMessage
	// Buffer for batching consecutive tool result messages into a single user message.
	var toolResultBatch []anthropicContentBlock

	flushToolResults := func() {
		if len(toolResultBatch) > 0 {
			result = append(result, anthropicToolMessage{
				Role:    "user",
				Content: toolResultBatch,
			})
			toolResultBatch = nil
		}
	}

	for _, msg := range messages {
		switch msg.Role {
		case llm.RoleTool:
			// Tool results are batched into a single user message.
			toolResultBatch = append(toolResultBatch, anthropicContentBlock{
				Type:      "tool_result",
				ToolUseID: msg.ToolCallID,
				Content:   msg.Content,
			})
		case llm.RoleAssistant:
			flushToolResults()
			if len(msg.ToolCalls) > 0 {
				// Assistant message with tool calls becomes content blocks.
				var blocks []anthropicContentBlock
				if msg.Content != "" {
					blocks = append(blocks, anthropicContentBlock{
						Type: "text",
						Text: msg.Content,
					})
				}
				for _, tc := range msg.ToolCalls {
					blocks = append(blocks, anthropicContentBlock{
						Type:  "tool_use",
						ID:    tc.ID,
						Name:  tc.Name,
						Input: tc.Arguments,
					})
				}
				result = append(result, anthropicToolMessage{
					Role:    "assistant",
					Content: blocks,
				})
			} else {
				result = append(result, anthropicToolMessage{
					Role:    "assistant",
					Content: msg.Content,
				})
			}
		default:
			flushToolResults()
			result = append(result, anthropicToolMessage{
				Role:    string(msg.Role),
				Content: msg.Content,
			})
		}
	}
	flushToolResults()
	return result
}

// convertToolDefinitions converts llm.Tool to Anthropic tool definitions.
func convertToolDefinitions(tools []llm.Tool) []anthropicToolDefinition {
	defs := make([]anthropicToolDefinition, len(tools))
	for i, t := range tools {
		defs[i] = anthropicToolDefinition{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Parameters,
		}
	}
	return defs
}

// parseToolResponse converts the Anthropic tool response into llm.Response.
func parseToolResponse(apiResp anthropicToolResponse) *llm.Response {
	resp := &llm.Response{
		FinishReason: apiResp.StopReason,
		Usage: llm.TokenUsage{
			InputTokens:  apiResp.Usage.InputTokens,
			OutputTokens: apiResp.Usage.OutputTokens,
		},
	}

	for _, block := range apiResp.Content {
		switch block.Type {
		case "text":
			resp.Content += block.Text
		case "tool_use":
			resp.ToolCalls = append(resp.ToolCalls, llm.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: block.Input,
			})
		}
	}

	return resp
}

// Model returns the model name being used.
func (c *anthropicClient) Model() string {
	return c.model
}

// Provider returns the provider name.
func (c *anthropicClient) Provider() string {
	return "anthropic"
}

// Close releases resources held by the client.
func (c *anthropicClient) Close() error {
	return nil
}
