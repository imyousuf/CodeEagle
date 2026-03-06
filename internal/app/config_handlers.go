//go:build app

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/imyousuf/CodeEagle/internal/config"
	internalllm "github.com/imyousuf/CodeEagle/internal/llm"
	"github.com/imyousuf/CodeEagle/pkg/llm"
)

// GetConfig returns the current project configuration as a DTO.
func (a *App) GetConfig() *ConfigDTO {
	return configToDTO(a.cfg)
}

// GetAllLanguages returns the full list of supported languages.
func (a *App) GetAllLanguages() []string {
	return config.AllLanguages
}

// GetConfigPath returns the path to the config file.
func (a *App) GetConfigPath() string {
	if a.cfg.ConfigDir == "" {
		return ""
	}
	return filepath.Join(a.cfg.ConfigDir, config.ProjectConfigFile)
}

// DetectAll runs auto-detection for LLM provider, languages, and service availability.
func (a *App) DetectAll() *DetectionResult {
	provider, hint := config.DetectLLMProvider()

	// Detect languages from all configured repositories.
	var langs []string
	langSet := make(map[string]bool)
	for _, repo := range a.cfg.Repositories {
		for _, l := range config.DetectLanguages(repo.Path) {
			if !langSet[l] {
				langSet[l] = true
				langs = append(langs, l)
			}
		}
	}

	result := &DetectionResult{
		LLMProvider:  provider,
		LLMHint:      hint,
		Languages:    langs,
		OllamaStatus: "unknown",
		ClaudeCLI:    internalllm.FindClaudeCLI() != "",
		VertexAI:     os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") != "" || os.Getenv("GOOGLE_CLOUD_PROJECT") != "",
		AnthropicKey: os.Getenv("ANTHROPIC_API_KEY") != "",
	}

	// Check Ollama availability.
	if status := checkOllama("http://localhost:11434"); status.Available {
		result.OllamaStatus = "available"
	} else {
		result.OllamaStatus = "unavailable"
	}

	return result
}

// DetectLanguages detects programming languages in the given directory.
func (a *App) DetectLanguages(path string) []string {
	if path == "" {
		return nil
	}
	return config.DetectLanguages(path)
}

// BrowseDirectory opens a native OS directory picker dialog.
func (a *App) BrowseDirectory(title string) string {
	dir, err := wailsRuntime.OpenDirectoryDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: title,
	})
	if err != nil {
		return ""
	}
	return dir
}

// BrowseFile opens a native OS file picker dialog.
func (a *App) BrowseFile(title string, filter string) string {
	var filters []wailsRuntime.FileFilter
	if filter != "" {
		filters = []wailsRuntime.FileFilter{
			{DisplayName: filter, Pattern: filter},
		}
	}
	file, err := wailsRuntime.OpenFileDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title:   title,
		Filters: filters,
	})
	if err != nil {
		return ""
	}
	return file
}

