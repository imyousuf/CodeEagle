package docs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"google.golang.org/genai"
)

const defaultVertexDocsModel = "gemini-2.0-flash"

type vertexProvider struct {
	client *genai.Client
	model  string
}

func init() {
	RegisterProvider("vertex-ai", newVertexProvider)
}

func newVertexProvider(cfg Config) (Provider, error) {
	if cfg.Project == "" {
		return nil, fmt.Errorf("vertex-ai docs requires project")
	}
	if cfg.Location == "" {
		return nil, fmt.Errorf("vertex-ai docs requires location")
	}

	ctx := context.Background()
	if cfg.CredentialsFile != "" {
		if err := os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", cfg.CredentialsFile); err != nil {
			return nil, fmt.Errorf("set credentials env: %w", err)
		}
	}

	clientCfg := &genai.ClientConfig{
		Project:  cfg.Project,
		Location: cfg.Location,
		Backend:  genai.BackendVertexAI,
	}

	client, err := genai.NewClient(ctx, clientCfg)
	if err != nil {
		return nil, fmt.Errorf("create vertex-ai genai client: %w", err)
	}

	model := cfg.Model
	if model == "" {
		model = defaultVertexDocsModel
	}

	return &vertexProvider{
		client: client,
		model:  model,
	}, nil
}

func (v *vertexProvider) ExtractTopics(ctx context.Context, text string) (*ExtractionResult, error) {
	config := &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{genai.NewPartFromText(TopicExtractionPrompt)},
		},
	}

	resp, err := v.client.Models.GenerateContent(ctx, v.model, []*genai.Content{
		{
			Role:  "user",
			Parts: []*genai.Part{genai.NewPartFromText(text)},
		},
	}, config)
	if err != nil {
		return nil, fmt.Errorf("vertex-ai generate content: %w", err)
	}

	return parseVertexResponse(resp)
}

func (v *vertexProvider) DescribeImage(ctx context.Context, imageData []byte, mimeType string) (*ExtractionResult, error) {
	config := &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{genai.NewPartFromText(ImageDescriptionPrompt)},
		},
	}

	resp, err := v.client.Models.GenerateContent(ctx, v.model, []*genai.Content{
		{
			Role: "user",
			Parts: []*genai.Part{
				genai.NewPartFromText("Describe this image."),
				{InlineData: &genai.Blob{MIMEType: mimeType, Data: imageData}},
			},
		},
	}, config)
	if err != nil {
		return nil, fmt.Errorf("vertex-ai describe image: %w", err)
	}

	return parseVertexResponse(resp)
}

func parseVertexResponse(resp *genai.GenerateContentResponse) (*ExtractionResult, error) {
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return &ExtractionResult{}, nil
	}

	var text string
	for _, part := range resp.Candidates[0].Content.Parts {
		if part.Text != "" {
			text += part.Text
		}
	}

	var result ExtractionResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return &ExtractionResult{Summary: text}, nil
	}

	return &result, nil
}

func (v *vertexProvider) Name() string      { return "vertex-ai" }
func (v *vertexProvider) ModelName() string { return v.model }
