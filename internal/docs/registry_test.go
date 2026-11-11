package docs_test

import (
	"context"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/docs"
)

// TestRegistryBuiltins pins which providers register themselves and how the
// registry refuses a missing or unknown name.
func TestRegistryBuiltins(t *testing.T) {
	for _, name := range []string{"ollama", "vertex-ai", "baseten"} {
		if !docs.IsProviderRegistered(name) {
			t.Errorf("IsProviderRegistered(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "anthropic", "Baseten"} {
		if docs.IsProviderRegistered(name) {
			t.Errorf("IsProviderRegistered(%q) = true, want false", name)
		}
	}

	tests := []struct {
		name    string
		cfg     docs.Config
		wantErr string
	}{
		{"no provider", docs.Config{}, "docs provider is required"},
		{"unknown provider", docs.Config{Provider: "nope"}, "unknown docs provider: nope"},
		{
			"baseten without a key",
			docs.Config{Provider: "baseten"},
			"baseten docs provider needs an API key: set docs.api_key, " +
				"or transcripts.baseten_api_key, which it falls back to",
		},
		{"vertex without a project", docs.Config{Provider: "vertex-ai", Location: "us-central1"},
			"vertex-ai docs requires project"},
		{"vertex without a location", docs.Config{Provider: "vertex-ai", Project: "p"},
			"vertex-ai docs requires location"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := docs.NewProvider(tt.cfg)
			if err == nil || err.Error() != tt.wantErr {
				t.Errorf("NewProvider error = %v, want %q", err, tt.wantErr)
			}
			if p != nil {
				t.Errorf("NewProvider returned a provider alongside an error: %T", p)
			}
		})
	}
}

// TestRegistryOllamaDefaults pins the Ollama provider's identity, which needs
// no daemon to construct.
func TestRegistryOllamaDefaults(t *testing.T) {
	tests := []struct {
		model string
		want  string
	}{
		{"", "qwen3.5:9b"},
		{"llava:13b", "llava:13b"},
	}
	for _, tt := range tests {
		p, err := docs.NewProvider(docs.Config{Provider: "ollama", Model: tt.model})
		if err != nil {
			t.Fatalf("NewProvider(ollama): %v", err)
		}
		if p.Name() != "ollama" || p.ModelName() != tt.want {
			t.Errorf("ollama provider = %s/%s, want ollama/%s", p.Name(), p.ModelName(), tt.want)
		}
	}
}

// fakeProvider is a registered provider that remembers the config it was
// built from.
type fakeProvider struct{ cfg docs.Config }

func (f *fakeProvider) ExtractTopics(context.Context, string) (*docs.ExtractionResult, error) {
	return nil, nil
}

func (f *fakeProvider) DescribeImage(context.Context, []byte, string) (*docs.ExtractionResult, error) {
	return nil, nil
}
func (f *fakeProvider) Name() string      { return "test-fake" }
func (f *fakeProvider) ModelName() string { return f.cfg.Model }

// TestRegistryDispatch covers registering a provider and having NewProvider
// hand its factory the config unchanged.
func TestRegistryDispatch(t *testing.T) {
	const name = "test-fake-docs-provider"
	docs.RegisterProvider(name, func(cfg docs.Config) (docs.Provider, error) {
		return &fakeProvider{cfg: cfg}, nil
	})
	if !docs.IsProviderRegistered(name) {
		t.Fatalf("%s not registered after RegisterProvider", name)
	}

	cfg := docs.Config{
		Provider: name, Model: "m", OllamaBaseURL: "http://o", BaseURL: "http://b",
		APIKey: "k", Project: "p", Location: "l", CredentialsFile: "/c.json",
		MaxImageRes: 512, ContextWindow: 42, DisableThinking: true,
	}
	p, err := docs.NewProvider(cfg)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	fake, ok := p.(*fakeProvider)
	if !ok {
		t.Fatalf("NewProvider returned %T, want the registered factory's provider", p)
	}
	if fake.cfg != cfg {
		t.Errorf("factory got %+v, want %+v", fake.cfg, cfg)
	}
}
