package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/config"
)

// TestResolveIndexedPath covers turning the path recorded on an indexed node
// back into a file on disk, which is what lets a transcript found during
// document indexing be handed to meeting indexing.
func TestResolveIndexedPath(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "myrepo")
	if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	standup := filepath.Join(repo, "docs", "standup.docx")
	if err := os.WriteFile(standup, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Repositories: []config.RepositoryConfig{{Path: repo}},
	}

	tests := []struct {
		name string
		rel  string
		want string
	}{
		{"relative to the repository root", "docs/standup.docx", standup},
		// Non-git roots record the root's own basename in the path, so
		// joining naively would repeat it.
		{"basename-prefixed non-git path", "myrepo/docs/standup.docx", standup},
		{"absolute path that exists", standup, standup},
		{"absolute path that does not", filepath.Join(root, "gone.docx"), ""},
		{"file no longer on disk", "docs/removed.docx", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveIndexedPath(cfg, tt.rel)
			if got != tt.want {
				t.Errorf("resolveIndexedPath(%q) = %q, want %q", tt.rel, got, tt.want)
			}
		})
	}
}
