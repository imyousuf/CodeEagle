// Package baseten extracts topics and describes images with a model served
// on Baseten's OpenAI-compatible chat completions API.
//
// It uses the standard library only. The default model accepts image content
// parts, which most open-weight models on Baseten do not.
package baseten

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/imyousuf/CodeEagle/pkg/kit/topics"
)

const (
	// Name is the provider's name, as Provider.Name reports it.
	Name = "baseten"

	// DefaultBaseURL is Baseten's OpenAI-compatible endpoint.
	DefaultBaseURL = "https://inference.baseten.co/v1"

	// DefaultModel describes images and extracts topics. Verified against
	// the live API to accept image content parts, which most open-weight
	// models on Baseten do not.
	DefaultModel = "zai-org/GLM-5.3-Flash"

	// RequestTimeout bounds one request, covering a large image plus a
	// reasoning pass. It is the default client's timeout, and applies to
	// each request's context whatever client is in use.
	RequestTimeout = 5 * time.Minute

	// maxTokens must be generous. GLM spends its reasoning budget before
	// emitting any content, so a small cap returns an empty reply rather
	// than a short one -- a failure that reads like a model error and is
	// not.
	maxTokens = 8192
)

// ErrNoAPIKey is returned by New without an API key.
var ErrNoAPIKey = errors.New("baseten: no API key")

// Provider is a topics.Provider backed by Baseten.
type Provider struct {
	model     string
	baseURL   string
	apiKey    string
	client    *http.Client
	transport http.RoundTripper
	ctxWindow int
	maxChars  int
}

var _ topics.Provider = (*Provider)(nil)

// Option configures a Provider.
type Option func(*Provider)

// WithModel names the model; DefaultModel when empty.
func WithModel(model string) Option {
	return func(p *Provider) {
		if model != "" {
			p.model = model
		}
	}
}

// WithBaseURL sends requests somewhere other than DefaultBaseURL: a gateway,
// a proxy, or a test server. A trailing slash is dropped.
func WithBaseURL(url string) Option {
	return func(p *Provider) {
		if url != "" {
			p.baseURL = url
		}
	}
}

// WithContextWindow sets the model's context window in tokens, which bounds
// the text sent: 80% of it at 3.5 characters a token, cut by bytes.
// topics.DefaultContextWindow when not positive.
func WithContextWindow(tokens int) Option {
	return func(p *Provider) {
		if tokens > 0 {
			p.ctxWindow = tokens
		}
	}
}

// WithHTTPClient sends requests through c rather than a client with
// RequestTimeout, for a caller that wants its own pooling or timeouts. c is
// never modified.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) {
		if c != nil {
			p.client = c
		}
	}
}

// WithTransport sends requests through rt, for a caller that records or
// rewrites them. It applies to the default client, or to a copy of one given
// with WithHTTPClient, whichever order the two are passed in.
func WithTransport(rt http.RoundTripper) Option {
	return func(p *Provider) {
		if rt != nil {
			p.transport = rt
		}
	}
}

// New builds a provider that authenticates with apiKey.
func New(apiKey string, opts ...Option) (*Provider, error) {
	if apiKey == "" {
		return nil, ErrNoAPIKey
	}
	p := &Provider{
		model:     DefaultModel,
		baseURL:   DefaultBaseURL,
		apiKey:    apiKey,
		ctxWindow: topics.DefaultContextWindow,
	}
	for _, opt := range opts {
		opt(p)
	}
	p.baseURL = strings.TrimSuffix(p.baseURL, "/")
	if p.client == nil {
		p.client = &http.Client{Timeout: RequestTimeout}
	}
	if p.transport != nil {
		c := *p.client
		c.Transport = p.transport
		p.client = &c
	}
	// The same 80% / 3.5-characters-a-token estimate as a local model.
	p.maxChars = int(float64(p.ctxWindow) * 0.8 * 3.5)
	return p, nil
}

// content is one part of a multimodal message. Text parts carry Text; image
// parts carry ImageURL holding a data: URI.
type content struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

