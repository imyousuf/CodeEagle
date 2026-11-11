// Package ollama embeds text with a model served by Ollama's /api/embed.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/imyousuf/CodeEagle/pkg/kit/embedding"
)

const (
	// DefaultBaseURL is where Ollama listens unless configured otherwise.
	DefaultBaseURL = "http://localhost:11434"
	// DefaultModel is the embedding model used when none is configured.
	DefaultModel = "nomic-embed-text-v2-moe"
	// DefaultDimensions is DefaultModel's vector size.
	DefaultDimensions = 768
	// DocumentPrefix and QueryPrefix are the task prefixes DefaultModel was
	// trained with, added to every text unless WithPrefixes replaces them.
	DocumentPrefix = "search_document: "
	QueryPrefix    = "search_query: "
)

// Name is the provider's name, as Provider.Name reports it.
const Name = "ollama"

type provider struct {
	baseURL  string
	model    string
	dims     int
	client   *http.Client
	docPfx   string
	queryPfx string
}

// Option configures the provider.
type Option func(*provider)

// WithPrefixes replaces DocumentPrefix and QueryPrefix, which suit
// nomic-embed-text and no other model. Pass empty strings to send texts as
// given, for instance when embedding.WithInstructions adds a model's own.
func WithPrefixes(document, query string) Option {
	return func(p *provider) { p.docPfx, p.queryPfx = document, query }
}

// WithHTTPClient sends requests through c, for a caller that wants its own
// timeouts or a transport that records calls.
func WithHTTPClient(c *http.Client) Option {
	return func(p *provider) {
		if c != nil {
			p.client = c
		}
	}
}

// New builds an Ollama provider from cfg's OllamaBaseURL, Model and
// Dimensions, each defaulting when empty.
func New(cfg embedding.Config, opts ...Option) (embedding.Provider, error) {
	p := &provider{
		baseURL:  cfg.OllamaBaseURL,
		model:    cfg.Model,
		dims:     cfg.Dimensions,
		client:   &http.Client{},
		docPfx:   DocumentPrefix,
		queryPfx: QueryPrefix,
	}
	if p.baseURL == "" {
		p.baseURL = DefaultBaseURL
	}
	if p.model == "" {
		p.model = DefaultModel
	}
	if p.dims == 0 {
		p.dims = DefaultDimensions
	}
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

// embedRequest is the request body for the Ollama /api/embed endpoint.
type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// embedResponse is the response body from the Ollama /api/embed endpoint.
type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

func (o *provider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	prefixed := make([]string, len(texts))
	for i, t := range texts {
		prefixed[i] = o.docPfx + t
	}
	return o.embed(ctx, prefixed)
}

func (o *provider) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	results, err := o.embed(ctx, []string{o.queryPfx + text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("ollama embed returned no results")
	}
	return results[0], nil
}

func (o *provider) embed(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody := embedRequest{
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

	var embedResp embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}

	return embedResp.Embeddings, nil
}

func (o *provider) Dimensions() int   { return o.dims }
func (o *provider) Name() string      { return Name }
func (o *provider) ModelName() string { return o.model }
