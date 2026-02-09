package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromFile(t *testing.T) {
	// Create a temp directory with a config file
	tmpDir := t.TempDir()
	configContent := `project:
  name: "test-project"

repositories:
  - path: /tmp/test-repo
    type: monorepo
  - path: /tmp/shared-lib
    type: single

watch:
  exclude:
    - "**/node_modules/**"
    - "**/.git/**"

languages:
  - go
  - python

graph:
  storage: embedded

agents:
  llm_provider: anthropic
  model: claude-sonnet-4-5-20250929
`
	configPath := filepath.Join(tmpDir, ".codeeagle.yaml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	// Change to the temp directory so viper finds the config
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	defer func() {
		if err := os.Chdir(origDir); err != nil {
			t.Errorf("failed to restore working directory: %v", err)
		}
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Project.Name != "test-project" {
		t.Errorf("Project.Name = %q, want %q", cfg.Project.Name, "test-project")
	}

	if len(cfg.Repositories) != 2 {
		t.Fatalf("len(Repositories) = %d, want 2", len(cfg.Repositories))
	}

	if cfg.Repositories[0].Path != "/tmp/test-repo" {
		t.Errorf("Repositories[0].Path = %q, want %q", cfg.Repositories[0].Path, "/tmp/test-repo")
	}
	if cfg.Repositories[0].Type != "monorepo" {
		t.Errorf("Repositories[0].Type = %q, want %q", cfg.Repositories[0].Type, "monorepo")
	}
	if cfg.Repositories[1].Path != "/tmp/shared-lib" {
		t.Errorf("Repositories[1].Path = %q, want %q", cfg.Repositories[1].Path, "/tmp/shared-lib")
	}

	if len(cfg.Languages) != 2 {
		t.Fatalf("len(Languages) = %d, want 2", len(cfg.Languages))
	}
	if cfg.Languages[0] != "go" {
		t.Errorf("Languages[0] = %q, want %q", cfg.Languages[0], "go")
	}

	if cfg.Graph.Storage != "embedded" {
		t.Errorf("Graph.Storage = %q, want %q", cfg.Graph.Storage, "embedded")
	}

	if cfg.Agents.LLMProvider != "anthropic" {
		t.Errorf("Agents.LLMProvider = %q, want %q", cfg.Agents.LLMProvider, "anthropic")
	}
	if cfg.Agents.Model != "claude-sonnet-4-5-20250929" {
		t.Errorf("Agents.Model = %q, want %q", cfg.Agents.Model, "claude-sonnet-4-5-20250929")
	}
}

func TestLoadDefaults(t *testing.T) {
	// Load from an empty temp directory (no config file)
	tmpDir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	defer func() {
		if err := os.Chdir(origDir); err != nil {
			t.Errorf("failed to restore working directory: %v", err)
		}
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Should have default languages
	if len(cfg.Languages) != 7 {
		t.Errorf("len(Languages) = %d, want 7 (defaults)", len(cfg.Languages))
	}

	// Should have default graph storage
	if cfg.Graph.Storage != "embedded" {
		t.Errorf("Graph.Storage = %q, want %q", cfg.Graph.Storage, "embedded")
	}

	// Should have default watch excludes
	if len(cfg.Watch.Exclude) != 6 {
		t.Errorf("len(Watch.Exclude) = %d, want 6 (defaults)", len(cfg.Watch.Exclude))
	}

	// Should have default agent config
	if cfg.Agents.LLMProvider != "anthropic" {
		t.Errorf("Agents.LLMProvider = %q, want %q", cfg.Agents.LLMProvider, "anthropic")
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
		errMsg  string
	}{
		{
			name:    "no repositories",
			cfg:     Config{},
			wantErr: true,
			errMsg:  "at least one repository must be configured",
		},
		{
			name: "empty repo path",
			cfg: Config{
				Repositories: []RepositoryConfig{{Path: "", Type: "single"}},
			},
			wantErr: true,
			errMsg:  "path is required",
		},
		{
			name: "invalid repo type",
			cfg: Config{
				Repositories: []RepositoryConfig{{Path: "/tmp/repo", Type: "invalid"}},
			},
			wantErr: true,
			errMsg:  "type must be",
		},
		{
			name: "invalid graph storage",
			cfg: Config{
				Repositories: []RepositoryConfig{{Path: "/tmp/repo", Type: "single"}},
				Graph:        GraphConfig{Storage: "invalid"},
			},
			wantErr: true,
			errMsg:  "graph storage must be",
		},
		{
			name: "neo4j without uri",
			cfg: Config{
				Repositories: []RepositoryConfig{{Path: "/tmp/repo", Type: "single"}},
				Graph:        GraphConfig{Storage: "neo4j"},
			},
			wantErr: true,
			errMsg:  "neo4j_uri is required",
		},
		{
			name: "valid config",
			cfg: Config{
				Repositories: []RepositoryConfig{{Path: "/tmp/repo", Type: "single"}},
				Graph:        GraphConfig{Storage: "embedded"},
			},
			wantErr: false,
		},
		{
			name: "valid neo4j config",
			cfg: Config{
				Repositories: []RepositoryConfig{{Path: "/tmp/repo"}},
				Graph:        GraphConfig{Storage: "neo4j", Neo4jURI: "bolt://localhost:7687"},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				if err == nil {
					t.Errorf("Validate() error = nil, want error containing %q", tt.errMsg)
				} else if tt.errMsg != "" {
					if got := err.Error(); !contains(got, tt.errMsg) {
						t.Errorf("Validate() error = %q, want error containing %q", got, tt.errMsg)
					}
				}
			} else {
				if err != nil {
					t.Errorf("Validate() unexpected error: %v", err)
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
