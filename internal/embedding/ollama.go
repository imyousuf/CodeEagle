package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const (
	defaultOllamaBaseURL = "http://localhost:11434"
	defaultOllamaModel   = "nomic-embed-text-v2-moe"
)

type ollamaProvider struct {
	baseURL string
	model   string
	dims    int
	client  *http.Client
}

func init() {
	RegisterProvider("ollama", newOllamaProvider)
}

func newOllamaProvider(cfg Config) (Provider, error) {
	baseURL := cfg.OllamaBaseURL
	if baseURL == "" {
		baseURL = defaultOllamaBaseURL
	}
	model := cfg.Model
	if model == "" {
		model = defaultOllamaModel
	}
	dims := cfg.Dimensions
	if dims == 0 {
		dims = 768
	}
	return &ollamaProvider{
		baseURL: baseURL,
		model:   model,
		dims:    dims,
		client:  &http.Client{},
	}, nil
}

// ollamaEmbedRequest is the request body for the Ollama /api/embed endpoint.
type ollamaEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// ollamaEmbedResponse is the response body from the Ollama /api/embed endpoint.
type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

func (o *ollamaProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	// Add search_document: prefix for indexing (v2-moe convention).
	prefixed := make([]string, len(texts))
	for i, t := range texts {
		prefixed[i] = "search_document: " + t
	}

	return o.embed(ctx, prefixed)
}

func (o *ollamaProvider) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	// Add search_query: prefix for queries (v2-moe convention).
	results, err := o.embed(ctx, []string{"search_query: " + text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("ollama embed returned no results")
	}
	return results[0], nil
}

func (o *ollamaProvider) embed(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody := ollamaEmbedRequest{
		Model: o.model,
		Input: texts,
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	url := o.baseURL + "/api/embed"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama embed: status %d: %s", resp.StatusCode, body)
	}

	var embedResp ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}

	return embedResp.Embeddings, nil
}

func (o *ollamaProvider) Dimensions() int  { return o.dims }
func (o *ollamaProvider) Name() string     { return "ollama" }
func (o *ollamaProvider) ModelName() string { return o.model }
