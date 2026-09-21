// Package config handles configuration loading and validation for CodeEagle.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
	"go.yaml.in/yaml/v3"
)

const (
	// ProjectDirName is the per-project configuration directory name.
	ProjectDirName = ".CodeEagle"
	// HomeDirName is the user-level config directory name (lives in ~/).
	HomeDirName = ".CodeEagle"
	// ProjectConfigFile is the config filename inside the project dir.
	ProjectConfigFile = "config.yaml"
	// DefaultDBDir is the default database directory name inside the project dir.
	DefaultDBDir = "graph.db"
	// ProjectConfFile is the per-project conf file committed to git (lives at project root).
	ProjectConfFile = ".CodeEagle.conf"
)

// ProjectConf holds the contents of the .CodeEagle.conf file (committed to git).
type ProjectConf struct {
	// ExportFile is the relative path to the graph export file.
	ExportFile string `yaml:"export_file"`
}

// DocsConfig holds configuration for non-code file indexing (docs LLM).
type DocsConfig struct {
	// Provider is the docs LLM provider ("ollama", "vertex-ai", "baseten").
	Provider string `mapstructure:"provider" yaml:"provider,omitempty"`
	// APIKey authenticates against a hosted docs provider (Baseten). Left
	// empty, a Baseten provider falls back to transcripts.baseten_api_key,
	// so one key need not be written twice.
	APIKey string `mapstructure:"api_key" yaml:"api_key,omitempty"`
	// Model is the multimodal model name (e.g., "qwen3.5:9b", "gemini-3.8-flash").
	Model string `mapstructure:"model" yaml:"model,omitempty"`
	// Project is the GCP project ID (for Vertex AI).
	Project string `mapstructure:"project" yaml:"project,omitempty"`
	// Location is the GCP region (for Vertex AI).
	Location string `mapstructure:"location" yaml:"location,omitempty"`
	// CredentialsFile is the path to a GCP service account credentials JSON file.
	CredentialsFile string `mapstructure:"credentials_file" yaml:"credentials_file,omitempty"`
	// BaseURL is the base URL for the docs LLM provider API (e.g., Ollama endpoint).
	BaseURL string `mapstructure:"base_url" yaml:"base_url,omitempty"`
	// MaxImageRes is the maximum image resolution (longest edge in pixels) before LLM processing.
	MaxImageRes int `mapstructure:"max_image_resolution" yaml:"max_image_resolution,omitempty"`
	// ContextWindow is the Ollama num_ctx value. Default: docs.DefaultContextWindow (120000).
	ContextWindow int `mapstructure:"context_window" yaml:"context_window,omitempty"`
	// DisableThinking appends /no_think to prompts (saves tokens, may reduce quality).
	DisableThinking bool `mapstructure:"disable_thinking" yaml:"disable_thinking,omitempty"`
	// ExcludeExtensions lists file extensions to never index as non-code files.
	ExcludeExtensions []string `mapstructure:"exclude_extensions" yaml:"exclude_extensions,omitempty"`
	// Faces contains face detection configuration.
	Faces FacesConfig `mapstructure:"faces" yaml:"faces,omitempty"`
}

