package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/imyousuf/CodeEagle/pkg/llm"
)

const (
	defaultBasetenBaseURL = "https://inference.baseten.co/v1"

	// DefaultBasetenModel is a fast, inexpensive model with a very large
	// context window — enough to hold an entire meeting transcript in one
	// request, which keeps extraction coherent across a whole conversation.
	DefaultBasetenModel = "deepseek-ai/DeepSeek-V4.1-Flash"

	// defaultBasetenMaxTokens is generous because reasoning models spend this
	// budget on their own scratch work before emitting a single byte of the
	// answer. Too small a cap does not truncate the reply — it produces an
	// empty one, with the whole allowance consumed by reasoning.
	defaultBasetenMaxTokens = 32768

	// basetenMaxRetries bounds retries on transient failures. Batch jobs run
	// hundreds of requests, so rate limiting is expected rather than
	// exceptional and must not abort the run.
	basetenMaxRetries = 5

	// maxRetryAfter caps how long a server-supplied Retry-After can park a
	// worker. The value is not ours, so it is treated as a request rather
	// than an instruction.
	maxRetryAfter = 60 * time.Second
)

// defaultBasetenTemperature keeps extraction close to the transcript.
// Summarization and identity attribution are grounded tasks: creative sampling
// produces invented names and events.
var defaultBasetenTemperature = 0.2

func init() {
	llm.RegisterProvider("baseten", newBasetenClient)
}

// basetenClient talks to Baseten's OpenAI-compatible inference API. It serves
// open-weight models (GLM, DeepSeek, Kimi) at a fraction of the cost of
// frontier APIs, which matters when the workload is hundreds of hours of
// transcript rather than a handful of requests.
type basetenClient struct {
	apiKey          string
	baseURL         string
	model           string
	maxTokens       int
	temperature     float64
	reasoningEffort string
	client          *http.Client
}

func newBasetenClient(cfg llm.Config) (llm.Client, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("API key is required for Baseten provider")
	}

	model := cfg.Model
	if model == "" {
		model = DefaultBasetenModel
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBasetenBaseURL
	}
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultBasetenMaxTokens
	}
	temperature := defaultBasetenTemperature
	if cfg.Temperature != nil {
		temperature = *cfg.Temperature
	}

	return &basetenClient{
		apiKey:          cfg.APIKey,
		baseURL:         baseURL,
		model:           model,
		maxTokens:       maxTokens,
		temperature:     temperature,
		reasoningEffort: cfg.ReasoningEffort,
		// Long transcripts with large outputs can take minutes.
		client: &http.Client{Timeout: 10 * time.Minute},
	}, nil
}

// --- Wire format (OpenAI chat completions) ---

type basetenRequest struct {
	Model          string           `json:"model"`
	Messages       []basetenMessage `json:"messages"`
	MaxTokens      int              `json:"max_tokens,omitempty"`
	Temperature    float64          `json:"temperature"`
	Stream         bool             `json:"stream"`
	Tools          []basetenToolDef `json:"tools,omitempty"`
	ResponseFormat *responseFormat  `json:"response_format,omitempty"`
	// ReasoningEffort budgets internal deliberation on models that reason.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// ChatTemplateKwargs reaches the model's chat template directly. It is how
	// reasoning is switched off entirely, which is the fallback when a model
	// deliberates until its whole token budget is gone.
	ChatTemplateKwargs map[string]any `json:"chat_template_kwargs,omitempty"`
}

