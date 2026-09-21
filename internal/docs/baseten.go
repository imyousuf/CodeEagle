package docs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// defaultBasetenDocsBaseURL is Baseten's OpenAI-compatible endpoint.
	defaultBasetenDocsBaseURL = "https://inference.baseten.co/v1"

	// DefaultBasetenDocsModel describes images and extracts topics. Verified
	// against the live API to accept image content parts, which most
	// open-weight models on Baseten do not.
	DefaultBasetenDocsModel = "zai-org/GLM-5.3-Flash"

	// basetenMaxTokens must be generous. GLM spends its reasoning budget
	// before emitting any content, so a small cap returns an empty reply
	// rather than a short one -- a failure that reads like a model error and
	// is not.
	basetenMaxTokens = 8192

	// basetenRequestTimeout covers a large image plus a reasoning pass.
	basetenRequestTimeout = 5 * time.Minute
)

type basetenProvider struct {
	model    string
	baseURL  string
	apiKey   string
	client   *http.Client
	maxChars int
}

func init() {
	RegisterProvider("baseten", newBasetenProvider)
}

func newBasetenProvider(cfg Config) (Provider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("baseten docs provider needs an API key: set docs.api_key, " +
			"or transcripts.baseten_api_key, which it falls back to")
	}

	model := cfg.Model
	if model == "" {
		model = DefaultBasetenDocsModel
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBasetenDocsBaseURL
	}
	ctxWindow := cfg.ContextWindow
	if ctxWindow <= 0 {
		ctxWindow = DefaultContextWindow
	}

	return &basetenProvider{
		model:   model,
		baseURL: strings.TrimSuffix(baseURL, "/"),
		apiKey:  cfg.APIKey,
		client:  &http.Client{Timeout: basetenRequestTimeout},
		// Same 80% / 3.5-chars-per-token estimate the Ollama path uses.
		maxChars: int(float64(ctxWindow) * 0.8 * 3.5),
	}, nil
}

// basetenContent is one part of a multimodal message. Text parts carry Text;
// image parts carry ImageURL holding a data: URI.
type basetenContent struct {
	Type     string           `json:"type"`
	Text     string           `json:"text,omitempty"`
	ImageURL *basetenImageURL `json:"image_url,omitempty"`
}

type basetenImageURL struct {
	URL string `json:"url"`
}

// basetenMessage carries either a plain string (system/user text) or a list of
// content parts (multimodal). The API accepts both shapes on `content`, so it
// is typed as any rather than modelled twice.
type basetenMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type basetenRequest struct {
	Model          string           `json:"model"`
	Messages       []basetenMessage `json:"messages"`
	MaxTokens      int              `json:"max_tokens"`
	ResponseFormat *basetenFormat   `json:"response_format,omitempty"`
}

type basetenFormat struct {
	Type string `json:"type"`
}

type basetenResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (b *basetenProvider) ExtractTopics(ctx context.Context, text string) (*ExtractionResult, error) {
	if len(text) > b.maxChars {
		text = text[:b.maxChars]
	}

	messages := []basetenMessage{
		{Role: "system", Content: TopicExtractionPrompt},
		{Role: "user", Content: text},
	}
	return b.extractWithRetry(ctx, messages)
}

func (b *basetenProvider) DescribeImage(ctx context.Context, imageData []byte, mimeType string) (*ExtractionResult, error) {
	if mimeType == "" {
		mimeType = "image/png"
	}
	encoded := base64.StdEncoding.EncodeToString(imageData)

	messages := []basetenMessage{
		{Role: "system", Content: ImageDescriptionPrompt},
		{Role: "user", Content: []basetenContent{
			{Type: "text", Text: "Describe this image."},
			{Type: "image_url", ImageURL: &basetenImageURL{
				URL: fmt.Sprintf("data:%s;base64,%s", mimeType, encoded),
			}},
		}},
	}
	return b.extractWithRetry(ctx, messages)
}

// extractWithRetry mirrors the Ollama path: retry on error, and treat a reply
// that parses but says nothing as garbage worth one more attempt.
func (b *basetenProvider) extractWithRetry(ctx context.Context, messages []basetenMessage) (*ExtractionResult, error) {
	var lastErr error
	for range maxExtractRetries {
		if ctx.Err() != nil {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, ctx.Err()
		}

		result, err := b.call(ctx, messages)
		if err != nil {
			lastErr = err
			continue
		}
		if len(result.Topics) > 2 && !strings.Contains(result.Summary, "...") && len(result.Summary) > 20 {
			return result, nil
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, ErrExtractionSkipped
}

func (b *basetenProvider) call(ctx context.Context, messages []basetenMessage) (*ExtractionResult, error) {
	reqBody := basetenRequest{
		Model:          b.model,
		Messages:       messages,
		MaxTokens:      basetenMaxTokens,
		ResponseFormat: &basetenFormat{Type: "json_object"},
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal chat request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, basetenRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost,
		b.baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create chat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.apiKey)

	resp, err := b.client.Do(req)
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

	var chatResp basetenResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decode chat response: %w", err)
	}
	if chatResp.Error != nil {
		return nil, fmt.Errorf("baseten chat: %s", chatResp.Error.Message)
	}
	if len(chatResp.Choices) == 0 {
		// An empty choice list is what a spent reasoning budget looks like.
		return &ExtractionResult{}, nil
	}

	var result ExtractionResult
	if err := json.Unmarshal([]byte(salvageJSON(chatResp.Choices[0].Message.Content)), &result); err != nil {
		return &ExtractionResult{}, nil
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

func (b *basetenProvider) Name() string      { return "baseten" }
func (b *basetenProvider) ModelName() string { return b.model }
