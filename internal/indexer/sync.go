package indexer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/imyousuf/CodeEagle/internal/gitutil"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

const syncStateFile = "sync.state"

// hashEntry tracks a canonical node for a given content hash during sync.
type hashEntry struct {
	canonicalNodeID string
	mimeType        string
}

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

	// Auto-backpop UpdatedAt for existing nodes that lack it.
	if !state.UpdatedAtBackpopDone {
		count, err := BackpopUpdatedAt(ctx, idx.Store(), paths, idx.log)
		if err != nil {
			idx.log("Warning: UpdatedAt backpop: %v", err)
		} else {
			if count > 0 {
				idx.log("Backpopulated UpdatedAt for %d file nodes", count)
			}
			state.UpdatedAtBackpopDone = true
			_ = state.Save(statePath)
		}
	}

	// Auto-backpop content_hash for existing nodes that lack it.
	if !state.ContentHashBackpopDone {
		count, err := BackpopContentHash(ctx, idx.Store(), paths, idx.log)
		if err != nil {
			idx.log("Warning: content_hash backpop: %v", err)
		} else {
			if count > 0 {
				idx.log("Backpopulated content_hash for %d file nodes", count)
			}
			state.ContentHashBackpopDone = true
			_ = state.Save(statePath)
		}
	}

	for _, repoPath := range paths {
		if isGitRepo(repoPath) {
			if err := syncGitRepo(ctx, idx, repoPath, state, full, branch); err != nil {
				return fmt.Errorf("sync git repo %s: %w", repoPath, err)
			}
		} else {
			if err := syncDirectory(ctx, idx, repoPath, state, full, statePath); err != nil {
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

			// Re-index added and modified files with current sync time.
			syncTime := time.Now()
			for _, relPath := range append(added, modified...) {
				absPath := filepath.Join(repoPath, relPath)
				if err := idx.IndexFileWithTimestamp(ctx, absPath, syncTime); err != nil {
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
//
// Content-hash dedup: tracks SHA-256 hashes per file in sync state. When a
// file with the same content hash as an already-indexed file is encountered,
// it creates a minimal duplicate node + DuplicateOf edge instead of full
// parsing. This is a major optimization for directories with many duplicate
// files (e.g., photos copied across folders).
func syncDirectory(ctx context.Context, idx *Indexer, dirPath string, state *SyncState, full bool, statePath string) error {
	if full {
		if idx.verbose {
			idx.log("Full index of %s (non-git)", dirPath)
		}
		// Clear file times and hashes for this dir.
		if state.FileTimes != nil {
			for k := range state.FileTimes {
				if isSubPath(k, dirPath) || !filepath.IsAbs(k) {
					delete(state.FileTimes, k)
				}
			}
		}
		if state.FileHashes != nil {
			for k := range state.FileHashes {
				if isSubPath(k, dirPath) || !filepath.IsAbs(k) {
					delete(state.FileHashes, k)
				}
			}
		}
		return idx.IndexDirectory(ctx, dirPath)
	}

	if state.FileTimes == nil {
		state.FileTimes = make(map[string]time.Time)
	}
	if state.FileHashes == nil {
		state.FileHashes = make(map[string]string)
	}

	// Build in-memory hash index from existing sync state for dedup.
	// Maps content_hash → {canonicalNodeID, mimeType} for the first-seen file.
	hashIndex := make(map[string]*hashEntry)
	for relPath, hash := range state.FileHashes {
		if _, exists := hashIndex[hash]; !exists {
			fileName := filepath.Base(relPath)
			nodeID := graph.NewNodeID(string(graph.NodeDocument), relPath, fileName)
			hashIndex[hash] = &hashEntry{
				canonicalNodeID: nodeID,
				mimeType:        detectMIMEType(relPath),
			}
		}
	}

	// Pre-scan to count total files for progress reporting (-p flag).
	var progress *syncProgress
	if idx.showProgress {
		idx.log("Sync %s: counting files...", dirPath)
		totalFiles := countWalkableFiles(dirPath)
		idx.log("Sync %s: %d files to process", dirPath, totalFiles)
		progress = newSyncProgress(totalFiles, "sync", idx.log)
	}

	// Track which relative paths still exist.
	existing := make(map[string]struct{})

	// Periodic save: save sync state every N files to avoid losing progress on crash.
	const saveInterval = 10
	filesSinceLastSave := 0
	duplicatesSkipped := 0
	// gcInterval controls how often we force GC during the walk.
	// Image files (JPEG, PNG) decode into large RGBA pixel buffers (50-200MB
	// per 20-40MB image) during DownscaleImage. In a tight sequential loop,
	// Go's GC may not run frequently enough to collect these dead buffers
	// before the next image is allocated, causing RSS to grow unboundedly.
	const gcInterval = 50
	filesSinceLastGC := 0

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
			if idx.matcher.Match(path) {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip excluded files.
		if idx.matcher.Match(path) {
			return nil
		}

		relPath := idx.toRelativePath(path)
		existing[relPath] = struct{}{}
		modTime := info.ModTime()

		prevTime, hasPrev := state.FileTimes[relPath]
		didIndex := false
		if hasPrev && !modTime.After(prevTime) {
			// Unchanged per state — skip.
		} else if !hasPrev && hasMatchingUpdatedAt(ctx, idx.Store(), relPath, modTime) {
			// Unchanged per DB — skip.
			state.FileTimes[relPath] = modTime
		} else {
			// File is new or changed — check for content duplicates.
			indexed, hash := syncIndexFile(ctx, idx, path, relPath, modTime, hashIndex)
			if indexed {
				didIndex = true
			} else if hash != "" {
				// Was handled as a duplicate (skipped full parse).
				duplicatesSkipped++
			}
			state.FileTimes[relPath] = modTime
			if hash != "" {
				state.FileHashes[relPath] = hash
			}
		}

		if progress != nil {
			progress.tick(didIndex)
		}

		// Periodically force GC to reclaim large image pixel buffers.
		// Without this, processing thousands of 20-40MB images causes
		// RSS to grow unboundedly as Go's GC can't keep up with the
		// allocation rate of 50-200MB RGBA decode buffers per image.
		if didIndex {
			filesSinceLastGC++
			if filesSinceLastGC >= gcInterval {
				runtime.GC()
				debug.FreeOSMemory()
				filesSinceLastGC = 0
			}
		}

		// Periodically save sync state to avoid losing progress on crash.
		filesSinceLastSave++
		if statePath != "" && filesSinceLastSave >= saveInterval {
			_ = state.Save(statePath)
			filesSinceLastSave = 0
		}

		return nil
	})
	if err != nil {
		return err
	}

	if duplicatesSkipped > 0 && idx.showProgress {
		idx.log("Sync %s: %d duplicate files detected (skipped full parsing)", dirPath, duplicatesSkipped)
	}

	// Delete nodes for files that no longer exist.
	for relPath := range state.FileTimes {
		if _, ok := existing[relPath]; !ok {
			if err := idx.Store().DeleteByFile(ctx, relPath); err != nil {
				idx.log("Warning: delete by file %s: %v", relPath, err)
			}
			delete(state.FileTimes, relPath)
			delete(state.FileHashes, relPath)
		}
	}

	return nil
}

// syncIndexFile handles indexing a single file during directory sync, with
// content-hash-based duplicate detection. Returns (didFullIndex, contentHash).
// If the file is a duplicate of an already-indexed generic file, it creates
// a minimal node + DuplicateOf edge and returns (false, hash).
// For code files (non-generic parser), duplicates still get full indexing.
func syncIndexFile(ctx context.Context, idx *Indexer, absPath, relPath string, modTime time.Time, hashIndex map[string]*hashEntry) (bool, string) {
	// Check if this file would be handled by the generic parser (images, docs).
	// Only generic files get duplicate-skip optimization; code files always
	// get full parsing since their nodes depend on path context.
	p, hasParser := idx.registry.ParserForFile(absPath)
	if !hasParser {
		return false, "" // no parser at all
	}

	// Pre-read skip: avoid expensive os.ReadFile for files the parser would skip
	// (e.g., unknown binary formats like .CR3, .NEF in the generic parser).
	if skipper, ok := p.(parser.FileSkipper); ok && skipper.ShouldSkipFile(absPath) {
		return false, ""
	}

	isGeneric := p.Language() == "generic"

	// For generic files, compute hash via streaming (O(1) memory) to check
	// for duplicates BEFORE reading the full file content into memory.
	// This prevents OOM when syncing directories with many large files.
	if isGeneric {
		hash, err := computeContentHashFromFile(absPath)
		if err != nil {
			idx.log("Warning: hash file %s: %v", absPath, err)
			return false, ""
		}

		mimeType := detectMIMEType(relPath)

		// Check if we've already seen this content hash.
		if entry, exists := hashIndex[hash]; exists {
			// Duplicate! Create minimal node + DuplicateOf edge.
			// No need to read file content — saves significant memory.
			if err := idx.IndexDuplicateFile(ctx, absPath, modTime, hash, mimeType, entry.canonicalNodeID); err != nil {
				idx.log("Warning: index duplicate %s: %v", absPath, err)
			}
			return false, hash
		}

		// First occurrence — read content for full parsing.
		content, err := os.ReadFile(absPath)
		if err != nil {
			idx.log("Warning: read file %s: %v", absPath, err)
			return false, ""
		}

		if err := idx.IndexFileWithContent(ctx, absPath, content, hash, modTime); err != nil {
			idx.log("Warning: index file %s: %v", absPath, err)
		}

		// Register in hash index for future duplicates.
		fileName := filepath.Base(relPath)
		nodeID := graph.NewNodeID(string(graph.NodeDocument), relPath, fileName)
		hashIndex[hash] = &hashEntry{
			canonicalNodeID: nodeID,
			mimeType:        mimeType,
		}
		return true, hash
	}

	// Non-generic (code) file — always do full indexing.
	if err := idx.IndexFileWithTimestamp(ctx, absPath, modTime); err != nil {
		idx.log("Warning: index file %s: %v", absPath, err)
	}
	return true, ""
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
