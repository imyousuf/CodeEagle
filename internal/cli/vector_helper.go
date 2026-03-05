package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/embedding"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/vectorstore"
)

// vectorIndexPath returns the path to the HNSW index file.
func vectorIndexPath(cfg *config.Config) string {
	return filepath.Join(cfg.ConfigDir, "vec.idx")
}

// vectorDBPath returns the path to the vector BadgerDB directory.
func vectorDBPath(cfg *config.Config) string {
	return filepath.Join(cfg.ConfigDir, "vec.db")
}

// openVectorStore detects an embedding provider and creates a VectorStore.
// Returns nil, nil if no embedding provider is available.
func openVectorStore(cfg *config.Config, store *embedded.BranchStore, branch string, logFn func(string, ...any)) (*vectorstore.VectorStore, error) {
	if cfg.ConfigDir == "" {
		return nil, nil
	}

	embedder, err := embedding.DetectProvider(cfg)
	if err != nil {
		return nil, fmt.Errorf("detect embedding provider: %w", err)
	}
	if embedder == nil {
		if verbose && logFn != nil {
			logFn("[vector] No embedding provider detected, skipping vector search")
		}
		return nil, nil
	}

	if logFn != nil {
		logFn("[vector] Embedding provider: %s/%s (%d-dim)", embedder.Name(), embedder.ModelName(), embedder.Dimensions())
	}

	vs, err := vectorstore.New(
		store, embedder, branch,
		vectorIndexPath(cfg),
		vectorDBPath(cfg),
	)
	if err != nil {
		return nil, fmt.Errorf("open vector store: %w", err)
	}

	return vs, nil
}

// syncVectorIndex handles vector indexing during sync.
// If no index exists, builds a full index from the graph.
// If index exists, updates incrementally based on changed files (or full rebuild on --full).
func syncVectorIndex(vs *vectorstore.VectorStore, cfg *config.Config, full bool, logFn func(string, ...any)) error {
	if vs == nil {
		return nil
	}

	// Try to load existing index.
	loaded, err := vs.Load()
	if err != nil {
		logFn("Warning: failed to load vector index: %v", err)
		loaded = false
	}

	// Check if provider/model changed.
	if loaded && vs.NeedsReindex() {
		logFn("[vector] Embedding provider/model changed, rebuilding vector index...")
		full = true
	}

	if full || !loaded {
		if loaded {
			logFn("[vector] Rebuilding vector index...")
		} else {
			logFn("[vector] Building vector index from graph...")
		}
		if err := vs.Rebuild(ctx2()); err != nil {
			return fmt.Errorf("rebuild vector index: %w", err)
		}
		if err := vs.Save(); err != nil {
			return fmt.Errorf("save vector index: %w", err)
		}
		logFn("[vector] Indexed %d vectors", vs.Len())
		return nil
	}

	// Incremental mode — the caller handles per-file updates.
	// Just save the current state.
	if verbose && logFn != nil {
		logFn("[vector] Index up to date (%d vectors), saving...", vs.Len())
	}
	if err := vs.Save(); err != nil {
		return fmt.Errorf("save vector index: %w", err)
	}

	return nil
}

// openReadOnlyVectorStore opens the vector store with a bypassed lock guard,
// allowing concurrent access from multiple processes. Use for commands that
// only query the vector index (rag, agent, app).
func openReadOnlyVectorStore(cfg *config.Config, store *embedded.BranchStore, branch string, logFn func(string, ...any)) (*vectorstore.VectorStore, error) {
	if cfg.ConfigDir == "" {
		return nil, nil
	}

	embedder, err := embedding.DetectProvider(cfg)
	if err != nil {
		return nil, fmt.Errorf("detect embedding provider: %w", err)
	}
	if embedder == nil {
		if verbose && logFn != nil {
			logFn("[vector] No embedding provider detected, skipping vector search")
		}
		return nil, nil
	}

	if logFn != nil {
		logFn("[vector] Embedding provider: %s/%s (%d-dim)", embedder.Name(), embedder.ModelName(), embedder.Dimensions())
	}

	vs, err := vectorstore.NewReadOnly(
		store, embedder, branch,
		vectorIndexPath(cfg),
		vectorDBPath(cfg),
	)
	if err != nil {
		return nil, fmt.Errorf("open vector store: %w", err)
	}

	return vs, nil
}

// openAgentVectorStore opens the vector store for agent use (read-only).
// Returns nil silently if vector search is unavailable or the index hasn't been built.
func openAgentVectorStore(cfg *config.Config, store *embedded.BranchStore, branch string) *vectorstore.VectorStore {
	vs, err := openReadOnlyVectorStore(cfg, store, branch, func(string, ...any) {})
	if err != nil || vs == nil {
		return nil
	}
	loaded, err := vs.Load()
	if err != nil || !loaded {
		vs.Close()
		return nil
	}
	return vs
}

// ctx2 returns a background context. Named to avoid conflict with existing ctx() in sync.go.
func ctx2() context.Context {
	return context.Background()
}
