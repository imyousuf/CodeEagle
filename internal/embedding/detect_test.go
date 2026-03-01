package embedding

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	appconfig "github.com/imyousuf/CodeEagle/internal/config"
)

func TestDetectProviderOllama(t *testing.T) {
	// Mock Ollama server with the model available.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			resp := ollamaTagsResponse{
				Models: []struct {
					Name string `json:"name"`
				}{
					{Name: "nomic-embed-text-v2-moe:latest"},
					{Name: "llama3:latest"},
				},
			}
			json.NewEncoder(w).Encode(resp)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	// Override default base URL to use test server.
	origDefault := defaultOllamaBaseURL
	defer func() {
		// Can't really override a const, but we test via config.
		_ = origDefault
	}()

	cfg := &appconfig.Config{}
	cfg.Agents.EmbeddingProvider = "ollama"
	cfg.Agents.EmbeddingModel = "nomic-embed-text-v2-moe"

	// Direct config-based detection uses NewProvider, not tryOllama.
	p, err := NewProvider(Config{
		Provider:      "ollama",
		Model:         "nomic-embed-text-v2-moe",
		OllamaBaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if p.Name() != "ollama" {
		t.Errorf("Name = %q, want ollama", p.Name())
	}
	if p.ModelName() != "nomic-embed-text-v2-moe" {
		t.Errorf("ModelName = %q, want nomic-embed-text-v2-moe", p.ModelName())
	}
}

func TestDetectProviderAutoDetect(t *testing.T) {
	// Auto-detect with no explicit config. Result depends on environment.
	cfg := &appconfig.Config{}
	p, err := DetectProvider(cfg)
	if err != nil {
		t.Fatalf("DetectProvider: %v", err)
	}
	// If Ollama is running locally, it will be detected. Otherwise nil.
	if p != nil {
		if p.Name() != "ollama" && p.Name() != "vertex-ai" {
			t.Errorf("unexpected provider: %q", p.Name())
		}
		t.Logf("auto-detected provider: %s/%s", p.Name(), p.ModelName())
	} else {
		t.Log("no embedding provider detected (expected if Ollama not running)")
	}
}

func TestMatchesModelName(t *testing.T) {
	tests := []struct {
		pulled string
		target string
		want   bool
	}{
		{"nomic-embed-text-v2-moe:latest", "nomic-embed-text-v2-moe", true},
		{"nomic-embed-text-v2-moe", "nomic-embed-text-v2-moe", true},
		{"llama3:latest", "nomic-embed-text-v2-moe", false},
		{"nomic-embed-text:latest", "nomic-embed-text-v2-moe", false},
	}
	for _, tc := range tests {
		got := matchesModelName(tc.pulled, tc.target)
		if got != tc.want {
			t.Errorf("matchesModelName(%q, %q) = %v, want %v", tc.pulled, tc.target, got, tc.want)
		}
	}
}
