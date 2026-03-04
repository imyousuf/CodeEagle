// Package docs provides an interface and registry for document content extraction providers.
// It follows the same provider-registry pattern as internal/embedding/provider.go.
package docs

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrExtractionSkipped is returned when topic extraction fails after max retries.
// The file should still be indexed with raw text as fallback.
var ErrExtractionSkipped = errors.New("extraction skipped after max retries")

// ExtractionResult holds the structured output from LLM topic extraction.
type ExtractionResult struct {
	// Summary is a 2-3 sentence description of the content.
	Summary string `json:"summary"`
	// Topics is a list of extracted topic strings (e.g., "authentication", "database schema").
	Topics []string `json:"topics"`
	// Entities is a list of specific names (functions, classes, packages) mentioned.
	Entities []string `json:"entities"`
}

// Provider extracts topics and descriptions from document content.
type Provider interface {
	// ExtractTopics processes text content and returns structured extraction.
	ExtractTopics(ctx context.Context, text string) (*ExtractionResult, error)

	// DescribeImage processes an image and returns structured extraction.
	DescribeImage(ctx context.Context, imageData []byte, mimeType string) (*ExtractionResult, error)

	// Name returns the provider name (e.g., "ollama", "vertex-ai").
	Name() string

	// ModelName returns the model identifier.
	ModelName() string
}

// Config holds configuration for creating a docs provider.
type Config struct {
	// Provider specifies which docs provider to use ("ollama", "vertex-ai").
	Provider string
	// Model is the multimodal model name.
	Model string
	// OllamaBaseURL is the Ollama API base URL (default "http://localhost:11434").
	OllamaBaseURL string
	// Project is the GCP project ID (for Vertex AI).
	Project string
	// Location is the GCP region (for Vertex AI).
	Location string
	// CredentialsFile is the path to a GCP service account credentials JSON file.
	CredentialsFile string
	// MaxImageRes is the maximum image resolution (longest edge in pixels).
	MaxImageRes int
	// ContextWindow is the Ollama num_ctx value (default 49152).
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
