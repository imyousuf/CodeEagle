package indexer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/imyousuf/CodeEagle/internal/gitutil"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

const syncStateFile = "sync.state"

// SyncFiles performs an incremental (or full) sync of the given paths.
// For git repositories, it uses commit-based diffing. For non-git directories,
// it compares file modification times. The branch parameter controls which
// branch state to use for git-aware sync tracking.
func SyncFiles(ctx context.Context, idx *Indexer, paths []string, configDir string, full bool, branch string) error {
	statePath := filepath.Join(configDir, syncStateFile)
	state, err := LoadSyncState(statePath)
	if err != nil {
		return fmt.Errorf("load sync state: %w", err)
	}

	// Migrate legacy flat state to branch-aware on first load.
	state.MigrateLegacy(branch)

	for _, repoPath := range paths {
		if isGitRepo(repoPath) {
			if err := syncGitRepo(ctx, idx, repoPath, state, full, branch); err != nil {
				return fmt.Errorf("sync git repo %s: %w", repoPath, err)
			}
		} else {
			if err := syncDirectory(ctx, idx, repoPath, state, full); err != nil {
				return fmt.Errorf("sync directory %s: %w", repoPath, err)
			}
		}
	}

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
func syncGitRepo(ctx context.Context, idx *Indexer, repoPath string, state *SyncState, full bool, branch string) error {
	currentHEAD, err := gitutil.GetCurrentHEAD(repoPath)
	if err != nil {
		return fmt.Errorf("get HEAD: %w", err)
	}

	bs := state.GetBranchState(branch)

	if bs.LastCommit == "" || full {
		// Full re-index.
		if idx.verbose {
			idx.log("Full index of %s (HEAD: %s, branch: %s)", repoPath, currentHEAD[:min(12, len(currentHEAD))], branch)
		}
		if err := idx.IndexDirectory(ctx, repoPath); err != nil {
			return err
		}
	} else if bs.LastCommit == currentHEAD {
		if idx.verbose {
			idx.log("Already at HEAD %s, skipping %s (branch: %s)", currentHEAD[:min(12, len(currentHEAD))], repoPath, branch)
		}
		return nil
	} else {
		// Diff-aware incremental sync.
		added, modified, deleted, err := gitutil.GetChangedFilesSince(repoPath, bs.LastCommit)
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
			// Git diff returns relative paths — use them directly since the graph
			// now stores relative paths.
			for _, relPath := range deleted {
				if err := idx.Store().DeleteByFile(ctx, relPath); err != nil {
					idx.log("Warning: delete by file %s: %v", relPath, err)
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

	bs.LastCommit = currentHEAD
	bs.Timestamp = time.Now()
	return nil
}

// syncDirectory performs mtime-based sync for a non-git directory.
// State tracking uses relative paths (relative to repo roots) so the state
// file is portable across machines.
func syncDirectory(ctx context.Context, idx *Indexer, dirPath string, state *SyncState, full bool) error {
	if full {
		if idx.verbose {
			idx.log("Full index of %s (non-git)", dirPath)
		}
		// Clear file times for this dir (keys may be relative or absolute from legacy state).
		if state.FileTimes != nil {
			for k := range state.FileTimes {
				if isSubPath(k, dirPath) || !filepath.IsAbs(k) {
					delete(state.FileTimes, k)
				}
			}
		}
		return idx.IndexDirectory(ctx, dirPath)
	}

	if state.FileTimes == nil {
		state.FileTimes = make(map[string]time.Time)
	}

	// Track which relative paths still exist.
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

		relPath := idx.toRelativePath(path)
		existing[relPath] = struct{}{}
		modTime := info.ModTime()

		prevTime, hasPrev := state.FileTimes[relPath]
		if !hasPrev || modTime.After(prevTime) {
			if err := idx.IndexFile(ctx, path); err != nil {
				idx.log("Warning: index file %s: %v", path, err)
			}
			state.FileTimes[relPath] = modTime
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Delete nodes for files that no longer exist.
	for relPath := range state.FileTimes {
		if _, ok := existing[relPath]; !ok {
			if err := idx.Store().DeleteByFile(ctx, relPath); err != nil {
				idx.log("Warning: delete by file %s: %v", relPath, err)
			}
			delete(state.FileTimes, relPath)
		}
	}

	return nil
}

// AutoImportIfNeeded checks if the export file has been updated since the last import
// and imports it into the store if needed.
func AutoImportIfNeeded(ctx context.Context, store *embedded.BranchStore, exportFilePath string, state *SyncState, logFn func(format string, args ...any)) error {
	info, err := os.Stat(exportFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no export file, nothing to import
		}
		return fmt.Errorf("stat export file: %w", err)
	}

	exportMtime := info.ModTime()
	if !state.LastImportTime.IsZero() && !exportMtime.After(state.LastImportTime) {
		return nil // export hasn't changed since last import
	}

	// Read the export branch.
	f, err := os.Open(exportFilePath)
	if err != nil {
		return fmt.Errorf("open export file: %w", err)
	}

	exportBranch, err := embedded.ReadExportBranch(f)
	f.Close()
	if err != nil {
		return fmt.Errorf("read export branch: %w", err)
	}

	// Re-open for actual import.
	f, err = os.Open(exportFilePath)
	if err != nil {
		return fmt.Errorf("open export file for import: %w", err)
	}
	defer f.Close()

	targetBranch := exportBranch
	if targetBranch == "" {
		targetBranch = "main" // legacy exports assumed to be main
	}

	if logFn != nil {
		logFn("Auto-importing export file into branch %q", targetBranch)
	}

	if _, err := store.ImportIntoBranch(ctx, f, targetBranch); err != nil {
		return fmt.Errorf("import into branch %s: %w", targetBranch, err)
	}

	state.LastImportTime = time.Now()
	return nil
}

// CleanupStaleBranches removes graph data for branches that no longer exist in git.
func CleanupStaleBranches(ctx context.Context, store *embedded.BranchStore, repoPath string, state *SyncState, logFn func(format string, args ...any)) error {
	branches, err := gitutil.ListLocalBranches(repoPath)
	if err != nil {
		return fmt.Errorf("list local branches: %w", err)
	}

	existing := make(map[string]struct{}, len(branches))
	for _, b := range branches {
		existing[b] = struct{}{}
	}
	// Always keep "default" (used by NewStore backward-compat wrapper).
	existing["default"] = struct{}{}

	// Clean up sync state for dead branches.
	cleaned := state.CleanupStaleBranches(existing)

	// Clean up graph data for dead branches.
	dbBranches, err := store.ListBranches()
	if err != nil {
		return fmt.Errorf("list DB branches: %w", err)
	}

	for _, branch := range dbBranches {
		if _, ok := existing[branch]; !ok {
			if logFn != nil {
				logFn("Cleaning up stale branch data: %s", branch)
			}
			if err := store.DeleteByBranch(branch); err != nil {
				return fmt.Errorf("delete branch %s: %w", branch, err)
			}
			cleaned = append(cleaned, branch)
		}
	}

	if logFn != nil && len(cleaned) > 0 {
		logFn("Cleaned up %d stale branches", len(cleaned))
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
