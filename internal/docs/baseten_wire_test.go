package docs_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/docs"
)

// These tests pin the Baseten docs provider's behaviour on the wire, through
// the registry only, so that moving the provider elsewhere needs the
// constructor wiring changed and nothing else.

// Prompts as sent today. Spelled out rather than referenced so that moving
// them cannot change what the model is asked without a test noticing.
const (
	topicPrompt = `Extract topics, entities, and key concepts from the following text. If the text is very long, first summarize it then extract. Output JSON with: {"summary": "2-3 sentence summary", "topics": ["topic1", "topic2", ...], "entities": ["FunctionName", "ClassName", "package_name", ...]}. Be concise. Topics should be abstract themes, not specific names.`
	imagePrompt = `Describe this image in detail. If it is a diagram, describe the components, flow, and relationships. Extract any text visible in the image. Output JSON with: {"summary": "2-3 sentence description", "topics": ["topic1", "topic2", ...], "entities": ["visible names, labels, identifiers", ...]}.`
)

// goodContent is a model reply the quality gate accepts: more than two
// topics, a summary over twenty characters, and no ellipsis.
const goodContent = `{"summary":"A summary that is long enough.","topics":["a","b","c"],"entities":["X"]}`

// basetenRecorder is a test server that answers each request with the next
// scripted reply and keeps every request it saw.
type basetenRecorder struct {
	mu       sync.Mutex
	requests []recordedRequest
	replies  []basetenReply
	srv      *httptest.Server
}

type recordedRequest struct {
	Method      string
	Path        string
	Auth        string
	ContentType string
	Body        string
}

type basetenReply struct {
	status int
	body   string
}

// chatReply wraps message content in a chat completion envelope.
func chatReply(content string) basetenReply {
	data, _ := json.Marshal(content)
	return basetenReply{status: http.StatusOK, body: `{"choices":[{"message":{"content":` + string(data) + `}}]}`}
}

func newBasetenRecorder(t *testing.T, replies ...basetenReply) *basetenRecorder {
	t.Helper()
	rec := &basetenRecorder{replies: replies}
	rec.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		n := len(rec.requests)
		rec.requests = append(rec.requests, recordedRequest{
			Method:      r.Method,
			Path:        r.URL.Path,
			Auth:        r.Header.Get("Authorization"),
			ContentType: r.Header.Get("Content-Type"),
			Body:        string(body),
		})
		reply := rec.replies[min(n, len(rec.replies)-1)]
		rec.mu.Unlock()
		w.WriteHeader(reply.status)
		_, _ = w.Write([]byte(reply.body))
	}))
	t.Cleanup(rec.srv.Close)
	return rec
}

func (r *basetenRecorder) seen() []recordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedRequest(nil), r.requests...)
}

func newBaseten(t *testing.T, cfg docs.Config) docs.Provider {
	t.Helper()
	cfg.Provider = "baseten"
	if cfg.APIKey == "" {
		cfg.APIKey = "test-key"
	}
	p, err := docs.NewProvider(cfg)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	return p
}

// jsonString renders s as a JSON string literal, as encoding/json does.
func jsonString(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

func TestBasetenPromptsUnchanged(t *testing.T) {
	if docs.TopicExtractionPrompt != topicPrompt {
		t.Errorf("TopicExtractionPrompt changed:\n%s", docs.TopicExtractionPrompt)
	}
	if docs.ImageDescriptionPrompt != imagePrompt {
		t.Errorf("ImageDescriptionPrompt changed:\n%s", docs.ImageDescriptionPrompt)
	}
}

func TestBasetenNameAndModel(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  string
	}{
		{"default model", "", "zai-org/GLM-5.3-Flash"},
		{"configured model", "acme/vision-1", "acme/vision-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newBaseten(t, docs.Config{Model: tt.model})
			if got := p.Name(); got != "baseten" {
				t.Errorf("Name() = %q, want baseten", got)
			}
			if got := p.ModelName(); got != tt.want {
				t.Errorf("ModelName() = %q, want %q", got, tt.want)
			}
		})
	}
	if docs.DefaultBasetenDocsModel != "zai-org/GLM-5.3-Flash" {
		t.Errorf("DefaultBasetenDocsModel = %q", docs.DefaultBasetenDocsModel)
	}
}