// TestLLMConnection tests connectivity to the specified LLM provider.
func (a *App) TestLLMConnection(provider, model, project, location, credFile, baseURL string) *ServiceStatus {
	if provider == "claude-cli" {
		path := internalllm.FindClaudeCLI()
		if path == "" {
			return &ServiceStatus{Available: false, Message: "Claude Code CLI not found in PATH"}
		}
		return &ServiceStatus{Available: true, Message: fmt.Sprintf("Claude CLI found: %s", path)}
	}

	cfg := llm.Config{
		Provider:        provider,
		Model:           model,
		Project:         project,
		Location:        location,
		CredentialsFile: credFile,
		BaseURL:         baseURL,
		APIKey:          os.Getenv("ANTHROPIC_API_KEY"),
	}

	client, err := llm.NewClient(cfg)
	if err != nil {
		return &ServiceStatus{Available: false, Message: fmt.Sprintf("Failed to create client: %v", err)}
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = client.Chat(ctx, "Respond with exactly: OK", []llm.Message{
		{Role: "user", Content: "Test connection"},
	})
	if err != nil {
		return &ServiceStatus{Available: false, Message: fmt.Sprintf("Connection failed: %v", err)}
	}

	return &ServiceStatus{Available: true, Message: fmt.Sprintf("Connected (%s, model: %s)", provider, client.Model())}
}

// TestOllamaConnection checks if Ollama is reachable at the given base URL.
func (a *App) TestOllamaConnection(baseURL string) *ServiceStatus {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return checkOllama(baseURL)
}

// ValidatePath checks if a filesystem path exists and is a directory or file as expected.
func (a *App) ValidatePath(path string, expectDir bool) *ServiceStatus {
	if path == "" {
		return &ServiceStatus{Available: false, Message: "Path is empty"}
	}

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ServiceStatus{Available: false, Message: "Path does not exist"}
		}
		return &ServiceStatus{Available: false, Message: fmt.Sprintf("Cannot access: %v", err)}
	}

	if expectDir && !info.IsDir() {
		return &ServiceStatus{Available: false, Message: "Path is a file, expected a directory"}
	}
	if !expectDir && info.IsDir() {
		return &ServiceStatus{Available: false, Message: "Path is a directory, expected a file"}
	}

	return &ServiceStatus{Available: true, Message: "OK"}
}

