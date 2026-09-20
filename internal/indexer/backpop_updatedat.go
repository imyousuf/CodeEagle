package indexer

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// fileTypeNodes lists the node types that represent files on disk.
var fileTypeNodes = []graph.NodeType{
	graph.NodeFile,
	graph.NodeTestFile,
	graph.NodeDocument,
	graph.NodeDirectory,
	graph.NodeAIGuideline,
}

// needsUpdatedAtBackpop checks all file-type nodes in the DB and returns
// true if any lack an UpdatedAt timestamp.
func needsUpdatedAtBackpop(ctx context.Context, store graph.Store) bool {
	for _, nodeType := range fileTypeNodes {
		nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: nodeType})
		if err != nil || len(nodes) == 0 {
			continue
		}
		for _, node := range nodes {
			if node.UpdatedAt.IsZero() {
				return true
			}
		}
	}
	return false
}

// BackpopUpdatedAt populates the UpdatedAt field and date hierarchy nodes
// for existing file nodes that lack them. It resolves each file's absolute
// path from the given repo roots and uses the file's mtime as UpdatedAt.
func BackpopUpdatedAt(ctx context.Context, store graph.Store, repoRoots []string, logFn func(format string, args ...any)) (int, error) {
	updated := 0

	for _, nodeType := range fileTypeNodes {
		nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: nodeType})
		if err != nil {
			return updated, err
		}

		for _, node := range nodes {
			if !node.UpdatedAt.IsZero() {
				continue
			}

			// Resolve absolute path by searching repo roots.
			absPath := resolveAbsPath(node.FilePath, repoRoots)
			if absPath == "" {
				continue // file not found under any repo root
			}

			info, err := os.Stat(absPath)
			if err != nil {
				continue // file no longer exists on disk
			}

			node.UpdatedAt = info.ModTime()
			if err := store.UpdateNode(ctx, node); err != nil {
				if logFn != nil {
					logFn("Warning: backpop UpdatedAt for %s: %v", node.FilePath, err)
				}
				continue
			}

			// Create date hierarchy and UpdatedOn edge.
			if err := EnsureDateNodes(ctx, store, node.UpdatedAt, node.ID); err != nil {
				if logFn != nil {
					logFn("Warning: create date nodes for %s: %v", node.FilePath, err)
				}
			}

			updated++
		}
	}

	return updated, nil
}

// loadIndexedFileTimes batch-loads all file-type nodes from the DB and returns
// a map of FilePath → UpdatedAt. This single scan replaces both the watermark
// heuristic and per-file hasMatchingUpdatedAt queries, giving O(1) lookups
// during the filter phase.
func loadIndexedFileTimes(ctx context.Context, store graph.Store) map[string]time.Time {
	result := make(map[string]time.Time)
	for _, nodeType := range fileTypeNodes {
		nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: nodeType})
		if err != nil {
			continue
		}
		for _, node := range nodes {
			if node.FilePath != "" && !node.UpdatedAt.IsZero() {
				// Keep the latest UpdatedAt if multiple nodes share a path.
				if existing, ok := result[node.FilePath]; !ok || node.UpdatedAt.After(existing) {
					result[node.FilePath] = node.UpdatedAt
				}
			}
		}
	}
	return result
}

// indexedFileState holds batch-loaded file state from the DB for sync decisions.
type indexedFileState struct {
	times  map[string]time.Time // FilePath → UpdatedAt
	hashes map[string]string    // FilePath → content_hash
}

// loadIndexedFileState batch-loads all file-type nodes from the DB and returns
// both file timestamps and content hashes. This provides all the information
// needed for the sync filter phase (skip unchanged files) and duplicate
// detection (seed the hash index) in a single scan.
func loadIndexedFileState(ctx context.Context, store graph.Store) indexedFileState {
	result := indexedFileState{
		times:  make(map[string]time.Time),
		hashes: make(map[string]string),
	}
	for _, nodeType := range fileTypeNodes {
		nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: nodeType})
		if err != nil {
			continue
		}
		for _, node := range nodes {
			if node.FilePath == "" {
				continue
			}
			if !node.UpdatedAt.IsZero() {
				if existing, ok := result.times[node.FilePath]; !ok || node.UpdatedAt.After(existing) {
					result.times[node.FilePath] = node.UpdatedAt
				}
			}
			if hash := node.Properties[graph.PropContentHash]; hash != "" {
				result.hashes[node.FilePath] = hash
			}
		}
	}
	return result
}

// resolveAbsPath finds the absolute path for a relative file path by checking
// each repo root. Returns empty string if the file doesn't exist under any root.
func resolveAbsPath(relPath string, repoRoots []string) string {
	// If the path is already absolute, use it directly.
	if filepath.IsAbs(relPath) {
		return relPath
	}
	for _, root := range repoRoots {
		abs := filepath.Join(root, relPath)
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
	}
	return ""
}
