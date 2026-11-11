// Package docs is CodeEagle's use of pkg/kit/topics for document content
// extraction: a registry of providers under the names configuration uses
// (Ollama, Vertex AI, and the kit's Baseten provider), detection of which one
// a project has, and the Badger cache of results.
//
// The kit's types are re-exported as aliases, so the rest of CodeEagle keeps
// one import for the interface, the registry and the cache.
package docs

import (
	"fmt"
	"sync"

	"github.com/imyousuf/CodeEagle/pkg/kit/topics"
)

// DefaultContextWindow is the default Ollama num_ctx value (120K tokens).
// qwen3.5:9b supports up to 256K; 120K covers documents up to ~336KB
// (estimate: 80% × 3.5 chars/token) including large PDFs.
const DefaultContextWindow = topics.DefaultContextWindow

// ErrExtractionSkipped is returned when topic extraction fails after max retries.
// The file should still be indexed with raw text as fallback.
var ErrExtractionSkipped = topics.ErrExtractionSkipped

// ExtractionResult holds the structured output from LLM topic extraction.
type ExtractionResult = topics.ExtractionResult

// Provider extracts topics and descriptions from document content.
type Provider = topics.Provider

// Config holds configuration for creating a docs provider.
type Config struct {
	// Provider specifies which docs provider to use ("ollama", "vertex-ai",
	// "baseten").
	Provider string
	// Model is the multimodal model name.
	Model string
	// OllamaBaseURL is the Ollama API base URL (default "http://localhost:11434").
	OllamaBaseURL string
	// BaseURL overrides the endpoint for providers that speak to a hosted
	// API rather than a local daemon (Baseten).
	BaseURL string
	// APIKey authenticates against a hosted provider (Baseten).
	APIKey string
	// Project is the GCP project ID (for Vertex AI).
	Project string
	// Location is the GCP region (for Vertex AI).
	Location string
	// CredentialsFile is the path to a GCP service account credentials JSON file.
	CredentialsFile string
	// MaxImageRes is the maximum image resolution (longest edge in pixels).
	MaxImageRes int
	// ContextWindow is the Ollama num_ctx value (default DefaultContextWindow).
	ContextWindow int
	// DisableThinking appends /no_think to prompts.
	DisableThinking bool
}

// ProviderFactory is a function type for creating docs providers.
type ProviderFactory func(cfg Config) (Provider, error)

var (
	registry   = make(map[string]ProviderFactory)
	registryMu sync.RWMutex
)

// RegisterProvider registers a docs provider factory.
func RegisterProvider(name string, factory ProviderFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = factory
}

// NewProvider creates a new docs provider based on the configuration.
func NewProvider(cfg Config) (Provider, error) {
	if cfg.Provider == "" {
		return nil, fmt.Errorf("docs provider is required")
	}

	registryMu.RLock()
	factory, ok := registry[cfg.Provider]
	registryMu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown docs provider: %s", cfg.Provider)
	}

	return factory(cfg)
}

// IsProviderRegistered checks if a docs provider is registered.
func IsProviderRegistered(name string) bool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	_, ok := registry[name]
	return ok
}
