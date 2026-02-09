package indexer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/imyousuf/CodeEagle/internal/gitutil"
)

const syncStateFile = "sync.state"

// SyncFiles performs an incremental (or full) sync of the given paths.
// For git repositories, it uses commit-based diffing. For non-git directories,
// it compares file modification times.
func SyncFiles(ctx context.Context, idx *Indexer, paths []string, configDir string, full bool) error {
	statePath := filepath.Join(configDir, syncStateFile)
	state, err := LoadSyncState(statePath)
	if err != nil {
		return fmt.Errorf("load sync state: %w", err)
	}

	for _, repoPath := range paths {
		if isGitRepo(repoPath) {
			if err := syncGitRepo(ctx, idx, repoPath, state, full); err != nil {
				return fmt.Errorf("sync git repo %s: %w", repoPath, err)
			}
		} else {
			if err := syncDirectory(ctx, idx, repoPath, state, full); err != nil {
				return fmt.Errorf("sync directory %s: %w", repoPath, err)
			}
		}
	}

	state.Timestamp = time.Now()
	if err := state.Save(statePath); err != nil {
		return fmt.Errorf("save sync state: %w", err)
	}

	return nil
}

// isGitRepo checks if the given path has a .git directory.
func isGitRepo(path string) bool {
	info, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil && info.IsDir()
}

// syncGitRepo performs git-aware sync for a repository.
func syncGitRepo(ctx context.Context, idx *Indexer, repoPath string, state *SyncState, full bool) error {
	currentHEAD, err := gitutil.GetCurrentHEAD(repoPath)
	if err != nil {
		return fmt.Errorf("get HEAD: %w", err)
	}

	if state.LastCommit == "" || full {
		// Full re-index.
		if idx.verbose {
			idx.log("Full index of %s (HEAD: %s)", repoPath, currentHEAD[:min(12, len(currentHEAD))])
		}
		if err := idx.IndexDirectory(ctx, repoPath); err != nil {
			return err
		}
	} else if state.LastCommit == currentHEAD {
		if idx.verbose {
			idx.log("Already at HEAD %s, skipping %s", currentHEAD[:min(12, len(currentHEAD))], repoPath)
		}
		return nil
	} else {
		// Diff-aware incremental sync.
		added, modified, deleted, err := gitutil.GetChangedFilesSince(repoPath, state.LastCommit)
		if err != nil {
			// If diff fails (e.g. force push), fall back to full index.
			if idx.verbose {
				idx.log("Diff failed (%v), falling back to full index of %s", err, repoPath)
			}
			if err := idx.IndexDirectory(ctx, repoPath); err != nil {
				return err
			}
		} else {
			if idx.verbose {
				idx.log("Incremental sync of %s: %d added, %d modified, %d deleted",
					repoPath, len(added), len(modified), len(deleted))
			}

			// Delete nodes for deleted files.
			for _, relPath := range deleted {
				absPath := filepath.Join(repoPath, relPath)
				if err := idx.Store().DeleteByFile(ctx, absPath); err != nil {
					idx.log("Warning: delete by file %s: %v", absPath, err)
				}
			}

			// Re-index added and modified files.
			for _, relPath := range append(added, modified...) {
				absPath := filepath.Join(repoPath, relPath)
				if err := idx.IndexFile(ctx, absPath); err != nil {
					idx.log("Warning: index file %s: %v", absPath, err)
				}
			}
		}
	}

	state.LastCommit = currentHEAD
	return nil
}

// syncDirectory performs mtime-based sync for a non-git directory.
func syncDirectory(ctx context.Context, idx *Indexer, dirPath string, state *SyncState, full bool) error {
	if full {
		if idx.verbose {
			idx.log("Full index of %s (non-git)", dirPath)
		}
		// Clear file times for this dir.
		if state.FileTimes != nil {
			for k := range state.FileTimes {
				// Only clear times for files under this directory.
				if isSubPath(k, dirPath) {
					delete(state.FileTimes, k)
				}
			}
		}
		return idx.IndexDirectory(ctx, dirPath)
	}

	if state.FileTimes == nil {
		state.FileTimes = make(map[string]time.Time)
	}

	// Track which files still exist.
	existing := make(map[string]struct{})

	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if info.IsDir() {
			return nil
		}

		existing[path] = struct{}{}
		modTime := info.ModTime()

		prevTime, hasPrev := state.FileTimes[path]
		if !hasPrev || modTime.After(prevTime) {
			if err := idx.IndexFile(ctx, path); err != nil {
				idx.log("Warning: index file %s: %v", path, err)
			}
			state.FileTimes[path] = modTime
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Delete nodes for files that no longer exist.
	for path := range state.FileTimes {
		if !isSubPath(path, dirPath) {
			continue
		}
		if _, ok := existing[path]; !ok {
			if err := idx.Store().DeleteByFile(ctx, path); err != nil {
				idx.log("Warning: delete by file %s: %v", path, err)
			}
			delete(state.FileTimes, path)
		}
	}

	return nil
}

// isSubPath checks if child is under parent directory.
func isSubPath(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return len(rel) > 0 && !strings.HasPrefix(rel, "..")
}