// PreviewConfigChanges compares the proposed config against the current config
// and returns a list of differences.
func (a *App) PreviewConfigChanges(proposed ConfigDTO) []ConfigDiff {
	current := configToDTO(a.cfg)
	var diffs []ConfigDiff

	// Project
	if current.Project.Name != proposed.Project.Name {
		diffs = append(diffs, ConfigDiff{"Project", "Name", current.Project.Name, proposed.Project.Name})
	}

	// Repositories
	curRepos := formatRepos(current.Repositories)
	propRepos := formatRepos(proposed.Repositories)
	if curRepos != propRepos {
		diffs = append(diffs, ConfigDiff{"Repositories", "List", curRepos, propRepos})
	}

	// Languages
	curLangs := strings.Join(current.Languages, ", ")
	propLangs := strings.Join(proposed.Languages, ", ")
	if curLangs != propLangs {
		diffs = append(diffs, ConfigDiff{"Languages", "Selected", curLangs, propLangs})
	}

	// LLM
	if current.Agents.LLMProvider != proposed.Agents.LLMProvider {
		diffs = append(diffs, ConfigDiff{"AI", "LLM Provider", current.Agents.LLMProvider, proposed.Agents.LLMProvider})
	}
	if current.Agents.Model != proposed.Agents.Model {
		diffs = append(diffs, ConfigDiff{"AI", "Model", current.Agents.Model, proposed.Agents.Model})
	}
	if current.Agents.Project != proposed.Agents.Project {
		diffs = append(diffs, ConfigDiff{"AI", "GCP Project", current.Agents.Project, proposed.Agents.Project})
	}
	if current.Agents.Location != proposed.Agents.Location {
		diffs = append(diffs, ConfigDiff{"AI", "GCP Region", current.Agents.Location, proposed.Agents.Location})
	}
	if current.Agents.CredentialsFile != proposed.Agents.CredentialsFile {
		diffs = append(diffs, ConfigDiff{"AI", "Credentials File", current.Agents.CredentialsFile, proposed.Agents.CredentialsFile})
	}
	if current.Agents.BaseURL != proposed.Agents.BaseURL {
		diffs = append(diffs, ConfigDiff{"AI", "Base URL", current.Agents.BaseURL, proposed.Agents.BaseURL})
	}
	if current.Agents.AutoLink != proposed.Agents.AutoLink {
		diffs = append(diffs, ConfigDiff{"AI", "Auto-Link", boolStr(current.Agents.AutoLink), boolStr(proposed.Agents.AutoLink)})
	}
	if current.Agents.AutoSummarize != proposed.Agents.AutoSummarize {
		diffs = append(diffs, ConfigDiff{"AI", "Auto-Summarize", boolStr(current.Agents.AutoSummarize), boolStr(proposed.Agents.AutoSummarize)})
	}
	if current.Agents.EmbeddingProvider != proposed.Agents.EmbeddingProvider {
		diffs = append(diffs, ConfigDiff{"AI", "Embedding Provider", current.Agents.EmbeddingProvider, proposed.Agents.EmbeddingProvider})
	}
	if current.Agents.EmbeddingModel != proposed.Agents.EmbeddingModel {
		diffs = append(diffs, ConfigDiff{"AI", "Embedding Model", current.Agents.EmbeddingModel, proposed.Agents.EmbeddingModel})
	}

	// Docs
	if current.Docs.Provider != proposed.Docs.Provider {
		diffs = append(diffs, ConfigDiff{"Documents", "Provider", current.Docs.Provider, proposed.Docs.Provider})
	}
	if current.Docs.Model != proposed.Docs.Model {
		diffs = append(diffs, ConfigDiff{"Documents", "Model", current.Docs.Model, proposed.Docs.Model})
	}
	if current.Docs.MaxImageRes != proposed.Docs.MaxImageRes {
		diffs = append(diffs, ConfigDiff{"Documents", "Max Image Resolution", fmt.Sprint(current.Docs.MaxImageRes), fmt.Sprint(proposed.Docs.MaxImageRes)})
	}
	if current.Docs.ContextWindow != proposed.Docs.ContextWindow {
		diffs = append(diffs, ConfigDiff{"Documents", "Context Window", fmt.Sprint(current.Docs.ContextWindow), fmt.Sprint(proposed.Docs.ContextWindow)})
	}
	if current.Docs.DisableThinking != proposed.Docs.DisableThinking {
		diffs = append(diffs, ConfigDiff{"Documents", "Disable Thinking", boolStr(current.Docs.DisableThinking), boolStr(proposed.Docs.DisableThinking)})
	}
	curExts := strings.Join(current.Docs.ExcludeExtensions, ", ")
	propExts := strings.Join(proposed.Docs.ExcludeExtensions, ", ")
	if curExts != propExts {
		diffs = append(diffs, ConfigDiff{"Documents", "Exclude Extensions", curExts, propExts})
	}

	// Faces
	if current.Docs.Faces.Enabled != proposed.Docs.Faces.Enabled {
		diffs = append(diffs, ConfigDiff{"Face Detection", "Enabled", boolStr(current.Docs.Faces.Enabled), boolStr(proposed.Docs.Faces.Enabled)})
	}
	if current.Docs.Faces.ModelDir != proposed.Docs.Faces.ModelDir {
		diffs = append(diffs, ConfigDiff{"Face Detection", "Model Dir", current.Docs.Faces.ModelDir, proposed.Docs.Faces.ModelDir})
	}
	if current.Docs.Faces.MinFaceSize != proposed.Docs.Faces.MinFaceSize {
		diffs = append(diffs, ConfigDiff{"Face Detection", "Min Face Size", fmt.Sprint(current.Docs.Faces.MinFaceSize), fmt.Sprint(proposed.Docs.Faces.MinFaceSize)})
	}
	if current.Docs.Faces.SimilarityThreshold != proposed.Docs.Faces.SimilarityThreshold {
		diffs = append(diffs, ConfigDiff{"Face Detection", "Similarity Threshold", fmt.Sprintf("%.2f", current.Docs.Faces.SimilarityThreshold), fmt.Sprintf("%.2f", proposed.Docs.Faces.SimilarityThreshold)})
	}
	if current.Docs.Faces.ConfidenceThreshold != proposed.Docs.Faces.ConfidenceThreshold {
		diffs = append(diffs, ConfigDiff{"Face Detection", "Confidence Threshold", fmt.Sprintf("%.2f", current.Docs.Faces.ConfidenceThreshold), fmt.Sprintf("%.2f", proposed.Docs.Faces.ConfidenceThreshold)})
	}
	if current.Docs.Faces.ObjectDetection != proposed.Docs.Faces.ObjectDetection {
		diffs = append(diffs, ConfigDiff{"Face Detection", "Object Detection", boolStr(current.Docs.Faces.ObjectDetection), boolStr(proposed.Docs.Faces.ObjectDetection)})
	}

	// Watch
	curWatch := strings.Join(current.Watch.Exclude, ", ")
	propWatch := strings.Join(proposed.Watch.Exclude, ", ")
	if curWatch != propWatch {
		diffs = append(diffs, ConfigDiff{"Advanced", "Watch Exclude Patterns", curWatch, propWatch})
	}

	return diffs
}

