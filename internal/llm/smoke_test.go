//go:build llm_smoke

package llm

// Tests in this file require real LLM API credentials and are NOT run in CI.
// Run manually with: go test ./internal/llm/ -tags=llm_smoke -v

import (
	"context"
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