// message carries either a plain string (system/user text) or a list of
// content parts (multimodal). The API accepts both shapes on `content`, so it
// is typed as any rather than modelled twice.
type message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type request struct {
	Model          string    `json:"model"`
	Messages       []message `json:"messages"`
	MaxTokens      int       `json:"max_tokens"`
	ResponseFormat *format   `json:"response_format,omitempty"`
}

type format struct {
	Type string `json:"type"`
}

type response struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ExtractTopics summarizes text and names its topics, sending at most the
// context window's budget of it.
func (p *Provider) ExtractTopics(ctx context.Context, text string) (*topics.ExtractionResult, error) {
	if len(text) > p.maxChars {
		text = text[:p.maxChars]
	}

	messages := []message{
		{Role: "system", Content: topics.TopicExtractionPrompt},
		{Role: "user", Content: text},
	}
	return p.extractWithRetry(ctx, messages)
}

// DescribeImage describes an image and names its topics. An empty MIME type
// is taken as PNG.
func (p *Provider) DescribeImage(ctx context.Context, imageData []byte, mimeType string) (*topics.ExtractionResult, error) {
	if mimeType == "" {
		mimeType = "image/png"
	}
	encoded := base64.StdEncoding.EncodeToString(imageData)

	messages := []message{
		{Role: "system", Content: topics.ImageDescriptionPrompt},
		{Role: "user", Content: []content{
			{Type: "text", Text: "Describe this image."},
			{Type: "image_url", ImageURL: &imageURL{
				URL: fmt.Sprintf("data:%s;base64,%s", mimeType, encoded),
			}},
		}},
	}
	return p.extractWithRetry(ctx, messages)
}

// extractWithRetry retries on error, and treats a reply that parses but says
// nothing as garbage worth one more attempt. When every attempt fails the
// last error wins over topics.ErrExtractionSkipped.
func (p *Provider) extractWithRetry(ctx context.Context, messages []message) (*topics.ExtractionResult, error) {
	var lastErr error
	for range topics.MaxExtractRetries {
		if ctx.Err() != nil {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, ctx.Err()
		}

		result, err := p.call(ctx, messages)
		if err != nil {
			lastErr = err
			continue
		}
		if topics.Usable(result) {
			return result, nil
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, topics.ErrExtractionSkipped
}

func (p *Provider) call(ctx context.Context, messages []message) (*topics.ExtractionResult, error) {
	reqBody := request{
		Model:          p.model,
		Messages:       messages,
		MaxTokens:      maxTokens,
		ResponseFormat: &format{Type: "json_object"},
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal chat request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost,
		p.baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create chat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chat request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		// The key is in the request, never in the response, so echoing the
		// body here cannot leak it.
		return nil, fmt.Errorf("baseten chat: status %d: %s", resp.StatusCode, body)
	}

	var chatResp response
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decode chat response: %w", err)
	}
	if chatResp.Error != nil {
		return nil, fmt.Errorf("baseten chat: %s", chatResp.Error.Message)
	}
	if len(chatResp.Choices) == 0 {
		// An empty choice list is what a spent reasoning budget looks like.
		return &topics.ExtractionResult{}, nil
	}

	var result topics.ExtractionResult
	if err := json.Unmarshal([]byte(salvageJSON(chatResp.Choices[0].Message.Content)), &result); err != nil {
		return &topics.ExtractionResult{}, nil
	}
	return &result, nil
}

// salvageJSON pulls the JSON object out of a reply that wrapped it in prose or
// a fenced code block. response_format usually makes this unnecessary, but it
// is not guaranteed across models and costs nothing when the reply is clean.
func salvageJSON(s string) string {
	s = strings.TrimSpace(s)
	if fence := strings.Index(s, "```"); fence >= 0 {
		rest := s[fence+3:]
		rest = strings.TrimPrefix(rest, "json")
		if end := strings.Index(rest, "```"); end >= 0 {
			s = strings.TrimSpace(rest[:end])
		}
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}

// Name returns "baseten".
func (p *Provider) Name() string { return Name }

// ModelName returns the model the provider asks.
func (p *Provider) ModelName() string { return p.model }
