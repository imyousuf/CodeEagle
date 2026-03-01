package embedding

import (
	"context"
	"fmt"
	"os"

	"google.golang.org/genai"
)

const (
	defaultVertexModel = "gemini-embedding-001"
	defaultVertexDims  = 768
)

type vertexProvider struct {
	client *genai.Client
	model  string
	dims   int
}

func init() {
	RegisterProvider("vertex-ai", newVertexProvider)
}

func newVertexProvider(cfg Config) (Provider, error) {
	if cfg.Project == "" {
		return nil, fmt.Errorf("vertex-ai embedding requires project")
	}
	if cfg.Location == "" {
		return nil, fmt.Errorf("vertex-ai embedding requires location")
	}

	ctx := context.Background()
	// Set credentials via environment variable, same pattern as internal/llm/vertexai.go.
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
		model = defaultVertexModel
	}
	dims := cfg.Dimensions
	if dims == 0 {
		dims = defaultVertexDims
	}

	return &vertexProvider{
		client: client,
		model:  model,
		dims:   dims,
	}, nil
}

func (v *vertexProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	// Batch embed with the Vertex AI API.
	contents := make([]*genai.Content, len(texts))
	for i, t := range texts {
		contents[i] = &genai.Content{
			Parts: []*genai.Part{{Text: t}},
		}
	}

	dims := int32(v.dims)
	result, err := v.client.Models.EmbedContent(ctx, v.model, contents, &genai.EmbedContentConfig{
		OutputDimensionality: &dims,
	})
	if err != nil {
		return nil, fmt.Errorf("vertex-ai embed: %w", err)
	}

	embeddings := make([][]float32, len(result.Embeddings))
	for i, emb := range result.Embeddings {
		embeddings[i] = emb.Values
	}
	return embeddings, nil
}

func (v *vertexProvider) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	results, err := v.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("vertex-ai embed returned no results")
	}
	return results[0], nil
}

func (v *vertexProvider) Dimensions() int  { return v.dims }
func (v *vertexProvider) Name() string     { return "vertex-ai" }
func (v *vertexProvider) ModelName() string { return v.model }