// FacesConfig holds face detection and recognition configuration.
type FacesConfig struct {
	// Enabled enables OpenCV face detection + object detection.
	Enabled bool `mapstructure:"enabled" yaml:"enabled,omitempty"`
	// ModelDir is the directory for ONNX model files (auto-downloaded).
	ModelDir string `mapstructure:"model_dir" yaml:"model_dir,omitempty"`
	// MinFaceSize is the minimum face size in pixels.
	MinFaceSize int `mapstructure:"min_face_size" yaml:"min_face_size,omitempty"`
	// SimilarityThreshold is the cosine similarity threshold for clustering (default 0.30).
	SimilarityThreshold float64 `mapstructure:"similarity_threshold" yaml:"similarity_threshold,omitempty"`
	// ConfidenceThreshold is the minimum detection confidence for faces.
	ConfidenceThreshold float64 `mapstructure:"confidence_threshold" yaml:"confidence_threshold,omitempty"`
	// ObjectDetection enables YOLO object detection (labels → topics).
	ObjectDetection bool `mapstructure:"object_detection" yaml:"object_detection,omitempty"`
	// ObjectConfidence is the minimum confidence for object labels.
	ObjectConfidence float64 `mapstructure:"object_confidence" yaml:"object_confidence,omitempty"`
	// CheckpointClusters is the number of new clusters that triggers a checkpoint pause (default 10).
	CheckpointClusters int `mapstructure:"checkpoint_clusters" yaml:"checkpoint_clusters,omitempty"`
	// AutoAcceptThreshold is the KNN confidence above which faces are auto-assigned (default 0.55).
	AutoAcceptThreshold float64 `mapstructure:"auto_accept_threshold" yaml:"auto_accept_threshold,omitempty"`
	// RejectThreshold is the KNN confidence below which classifications are discarded (default 0.30).
	RejectThreshold float64 `mapstructure:"reject_threshold" yaml:"reject_threshold,omitempty"`
	// ClassifyK is the K value for KNN classification (default 7).
	ClassifyK int `mapstructure:"classify_k" yaml:"classify_k,omitempty"`
	// MaxExemplarsPerEvent caps exemplars per person per event (default 10).
	MaxExemplarsPerEvent int `mapstructure:"max_exemplars_per_event" yaml:"max_exemplars_per_event,omitempty"`
	// ConfidenceDecayWarning is the per-year confidence decay factor (default 0.10).
	ConfidenceDecayWarning float64 `mapstructure:"confidence_decay_warning" yaml:"confidence_decay_warning,omitempty"`
}

