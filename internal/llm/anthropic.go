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
