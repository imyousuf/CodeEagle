package llm

import (
	"context"
	"fmt"
	"os"

	"google.golang.org/genai"

	"github.com/imyousuf/CodeEagle/pkg/llm"
)

const (
	defaultVertexAILocation = "us-central1"
	defaultVertexAIModel    = "gemini-2.0-flash"
)

func init() {
	llm.RegisterProvider("vertex-ai", newVertexAIClient)
}

// vertexAIClient implements llm.Client using Google's GenAI SDK with the Vertex AI backend.
// It supports both Gemini models and Claude models hosted on Vertex AI.
type vertexAIClient struct {
	client *genai.Client
	model  string
}

// newVertexAIClient creates a new Vertex AI client.
func newVertexAIClient(cfg llm.Config) (llm.Client, error) {
	if cfg.Project == "" {
		return nil, fmt.Errorf("project is required for Vertex AI provider")
	}

	model := cfg.Model
	if model == "" {
		model = defaultVertexAIModel
	}

	location := cfg.Location
	if location == "" {
		location = defaultVertexAILocation
	}

	// Set credentials file if provided via config.
	if cfg.CredentialsFile != "" {
		if err := os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", cfg.CredentialsFile); err != nil {
			return nil, fmt.Errorf("failed to set credentials: %w", err)
		}
	}

	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		Project:  cfg.Project,
		Location: location,
		Backend:  genai.BackendVertexAI,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Vertex AI client: %w", err)
	}

	return &vertexAIClient{
		client: client,
		model:  model,
	}, nil
}

// Chat sends a system prompt and messages to the Vertex AI API and returns a response.
func (c *vertexAIClient) Chat(ctx context.Context, systemPrompt string, messages []llm.Message) (*llm.Response, error) {
	contents := convertMessages(messages)

	config := &genai.GenerateContentConfig{}
	if systemPrompt != "" {
		config.SystemInstruction = &genai.Content{
			Parts: []*genai.Part{
				genai.NewPartFromText(systemPrompt),
			},
		}
	}

	resp, err := c.client.Models.GenerateContent(ctx, c.model, contents, config)
	if err != nil {
		return nil, fmt.Errorf("generate content failed: %w", err)
	}

	return convertResponse(resp), nil
}

// ChatWithTools sends messages with tool definitions to the Vertex AI API.
func (c *vertexAIClient) ChatWithTools(ctx context.Context, systemPrompt string, messages []llm.Message, tools []llm.Tool) (*llm.Response, error) {
	contents := convertMessagesWithTools(messages)
	genaiTools := convertToGenAITools(tools)

	config := &genai.GenerateContentConfig{
		Tools:      genaiTools,
		ToolConfig: &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{Mode: genai.FunctionCallingConfigModeAuto}},
	}
	if systemPrompt != "" {
		config.SystemInstruction = &genai.Content{
			Parts: []*genai.Part{genai.NewPartFromText(systemPrompt)},
		}
	}

	resp, err := c.client.Models.GenerateContent(ctx, c.model, contents, config)
	if err != nil {
		return nil, fmt.Errorf("generate content with tools failed: %w", err)
	}

	return convertResponseWithTools(resp), nil
}

// convertToGenAITools converts llm.Tool to genai.Tool using ParametersJsonSchema.
func convertToGenAITools(tools []llm.Tool) []*genai.Tool {
	if len(tools) == 0 {
		return nil
	}
	decls := make([]*genai.FunctionDeclaration, len(tools))
	for i, t := range tools {
		decls[i] = &genai.FunctionDeclaration{
			Name:                 t.Name,
			Description:          t.Description,
			ParametersJsonSchema: t.Parameters,
		}
	}
	return []*genai.Tool{{FunctionDeclarations: decls}}
}