// TranscriptsConfig holds meeting transcript indexing configuration.
type TranscriptsConfig struct {
	// Enabled turns on meeting transcript indexing.
	Enabled bool `mapstructure:"enabled" yaml:"enabled,omitempty"`
	// SessionsDir lists the directories to search for transcripts.
	//
	// Recordings accumulate in more than one place — a recorder's folder, a
	// downloads folder, a shared drive — so this takes either a single path or
	// a list, under the one key:
	//
	//	sessions_dir: ~/.local/share/tomoe/sessions
	//	sessions_dir: [~/.local/share/tomoe/sessions, ~/Downloads]
	//
	// Transcripts that turn up during ordinary document indexing are found
	// without being listed here at all.
	SessionsDir []string `mapstructure:"sessions_dir" yaml:"sessions_dir,omitempty"`
	// Owner is the person whose microphone made these recordings. Microphone
	// audio is always this person, which anchors identity resolution.
	Owner string `mapstructure:"owner" yaml:"owner,omitempty"`
	// OwnerAliases lists other spellings of the owner's name, including ones
	// speech recognition produces (e.g. "Imron" for "Imran").
	OwnerAliases []string `mapstructure:"owner_aliases" yaml:"owner_aliases,omitempty"`
	// Provider is the LLM provider used for enrichment ("baseten", "ollama",
	// "anthropic", "vertex-ai").
	Provider string `mapstructure:"provider" yaml:"provider,omitempty"`
	// Model is the model identifier for the chosen provider.
	Model string `mapstructure:"model" yaml:"model,omitempty"`
	// BaseURL overrides the provider endpoint.
	BaseURL string `mapstructure:"base_url" yaml:"base_url,omitempty"`
	// APIKey is the credential for whichever provider is configured, used
	// when no provider-named key matches it. Kept so configs written before
	// the provider-named settings existed keep working.
	APIKey string `mapstructure:"api_key" yaml:"api_key,omitempty"`
	// APIKeyEnv names an environment variable holding the credential.
	//
	// Deprecated: write `baseten_api_key: ${THE_VARIABLE}` instead. Any value
	// in the configuration expands, so a setting does not need its own `_env`
	// companion. Still read, so older configurations keep working.
	APIKeyEnv string `mapstructure:"api_key_env" yaml:"api_key_env,omitempty"`
	// APIKeyCommand is a command whose output is the credential.
	//
	// Deprecated: write `baseten_api_key: $(keyring get baseten.co you)`
	// instead. Any value expands, so a setting does not need its own
	// `_command` companion. Still read, so older configurations keep working.
	APIKeyCommand string `mapstructure:"api_key_command" yaml:"api_key_command,omitempty"`
	// BasetenAPIKey and AnthropicAPIKey are credentials named
	// after the service they belong to, so several can sit in one config and
	// changing `provider` does not mean moving a key to a differently-named
	// setting.
	//
	// The one matching `provider` is used. APIKey below is consulted only
	// when the matching one is absent, which is what keeps older configs
	// working. Vertex AI is not here because it authenticates with Google
	// application default credentials rather than a key — see `gcloud auth
	// application-default login` — and Ollama needs none at all.
	BasetenAPIKey   string `mapstructure:"baseten_api_key" yaml:"baseten_api_key,omitempty"`
	AnthropicAPIKey string `mapstructure:"anthropic_api_key" yaml:"anthropic_api_key,omitempty"`
	// JevAPIKey enables adjudicating speaker identity with the TypeSafe Jev
	// decision model instead of the language model.
	//
	// Optional. Without it identification runs as it always has. With it, the
	// confidence attached to an identification is calibrated against outcomes
	// rather than self-reported — which matters because MinConfidence decides
	// whether a speaker is written into the graph at all.
	//
	// Keep the key out of this file; the value is expanded at read time:
	//
	//	jev_api_key: ${JEV_API_KEY}
	//	jev_api_key: $(keyring get typesafe.ai me@example.com)
	JevAPIKey string `mapstructure:"jev_api_key" yaml:"jev_api_key,omitempty"`
	// JevModel pins the decision model version. Defaults to a pinned release
	// rather than a rolling alias, because a confidence threshold tuned
	// against one set of weights does not transfer silently to another.
	JevModel string `mapstructure:"jev_model" yaml:"jev_model,omitempty"`
	// BackgroundFilter screens out voices that are not people — a television,
	// a video being demonstrated, a stream left running nearby. Requires a
	// decision model; on by default when one is configured.
	BackgroundFilter *bool `mapstructure:"background_filter" yaml:"background_filter,omitempty"`
	// BackgroundMinConfidence is the bar for treating a voice as background
	// audio rather than a person. Zero uses the package default of 0.85, set
	// from reading every flag the model produced over the whole corpus.
	BackgroundMinConfidence float64 `mapstructure:"background_min_confidence" yaml:"background_min_confidence,omitempty"`
	// MinConfidence is the score at or above which a speaker is automatically
	// identified. Below it, the speaker is left for manual review.
	MinConfidence float64 `mapstructure:"min_confidence" yaml:"min_confidence,omitempty"`
	// MaxTokens caps enrichment responses. It needs to be generous: reasoning
	// models spend this budget on internal deliberation before emitting any
	// answer, and too small a cap yields an empty reply rather than a short one.
	MaxTokens int `mapstructure:"max_tokens" yaml:"max_tokens,omitempty"`
	// ContextWindow is how much context the model is given, in tokens. It
	// matters for a locally served model: Ollama defaults to a small window and
	// silently drops whatever does not fit, which summarizes a long meeting
	// from its opening minutes.
	ContextWindow int `mapstructure:"context_window" yaml:"context_window,omitempty"`
	// ReasoningEffort budgets a reasoning model's deliberation ("low",
	// "medium", "high"). Low measurably reduces cost on meeting transcripts
	// without hurting identification quality; switching reasoning off entirely
	// does hurt it, so that is only used as a fallback.
	ReasoningEffort string `mapstructure:"reasoning_effort" yaml:"reasoning_effort,omitempty"`
	// Concurrency is how many sessions are enriched in parallel.
	Concurrency int `mapstructure:"concurrency" yaml:"concurrency,omitempty"`
	// Roster lists people known to attend these meetings. Supplying it
	// markedly improves identification: it turns an open-ended guess into a
	// choice among known colleagues and fixes the spelling of their names.
	Roster []string `mapstructure:"roster" yaml:"roster,omitempty"`
	// ExcludeNames lists terms never to treat as people — product and team
	// names that otherwise look like names in conversation.
	ExcludeNames []string `mapstructure:"exclude_names" yaml:"exclude_names,omitempty"`
}