// TestBasetenExtractTopicsRequest pins the whole request for a topic
// extraction: method, path, headers and the exact JSON body.
func TestBasetenExtractTopicsRequest(t *testing.T) {
	rec := newBasetenRecorder(t, chatReply(goodContent))
	// A trailing slash on the base URL is trimmed, not doubled.
	p := newBaseten(t, docs.Config{BaseURL: rec.srv.URL + "/v1/", APIKey: "sk-test", Model: "acme/m"})

	got, err := p.ExtractTopics(context.Background(), "Some document text.")
	if err != nil {
		t.Fatalf("ExtractTopics: %v", err)
	}
	want := &docs.ExtractionResult{
		Summary:  "A summary that is long enough.",
		Topics:   []string{"a", "b", "c"},
		Entities: []string{"X"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("result = %+v, want %+v", got, want)
	}

	reqs := rec.seen()
	if len(reqs) != 1 {
		t.Fatalf("sent %d requests, want 1", len(reqs))
	}
	r := reqs[0]
	if r.Method != http.MethodPost || r.Path != "/v1/chat/completions" {
		t.Errorf("request = %s %s, want POST /v1/chat/completions", r.Method, r.Path)
	}
	if r.Auth != "Bearer sk-test" {
		t.Errorf("Authorization = %q, want Bearer sk-test", r.Auth)
	}
	if r.ContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", r.ContentType)
	}
	wantBody := `{"model":"acme/m","messages":[` +
		`{"role":"system","content":` + jsonString(topicPrompt) + `},` +
		`{"role":"user","content":"Some document text."}],` +
		`"max_tokens":8192,"response_format":{"type":"json_object"}}`
	if r.Body != wantBody {
		t.Errorf("body =\n%s\nwant\n%s", r.Body, wantBody)
	}
}

// TestBasetenDescribeImageRequest pins the exact body for an image: a text
// part and an image_url part holding a data URI, with PNG assumed when the
// MIME type is unknown.
func TestBasetenDescribeImageRequest(t *testing.T) {
	tests := []struct {
		name     string
		mimeType string
		wantURI  string
	}{
		{"given type", "image/jpeg", "data:image/jpeg;base64,YWJj"},
		{"empty type assumes png", "", "data:image/png;base64,YWJj"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := newBasetenRecorder(t, chatReply(goodContent))
			p := newBaseten(t, docs.Config{BaseURL: rec.srv.URL})

			if _, err := p.DescribeImage(context.Background(), []byte("abc"), tt.mimeType); err != nil {
				t.Fatalf("DescribeImage: %v", err)
			}
			reqs := rec.seen()
			if len(reqs) != 1 {
				t.Fatalf("sent %d requests, want 1", len(reqs))
			}
			if reqs[0].Path != "/chat/completions" {
				t.Errorf("path = %q, want /chat/completions", reqs[0].Path)
			}
			wantBody := `{"model":"zai-org/GLM-5.3-Flash","messages":[` +
				`{"role":"system","content":` + jsonString(imagePrompt) + `},` +
				`{"role":"user","content":[{"type":"text","text":"Describe this image."},` +
				`{"type":"image_url","image_url":{"url":"` + tt.wantURI + `"}}]}],` +
				`"max_tokens":8192,"response_format":{"type":"json_object"}}`
			if reqs[0].Body != wantBody {
				t.Errorf("body =\n%s\nwant\n%s", reqs[0].Body, wantBody)
			}
		})
	}
}

// userContent decodes the user message's text out of a recorded body.
func userContent(t *testing.T, body string) string {
	t.Helper()
	var req struct {
		Messages []struct {
			Content any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(req.Messages))
	}
	s, _ := req.Messages[1].Content.(string)
	return s
}

