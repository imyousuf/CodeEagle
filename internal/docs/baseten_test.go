package docs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	if _, err := newBasetenProvider(Config{Provider: "baseten"}); err == nil {
		t.Fatal("expected an error without an API key")
	}
}

// TestBasetenDescribeImageSendsImagePart checks the wire shape, which is the
// part that differs from every other provider here: an image travels as a
// content part holding a data URI, not as a separate field.
func TestBasetenDescribeImageSendsImagePart(t *testing.T) {
	var got basetenRequest
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

	p, err := newBasetenProvider(Config{
		Provider: "baseten", APIKey: "test-key", BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("newBasetenProvider: %v", err)
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

	p, err := newBasetenProvider(Config{Provider: "baseten", APIKey: "k", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("newBasetenProvider: %v", err)
	}
	// Retries are exhausted and the caller is told extraction was skipped,
	// rather than being handed a bogus empty result as if it were real.
	if _, err := p.ExtractTopics(context.Background(), "some text"); err == nil {
		t.Error("expected ErrExtractionSkipped, got nil")
	}
}