// QueueConfig holds enrichment queue configuration.
type QueueConfig struct {
	// MaxWorkers is the maximum number of concurrent workers (0 = NumCPU/2).
	MaxWorkers int `mapstructure:"max_workers" yaml:"max_workers,omitempty"`
	// TargetCPU is the target CPU percentage for auto-throttle (default 70).
	TargetCPU int `mapstructure:"target_cpu" yaml:"target_cpu,omitempty"`
	// RetryAttempts is the maximum number of retry attempts per job (default 3).
	RetryAttempts int `mapstructure:"retry_attempts" yaml:"retry_attempts,omitempty"`
}

// Config holds all configuration for CodeEagle.
type Config struct {
	// resolved remembers which values came from a ${VAR} or $(command)
	// reference and what each one resolved to.
	//
	// Loading expands references in place, so by the time anything reads a
	// credential it holds the secret rather than the expression that fetched
	// it. Writing the file back would then replace `$(keyring get ...)` with
	// the key itself — destroying the reference and committing the secret to
	// a file, which is the exact outcome the syntax exists to prevent. This
	// lets the write put the expression back.
	//
	// Unexported, so neither yaml nor mapstructure sees it and the reflection
	// walk skips it.
	resolved map[string]resolvedValue

	// Project contains project metadata.
	Project ProjectConfig `mapstructure:"project" yaml:"project"`
	// Repositories lists the repositories to index.
	Repositories []RepositoryConfig `mapstructure:"repositories" yaml:"repositories"`
	// Federate lists other CodeEagle directories to search alongside this
	// one, for `query` and `rag` only.
	//
	// A project's index holds its code; meetings and personal documents
	// usually live in the home configuration. Asking "what did we decide
	// about the retention job?" from inside a repository should find the
	// meeting, and this is what lets it.
	//
	// Listed explicitly rather than discovered by walking up the directory
	// tree. Discovery would make the same question answer differently
	// depending on where it was asked from, with nothing on screen explaining
	// why, and would be unbounded when run from / or a temporary directory.
	//
	//	federate:
	//	  - ~/.CodeEagle
	//
	// Reads only. Nothing is ever written outside the local index.
	Federate []string `mapstructure:"federate" yaml:"federate,omitempty"`
	// Watch contains file watching configuration.
	Watch WatchConfig `mapstructure:"watch" yaml:"watch"`
	// Languages lists the languages to parse.
	Languages []string `mapstructure:"languages" yaml:"languages"`
	// Graph contains knowledge graph storage configuration.
	Graph GraphConfig `mapstructure:"graph" yaml:"graph"`
	// Agents contains AI agent configuration.
	Agents AgentsConfig `mapstructure:"agents" yaml:"agents"`
	// Docs contains non-code file indexing configuration.
	Docs DocsConfig `mapstructure:"docs" yaml:"docs"`
	// Queue contains enrichment queue configuration.
	Queue QueueConfig `mapstructure:"queue" yaml:"queue,omitempty"`
	// Transcripts contains meeting transcript indexing configuration.
	Transcripts TranscriptsConfig `mapstructure:"transcripts" yaml:"transcripts,omitempty"`
	// ConfigDir is the resolved .CodeEagle directory path (not persisted in YAML).
	ConfigDir string `mapstructure:"-" yaml:"-"`
	// ProjectConf is the parsed .CodeEagle.conf if found (not persisted).
	ProjectConf *ProjectConf `mapstructure:"-" yaml:"-"`
	// ProjectConfDir is the directory containing .CodeEagle.conf (not persisted).
	ProjectConfDir string `mapstructure:"-" yaml:"-"`
}

// ProjectConfig holds project metadata.
type ProjectConfig struct {
	// Name is the project name.
	Name string `mapstructure:"name" yaml:"name"`
}

