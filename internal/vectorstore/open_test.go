package vectorstore

import (
	"testing"

	"github.com/imyousuf/CodeEagle/internal/config"
)

func TestOpenReadOnlyWithLoad_NoConfigDir(t *testing.T) {
	cfg := &config.Config{}
	vs, err := OpenReadOnlyWithLoad(cfg, nil, "main")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if vs != nil {
		t.Error("should return nil when ConfigDir is empty")
	}
}

func TestOpenReadOnlyWithLoad_NoEmbedder(t *testing.T) {
	cfg := &config.Config{
		ConfigDir: t.TempDir(),
		// No embedding provider configured — DetectProvider returns nil.
	}
	vs, err := OpenReadOnlyWithLoad(cfg, nil, "main")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if vs != nil {
		t.Error("should return nil when no embedding provider is detected")
	}
}