// SaveConfig validates and saves the proposed configuration to disk.
func (a *App) SaveConfig(proposed ConfigDTO) *SaveResult {
	if a.cfg.ConfigDir == "" {
		return &SaveResult{Success: false, Message: "No project config found; run 'codeeagle init' first"}
	}

	// Convert DTO back to config.
	dtoToConfig(a.cfg, &proposed)

	// Validate.
	if err := a.cfg.Validate(); err != nil {
		return &SaveResult{Success: false, Message: fmt.Sprintf("Validation failed: %v", err)}
	}

	// Write to disk.
	configPath := filepath.Join(a.cfg.ConfigDir, config.ProjectConfigFile)
	if err := config.WriteConfig(a.cfg, configPath); err != nil {
		return &SaveResult{Success: false, Message: fmt.Sprintf("Failed to save: %v", err)}
	}

	return &SaveResult{Success: true, Message: "Configuration saved", Path: configPath}
}

// configToDTO converts internal config to a frontend-friendly DTO.
func configToDTO(cfg *config.Config) *ConfigDTO {
	repos := make([]RepositoryDTO, len(cfg.Repositories))
	for i, r := range cfg.Repositories {
		t := r.Type
		if t == "" {
			t = "single"
		}
		repos[i] = RepositoryDTO{Path: r.Path, Type: t}
	}

	return &ConfigDTO{
		Project:      ProjectDTO{Name: cfg.Project.Name},
		Repositories: repos,
		Watch:        WatchDTO{Exclude: cfg.Watch.Exclude},
		Languages:    cfg.Languages,
		Agents: AgentsDTO{
			LLMProvider:       cfg.Agents.LLMProvider,
			Model:             cfg.Agents.Model,
			Project:           cfg.Agents.Project,
			Location:          cfg.Agents.Location,
			CredentialsFile:   cfg.Agents.CredentialsFile,
			BaseURL:           cfg.Agents.BaseURL,
			AutoSummarize:     cfg.Agents.AutoSummarize,
			AutoLink:          cfg.Agents.AutoLink,
			EmbeddingProvider: cfg.Agents.EmbeddingProvider,
			EmbeddingModel:    cfg.Agents.EmbeddingModel,
		},
		Docs: DocsDTO{
			Provider:          cfg.Docs.Provider,
			Model:             cfg.Docs.Model,
			Project:           cfg.Docs.Project,
			Location:          cfg.Docs.Location,
			CredentialsFile:   cfg.Docs.CredentialsFile,
			BaseURL:           cfg.Docs.BaseURL,
			MaxImageRes:       cfg.Docs.MaxImageRes,
			ContextWindow:     cfg.Docs.ContextWindow,
			DisableThinking:   cfg.Docs.DisableThinking,
			ExcludeExtensions: cfg.Docs.ExcludeExtensions,
			Faces: FacesDTO{
				Enabled:             cfg.Docs.Faces.Enabled,
				ModelDir:            cfg.Docs.Faces.ModelDir,
				MinFaceSize:         cfg.Docs.Faces.MinFaceSize,
				SimilarityThreshold: cfg.Docs.Faces.SimilarityThreshold,
				ConfidenceThreshold: cfg.Docs.Faces.ConfidenceThreshold,
				ObjectDetection:     cfg.Docs.Faces.ObjectDetection,
				ObjectConfidence:    cfg.Docs.Faces.ObjectConfidence,
			},
		},
	}
}

