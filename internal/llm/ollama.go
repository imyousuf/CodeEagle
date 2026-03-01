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

const defaultOllamaBaseURL = "http://localhost:11434"

func init() {
	llm.RegisterProvider("ollama", newOllamaClient)
}

// ollamaClient implements llm.Client and llm.ToolCapableClient using the
// Ollama /api/chat endpoint.
type ollamaClient struct {
	baseURL string
	model   string
	client  *http.Client
}

// newOllamaClient creates a new Ollama LLM client.
func newOllamaClient(cfg llm.Config) (llm.Client, error) {
	if cfg.Model == "" {
		return nil, fmt.Errorf("model is required for Ollama provider")
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultOllamaBaseURL
	}

	return &ollamaClient{
		baseURL: baseURL,
		model:   cfg.Model,
		client:  &http.Client{},
	}, nil
}

// --- Wire format types ---

// ollamaChatRequest is the request body for the Ollama /api/chat endpoint.
type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Tools    []ollamaToolDef `json:"tools,omitempty"`
}

// ollamaMessage represents a message in the Ollama chat format.
type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

// ollamaToolCall represents a tool call in an Ollama response.
type ollamaToolCall struct {
	Function ollamaToolFunction `json:"function"`
}

// ollamaToolFunction holds the function name and arguments of a tool call.
type ollamaToolFunction struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// ollamaToolDef describes a tool definition for the Ollama API.
type ollamaToolDef struct {
	Type     string              `json:"type"`
	Function ollamaFunctionDecl `json:"function"`
}

// ollamaFunctionDecl describes a function in a tool definition.
type ollamaFunctionDecl struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// ollamaChatResponse is the response from the Ollama /api/chat endpoint.
type ollamaChatResponse struct {
	Model           string        `json:"model"`
	Message         ollamaMessage `json:"message"`
	Done            bool          `json:"done"`
	DoneReason      string        `json:"done_reason"`
	PromptEvalCount int           `json:"prompt_eval_count"`
	EvalCount       int           `json:"eval_count"`
}

// ollamaErrorResponse is the error response from the Ollama API.
type ollamaErrorResponse struct {
	Error string `json:"error"`
}

// Chat sends a system prompt and messages to the Ollama /api/chat endpoint.
func (c *ollamaClient) Chat(ctx context.Context, systemPrompt string, messages []llm.Message) (*llm.Response, error) {
	apiMessages := convertToOllamaMessages(systemPrompt, messages)

	reqBody := ollamaChatRequest{
		Model:    c.model,
		Messages: apiMessages,
		Stream:   false,
	}

	return c.doChat(ctx, reqBody)
}

// ChatWithTools sends messages with tool definitions to the Ollama /api/chat endpoint.
func (c *ollamaClient) ChatWithTools(ctx context.Context, systemPrompt string, messages []llm.Message, tools []llm.Tool) (*llm.Response, error) {
	apiMessages := convertToOllamaToolMessages(systemPrompt, messages)
	apiTools := convertToOllamaTools(tools)

	reqBody := ollamaChatRequest{
		Model:    c.model,
		Messages: apiMessages,
		Stream:   false,
		Tools:    apiTools,
	}

	return c.doChat(ctx, reqBody)
}

// doChat sends a chat request to the Ollama API and parses the response.
func (c *ollamaClient) doChat(ctx context.Context, reqBody ollamaChatRequest) (*llm.Response, error) {
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal chat request: %w", err)
	}

	url := c.baseURL + "/api/chat"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create chat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chat request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr ollamaErrorResponse
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Error != "" {
			return nil, fmt.Errorf("ollama API error (HTTP %d): %s", resp.StatusCode, apiErr.Error)
		}
		return nil, fmt.Errorf("ollama API error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var chatResp ollamaChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("unmarshal chat response: %w", err)
	}

	return parseOllamaResponse(chatResp), nil
}

// parseOllamaResponse converts an Ollama chat response to llm.Response.
func parseOllamaResponse(chatResp ollamaChatResponse) *llm.Response {
	resp := &llm.Response{
		Content: chatResp.Message.Content,
		Usage: llm.TokenUsage{
			InputTokens:  chatResp.PromptEvalCount,
			OutputTokens: chatResp.EvalCount,
		},
	}

	if chatResp.DoneReason != "" {
		resp.FinishReason = chatResp.DoneReason
	}

	for i, tc := range chatResp.Message.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, llm.ToolCall{
			ID:        fmt.Sprintf("call_%d", i),
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}

	return resp
}

// convertToOllamaMessages converts a system prompt and llm.Messages to
// Ollama message format for simple chat (no tools).
func convertToOllamaMessages(systemPrompt string, messages []llm.Message) []ollamaMessage {
	var result []ollamaMessage

	if systemPrompt != "" {
		result = append(result, ollamaMessage{
			Role:    "system",
			Content: systemPrompt,
		})
	}

	for _, msg := range messages {
		result = append(result, ollamaMessage{
			Role:    string(msg.Role),
			Content: msg.Content,
		})
	}

	return result
}

// convertToOllamaToolMessages converts messages including tool calls and
// tool results to Ollama message format.
func convertToOllamaToolMessages(systemPrompt string, messages []llm.Message) []ollamaMessage {
	var result []ollamaMessage

	if systemPrompt != "" {
		result = append(result, ollamaMessage{
			Role:    "system",
			Content: systemPrompt,
		})
	}

	for _, msg := range messages {
		switch msg.Role {
		case llm.RoleTool:
			result = append(result, ollamaMessage{
				Role:    "tool",
				Content: msg.Content,
			})
		case llm.RoleAssistant:
			om := ollamaMessage{
				Role:    "assistant",
				Content: msg.Content,
			}
			for _, tc := range msg.ToolCalls {
				om.ToolCalls = append(om.ToolCalls, ollamaToolCall{
					Function: ollamaToolFunction{
						Name:      tc.Name,
						Arguments: tc.Arguments,
					},
				})
			}
			result = append(result, om)
		default:
			result = append(result, ollamaMessage{
				Role:    string(msg.Role),
				Content: msg.Content,
			})
		}
	}

	return result
}

// convertToOllamaTools converts llm.Tool to Ollama tool definitions.
func convertToOllamaTools(tools []llm.Tool) []ollamaToolDef {
	if len(tools) == 0 {
		return nil
	}
	defs := make([]ollamaToolDef, len(tools))
	for i, t := range tools {
		defs[i] = ollamaToolDef{
			Type: "function",
			Function: ollamaFunctionDecl{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		}
	}
	return defs
}

// Model returns the model name being used.
func (c *ollamaClient) Model() string {
	return c.model
}

// Provider returns the provider name.
func (c *ollamaClient) Provider() string {
	return "ollama"
}

// Close releases resources held by the client.
func (c *ollamaClient) Close() error {
	return nil
}
