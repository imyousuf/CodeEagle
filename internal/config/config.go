// Package config handles configuration loading and validation for CodeEagle.
package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

const (
	// DefaultConfigFile is the default configuration file name (without extension).
	DefaultConfigFile = ".codeeagle"
	// DefaultConfigType is the default configuration file type.
	DefaultConfigType = "yaml"
)

// Config holds all configuration for CodeEagle.
type Config struct {
	// Project contains project metadata.
	Project ProjectConfig `mapstructure:"project"`
	// Repositories lists the repositories to index.
	Repositories []RepositoryConfig `mapstructure:"repositories"`
	// Watch contains file watching configuration.
	Watch WatchConfig `mapstructure:"watch"`
	// Languages lists the languages to parse.
	Languages []string `mapstructure:"languages"`
	// Graph contains knowledge graph storage configuration.
	Graph GraphConfig `mapstructure:"graph"`
	// Agents contains AI agent configuration.
	Agents AgentsConfig `mapstructure:"agents"`
}

// ProjectConfig holds project metadata.
type ProjectConfig struct {
	// Name is the project name.
	Name string `mapstructure:"name"`
}

// RepositoryConfig describes a repository to index.
type RepositoryConfig struct {
	// Path is the filesystem path to the repository.
	Path string `mapstructure:"path"`
	// Type is the repository type (monorepo or single).
	Type string `mapstructure:"type"`
}

// WatchConfig holds file watching configuration.
type WatchConfig struct {
	// Exclude lists glob patterns to exclude from watching.
	Exclude []string `mapstructure:"exclude"`
}

// GraphConfig holds knowledge graph storage configuration.
type GraphConfig struct {
	// Storage is the storage backend (embedded or neo4j).
	Storage string `mapstructure:"storage"`
	// Neo4jURI is the Neo4j connection URI (used when Storage is "neo4j").
	Neo4jURI string `mapstructure:"neo4j_uri"`
}

// AgentsConfig holds AI agent configuration.
type AgentsConfig struct {
	// LLMProvider is the LLM provider (anthropic, vertex-ai, openai, ollama).
	LLMProvider string `mapstructure:"llm_provider"`
	// Model is the model identifier.
	Model string `mapstructure:"model"`
	// Project is the GCP project ID (used when LLMProvider is "vertex-ai").
	Project string `mapstructure:"project"`
	// Location is the GCP region (used when LLMProvider is "vertex-ai", e.g. "us-central1").
	Location string `mapstructure:"location"`
	// AutoSummarize enables LLM-based summarization after indexing.
	AutoSummarize bool `mapstructure:"auto_summarize"`
}

// Load loads configuration from file, environment variables, and defaults.
func Load() (*Config, error) {
	v := viper.New()

	// Set defaults
	setDefaults(v)

	// Check if a specific config file was set via CLI flag (stored in global viper)
	globalViper := viper.GetViper()
	if configFile := globalViper.GetString("config_file"); configFile != "" {
		v.SetConfigFile(configFile)
	} else {
		// Config file settings for default paths
		v.SetConfigName(DefaultConfigFile)
		v.SetConfigType(DefaultConfigType)

		// Look for config in current directory
		v.AddConfigPath(".")
	}

	// Environment variables
	v.SetEnvPrefix("CODEEAGLE")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Read config file (ignore if not found)
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
	}

	// Unmarshal into struct
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("error parsing config: %w", err)
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
	v.SetDefault("agents.model", "claude-sonnet-4-5-20250929")
	v.SetDefault("agents.auto_summarize", false)
}
