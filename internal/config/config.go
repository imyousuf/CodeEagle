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
	// ConfigDir is the resolved .CodeEagle directory path (not persisted in YAML).
	ConfigDir string `mapstructure:"-"`
	// ProjectConf is the parsed .CodeEagle.conf if found (not persisted).
	ProjectConf *ProjectConf `mapstructure:"-"`
	// ProjectConfDir is the directory containing .CodeEagle.conf (not persisted).
	ProjectConfDir string `mapstructure:"-"`
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
	// DBPath is the path to the graph database directory.
	DBPath string `mapstructure:"db_path"`
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
	// AutoLink enables LLM-assisted cross-service edge detection after static linking.
	AutoLink bool `mapstructure:"auto_link"`
	// CredentialsFile is the path to a GCP service account credentials JSON file (for Vertex AI).
	CredentialsFile string `mapstructure:"credentials_file"`
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
	v.SetDefault("agents.model", "claude-sonnet-4-5-20250929")
	v.SetDefault("agents.auto_summarize", false)
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
