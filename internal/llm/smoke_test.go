//go:build llm_smoke

package llm

// Tests in this file require real LLM API credentials and are NOT run in CI.
// Run manually with: go test ./internal/llm/ -tags=llm_smoke -v

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/pkg/llm"
)

func TestAnthropicSmoke(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set, skipping Anthropic smoke test")
	}

	client, err := llm.NewClient(llm.Config{
		Provider: "anthropic",
		APIKey:   apiKey,
	})
	if err != nil {
		t.Fatalf("failed to create Anthropic client: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := client.Chat(ctx, "You are a helpful assistant.", []llm.Message{
		{Role: llm.RoleUser, Content: "What is 2+2? Reply with just the number."},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if resp.Content == "" {
		t.Fatal("expected non-empty response content")
	}
	t.Logf("Anthropic response: %s", resp.Content)
	t.Logf("Token usage: input=%d, output=%d", resp.Usage.InputTokens, resp.Usage.OutputTokens)
}

func ollamaAvailable(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:11434/api/tags", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Skip("Ollama not running, skipping: ", err)
	}
	resp.Body.Close()
}

func ollamaHasModel(t *testing.T, model string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:11434/api/tags", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Skipf("Ollama not running, skipping: %v", err)
	}
	defer resp.Body.Close()

	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		t.Skipf("failed to decode Ollama tags: %v", err)
	}
	for _, m := range tags.Models {
		if m.Name == model {
			return
		}
	}
	t.Skipf("model %q not found in Ollama, skipping", model)
}

func ollamaChatSmoke(t *testing.T, model string) {
	t.Helper()
	client, err := llm.NewClient(llm.Config{
		Provider: "ollama",
		Model:    model,
	})
	if err != nil {
		t.Fatalf("failed to create Ollama client: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	resp, err := client.Chat(ctx, "You are a helpful assistant.", []llm.Message{
		{Role: llm.RoleUser, Content: "What is 2+2? Reply with just the number."},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if resp.Content == "" {
		t.Fatal("expected non-empty response content")
	}
	t.Logf("Ollama %s response: %s", model, resp.Content)
	t.Logf("Token usage: input=%d, output=%d", resp.Usage.InputTokens, resp.Usage.OutputTokens)
}

func TestOllamaSmokeGPTOSS(t *testing.T) {
	ollamaAvailable(t)
	ollamaHasModel(t, "gpt-oss:20b")
	ollamaChatSmoke(t, "gpt-oss:20b")
}

func TestOllamaSmokeQwen3(t *testing.T) {
	ollamaAvailable(t)
	ollamaHasModel(t, "qwen3:14b-q8_0")
	ollamaChatSmoke(t, "qwen3:14b-q8_0")
}

func TestVertexAISmoke(t *testing.T) {
	project := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if project == "" {
		t.Skip("GOOGLE_CLOUD_PROJECT not set, skipping Vertex AI smoke test")
	}

	client, err := llm.NewClient(llm.Config{
		Provider: "vertex-ai",
		Project:  project,
	})
	if err != nil {
		t.Fatalf("failed to create Vertex AI client: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := client.Chat(ctx, "You are a helpful assistant.", []llm.Message{
		{Role: llm.RoleUser, Content: "What is 2+2? Reply with just the number."},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if resp.Content == "" {
		t.Fatal("expected non-empty response content")
	}
	t.Logf("Vertex AI response: %s", resp.Content)
	t.Logf("Token usage: input=%d, output=%d", resp.Usage.InputTokens, resp.Usage.OutputTokens)
}
