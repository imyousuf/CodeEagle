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
)

const (
	defaultOllamaBaseURL   = "http://localhost:11434"
	defaultOllamaDocsModel = "qwen3.5:9b"
	defaultContextWindow   = 49152
	maxExtractRetries      = 4
)

type ollamaProvider struct {
	baseURL         string
	model           string
	contextWindow   int
	disableThinking bool
	client          *http.Client
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
		model = defaultOllamaDocsModel
	}
	contextWindow := cfg.ContextWindow
	if contextWindow == 0 {
		contextWindow = defaultContextWindow
	}
	return &ollamaProvider{
		baseURL:         baseURL,
		model:           model,
		contextWindow:   contextWindow,
		disableThinking: cfg.DisableThinking,
		client:          &http.Client{},
	}, nil
}

// ollamaChatMessage represents a message in the Ollama chat API.
type ollamaChatMessage struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"` // base64-encoded images
}

// ollamaChatRequest is the request body for Ollama /api/chat.
type ollamaChatRequest struct {
	Model    string              `json:"model"`
	Stream   bool                `json:"stream"`
	Format   string              `json:"format"`
	Options  map[string]int      `json:"options,omitempty"`
	Messages []ollamaChatMessage `json:"messages"`
}

// ollamaChatResponse is the response body from Ollama /api/chat.
type ollamaChatResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
}

func (o *ollamaProvider) ExtractTopics(ctx context.Context, text string) (*ExtractionResult, error) {
	systemPrompt := TopicExtractionPrompt
	if o.disableThinking {
		systemPrompt += NoThinkSuffix
	}

	// Truncate if text exceeds ~80% of context window (estimate: 1 token ≈ 3.5 chars).
	maxChars := int(float64(o.contextWindow) * 0.8 * 3.5)
	if len(text) > maxChars {
		text = text[:maxChars]
	}

	messages := []ollamaChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: text},
	}

	return o.extractWithRetry(ctx, messages)
}

func (o *ollamaProvider) DescribeImage(ctx context.Context, imageData []byte, mimeType string) (*ExtractionResult, error) {
	systemPrompt := ImageDescriptionPrompt
	if o.disableThinking {
		systemPrompt += NoThinkSuffix
	}

	encoded := base64.StdEncoding.EncodeToString(imageData)
	messages := []ollamaChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: "Describe this image.", Images: []string{encoded}},
	}

	return o.extractWithRetry(ctx, messages)
}

func (o *ollamaProvider) extractWithRetry(ctx context.Context, messages []ollamaChatMessage) (*ExtractionResult, error) {
	for range maxExtractRetries {
		result, err := o.callOllama(ctx, messages)
		if err != nil {
			return nil, err
		}
		// Validate: must have >2 topics and non-placeholder summary.
		if len(result.Topics) > 2 && !strings.Contains(result.Summary, "...") && len(result.Summary) > 20 {
			return result, nil
		}
		// Garbage output — retry.
	}
	return nil, ErrExtractionSkipped
}

func (o *ollamaProvider) callOllama(ctx context.Context, messages []ollamaChatMessage) (*ExtractionResult, error) {
	reqBody := ollamaChatRequest{
		Model:    o.model,
		Stream:   false,
		Format:   "json",
		Options:  map[string]int{"num_ctx": o.contextWindow},
		Messages: messages,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal chat request: %w", err)
	}

	url := o.baseURL + "/api/chat"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create chat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chat request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama chat: status %d: %s", resp.StatusCode, body)
	}

	var chatResp ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decode chat response: %w", err)
	}

	var result ExtractionResult
	if err := json.Unmarshal([]byte(chatResp.Message.Content), &result); err != nil {
		// JSON parse failure counts as garbage output.
		return &ExtractionResult{}, nil
	}

	return &result, nil
}

func (o *ollamaProvider) Name() string      { return "ollama" }
func (o *ollamaProvider) ModelName() string { return o.model }
