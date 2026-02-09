package llm

import (
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/llm"
)

func TestProviderRegistration(t *testing.T) {
	if !llm.IsProviderRegistered("anthropic") {
		t.Fatal("expected 'anthropic' provider to be registered via init()")
	}
}

func TestNewClientValidation(t *testing.T) {
	_, err := llm.NewClient(llm.Config{
		Provider: "anthropic",
		// No API key
	})
	if err == nil {
		t.Fatal("expected error when API key is missing")
	}
	expected := "API key is required for Anthropic provider"
	if err.Error() != expected {
		t.Fatalf("expected error %q, got %q", expected, err.Error())
	}
}

func TestNewClientDefaults(t *testing.T) {
	client, err := llm.NewClient(llm.Config{
		Provider: "anthropic",
		APIKey:   "test-key",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer client.Close()

	if client.Model() != "claude-sonnet-4-5-20250929" {
		t.Errorf("expected default model %q, got %q", "claude-sonnet-4-5-20250929", client.Model())
	}

	if client.Provider() != "anthropic" {
		t.Errorf("expected provider %q, got %q", "anthropic", client.Provider())
	}
}

func TestNewClientCustomModel(t *testing.T) {
	client, err := llm.NewClient(llm.Config{
		Provider: "anthropic",
		APIKey:   "test-key",
		Model:    "claude-haiku-4-5-20251001",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer client.Close()

	if client.Model() != "claude-haiku-4-5-20251001" {
		t.Errorf("expected model %q, got %q", "claude-haiku-4-5-20251001", client.Model())
	}
}

func TestNewClientCustomBaseURL(t *testing.T) {
	client, err := newAnthropicClient(llm.Config{
		APIKey:  "test-key",
		BaseURL: "https://custom.api.example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ac := client.(*anthropicClient)
	if ac.baseURL != "https://custom.api.example.com" {
		t.Errorf("expected base URL %q, got %q", "https://custom.api.example.com", ac.baseURL)
	}
}

func TestUnknownProvider(t *testing.T) {
	_, err := llm.NewClient(llm.Config{
		Provider: "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestEmptyProvider(t *testing.T) {
	_, err := llm.NewClient(llm.Config{})
	if err == nil {
		t.Fatal("expected error when provider is empty")
	}
	expected := "provider is required"
	if err.Error() != expected {
		t.Fatalf("expected error %q, got %q", expected, err.Error())
	}
}