// RepositoryConfig describes a repository to index.
type RepositoryConfig struct {
	// Path is the filesystem path to the repository.
	Path string `mapstructure:"path" yaml:"path"`
	// Type is the repository type (monorepo or single).
	Type string `mapstructure:"type" yaml:"type"`
}

// WatchConfig holds file watching configuration.
type WatchConfig struct {
	// Exclude lists glob patterns to exclude from watching.
	Exclude []string `mapstructure:"exclude" yaml:"exclude"`
}

// GraphConfig holds knowledge graph storage configuration.
type GraphConfig struct {
	// Storage is the storage backend (embedded or neo4j).
	Storage string `mapstructure:"storage" yaml:"storage"`
	// Neo4jURI is the Neo4j connection URI (used when Storage is "neo4j").
	Neo4jURI string `mapstructure:"neo4j_uri" yaml:"neo4j_uri,omitempty"`
	// DBPath is the path to the graph database directory.
	DBPath string `mapstructure:"db_path" yaml:"db_path,omitempty"`
}

// AgentsConfig holds AI agent configuration.
type AgentsConfig struct {
	// LLMProvider is the LLM provider (anthropic, vertex-ai, openai, ollama).
	LLMProvider string `mapstructure:"llm_provider" yaml:"llm_provider"`
	// Model is the model identifier.
	Model string `mapstructure:"model" yaml:"model,omitempty"`
	// Project is the GCP project ID (used when LLMProvider is "vertex-ai").
	Project string `mapstructure:"project" yaml:"project,omitempty"`
	// Location is the GCP region (used when LLMProvider is "vertex-ai", e.g. "us-central1").
	Location string `mapstructure:"location" yaml:"location,omitempty"`
	// AutoSummarize enables LLM-based summarization after indexing.
	AutoSummarize bool `mapstructure:"auto_summarize" yaml:"auto_summarize"`
	// AutoLink enables LLM-assisted cross-service edge detection after static linking.
	AutoLink bool `mapstructure:"auto_link" yaml:"auto_link"`
	// APIKey is the credential for whichever provider is configured.
	//
	// Expanded like any other value, so it belongs in a keyring rather than
	// in the file: `api_key: $(keyring get anthropic.com you@example.com)`.
	// For Anthropic the ANTHROPIC_API_KEY environment variable is still read
	// when this is empty; for every other provider this is the only way to
	// supply one.
	APIKey string `mapstructure:"api_key" yaml:"api_key,omitempty"`
	// CredentialsFile is the path to a GCP service account credentials JSON file (for Vertex AI).
	CredentialsFile string `mapstructure:"credentials_file" yaml:"credentials_file,omitempty"`
	// BaseURL is the base URL for the LLM provider API (e.g. Ollama endpoint).
	BaseURL string `mapstructure:"base_url" yaml:"base_url,omitempty"`
	// EmbeddingProvider is the embedding provider ("ollama", "vertex-ai"). Empty means auto-detect.
	EmbeddingProvider string `mapstructure:"embedding_provider" yaml:"embedding_provider,omitempty"`
	// EmbeddingModel is the embedding model name. Empty means use provider default.
	EmbeddingModel string `mapstructure:"embedding_model" yaml:"embedding_model,omitempty"`
}

// HomeDir returns the path to the user-level CodeEagle directory (~/.CodeEagle/).
func HomeDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(homeDir, HomeDirName), nil
}

// EnsureHomeDir creates the user-level config directory if it doesn't exist.
func EnsureHomeDir() error {
	dir, err := HomeDir()
	if err != nil {
		return err
	}
	return os.MkdirAll(dir, 0755)
}

// DiscoverProjectDir walks up from startDir looking for a .CodeEagle/ directory.
// Returns the full path to the .CodeEagle/ directory if found, or empty string if not.
func DiscoverProjectDir(startDir string) string {
	dir := startDir
	for {
		candidate := filepath.Join(dir, ProjectDirName)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached filesystem root
		}
		dir = parent
	}
	return ""
}

