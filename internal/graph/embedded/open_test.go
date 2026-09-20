package embedded

import (
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/config"
)

func TestOpenReadOnly_NoDBPath(t *testing.T) {
	cfg := &config.Config{}
	_, _, err := OpenReadOnly(cfg, nil, "")
	if err == nil {
		t.Fatal("OpenReadOnly should fail with no DB path")
	}
	if !strings.Contains(err.Error(), "no graph database path") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestOpenReadOnly_MissingDir(t *testing.T) {
	cfg := &config.Config{
		ConfigDir: t.TempDir(),
		Graph:     config.GraphConfig{Storage: "embedded"},
	}
	_, _, err := OpenReadOnly(cfg, nil, "")
	if err == nil {
		t.Fatal("OpenReadOnly should fail when DB dir doesn't exist")
	}
	if !strings.Contains(err.Error(), "graph database not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestOpenReadWrite_NoDBPath(t *testing.T) {
	cfg := &config.Config{}
	_, _, err := OpenReadWrite(cfg, nil, "")
	if err == nil {
		t.Fatal("OpenReadWrite should fail with no DB path")
	}
	if !strings.Contains(err.Error(), "no graph database path") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestOpenReadOnly_WithOverride(t *testing.T) {
	cfg := &config.Config{
		ConfigDir: t.TempDir(),
	}
	// Override points to nonexistent path — should get "not found" error.
	_, _, err := OpenReadOnly(cfg, nil, "/nonexistent/db/path")
	if err == nil {
		t.Fatal("OpenReadOnly should fail with nonexistent override path")
	}
	if !strings.Contains(err.Error(), "graph database not found") {
		t.Errorf("unexpected error: %v", err)
	}
}
