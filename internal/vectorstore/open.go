package vectorstore

import (
	"path/filepath"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/embedding"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

// OpenReadOnlyWithLoad opens a read-only vector store, detects the embedding
// provider, and loads the index. Returns (nil, nil) if vector search is
// unavailable (no config dir, no embedder, no index) — this is not an error.
// The caller must Close() the returned store when done (if non-nil).
func OpenReadOnlyWithLoad(cfg *config.Config, store *embedded.BranchStore, branch string) (*VectorStore, error) {
	if cfg.ConfigDir == "" {
		return nil, nil
	}

	embedder, err := embedding.DetectProvider(cfg)
	if err != nil || embedder == nil {
		return nil, nil
	}

	idxPath := filepath.Join(cfg.ConfigDir, "vec.idx")
	dbPath := filepath.Join(cfg.ConfigDir, "vec.db")

	vs, err := NewReadOnly(store, embedder, branch, idxPath, dbPath)
	if err != nil {
		return nil, nil
	}

	loaded, err := vs.Load()
	if err != nil || !loaded {
		vs.Close()
		return nil, nil
	}

	return vs, nil
}
