package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/embedding"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/vectorstore"
)

// vectorGraphView is the graph the vector index is maintained against: the
// branch, plus the scopes whose nodes are embedded alongside it. Without the
// shared scopes in view, their vectors look like vectors of deleted nodes and
// a reconcile would drop every meeting from search.
func vectorGraphView(store *embedded.BranchStore) *embedded.BranchStore {
	return store.WithReadScopes(embedded.MeetingScope, embedded.RelationScope)
}

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
// store is the view the vector store reads; changedFiles are the graph paths
// this sync re-indexed.
func syncVectorIndex(vs *vectorstore.VectorStore, cfg *config.Config, full bool, store graph.Store, changedFiles []string, logFn func(string, ...any)) error {
	if vs == nil {
		return nil
	}

	// Try to load existing index.
	loaded, err := vs.Load()
	if err != nil {
		logFn("Warning: failed to load vector index: %v", err)
		loaded = false
	}

	// A changed provider, model, or embeddable text all mean the existing
	// vectors cannot be mixed with new ones.
	if loaded && vs.NeedsReindex() {
		logFn("[vector] Rebuilding vector index: %s", vs.ReindexReason())
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

	// Incremental. A changed file's nodes keep their IDs but not their text,
	// so their vectors are dropped; the reconcile then embeds them along with
	// anything else the index lacks, newest first and within the backfill
	// limit, and drops the vectors of nodes that are gone.
	ctx := ctx2()
	if store != nil {
		for _, path := range changedFiles {
			nodes, err := store.QueryNodes(ctx, graph.NodeFilter{FilePath: path})
			if err != nil {
				continue
			}
			for _, n := range nodes {
				_ = vs.RemoveNode(ctx, n.ID)
			}
		}
	}
	added, removed, err := vs.Reconcile(ctx, cfg.Vector.BackfillLimit)
	if err != nil {
		return fmt.Errorf("update vector index: %w", err)
	}
	if added > 0 || removed > 0 {
		logFn("[vector] Embedded %d nodes, dropped %d gone (%d vectors)", added, removed, vs.Len())
	} else if verbose && logFn != nil {
		logFn("[vector] Index up to date (%d vectors), saving...", vs.Len())
	}
	if err := vs.Save(); err != nil {
		return fmt.Errorf("save vector index: %w", err)
	}

	return nil
}

// embedMissingMeetings embeds the meeting nodes the vector index lacks, after
// a meetings run. It is best effort: if the index is held by another process,
// does not exist yet or needs rebuilding, it does nothing and the next code
// sync catches up. It never removes anything, because the meetings store sees
// only meetings and everything else would look deleted to it.
func embedMissingMeetings(cfg *config.Config, store *embedded.BranchStore, branch string, out io.Writer) {
	vs, err := openVectorStore(cfg, store, branch, nil)
	if err != nil || vs == nil {
		return
	}
	defer vs.Close()
	if loaded, err := vs.Load(); err != nil || !loaded || vs.NeedsReindex() {
		return
	}
	types := append(graph.MeetingNodeTypes(), graph.NodeTopic)
	n, err := vs.EmbedMissing(ctx2(), cfg.Vector.BackfillLimit, types...)
	if err != nil {
		fmt.Fprintf(out, "\nNote: could not embed new meetings for search by meaning: %v\n", err)
		return
	}
	if n == 0 {
		return
	}
	if err := vs.Save(); err != nil {
		fmt.Fprintf(out, "\nNote: could not save the vector index: %v\n", err)
		return
	}
	fmt.Fprintf(out, "\nEmbedded %d meeting nodes for search by meaning.\n", n)
}

// openReadOnlyVectorStore opens the vector store for reading. Readers share
// the database, but a writer cannot open it while any reader has it open, so
// a reader that lives long should Detach once the index is loaded. Use for
// commands that only query the vector index (rag, agent, app).
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
	vs, _ := vectorstore.OpenReadOnlyWithLoad(cfg, store, branch)
	return vs
}

// ctx2 returns a background context. Named to avoid conflict with existing ctx() in sync.go.
func ctx2() context.Context {
	return context.Background()
}