// convertMessagesWithTools converts llm.Message (including tool calls/results) to genai.Content.
func convertMessagesWithTools(messages []llm.Message) []*genai.Content {
	var contents []*genai.Content
	// Buffer for batching consecutive tool result parts.
	var toolResultParts []*genai.Part

	flushToolResults := func() {
		if len(toolResultParts) > 0 {
			contents = append(contents, &genai.Content{
				Role:  "user",
				Parts: toolResultParts,
			})
			toolResultParts = nil
		}
	}

	for _, msg := range messages {
		switch msg.Role {
		case llm.RoleTool:
			// Batch tool results into a single user message.
			toolResultParts = append(toolResultParts, &genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					Name:     msg.ToolCallID, // ToolCallID maps to function name for Gemini
					ID:       msg.ToolCallID,
					Response: map[string]any{"result": msg.Content},
				},
			})
		case llm.RoleAssistant:
			flushToolResults()
			if len(msg.ToolCalls) > 0 {
				var parts []*genai.Part
				if msg.Content != "" {
					parts = append(parts, genai.NewPartFromText(msg.Content))
				}
				for _, tc := range msg.ToolCalls {
					parts = append(parts, &genai.Part{
						FunctionCall: &genai.FunctionCall{
							ID:   tc.ID,
							Name: tc.Name,
							Args: tc.Arguments,
						},
					})
				}
				contents = append(contents, &genai.Content{
					Role:  "model",
					Parts: parts,
				})
			} else {
				contents = append(contents, &genai.Content{
					Role:  "model",
					Parts: []*genai.Part{genai.NewPartFromText(msg.Content)},
				})
			}
		default:
			flushToolResults()
			contents = append(contents, &genai.Content{
				Role:  "user",
				Parts: []*genai.Part{genai.NewPartFromText(msg.Content)},
			})
		}
	}
	flushToolResults()
	return contents
}

// convertResponseWithTools converts a genai response (possibly with function calls) to llm.Response.
func convertResponseWithTools(resp *genai.GenerateContentResponse) *llm.Response {
	response := &llm.Response{}
	if resp == nil || len(resp.Candidates) == 0 {
		return response
	}

	candidate := resp.Candidates[0]
	if candidate.Content != nil {
		for _, part := range candidate.Content.Parts {
			if part.Text != "" {
				response.Content += part.Text
			}
			if part.FunctionCall != nil {
				response.ToolCalls = append(response.ToolCalls, llm.ToolCall{
					ID:        part.FunctionCall.ID,
					Name:      part.FunctionCall.Name,
					Arguments: part.FunctionCall.Args,
				})
			}
		}
	}

	if candidate.FinishReason != "" {
		response.FinishReason = string(candidate.FinishReason)
	}

	if resp.UsageMetadata != nil {
		response.Usage = llm.TokenUsage{
			InputTokens:  int(resp.UsageMetadata.PromptTokenCount),
			OutputTokens: int(resp.UsageMetadata.CandidatesTokenCount),
		}
	}

	return response
}

// Model returns the model name being used.
func (c *vertexAIClient) Model() string {
	return c.model
}

// Provider returns the provider name.
func (c *vertexAIClient) Provider() string {
	return "vertex-ai"
}

// Close releases resources held by the client.
func (c *vertexAIClient) Close() error {
	return nil
}

// convertMessages converts llm.Message to genai.Content.
func convertMessages(messages []llm.Message) []*genai.Content {
	contents := make([]*genai.Content, 0, len(messages))
	for _, msg := range messages {
		var role string
		switch msg.Role {
		case llm.RoleUser:
			role = "user"
		case llm.RoleAssistant:
			role = "model"
		default:
			role = "user"
		}

		contents = append(contents, &genai.Content{
			Role: role,
			Parts: []*genai.Part{
				genai.NewPartFromText(msg.Content),
			},
		})
	}
	return contents
}

// convertResponse converts a genai response to llm.Response.
func convertResponse(resp *genai.GenerateContentResponse) *llm.Response {
	response := &llm.Response{}

	if resp == nil || len(resp.Candidates) == 0 {
		return response
	}

	candidate := resp.Candidates[0]
	if candidate.Content != nil {
		for _, part := range candidate.Content.Parts {
			if part.Text != "" {
				response.Content += part.Text
			}
		}
	}

	if resp.UsageMetadata != nil {
		response.Usage = llm.TokenUsage{
			InputTokens:  int(resp.UsageMetadata.PromptTokenCount),
			OutputTokens: int(resp.UsageMetadata.CandidatesTokenCount),
		}
	}

	return response
}
