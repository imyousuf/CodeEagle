package docs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	appconfig "github.com/imyousuf/CodeEagle/internal/config"
)

// ollamaTagsResponse is the response from Ollama /api/tags endpoint.
type ollamaTagsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

// DetectProvider checks available docs providers in priority order.
// Priority: 1. Explicit config 2. Ollama (local) 3. Vertex AI (cloud) 4. nil (disabled)
// Returns nil with no error if no provider is available (graceful degradation).
func DetectProvider(cfg *appconfig.Config) (Provider, error) {
	docsCfg := docsConfigFromApp(cfg)

	// If user explicitly configured a provider, use only that.
	if docsCfg.Provider != "" {
		p, err := NewProvider(docsCfg)
		if err != nil {
			return nil, fmt.Errorf("configured docs provider %q: %w", docsCfg.Provider, err)
		}
		return p, nil
	}

	// Auto-detect: try Ollama first.
	if p, err := tryOllama(docsCfg); err == nil && p != nil {
		return p, nil
	}

	// Try Vertex AI.
	if cfg.Docs.Project != "" && cfg.Docs.Location != "" {
		docsCfg.Provider = "vertex-ai"
		docsCfg.Project = cfg.Docs.Project
		docsCfg.Location = cfg.Docs.Location
		docsCfg.CredentialsFile = cfg.Docs.CredentialsFile
		p, err := NewProvider(docsCfg)
		if err != nil {
			return nil, nil // silently skip if Vertex AI fails
		}
		return p, nil
	}

	// Also try Vertex AI from agents config.
	if cfg.Agents.Project != "" && cfg.Agents.Location != "" {
		docsCfg.Provider = "vertex-ai"
		docsCfg.Project = cfg.Agents.Project
		docsCfg.Location = cfg.Agents.Location
		docsCfg.CredentialsFile = cfg.Agents.CredentialsFile
		p, err := NewProvider(docsCfg)
		if err != nil {
			return nil, nil
		}
		return p, nil
	}

	return nil, nil
}

// tryOllama checks if Ollama is running and has the required model.
func tryOllama(cfg Config) (Provider, error) {
	baseURL := cfg.OllamaBaseURL
	if baseURL == "" {
		baseURL = defaultOllamaBaseURL
	}
	model := cfg.Model
	if model == "" {
		model = defaultOllamaDocsModel
	}

	client := &http.Client{Timeout: 2 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err // Ollama not running
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama tags: status %d", resp.StatusCode)
	}

	var tags ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, fmt.Errorf("decode ollama tags: %w", err)
	}

	for _, m := range tags.Models {
		if matchesModelName(m.Name, model) {
			cfg.Provider = "ollama"
			cfg.Model = model
			return NewProvider(cfg)
		}
	}

	return nil, fmt.Errorf("ollama model %q not found", model)
}

// matchesModelName checks if a pulled model name matches the target.
func matchesModelName(pulled, target string) bool {
	if pulled == target {
		return true
	}
	for i := range pulled {
		if pulled[i] == ':' {
			return pulled[:i] == target
		}
	}
	return false
}

// docsConfigFromApp builds a docs Config from the app config.
func docsConfigFromApp(cfg *appconfig.Config) Config {
	return Config{
		Provider:        cfg.Docs.Provider,
		Model:           cfg.Docs.Model,
		OllamaBaseURL:   cfg.Docs.BaseURL,
		Project:         cfg.Docs.Project,
		Location:        cfg.Docs.Location,
		CredentialsFile: cfg.Docs.CredentialsFile,
		MaxImageRes:     cfg.Docs.MaxImageRes,
		ContextWindow:   cfg.Docs.ContextWindow,
		DisableThinking: cfg.Docs.DisableThinking,
	}
}
