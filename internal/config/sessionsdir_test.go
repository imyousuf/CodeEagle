package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSessionsDirAcceptsPathOrList guards the one-key design: `sessions_dir`
// takes either a single directory or a list of them. Having a second plural
// key alongside it was confusing, and this is what makes the second key
// unnecessary rather than merely undocumented.
func TestSessionsDirAcceptsPathOrList(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want []string
	}{
		{
			name: "a single path",
			yaml: "  sessions_dir: /a/b\n",
			want: []string{"/a/b"},
		},
		{
			name: "a list",
			yaml: "  sessions_dir:\n    - /a/b\n    - /c/d\n",
			want: []string{"/a/b", "/c/d"},
		},
		{
			name: "an inline list",
			yaml: "  sessions_dir: [/a/b, /c/d]\n",
			want: []string{"/a/b", "/c/d"},
		},
		{
			name: "no directory at all, which is allowed",
			yaml: "  enabled: true\n",
			want: nil,
		},
		{
			name: "duplicates and blanks are dropped",
			yaml: "  sessions_dir: [/a/b, \"\", /a/b, /c/d]\n",
			want: []string{"/a/b", "/c/d"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := loadTranscriptsConfig(t, tt.yaml)
			got := cfg.TranscriptDirs()
			if len(got) != len(tt.want) {
				t.Fatalf("TranscriptDirs() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("TranscriptDirs()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// loadTranscriptsConfig writes a config whose transcripts section is the given
// YAML body and loads it through Load, so the test exercises the real path.
func loadTranscriptsConfig(t *testing.T, transcripts string) *Config {
	t.Helper()

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, ProjectDirName)
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("create project dir: %v", err)
	}
	content := "project:\n  name: test\n\nrepositories:\n  - path: " + tmpDir +
		"\n    type: single\n\ntranscripts:\n" + transcripts
	if err := os.WriteFile(filepath.Join(projectDir, ProjectConfigFile), []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(origDir); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	return cfg
}
