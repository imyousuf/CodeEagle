package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromFile(t *testing.T) {
	// Create a temp directory with .CodeEagle/config.yaml
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, ProjectDirName)
	if err := os.Mkdir(projectDir, 0755); err != nil {
		t.Fatalf("failed to create .CodeEagle dir: %v", err)
	}

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
	configPath := filepath.Join(projectDir, ProjectConfigFile)
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	// Change to the temp directory so Load() discovers .CodeEagle/config.yaml
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
	if cfg.ConfigDir != projectDir {
		t.Errorf("ConfigDir = %q, want %q", cfg.ConfigDir, projectDir)
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

func TestDiscoverProjectDir(t *testing.T) {
	// Create a hierarchy: tmpDir/sub1/sub2 with .CodeEagle/ at tmpDir level.
	tmpDir := t.TempDir()
	sub1 := filepath.Join(tmpDir, "sub1")
	sub2 := filepath.Join(sub1, "sub2")
	if err := os.MkdirAll(sub2, 0755); err != nil {
		t.Fatalf("create subdirs: %v", err)
	}
	projectDir := filepath.Join(tmpDir, ProjectDirName)
	if err := os.Mkdir(projectDir, 0755); err != nil {
		t.Fatalf("create .CodeEagle: %v", err)
	}

	// Discover from sub2 should find the .CodeEagle at tmpDir.
	got := DiscoverProjectDir(sub2)
	if got != projectDir {
		t.Errorf("DiscoverProjectDir(%q) = %q, want %q", sub2, got, projectDir)
	}

	// Discover from sub1 should also find it.
	got = DiscoverProjectDir(sub1)
	if got != projectDir {
		t.Errorf("DiscoverProjectDir(%q) = %q, want %q", sub1, got, projectDir)
	}

	// Discover from the root itself should find it.
	got = DiscoverProjectDir(tmpDir)
	if got != projectDir {
		t.Errorf("DiscoverProjectDir(%q) = %q, want %q", tmpDir, got, projectDir)
	}

	// Discover from a directory without .CodeEagle should return empty.
	isolatedDir := t.TempDir()
	got = DiscoverProjectDir(isolatedDir)
	if got != "" {
		t.Errorf("DiscoverProjectDir(%q) = %q, want empty", isolatedDir, got)
	}
}

func TestLoadFromProjectDir(t *testing.T) {
	// Create a project dir hierarchy with .CodeEagle/config.yaml.
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, ProjectDirName)
	if err := os.Mkdir(projectDir, 0755); err != nil {
		t.Fatalf("create .CodeEagle: %v", err)
	}

	configContent := `project:
  name: "proj-dir-test"

repositories:
  - path: /tmp/repo1
    type: single

graph:
  storage: embedded
  db_path: /custom/db/path
`
	if err := os.WriteFile(filepath.Join(projectDir, ProjectConfigFile), []byte(configContent), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// cd into a subdirectory
	subDir := filepath.Join(tmpDir, "deep", "sub")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("create subdirs: %v", err)
	}

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	if err := os.Chdir(subDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Project.Name != "proj-dir-test" {
		t.Errorf("Project.Name = %q, want %q", cfg.Project.Name, "proj-dir-test")
	}
	if cfg.ConfigDir != projectDir {
		t.Errorf("ConfigDir = %q, want %q", cfg.ConfigDir, projectDir)
	}
	if cfg.Graph.DBPath != "/custom/db/path" {
		t.Errorf("Graph.DBPath = %q, want %q", cfg.Graph.DBPath, "/custom/db/path")
	}
}

func TestResolveDBPath(t *testing.T) {
	tests := []struct {
		name      string
		cfg       Config
		flagValue string
		want      string
	}{
		{
			name:      "flag takes priority",
			cfg:       Config{Graph: GraphConfig{DBPath: "/yaml/path"}, ConfigDir: "/proj/.CodeEagle"},
			flagValue: "/flag/path",
			want:      "/flag/path",
		},
		{
			name:      "yaml db_path second",
			cfg:       Config{Graph: GraphConfig{DBPath: "/yaml/path"}, ConfigDir: "/proj/.CodeEagle"},
			flagValue: "",
			want:      "/yaml/path",
		},
		{
			name:      "config dir default",
			cfg:       Config{ConfigDir: "/proj/.CodeEagle"},
			flagValue: "",
			want:      "/proj/.CodeEagle/graph.db",
		},
		{
			name:      "all empty",
			cfg:       Config{},
			flagValue: "",
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.ResolveDBPath(tt.flagValue)
			if got != tt.want {
				t.Errorf("ResolveDBPath(%q) = %q, want %q", tt.flagValue, got, tt.want)
			}
		})
	}
}

func TestDiscoverProjectConf(t *testing.T) {
	// Create a hierarchy: tmpDir/sub1/sub2 with .CodeEagle.conf at tmpDir level.
	tmpDir := t.TempDir()
	sub1 := filepath.Join(tmpDir, "sub1")
	sub2 := filepath.Join(sub1, "sub2")
	if err := os.MkdirAll(sub2, 0755); err != nil {
		t.Fatalf("create subdirs: %v", err)
	}
	confPath := filepath.Join(tmpDir, ProjectConfFile)
	if err := os.WriteFile(confPath, []byte("export_file: codeeagle-graph.export\n"), 0644); err != nil {
		t.Fatalf("write conf: %v", err)
	}

	// Discover from sub2 should find .CodeEagle.conf at tmpDir.
	gotPath, gotConf, err := DiscoverProjectConf(sub2)
	if err != nil {
		t.Fatalf("DiscoverProjectConf: %v", err)
	}
	if gotPath != confPath {
		t.Errorf("confPath = %q, want %q", gotPath, confPath)
	}
	if gotConf == nil {
		t.Fatal("conf should not be nil")
	}
	if gotConf.ExportFile != "codeeagle-graph.export" {
		t.Errorf("ExportFile = %q, want %q", gotConf.ExportFile, "codeeagle-graph.export")
	}

	// Discover from a directory without .CodeEagle.conf should return nil.
	isolatedDir := t.TempDir()
	_, gotConf, err = DiscoverProjectConf(isolatedDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotConf != nil {
		t.Errorf("expected nil conf, got %+v", gotConf)
	}
}

func TestExportFilePath(t *testing.T) {
	conf := &ProjectConf{ExportFile: "codeeagle-graph.export"}
	got := ExportFilePath("/home/user/project", conf)
	want := "/home/user/project/codeeagle-graph.export"
	if got != want {
		t.Errorf("ExportFilePath = %q, want %q", got, want)
	}

	// Nil conf should return empty.
	if got := ExportFilePath("/home/user/project", nil); got != "" {
		t.Errorf("ExportFilePath(nil) = %q, want empty", got)
	}

	// Empty export file should return empty.
	if got := ExportFilePath("/home/user/project", &ProjectConf{}); got != "" {
		t.Errorf("ExportFilePath(empty) = %q, want empty", got)
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