// TestBasetenTruncatesText pins the input budget: 80% of the context window
// at 3.5 characters a token, cut by bytes.
func TestBasetenTruncatesText(t *testing.T) {
	tests := []struct {
		name          string
		contextWindow int
		text          string
		want          string
	}{
		// 10 tokens * 0.8 * 3.5 = 28 bytes.
		{"over budget", 10, strings.Repeat("abcd", 10), strings.Repeat("abcd", 7)},
		{"at budget", 10, strings.Repeat("x", 28), strings.Repeat("x", 28)},
		{"under budget", 10, "short", "short"},
		// The cut is by bytes, so it can land inside a multi-byte rune; the
		// broken rune is sent as U+FFFD.
		{"cut inside a rune", 10, strings.Repeat("a", 27) + "é", strings.Repeat("a", 27) + "�"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := newBasetenRecorder(t, chatReply(goodContent))
			p := newBaseten(t, docs.Config{BaseURL: rec.srv.URL, ContextWindow: tt.contextWindow})
			if _, err := p.ExtractTopics(context.Background(), tt.text); err != nil {
				t.Fatalf("ExtractTopics: %v", err)
			}
			if got := userContent(t, rec.seen()[0].Body); got != tt.want {
				t.Errorf("user content = %q, want %q", got, tt.want)
			}
		})
	}

	// The default window of 120,000 tokens allows 336,000 bytes.
	rec := newBasetenRecorder(t, chatReply(goodContent))
	p := newBaseten(t, docs.Config{BaseURL: rec.srv.URL})
	if _, err := p.ExtractTopics(context.Background(), strings.Repeat("z", 336_001)); err != nil {
		t.Fatalf("ExtractTopics: %v", err)
	}
	if got := len(userContent(t, rec.seen()[0].Body)); got != 336_000 {
		t.Errorf("default budget sent %d bytes, want 336000", got)
	}
}

// TestBasetenRetry pins extractWithRetry: up to four attempts, each the same
// request again; an error or a reply the quality gate rejects earns another
// attempt; when every attempt fails the last error wins over
// ErrExtractionSkipped, even if later attempts did not error.
func TestBasetenRetry(t *testing.T) {
	status500 := basetenReply{status: http.StatusInternalServerError, body: "upstream exploded"}
	tests := []struct {
		name         string
		replies      []basetenReply
		wantRequests int
		wantSummary  string
		wantErr      string
		wantSkipped  bool
	}{
		{
			name:         "unparseable then valid",
			replies:      []basetenReply{chatReply("sorry, I cannot"), chatReply(goodContent)},
			wantRequests: 2,
			wantSummary:  "A summary that is long enough.",
		},
		{
			name:         "empty choices then valid",
			replies:      []basetenReply{{status: http.StatusOK, body: `{"choices":[]}`}, chatReply(goodContent)},
			wantRequests: 2,
			wantSummary:  "A summary that is long enough.",
		},
		{
			name:         "status error then valid",
			replies:      []basetenReply{status500, chatReply(goodContent)},
			wantRequests: 2,
			wantSummary:  "A summary that is long enough.",
		},
		{
			name:         "fenced reply is salvaged",
			replies:      []basetenReply{chatReply("Here:\n```json\n" + goodContent + "\n```\n")},
			wantRequests: 1,
			wantSummary:  "A summary that is long enough.",
		},
		{
			name:         "two topics is not enough",
			replies:      []basetenReply{chatReply(`{"summary":"A summary that is long enough.","topics":["a","b"]}`)},
			wantRequests: 4,
			wantSkipped:  true,
		},
		{
			name:         "ellipsis in summary is rejected",
			replies:      []basetenReply{chatReply(`{"summary":"A summary that trails off...","topics":["a","b","c"]}`)},
			wantRequests: 4,
			wantSkipped:  true,
		},
		{
			name:         "twenty character summary is rejected",
			replies:      []basetenReply{chatReply(`{"summary":"exactly twenty chars","topics":["a","b","c"]}`)},
			wantRequests: 4,
			wantSkipped:  true,
		},
		{
			name:         "twenty-one character summary is accepted",
			replies:      []basetenReply{chatReply(`{"summary":"exactly twenty-1 char","topics":["a","b","c"]}`)},
			wantRequests: 1,
			wantSummary:  "exactly twenty-1 char",
		},
		{
			name:         "status error every time",
			replies:      []basetenReply{status500},
			wantRequests: 4,
			wantErr:      "baseten chat: status 500: upstream exploded",
		},
		{
			name: "earlier error outlives later garbage",
			replies: []basetenReply{
				{status: http.StatusBadGateway, body: "bad gateway"},
				chatReply("nope"), chatReply("nope"), chatReply("nope"),
			},
			wantRequests: 4,
			wantErr:      "baseten chat: status 502: bad gateway",
		},
		{
			name: "latest error wins",
			replies: []basetenReply{
				{status: http.StatusBadGateway, body: "first"},
				chatReply("nope"),
				{status: http.StatusTooManyRequests, body: "second"},
				chatReply("nope"),
			},
			wantRequests: 4,
			wantErr:      "baseten chat: status 429: second",
		},
		{
			name:         "error object in a 200 reply",
			replies:      []basetenReply{{status: http.StatusOK, body: `{"error":{"message":"model is loading"}}`}},
			wantRequests: 4,
			wantErr:      "baseten chat: model is loading",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := newBasetenRecorder(t, tt.replies...)
			p := newBaseten(t, docs.Config{BaseURL: rec.srv.URL})

			got, err := p.ExtractTopics(context.Background(), "the text")

			reqs := rec.seen()
			if len(reqs) != tt.wantRequests {
				t.Errorf("sent %d requests, want %d", len(reqs), tt.wantRequests)
			}
			for i := 1; i < len(reqs); i++ {
				if reqs[i].Body != reqs[0].Body {
					t.Errorf("attempt %d sent a different body from the first:\n%s\nvs\n%s",
						i+1, reqs[i].Body, reqs[0].Body)
				}
			}
			switch {
			case tt.wantSkipped:
				if !errors.Is(err, docs.ErrExtractionSkipped) || got != nil {
					t.Errorf("got (%+v, %v), want (nil, ErrExtractionSkipped)", got, err)
				}
			case tt.wantErr != "":
				if err == nil || err.Error() != tt.wantErr || got != nil {
					t.Errorf("got (%+v, %v), want error %q", got, err, tt.wantErr)
				}
			default:
				if err != nil {
					t.Fatalf("ExtractTopics: %v", err)
				}
				if got.Summary != tt.wantSummary {
					t.Errorf("summary = %q, want %q", got.Summary, tt.wantSummary)
				}
			}
		})
	}
}