// dtoToConfig updates the internal config from a DTO.
func dtoToConfig(cfg *config.Config, dto *ConfigDTO) {
	cfg.Project.Name = dto.Project.Name

	repos := make([]config.RepositoryConfig, len(dto.Repositories))
	for i, r := range dto.Repositories {
		repos[i] = config.RepositoryConfig{Path: r.Path, Type: r.Type}
	}
	cfg.Repositories = repos

	cfg.Watch.Exclude = dto.Watch.Exclude
	cfg.Languages = dto.Languages

	cfg.Agents.LLMProvider = dto.Agents.LLMProvider
	cfg.Agents.Model = dto.Agents.Model
	cfg.Agents.Project = dto.Agents.Project
	cfg.Agents.Location = dto.Agents.Location
	cfg.Agents.CredentialsFile = dto.Agents.CredentialsFile
	cfg.Agents.BaseURL = dto.Agents.BaseURL
	cfg.Agents.AutoSummarize = dto.Agents.AutoSummarize
	cfg.Agents.AutoLink = dto.Agents.AutoLink
	cfg.Agents.EmbeddingProvider = dto.Agents.EmbeddingProvider
	cfg.Agents.EmbeddingModel = dto.Agents.EmbeddingModel

	cfg.Docs.Provider = dto.Docs.Provider
	cfg.Docs.Model = dto.Docs.Model
	cfg.Docs.Project = dto.Docs.Project
	cfg.Docs.Location = dto.Docs.Location
	cfg.Docs.CredentialsFile = dto.Docs.CredentialsFile
	cfg.Docs.BaseURL = dto.Docs.BaseURL
	cfg.Docs.MaxImageRes = dto.Docs.MaxImageRes
	cfg.Docs.ContextWindow = dto.Docs.ContextWindow
	cfg.Docs.DisableThinking = dto.Docs.DisableThinking
	cfg.Docs.ExcludeExtensions = dto.Docs.ExcludeExtensions

	cfg.Docs.Faces.Enabled = dto.Docs.Faces.Enabled
	cfg.Docs.Faces.ModelDir = dto.Docs.Faces.ModelDir
	cfg.Docs.Faces.MinFaceSize = dto.Docs.Faces.MinFaceSize
	cfg.Docs.Faces.SimilarityThreshold = dto.Docs.Faces.SimilarityThreshold
	cfg.Docs.Faces.ConfidenceThreshold = dto.Docs.Faces.ConfidenceThreshold
	cfg.Docs.Faces.ObjectDetection = dto.Docs.Faces.ObjectDetection
	cfg.Docs.Faces.ObjectConfidence = dto.Docs.Faces.ObjectConfidence
}

// checkOllama tests Ollama connectivity at the given base URL.
func checkOllama(baseURL string) *ServiceStatus {
	client := &http.Client{Timeout: 3 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/tags", nil)
	if err != nil {
		return &ServiceStatus{Available: false, Message: fmt.Sprintf("Invalid URL: %v", err)}
	}

	resp, err := client.Do(req)
	if err != nil {
		return &ServiceStatus{Available: false, Message: "Ollama is not running"}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &ServiceStatus{Available: false, Message: fmt.Sprintf("Ollama returned status %d", resp.StatusCode)}
	}

	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return &ServiceStatus{Available: false, Message: "Failed to parse Ollama response"}
	}

	models := make([]string, len(tags.Models))
	for i, m := range tags.Models {
		models[i] = m.Name
	}

	msg := fmt.Sprintf("Ollama running (%d models)", len(models))
	if len(models) > 0 && len(models) <= 5 {
		msg = fmt.Sprintf("Ollama running: %s", strings.Join(models, ", "))
	}

	return &ServiceStatus{Available: true, Message: msg}
}

// formatRepos formats repositories for diff display.
func formatRepos(repos []RepositoryDTO) string {
	parts := make([]string, len(repos))
	for i, r := range repos {
		parts[i] = fmt.Sprintf("%s (%s)", r.Path, r.Type)
	}
	return strings.Join(parts, "; ")
}

// boolStr converts a bool to a display string.
func boolStr(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}
