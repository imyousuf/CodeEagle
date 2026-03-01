package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOllamaEmbed(t *testing.T) {
	// Mock Ollama server.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		var req ollamaEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// Verify search_document: prefix is added.
		for _, text := range req.Input {
			if !strings.HasPrefix(text, "search_document: ") {
				t.Errorf("missing search_document: prefix in %q", text[:min(30, len(text))])
			}
		}

		// Return mock embeddings.
		embeddings := make([][]float32, len(req.Input))
		for i := range embeddings {
			embeddings[i] = make([]float32, 768)
			embeddings[i][0] = float32(i + 1) // distinguish each
		}

		resp := ollamaEmbedResponse{Embeddings: embeddings}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider, err := newOllamaProvider(Config{
		OllamaBaseURL: server.URL,
		Model:         "test-model",
		Dimensions:    768,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	ctx := context.Background()

	t.Run("Embed", func(t *testing.T) {
		results, err := provider.Embed(ctx, []string{"hello world", "test text"})
		if err != nil {
			t.Fatalf("Embed: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("got %d embeddings, want 2", len(results))
		}
		if len(results[0]) != 768 {
			t.Fatalf("got %d dimensions, want 768", len(results[0]))
		}
		if results[0][0] != 1.0 {
			t.Errorf("first embedding[0] = %f, want 1.0", results[0][0])
		}
	})

	t.Run("Embed empty", func(t *testing.T) {
		results, err := provider.Embed(ctx, nil)
		if err != nil {
			t.Fatalf("Embed nil: %v", err)
		}
		if results != nil {
			t.Errorf("expected nil for empty input, got %d results", len(results))
		}
	})

	t.Run("EmbedQuery", func(t *testing.T) {
		// Create a server that checks for search_query: prefix.
		queryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req ollamaEmbedRequest
			json.NewDecoder(r.Body).Decode(&req)

			if len(req.Input) != 1 {
				t.Errorf("expected 1 input, got %d", len(req.Input))
			}
			if len(req.Input[0]) < 14 || req.Input[0][:14] != "search_query: " {
				t.Errorf("expected search_query: prefix, got %q", req.Input[0])
			}

			resp := ollamaEmbedResponse{Embeddings: [][]float32{make([]float32, 768)}}
			json.NewEncoder(w).Encode(resp)
		}))
		defer queryServer.Close()

		qProvider, _ := newOllamaProvider(Config{
			OllamaBaseURL: queryServer.URL,
			Model:         "test-model",
		})
		vec, err := qProvider.EmbedQuery(ctx, "what is auth")
		if err != nil {
			t.Fatalf("EmbedQuery: %v", err)
		}
		if len(vec) != 768 {
			t.Errorf("got %d dimensions, want 768", len(vec))
		}
	})

	t.Run("metadata", func(t *testing.T) {
		if provider.Name() != "ollama" {
			t.Errorf("Name = %q, want ollama", provider.Name())
		}
		if provider.ModelName() != "test-model" {
			t.Errorf("ModelName = %q, want test-model", provider.ModelName())
		}
		if provider.Dimensions() != 768 {
			t.Errorf("Dimensions = %d, want 768", provider.Dimensions())
		}
	})
}

func TestOllamaEmbedError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	}))
	defer server.Close()

	provider, _ := newOllamaProvider(Config{OllamaBaseURL: server.URL})
	_, err := provider.Embed(context.Background(), []string{"test"})
	if err == nil {
		t.Error("expected error for 404 response")
	}
}