type basetenMessage struct {
	Role       string            `json:"role"`
	Content    string            `json:"content"`
	ToolCalls  []basetenToolCall `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
}

type basetenToolCall struct {
	ID       string              `json:"id"`
	Type     string              `json:"type"`
	Function basetenToolFunction `json:"function"`
}

// basetenToolFunction carries arguments as a JSON string, as the OpenAI wire
// format specifies.
type basetenToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type basetenToolDef struct {
	Type     string              `json:"type"`
	Function basetenFunctionDecl `json:"function"`
}

type basetenFunctionDecl struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type responseFormat struct {
	Type       string          `json:"type"`
	JSONSchema *schemaEnvelope `json:"json_schema,omitempty"`
}

type schemaEnvelope struct {
	Name   string         `json:"name"`
	Strict bool           `json:"strict,omitempty"`
	Schema map[string]any `json:"schema"`
}

type basetenResponse struct {
	Choices []struct {
		Message struct {
			Content   string            `json:"content"`
			ToolCalls []basetenToolCall `json:"tool_calls"`
			// ReasoningContent holds a reasoning model's scratch work. It is
			// deliberately ignored: only the answer belongs downstream.
			ReasoningContent string `json:"reasoning_content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Chat sends a prompt and returns the model's reply.
func (c *basetenClient) Chat(ctx context.Context, systemPrompt string, messages []llm.Message) (*llm.Response, error) {
	return c.do(ctx, basetenRequest{
		Model:           c.model,
		Messages:        toBasetenMessages(systemPrompt, messages),
		MaxTokens:       c.maxTokens,
		Temperature:     c.temperature,
		ReasoningEffort: c.reasoningEffort,
	}, false)
}

// ChatWithTools sends a prompt with tool definitions available.
func (c *basetenClient) ChatWithTools(ctx context.Context, systemPrompt string, messages []llm.Message, tools []llm.Tool) (*llm.Response, error) {
	return c.do(ctx, basetenRequest{
		Model:           c.model,
		Messages:        toBasetenMessages(systemPrompt, messages),
		MaxTokens:       c.maxTokens,
		Temperature:     c.temperature,
		ReasoningEffort: c.reasoningEffort,
		Tools:           toBasetenTools(tools),
	}, false)
}

// ChatJSON constrains the reply to a JSON Schema, so callers can unmarshal the
// result instead of parsing prose.
func (c *basetenClient) ChatJSON(ctx context.Context, systemPrompt string, messages []llm.Message, schema *llm.JSONSchema) (*llm.Response, error) {
	req := basetenRequest{
		Model:           c.model,
		Messages:        toBasetenMessages(systemPrompt, messages),
		MaxTokens:       c.maxTokens,
		Temperature:     c.temperature,
		ReasoningEffort: c.reasoningEffort,
	}
	if schema != nil {
		req.ResponseFormat = &responseFormat{
			Type: "json_schema",
			JSONSchema: &schemaEnvelope{
				Name:   schema.Name,
				Strict: schema.Strict,
				Schema: schema.Schema,
			},
		}
	} else {
		req.ResponseFormat = &responseFormat{Type: "json_object"}
	}
	// A JSON reply must be complete to be worth anything, so truncation is
	// reported rather than handed back as half a document.
	return c.do(ctx, req, true)
}

// do sends a request, retrying transient failures with exponential backoff.
func (c *basetenClient) do(ctx context.Context, reqBody basetenRequest, requireComplete bool) (*llm.Response, error) {
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal chat request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= basetenMaxRetries; attempt++ {
		if attempt > 0 {
			if err := sleepCtx(ctx, backoff(attempt)); err != nil {
				return nil, err
			}
		}

		resp, retryAfter, err := c.attempt(ctx, data, requireComplete)
		if err == nil {
			return resp, nil
		}
		lastErr = err

		// A model that deliberated past its budget will do so again on an
		// identical request. Retry once with reasoning switched off: a
		// slightly weaker answer beats losing the item entirely, which matters
		// when a batch spans hundreds of recordings.
		var ee *emptyResponseError
		if errors.As(err, &ee) {
			if reasoningDisabled(reqBody) {
				return nil, err
			}
			reqBody.ReasoningEffort = ""
			reqBody.ChatTemplateKwargs = map[string]any{"thinking": false}
			data, err = json.Marshal(reqBody)
			if err != nil {
				return nil, fmt.Errorf("marshal fallback request: %w", err)
			}
			continue
		}

		var re *retryableError
		if !errors.As(err, &re) {
			return nil, err
		}
		if retryAfter > 0 {
			if err := sleepCtx(ctx, retryAfter); err != nil {
				return nil, err
			}
		}
	}
	return nil, fmt.Errorf("baseten: giving up after %d retries: %w", basetenMaxRetries, lastErr)
}

// emptyResponseError reports that the model produced no answer, having spent
// its entire token budget on internal reasoning.
type emptyResponseError struct {
	finishReason    string
	reasoningChars  int
	completionToken int
	// truncated distinguishes an answer cut off mid-way from one never begun.
	truncated bool
}

func (e *emptyResponseError) Error() string {
	what := "produced no content"
	if e.truncated {
		what = "produced an incomplete answer"
	}
	return fmt.Sprintf(
		"model %s (finish_reason=%q, ~%d reasoning tokens, %d completion tokens): "+
			"its token budget ran out",
		what, e.finishReason, e.reasoningChars/4, e.completionToken)
}

// reasoningDisabled reports whether a request has already switched reasoning off.
func reasoningDisabled(req basetenRequest) bool {
	v, ok := req.ChatTemplateKwargs["thinking"]
	if !ok {
		return false
	}
	enabled, isBool := v.(bool)
	return isBool && !enabled
}

// retryableError marks a failure worth trying again.
type retryableError struct{ err error }

func (e *retryableError) Error() string { return e.err.Error() }
func (e *retryableError) Unwrap() error { return e.err }

// attempt performs one request. It returns a retryAfter hint when the server
// supplied one.
func (c *basetenClient) attempt(ctx context.Context, body []byte, requireComplete bool) (*llm.Response, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("create chat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Api-Key "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		// Connection-level failures are worth another try.
		return nil, 0, &retryableError{fmt.Errorf("chat request: %w", err)}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, &retryableError{fmt.Errorf("read response body: %w", err)}
	}

	if resp.StatusCode != http.StatusOK {
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		err := fmt.Errorf("baseten API error (HTTP %d): %s", resp.StatusCode, apiErrorMessage(raw))
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return nil, retryAfter, &retryableError{err}
		}
		return nil, 0, err
	}

	var parsed basetenResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, 0, fmt.Errorf("unmarshal chat response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return nil, 0, fmt.Errorf("baseten API error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return nil, 0, &retryableError{errors.New("baseten returned no choices")}
	}

	choice := parsed.Choices[0]
	// Two ways the token budget bites: the model deliberates until nothing is
	// left for the answer, or it starts the answer and is cut off mid-way.
	// Both leave a JSON caller with nothing usable, and both are fixed the
	// same way, so they are reported as one condition.
	truncated := requireComplete && choice.FinishReason == "length"
	if choice.Message.Content == "" || truncated {
		return nil, 0, &emptyResponseError{
			finishReason:    choice.FinishReason,
			reasoningChars:  len(choice.Message.ReasoningContent),
			completionToken: parsed.Usage.CompletionTokens,
			truncated:       truncated && choice.Message.Content != "",
		}
	}
	out := &llm.Response{
		Content:      choice.Message.Content,
		FinishReason: choice.FinishReason,
		Usage: llm.TokenUsage{
			InputTokens:  parsed.Usage.PromptTokens,
			OutputTokens: parsed.Usage.CompletionTokens,
		},
	}
	for _, tc := range choice.Message.ToolCalls {
		args := map[string]any{}
		if tc.Function.Arguments != "" {
			// A malformed argument blob should not discard the call itself.
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
		}
		out.ToolCalls = append(out.ToolCalls, llm.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}
	return out, 0, nil
}

// apiErrorMessage pulls a human-readable message out of an error body,
// falling back to the raw payload.
func apiErrorMessage(raw []byte) string {
	var parsed basetenResponse
	if json.Unmarshal(raw, &parsed) == nil && parsed.Error != nil && parsed.Error.Message != "" {
		return parsed.Error.Message
	}
	const maxLen = 512
	if len(raw) > maxLen {
		return string(raw[:maxLen])
	}
	return string(raw)
}

// parseRetryAfter reads a Retry-After header given as whole seconds.
//
// The value is capped: it comes from the server, and an unbounded one would
// park a worker for as long as the other end cared to name.
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return min(time.Duration(secs)*time.Second, maxRetryAfter)
	}
	return 0
}