// ResolveDBPath determines the graph database path using this priority:
//  1. flagValue (CLI --db-path flag) if non-empty
//  2. graph.db_path from config YAML if non-empty
//  3. <ConfigDir>/graph.db if ConfigDir is set
//  4. empty string (caller should handle)
func (c *Config) ResolveDBPath(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if c.Graph.DBPath != "" {
		return c.Graph.DBPath
	}
	if c.ConfigDir != "" {
		return filepath.Join(c.ConfigDir, DefaultDBDir)
	}
	return ""
}

// DiscoverProjectConf walks up from startDir looking for a .CodeEagle.conf file.
// Returns the conf file path, parsed conf, and any error.
func DiscoverProjectConf(startDir string) (confPath string, conf *ProjectConf, err error) {
	dir := startDir
	for {
		candidate := filepath.Join(dir, ProjectConfFile)
		if _, err := os.Stat(candidate); err == nil {
			data, err := os.ReadFile(candidate)
			if err != nil {
				return "", nil, fmt.Errorf("read %s: %w", candidate, err)
			}
			var pc ProjectConf
			if err := yaml.Unmarshal(data, &pc); err != nil {
				return "", nil, fmt.Errorf("parse %s: %w", candidate, err)
			}
			return candidate, &pc, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", nil, nil
}

// ExportFilePath resolves the export file path relative to the conf directory.
func ExportFilePath(confDir string, conf *ProjectConf) string {
	if conf == nil || conf.ExportFile == "" {
		return ""
	}
	return filepath.Join(confDir, conf.ExportFile)
}

// Load loads configuration from file, environment variables, and defaults.
// Search order:
//  1. --config flag (explicit path via global viper)
//  2. --project-name flag -> registry lookup
//  3. Walk up from CWD for .CodeEagle/config.yaml
//  4. Registry lookup by CWD path
func Load() (*Config, error) {
	v := viper.New()
	setDefaults(v)

	// Environment variables
	v.SetEnvPrefix("CODEEAGLE")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	var configDir string

	// 1. Check --config flag
	globalViper := viper.GetViper()
	if configFile := globalViper.GetString("config_file"); configFile != "" {
		v.SetConfigFile(configFile)
		// Derive configDir from the config file's directory if it's inside a .CodeEagle dir.
		cfgParent := filepath.Dir(configFile)
		if filepath.Base(cfgParent) == ProjectDirName {
			configDir = cfgParent
		}
	} else {
		// 2. Check --project-name flag -> registry lookup
		if projectName := globalViper.GetString("project_name"); projectName != "" {
			entries := ListProjects()
			for _, entry := range entries {
				if entry.Name == projectName {
					configDir = entry.ConfigDir
					configFile := filepath.Join(configDir, ProjectConfigFile)
					if _, err := os.Stat(configFile); err == nil {
						v.SetConfigFile(configFile)
					}
					break
				}
			}
		}

		// 3. Walk up from CWD for .CodeEagle/config.yaml
		if v.ConfigFileUsed() == "" {
			cwd, err := os.Getwd()
			if err == nil {
				if projDir := DiscoverProjectDir(cwd); projDir != "" {
					configDir = projDir
					configFile := filepath.Join(projDir, ProjectConfigFile)
					if _, err := os.Stat(configFile); err == nil {
						v.SetConfigFile(configFile)
					}
				}
			}
		}
	}

	// Read config file
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
		// 4. If still no config found, try registry lookup by CWD path
		if configDir == "" {
			cwd, err := os.Getwd()
			if err == nil {
				if entry, ok := LookupProject(cwd); ok {
					configDir = entry.ConfigDir
					configFile := filepath.Join(configDir, ProjectConfigFile)
					if _, err := os.Stat(configFile); err == nil {
						v.SetConfigFile(configFile)
						if err := v.ReadInConfig(); err != nil {
							return nil, fmt.Errorf("error reading config file: %w", err)
						}
					}
				}
			}
		}
	}

	// Load .env from the discovered .CodeEagle/ directory.
	if configDir != "" {
		loadEnvFile(filepath.Join(configDir, ".env"))
	}

	// Unmarshal into struct
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("error parsing config: %w", err)
	}

	// Resolve ${VAR} and $(command) references before anything reads a value,
	// so a credential can live in the environment or the system keyring
	// rather than in a file that gets committed.
	if err := expandConfig(&cfg); err != nil {
		return nil, fmt.Errorf("error expanding config: %w", err)
	}

	cfg.ConfigDir = configDir

	// Discover .CodeEagle.conf from CWD (or configDir parent).
	searchDir := ""
	if configDir != "" {
		searchDir = filepath.Dir(configDir)
	} else {
		searchDir, _ = os.Getwd()
	}
	if searchDir != "" {
		confPath, pc, err := DiscoverProjectConf(searchDir)
		if err == nil && pc != nil {
			cfg.ProjectConf = pc
			cfg.ProjectConfDir = filepath.Dir(confPath)
		}
	}

	return &cfg, nil
}