// TestBasetenErrorBodies pins the error text for a refusal, whose body is
// echoed up to 2 KiB, and for a reply that is not JSON at all.
func TestBasetenErrorBodies(t *testing.T) {
	long := strings.Repeat("x", 3000)
	rec := newBasetenRecorder(t, basetenReply{status: http.StatusBadRequest, body: long})
	p := newBaseten(t, docs.Config{BaseURL: rec.srv.URL})
	_, err := p.ExtractTopics(context.Background(), "t")
	if want := "baseten chat: status 400: " + long[:2048]; err == nil || err.Error() != want {
		t.Errorf("error = %v, want the status and the first 2048 bytes of the body", err)
	}

	rec = newBasetenRecorder(t, basetenReply{status: http.StatusOK, body: "<html>not json</html>"})
	p = newBaseten(t, docs.Config{BaseURL: rec.srv.URL})
	_, err = p.ExtractTopics(context.Background(), "t")
	if err == nil || !strings.HasPrefix(err.Error(), "decode chat response: ") {
		t.Errorf("error = %v, want a decode chat response error", err)
	}
	if n := len(rec.seen()); n != 4 {
		t.Errorf("sent %d requests for an undecodable reply, want 4", n)
	}
}

// TestBasetenContextCancellation pins that a cancelled context stops the
// retries: before any attempt nothing is sent, and a cancellation during an
// attempt ends the loop after that attempt.
func TestBasetenContextCancellation(t *testing.T) {
	rec := newBasetenRecorder(t, chatReply(goodContent))
	p := newBaseten(t, docs.Config{BaseURL: rec.srv.URL})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.ExtractTopics(ctx, "t"); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if n := len(rec.seen()); n != 0 {
		t.Errorf("sent %d requests with a cancelled context, want 0", n)
	}

	// The reply would earn a retry, but the context is cancelled while it is
	// being produced. Whether the client sees the reply or the cancellation
	// first, the error carries context.Canceled and no second attempt goes out.
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		cancel()
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()
	p = newBaseten(t, docs.Config{BaseURL: srv.URL})
	if _, err := p.ExtractTopics(ctx, "t"); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("sent %d requests, want 1 before cancellation was noticed", n)
	}
}
