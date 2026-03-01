// Package embedding provides an interface and registry for text embedding providers.
// It follows the same provider-registry pattern as pkg/llm/client.go.
package embedding

import (
	"context"
	"fmt"
	"sync"
)

// Provider generates vector embeddings from text.
type Provider interface {
	// Embed converts texts into vector embeddings.
	// For indexing, texts should be prefixed with the document prefix (handled internally).
	// For queries, use EmbedQuery instead.
	Embed(ctx context.Context, texts []string) ([][]float32, error)

	// EmbedQuery embeds a query text with appropriate prefix for search.
	EmbedQuery(ctx context.Context, text string) ([]float32, error)

	// Dimensions returns the dimensionality of the embedding vectors.
	Dimensions() int

	// Name returns the provider name (e.g., "ollama", "vertex-ai").
	Name() string

	// ModelName returns the embedding model name.
	ModelName() string
}

// Config holds configuration for creating an embedding provider.
type Config struct {
	// Provider specifies which embedding provider to use ("ollama", "vertex-ai").
	Provider string
	// Model is the embedding model name.
	Model string
	// Dimensions is the target dimensionality (for MRL-capable models).
	Dimensions int
	// OllamaBaseURL is the Ollama API base URL (default "http://localhost:11434").
	OllamaBaseURL string
	// Project is the GCP project ID (for Vertex AI).
	Project string
	// Location is the GCP region (for Vertex AI).
	Location string
	// CredentialsFile is the path to a GCP service account credentials JSON file.
	CredentialsFile string
}

// ProviderFactory is a function type for creating embedding providers.
type ProviderFactory func(cfg Config) (Provider, error)

var (
	registry   = make(map[string]ProviderFactory)
	registryMu sync.RWMutex
)

// RegisterProvider registers an embedding provider factory.
// This should be called in init() functions of provider implementations.
func RegisterProvider(name string, factory ProviderFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = factory
}

// NewProvider creates a new embedding provider based on the configuration.
func NewProvider(cfg Config) (Provider, error) {
	if cfg.Provider == "" {
		return nil, fmt.Errorf("embedding provider is required")
	}

	registryMu.RLock()
	factory, ok := registry[cfg.Provider]
	registryMu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown embedding provider: %s", cfg.Provider)
	}

	return factory(cfg)
}

// IsProviderRegistered checks if an embedding provider is registered.
func IsProviderRegistered(name string) bool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	_, ok := registry[name]
	return ok
}