// Validate checks that the configuration is valid.
func (c *Config) Validate() error {
	if len(c.Repositories) == 0 {
		return fmt.Errorf("at least one repository must be configured")
	}

	for i, repo := range c.Repositories {
		if repo.Path == "" {
			return fmt.Errorf("repository %d: path is required", i)
		}
		if repo.Type != "" && repo.Type != "monorepo" && repo.Type != "single" {
			return fmt.Errorf("repository %d: type must be 'monorepo' or 'single', got %q", i, repo.Type)
		}
	}

	if c.Graph.Storage != "" && c.Graph.Storage != "embedded" && c.Graph.Storage != "neo4j" {
		return fmt.Errorf("graph storage must be 'embedded' or 'neo4j', got %q", c.Graph.Storage)
	}

	if c.Graph.Storage == "neo4j" && c.Graph.Neo4jURI == "" {
		return fmt.Errorf("neo4j_uri is required when graph storage is 'neo4j'")
	}

	return nil
}

// setDefaults sets default configuration values.
func setDefaults(v *viper.Viper) {
	v.SetDefault("project.name", "")

	v.SetDefault("watch.exclude", []string{
		"**/node_modules/**",
		"**/.git/**",
		"**/vendor/**",
		"**/__pycache__/**",
		"**/dist/**",
		"**/build/**",
	})

	v.SetDefault("languages", []string{
		"go",
		"python",
		"typescript",
		"javascript",
		"java",
		"html",
		"markdown",
	})

	v.SetDefault("graph.storage", "embedded")

	v.SetDefault("agents.llm_provider", "anthropic")
	// Deliberately unset: each provider supplies its own default, and a
	// provider-agnostic one is wrong for every provider but the first. A
	// Claude identifier handed to Gemini's API is a 404 that reads like the
	// model was withdrawn, which is the wrong thing to go looking for.
	v.SetDefault("agents.model", "")
	v.SetDefault("agents.auto_summarize", false)

	v.SetDefault("docs.max_image_resolution", 1024)
	v.SetDefault("docs.context_window", 120000) // Must match docs.DefaultContextWindow
	v.SetDefault("docs.exclude_extensions", []string{".lock", ".min.js", ".min.css", ".map", ".wasm", ".pb.go"})
	v.SetDefault("docs.faces.enabled", false)
	v.SetDefault("docs.faces.model_dir", "~/.codeeagle/models/")
	v.SetDefault("docs.faces.min_face_size", 40)
	v.SetDefault("docs.faces.similarity_threshold", 0.30)
	v.SetDefault("docs.faces.confidence_threshold", 0.7)
	v.SetDefault("docs.faces.object_detection", true)
	v.SetDefault("docs.faces.object_confidence", 0.5)

	v.SetDefault("transcripts.enabled", false)
	v.SetDefault("transcripts.provider", "baseten")
	v.SetDefault("transcripts.model", "deepseek-ai/DeepSeek-V4.1-Flash")
	v.SetDefault("transcripts.api_key_env", "BASETEN_API_KEY")
	v.SetDefault("transcripts.min_confidence", 0.70)
	v.SetDefault("transcripts.max_tokens", 65536)
	v.SetDefault("transcripts.reasoning_effort", "low")
	v.SetDefault("transcripts.concurrency", 4)
	v.SetDefault("docs.faces.checkpoint_clusters", 10)
	v.SetDefault("docs.faces.auto_accept_threshold", 0.55)
	v.SetDefault("docs.faces.reject_threshold", 0.30)
	v.SetDefault("docs.faces.classify_k", 7)
	v.SetDefault("docs.faces.max_exemplars_per_event", 10)
	v.SetDefault("docs.faces.confidence_decay_warning", 0.10)

	v.SetDefault("queue.max_workers", 0)
	v.SetDefault("queue.target_cpu", 70)
	v.SetDefault("queue.retry_attempts", 3)
}

