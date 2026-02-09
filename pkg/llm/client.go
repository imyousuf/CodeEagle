package llm

import (
	"context"
	"fmt"
	"sync"
)

// Client is the interface for interacting with LLM providers.
type Client interface {
	// Chat sends a system prompt and messages to the LLM and returns a response.
	Chat(ctx context.Context, systemPrompt string, messages []Message) (*Response, error)

	// Model returns the name of the model being used.
	Model() string

	// Provider returns the provider name (e.g., "anthropic", "openai").
	Provider() string

	// Close releases any resources held by the client.
	Close() error
}

// Config holds configuration for creating an LLM client.
type Config struct {
	// Provider specifies which LLM provider to use.
	Provider string
	// Model is the model name/ID to use.
	Model string
	// APIKey is the API key for authentication.
	APIKey string
	// BaseURL is an optional base URL override for the provider API.
	BaseURL string
	// Project is the GCP project ID (for Vertex AI).
	Project string
	// Location is the GCP region (for Vertex AI, e.g. "us-central1").
	Location string
	// CredentialsFile is the path to a GCP service account credentials JSON file (for Vertex AI).
	CredentialsFile string
}

// ProviderFactory is a function type for creating LLM clients.
type ProviderFactory func(cfg Config) (Client, error)

var (
	registry   = make(map[string]ProviderFactory)
	registryMu sync.RWMutex
)

// RegisterProvider registers a client factory for a provider.
// This should be called in init() functions of provider implementations.
func RegisterProvider(name string, factory ProviderFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = factory
}

// NewClient creates a new LLM client based on the configuration.
func NewClient(cfg Config) (Client, error) {
	if cfg.Provider == "" {
		return nil, fmt.Errorf("provider is required")
	}

	registryMu.RLock()
	factory, ok := registry[cfg.Provider]
	registryMu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown provider: %s (available: %v)", cfg.Provider, availableProviders())
	}

	return factory(cfg)
}

// IsProviderRegistered checks if a provider is registered.
func IsProviderRegistered(name string) bool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	_, ok := registry[name]
	return ok
}

// availableProviders returns a list of registered provider names.
func availableProviders() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	providers := make([]string, 0, len(registry))
	for p := range registry {
		providers = append(providers, p)
	}
	return providers
}
