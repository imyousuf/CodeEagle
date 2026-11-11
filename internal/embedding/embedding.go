// Package embedding is CodeEagle's use of pkg/kit/embedding: it registers the
// Ollama and Vertex AI providers under the names configuration uses, and
// detects which one a project has (DetectProvider).
//
// The kit's types are re-exported as aliases, so the rest of CodeEagle keeps
// one import for the interface, the registry and detection.
package embedding

import (
	kitembedding "github.com/imyousuf/CodeEagle/pkg/kit/embedding"
	"github.com/imyousuf/CodeEagle/pkg/kit/embedding/ollama"
	"github.com/imyousuf/CodeEagle/pkg/kit/embedding/vertex"
)

// Provider generates vector embeddings from text.
type Provider = kitembedding.Provider

// Config holds configuration for creating an embedding provider.
type Config = kitembedding.Config

// ProviderFactory creates a provider from configuration.
type ProviderFactory = kitembedding.ProviderFactory

var (
	// RegisterProvider registers a provider factory under a name.
	RegisterProvider = kitembedding.RegisterProvider
	// NewProvider creates the provider configuration names.
	NewProvider = kitembedding.NewProvider
	// IsProviderRegistered reports whether a provider name is registered.
	IsProviderRegistered = kitembedding.IsProviderRegistered
)

func init() {
	kitembedding.RegisterProvider(ollama.Name, func(cfg Config) (Provider, error) { return ollama.New(cfg) })
	kitembedding.RegisterProvider(vertex.Name, vertex.New)
}