// loadEnvFile reads a .env file and sets environment variables from it.
// Each line should be in KEY=VALUE format. Lines starting with # and blank lines are skipped.
// Values are not overridden if the environment variable is already set.
func loadEnvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return // file doesn't exist or can't be read; silently skip
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" {
			continue
		}
		// Only set if not already present in the environment.
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
}

// CredentialWarning describes a credential supplied through a setting that
// has been superseded, or "" when none is.
//
// There were four ways to give CodeEagle a key — a literal, a named
// environment variable, a command, and now expansion of any value — and the
// last does everything the middle two did, on every setting rather than only
// those given bespoke companions. Two ways to say one thing is how
// `sessions_dir` and `sessions_dirs` went wrong. The old settings still work;
// this is what says they need not be used.
//
// Reported only when a superseded setting actually supplies the credential,
// so a configuration that has moved on never hears about it.
func (c *TranscriptsConfig) CredentialWarning() string {
	provider := c.TranscriptProvider()
	named := map[string]string{
		"baseten":   c.BasetenAPIKey,
		"anthropic": c.AnthropicAPIKey,
	}
	if strings.TrimSpace(named[provider]) != "" {
		return ""
	}

	switch {
	case strings.TrimSpace(c.APIKeyCommand) != "":
		return fmt.Sprintf(
			"transcripts.api_key_command is superseded; write "+
				"%s_api_key: $(%s) instead", provider, c.APIKeyCommand)
	case strings.TrimSpace(c.APIKeyEnv) != "" && os.Getenv(c.APIKeyEnv) != "":
		return fmt.Sprintf(
			"transcripts.api_key_env is superseded; write "+
				"%s_api_key: ${%s} instead", provider, c.APIKeyEnv)
	case strings.TrimSpace(c.APIKey) != "":
		return fmt.Sprintf(
			"transcripts.api_key holds the credential for %s; "+
				"%s_api_key names what it is for", provider, provider)
	}
	return ""
}

// TranscriptProvider returns the configured provider, or the default.
func (c *TranscriptsConfig) TranscriptProvider() string {
	if p := strings.TrimSpace(c.Provider); p != "" {
		return p
	}
	return "baseten"
}

// ProviderSecret returns where to find the credential for the configured
// provider.
//
// A key named after its service is preferred, so switching provider is a
// one-line change rather than a rewrite of whichever setting happened to hold
// the old key, and several services can sit configured at once. The unnamed
// `api_key` is the fallback, for configs written before that was possible.
func (c *TranscriptsConfig) ProviderSecret() SecretSource {
	named := map[string]string{
		"baseten":   c.BasetenAPIKey,
		"anthropic": c.AnthropicAPIKey,
	}
	if key := strings.TrimSpace(named[c.TranscriptProvider()]); key != "" {
		return SecretSource{Literal: key}
	}
	return SecretSource{
		Literal: c.APIKey,
		EnvVar:  c.APIKeyEnv,
		Command: c.APIKeyCommand,
	}
}

// TranscriptDirs returns every directory to search for transcripts, with
// blanks and duplicates removed.
func (c *Config) TranscriptDirs() []string {
	var out []string
	seen := make(map[string]bool)
	for _, dir := range c.Transcripts.SessionsDir {
		dir = strings.TrimSpace(dir)
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
	}
	return out
}
