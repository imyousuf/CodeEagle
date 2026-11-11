package docs

import (
	"fmt"

	"github.com/imyousuf/CodeEagle/pkg/kit/topics/baseten"
)

// DefaultBasetenDocsModel describes images and extracts topics. Verified
// against the live API to accept image content parts, which most
// open-weight models on Baseten do not.
const DefaultBasetenDocsModel = baseten.DefaultModel

func init() {
	RegisterProvider(baseten.Name, newBasetenProvider)
}

// newBasetenProvider builds the kit's Baseten provider from configuration.
// The key is checked here rather than left to the kit, so the error names
// the settings that supply it.
func newBasetenProvider(cfg Config) (Provider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("baseten docs provider needs an API key: set docs.api_key, " +
			"or transcripts.baseten_api_key, which it falls back to")
	}
	return baseten.New(cfg.APIKey,
		baseten.WithModel(cfg.Model),
		baseten.WithBaseURL(cfg.BaseURL),
		baseten.WithContextWindow(cfg.ContextWindow),
	)
}
