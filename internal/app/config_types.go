//go:build app

package app

// ConfigDTO is the JSON-friendly representation of the full project config
// for the frontend Settings page.
type ConfigDTO struct {
	Project      ProjectDTO      `json:"project"`
	Repositories []RepositoryDTO `json:"repositories"`
	Watch        WatchDTO        `json:"watch"`
	Languages    []string        `json:"languages"`
	Agents       AgentsDTO       `json:"agents"`
	Docs         DocsDTO         `json:"docs"`
}

// ProjectDTO holds project metadata.
type ProjectDTO struct {
	Name string `json:"name"`
}

// RepositoryDTO describes a repository entry.
type RepositoryDTO struct {
	Path string `json:"path"`
	Type string `json:"type"` // "single" or "monorepo"
}

// WatchDTO holds watch exclusion patterns.
type WatchDTO struct {
	Exclude []string `json:"exclude"`
}

// AgentsDTO holds LLM and embedding configuration.
type AgentsDTO struct {
	LLMProvider       string `json:"llm_provider"`
	Model             string `json:"model"`
	Project           string `json:"project"`
	Location          string `json:"location"`
	CredentialsFile   string `json:"credentials_file"`
	BaseURL           string `json:"base_url"`
	AutoSummarize     bool   `json:"auto_summarize"`
	AutoLink          bool   `json:"auto_link"`
	EmbeddingProvider string `json:"embedding_provider"`
	EmbeddingModel    string `json:"embedding_model"`
}

// DocsDTO holds non-code file indexing configuration.
type DocsDTO struct {
	Provider          string   `json:"provider"`
	Model             string   `json:"model"`
	Project           string   `json:"project"`
	Location          string   `json:"location"`
	CredentialsFile   string   `json:"credentials_file"`
	BaseURL           string   `json:"base_url"`
	MaxImageRes       int      `json:"max_image_resolution"`
	ContextWindow     int      `json:"context_window"`
	DisableThinking   bool     `json:"disable_thinking"`
	ExcludeExtensions []string `json:"exclude_extensions"`
	Faces             FacesDTO `json:"faces"`
}

// FacesDTO holds face detection configuration.
type FacesDTO struct {
	Enabled             bool    `json:"enabled"`
	ModelDir            string  `json:"model_dir"`
	MinFaceSize         int     `json:"min_face_size"`
	SimilarityThreshold float64 `json:"similarity_threshold"`
	ConfidenceThreshold float64 `json:"confidence_threshold"`
	ObjectDetection     bool    `json:"object_detection"`
	ObjectConfidence    float64 `json:"object_confidence"`
}

// DetectionResult holds auto-detected service availability and settings.
type DetectionResult struct {
	LLMProvider  string   `json:"llm_provider"`
	LLMHint      string   `json:"llm_hint"`
	Languages    []string `json:"languages"`
	OllamaStatus string   `json:"ollama_status"` // "available", "unavailable", "unknown"
	ClaudeCLI    bool     `json:"claude_cli"`    // true if Claude Code CLI is found
	VertexAI     bool     `json:"vertex_ai"`     // true if GCP credentials are set
	AnthropicKey bool     `json:"anthropic_key"` // true if ANTHROPIC_API_KEY is set
}

// ServiceStatus holds the result of a connection test.
type ServiceStatus struct {
	Available bool   `json:"available"`
	Message   string `json:"message"`
}

// ConfigDiff describes a single field change between current and proposed config.
type ConfigDiff struct {
	Section  string `json:"section"`
	Field    string `json:"field"`
	OldValue string `json:"old_value"`
	NewValue string `json:"new_value"`
}

// SaveResult holds the result of saving configuration.
type SaveResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Path    string `json:"path"`
}
