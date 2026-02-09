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
