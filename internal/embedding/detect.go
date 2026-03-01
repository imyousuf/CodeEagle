package embedding

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

// DetectProvider checks available embedding providers in priority order.
// Priority: 1. Ollama (local) 2. Vertex AI (cloud) 3. nil (disabled)
// Returns nil with no error if no provider is available.
func DetectProvider(cfg *appconfig.Config) (Provider, error) {
	embCfg := embeddingConfigFromApp(cfg)

	// If user explicitly configured a provider, use only that.
	if embCfg.Provider != "" {
		p, err := NewProvider(embCfg)
		if err != nil {
			return nil, fmt.Errorf("configured embedding provider %q: %w", embCfg.Provider, err)
		}
		return p, nil
	}

	// Auto-detect: try Ollama first.
	if p, err := tryOllama(embCfg); err == nil && p != nil {
		return p, nil
	}

	// Try Vertex AI.
	if cfg.Agents.Project != "" && cfg.Agents.Location != "" {
		embCfg.Provider = "vertex-ai"
		embCfg.Project = cfg.Agents.Project
		embCfg.Location = cfg.Agents.Location
		embCfg.CredentialsFile = cfg.Agents.CredentialsFile
		p, err := NewProvider(embCfg)
		if err != nil {
			return nil, nil // silently skip if Vertex AI fails
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
		model = defaultOllamaModel
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

	// Check if the required model is available.
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
// Ollama model names may include tags like ":latest", so "nomic-embed-text-v2-moe"
// matches "nomic-embed-text-v2-moe:latest".
func matchesModelName(pulled, target string) bool {
	if pulled == target {
		return true
	}
	// Strip ":latest" or other tags for comparison.
	for i := range pulled {
		if pulled[i] == ':' {
			return pulled[:i] == target
		}
	}
	return false
}

// embeddingConfigFromApp builds an embedding Config from the app config.
func embeddingConfigFromApp(cfg *appconfig.Config) Config {
	return Config{
		Provider:      cfg.Agents.EmbeddingProvider,
		Model:         cfg.Agents.EmbeddingModel,
		OllamaBaseURL: "", // use default
		Project:       cfg.Agents.Project,
		Location:      cfg.Agents.Location,
		CredentialsFile: cfg.Agents.CredentialsFile,
	}
}