// backoff returns an exponentially growing delay with jitter, so that a batch
// of workers throttled at the same moment does not retry in lockstep.
func backoff(attempt int) time.Duration {
	base := time.Duration(1<<uint(attempt-1)) * time.Second
	if base > 30*time.Second {
		base = 30 * time.Second
	}
	return base + time.Duration(rand.Int63n(int64(500*time.Millisecond)))
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func toBasetenMessages(systemPrompt string, messages []llm.Message) []basetenMessage {
	var out []basetenMessage
	if systemPrompt != "" {
		out = append(out, basetenMessage{Role: "system", Content: systemPrompt})
	}
	for _, m := range messages {
		bm := basetenMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			args, err := json.Marshal(tc.Arguments)
			if err != nil {
				args = []byte("{}")
			}
			bm.ToolCalls = append(bm.ToolCalls, basetenToolCall{
				ID:       tc.ID,
				Type:     "function",
				Function: basetenToolFunction{Name: tc.Name, Arguments: string(args)},
			})
		}
		out = append(out, bm)
	}
	return out
}

func toBasetenTools(tools []llm.Tool) []basetenToolDef {
	if len(tools) == 0 {
		return nil
	}
	defs := make([]basetenToolDef, len(tools))
	for i, t := range tools {
		defs[i] = basetenToolDef{
			Type: "function",
			Function: basetenFunctionDecl{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		}
	}
	return defs
}

// Model returns the model name being used.
func (c *basetenClient) Model() string { return c.model }

// Provider returns the provider name.
func (c *basetenClient) Provider() string { return "baseten" }

// Close releases resources held by the client.
func (c *basetenClient) Close() error { return nil }
