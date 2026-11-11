package baseten

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/kit/topics"
)

func TestSalvageJSON(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"clean", `{"summary":"a"}`, `{"summary":"a"}`},
		{"fenced", "```json\n{\"summary\":\"a\"}\n```", `{"summary":"a"}`},
		{"fenced no language", "```\n{\"summary\":\"a\"}\n```", `{"summary":"a"}`},
		{"prose around it", `Here you go: {"summary":"a"} hope that helps`, `{"summary":"a"}`},
		{"nested braces", `{"a":{"b":1}}`, `{"a":{"b":1}}`},
		{"no json at all", `sorry, I cannot`, `sorry, I cannot`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := salvageJSON(tt.in); got != tt.want {
				t.Errorf("salvageJSON(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestBasetenProviderNeedsKey(t *testing.T) {
	if _, err := New(""); !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("New without a key: %v, want ErrNoAPIKey", err)
	}
}

// TestBasetenDescribeImageSendsImagePart checks the wire shape, which is the
// part that differs from every other provider: an image travels as a content
// part holding a data URI, not as a separate field.
func TestBasetenDescribeImageSendsImagePart(t *testing.T) {
	var got request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
			t.Errorf("Authorization = %q, want the bearer token", auth)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		// One line: a newline inside a JSON string is invalid JSON.
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"A long enough summary of the picture here.\",\"topics\":[\"a\",\"b\",\"c\"],\"entities\":[]}"}}]}`))
	}))
	defer srv.Close()

	p, err := New("test-key", WithBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := p.DescribeImage(context.Background(), []byte("fake-png-bytes"), "image/png"); err != nil {
		t.Fatalf("DescribeImage: %v", err)
	}

	if len(got.Messages) != 2 {
		t.Fatalf("sent %d messages, want 2", len(got.Messages))
	}

	// The user message must be a list of parts, one of which carries the image.
	parts, ok := got.Messages[1].Content.([]any)
	if !ok {
		t.Fatalf("user content is %T, want a list of content parts", got.Messages[1].Content)
	}
	var sawImage bool
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok || part["type"] != "image_url" {
			continue
		}
		sawImage = true
		url, _ := part["image_url"].(map[string]any)["url"].(string)
		if !strings.HasPrefix(url, "data:image/png;base64,") {
			t.Errorf("image url = %q, want a data URI carrying the mime type", url)
		}
	}
	if !sawImage {
		t.Error("no image_url part was sent; the model would have described nothing")
	}

	if got.MaxTokens < 4096 {
		t.Errorf("max_tokens = %d; too small a budget is spent on reasoning and "+
			"returns an empty reply", got.MaxTokens)
	}
}

// TestBasetenEmptyChoicesIsNotAnError covers a spent reasoning budget, which
// returns no choices rather than an error.
func TestBasetenEmptyChoicesIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()

	p, err := New("k", WithBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Retries are exhausted and the caller is told extraction was skipped,
	// rather than being handed a bogus empty result as if it were real.
	if _, err := p.ExtractTopics(context.Background(), "some text"); !errors.Is(err, topics.ErrExtractionSkipped) {
		t.Errorf("error = %v, want ErrExtractionSkipped", err)
	}
}

// capture is a transport that records every request and answers without a
// network, as a caller recording calls would wrap the real one.
type capture struct {
	mu     sync.Mutex
	bodies []string
	urls   []string
}

func (c *capture) RoundTrip(r *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	c.bodies = append(c.bodies, string(body))
	c.urls = append(c.urls, r.URL.String())
	c.mu.Unlock()
	reply := `{"choices":[{"message":{"content":"{\"summary\":\"A summary that is long enough.\",\"topics\":[\"a\",\"b\",\"c\"]}"}}]}`
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(reply)),
		Request:    r,
	}, nil
}

// TestWithTransportCapturesTheRequest pins that an injected transport sees
// the exact request the default client would send, to the default
// endpoint, and that the defaults are today's.
func TestWithTransportCapturesTheRequest(t *testing.T) {
	rec := &capture{}
	p, err := New("sk-test", WithTransport(rec))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "baseten" || p.ModelName() != "zai-org/GLM-5.3-Flash" {
		t.Errorf("Name, ModelName = %q, %q", p.Name(), p.ModelName())
	}
	if p.client.Timeout != RequestTimeout || RequestTimeout.Minutes() != 5 {
		t.Errorf("client timeout = %v, want five minutes", p.client.Timeout)
	}
	got, err := p.ExtractTopics(context.Background(), "Some document text.")
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != "A summary that is long enough." {
		t.Errorf("summary = %q", got.Summary)
	}
	if len(rec.bodies) != 1 {
		t.Fatalf("captured %d requests, want 1", len(rec.bodies))
	}
	if rec.urls[0] != "https://inference.baseten.co/v1/chat/completions" {
		t.Errorf("url = %q", rec.urls[0])
	}
	prompt, _ := json.Marshal(topics.TopicExtractionPrompt)
	want := `{"model":"zai-org/GLM-5.3-Flash","messages":[` +
		`{"role":"system","content":` + string(prompt) + `},` +
		`{"role":"user","content":"Some document text."}],` +
		`"max_tokens":8192,"response_format":{"type":"json_object"}}`
	if rec.bodies[0] != want {
		t.Errorf("body =\n%s\nwant\n%s", rec.bodies[0], want)
	}
}

// TestWithHTTPClientIsNotModified pins that a caller's client is used as
// given, and that a transport given with it applies to a copy.
func TestWithHTTPClientIsNotModified(t *testing.T) {
	mine := &http.Client{Timeout: 7}
	rec := &capture{}
	for _, opts := range [][]Option{
		{WithHTTPClient(mine), WithTransport(rec)},
		{WithTransport(rec), WithHTTPClient(mine)},
	} {
		p, err := New("k", append(opts, WithBaseURL("http://example.invalid/v1/"), WithModel("acme/m"))...)
		if err != nil {
			t.Fatal(err)
		}
		if p.client == mine || p.client.Timeout != 7 || p.client.Transport != rec {
			t.Errorf("client = %+v, want a copy of mine with the transport", p.client)
		}
		if _, err := p.ExtractTopics(context.Background(), "t"); err != nil {
			t.Fatal(err)
		}
	}
	if mine.Transport != nil {
		t.Error("the caller's client was modified")
	}
	if len(rec.urls) != 2 || rec.urls[0] != "http://example.invalid/v1/chat/completions" {
		t.Errorf("urls = %v", rec.urls)
	}
	if !strings.Contains(rec.bodies[0], `"model":"acme/m"`) {
		t.Errorf("body = %s", rec.bodies[0])
	}

	p, err := New("k", WithHTTPClient(mine))
	if err != nil {
		t.Fatal(err)
	}
	if p.client != mine {
		t.Error("a client given without a transport was not used as given")
	}
}

// TestWithContextWindow pins the input budget: 80% of the window at 3.5
// characters a token.
func TestWithContextWindow(t *testing.T) {
	for _, tt := range []struct {
		opts []Option
		want int
	}{
		{nil, 336_000},
		{[]Option{WithContextWindow(10)}, 28},
		{[]Option{WithContextWindow(0)}, 336_000},
	} {
		p, err := New("k", tt.opts...)
		if err != nil {
			t.Fatal(err)
		}
		if p.maxChars != tt.want {
			t.Errorf("maxChars = %d, want %d", p.maxChars, tt.want)
		}
	}
}
