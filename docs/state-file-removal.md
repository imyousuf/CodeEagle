# State File Removal: DB as Single Source of Truth for Sync

## Problem

Non-git directory sync (Documents, Pictures, Videos, Downloads) uses a `sync.state` JSON file to track which files have been indexed and their modification times (`FileTimes`) and content hashes (`FileHashes`). This state is a **flat map shared across all configured directories**, creating two critical bugs:

1. **Cross-directory state corruption**: The cleanup loop in `syncDirectory` iterates `state.FileTimes` to find deleted files. Because the map is shared, syncing Documents deletes state entries (and potentially DB nodes) belonging to Pictures. The `isSubPath` check always returns true when `root == dirPath`, so every entry appears to "belong" to the current directory.

2. **Redundant source of truth**: The DB already stores `UpdatedAt` on every file-type node and `content_hash` as a node property. The state file duplicates this data and falls out of sync when the cleanup bug runs, when syncs are interrupted, or when the binary is updated.

With <100K nodes, BadgerDB can serve as the sole authority. A single sequential scan loads all indexed file paths, timestamps, and content hashes into memory in milliseconds.

## Goals

1. Remove `FileTimes` and `FileHashes` from `SyncState` — eliminate the cross-directory corruption bug entirely
2. Use `loadIndexedFileTimes` (batch DB scan) as the only skip mechanism in the filter phase
3. Skip non-parseable files (RAW, video) via parser registry check, not state tracking
4. Clean up deleted files by diffing walked files against DB entries scoped to the current directory
5. Prefix file paths with repo root basename (e.g., `Pictures/`, `Documents/`) to ensure unique identity across directories

## Prerequisite: Basename-Prefixed Paths

### Why

Currently, `toRelativePath` strips the repo root entirely:
- `/home/user/Pictures/.Gloria/F95A0315.JPG` → `.Gloria/F95A0315.JPG`
- `/home/user/Documents/README.md` → `README.md`

Two directories with a file at the same relative path produce identical NodeIDs, causing collision. More importantly, `loadIndexedFileTimes` uses a flat `map[string]time.Time` — same relative path from different directories would shadow each other.

### Fix

Prefix with the repo root's basename:
- `/home/user/Pictures/.Gloria/F95A0315.JPG` → `Pictures/.Gloria/F95A0315.JPG`
- `/home/user/Documents/README.md` → `Documents/README.md`

This gives each file a unique identity across directories without requiring fully absolute paths.

### Migration (Backpop)

A one-time backpop migrates existing nodes without re-indexing:

1. Walk each configured non-git directory to build `{relPath → rootBasename}`
2. For each file-type node in DB whose `FilePath` doesn't already have a basename prefix:
   - Compute `newFilePath = basename + "/" + oldFilePath`
   - Compute `newNodeID = NewNodeID(type, newFilePath, name)`
   - Update all edges referencing `oldNodeID` (both Source and Target)
   - Delete old node + indexes, insert new node + indexes
3. Non-file nodes (Date, Topic) are shared and need no migration — only edge endpoints change

### toRelativePath Change

Update `Indexer.toRelativePath` to prefix the root basename for non-git directories:

```go
func (idx *Indexer) toRelativePath(absPath string) string {
    for _, root := range idx.repoRoots {
        rel, err := filepath.Rel(root, absPath)
        if err == nil && !strings.HasPrefix(rel, "..") {
            if idx.nonGitRoots[root] {
                return filepath.Join(filepath.Base(root), rel)
            }
            return rel
        }
    }
    return absPath
}
```

The indexer needs a `nonGitRoots map[string]bool` field, populated by the sync orchestrator which already distinguishes git vs non-git repos.

## Design

### Filter Phase (syncDirectory)

Replace the two-source skip logic with DB-only:

```
Phase 1: Parallel walk (unchanged)
Phase 2: Filter
  - Batch-load indexed file state from DB (one scan):
    indexedTimes: map[string]time.Time    (FilePath → UpdatedAt)
    indexedHashes: map[string]string      (FilePath → content_hash)
  - For each walked file:
    - Skip if no parser registered (registry.ParserForFile)
    - Skip if indexedTimes[relPath] matches walk mtime
    - Otherwise add to toIndex
Phase 3: Index (unchanged)
```

No `state.FileTimes` lookup. No `state.FileHashes` seeding. The `hashIndex` for duplicate detection during sync is seeded from `indexedHashes` instead.

### Deleted File Cleanup

After walking, we have the `existing` set (all files found on disk). To find deleted files:

1. Query all DB nodes whose `FilePath` starts with `basename/` (e.g., `Pictures/`)
2. For each node, check if its `FilePath` is in `existing`
3. If not, the file was deleted — remove the node from DB

This is correctly scoped to the current directory because basename-prefixed paths are unique per directory. No cross-directory deletion possible.

### What Stays in SyncState

- `BranchStates` — git commit tracking per branch
- `UpdatedAtBackpopDone` — one-time backpop gate
- `ContentHashBackpopDone` — one-time backpop gate
- `LastImportTime` — export/import tracking

These are small, stable, and unrelated to the bug.

### What Gets Removed

- `FileTimes map[string]time.Time` — replaced by `loadIndexedFileTimes` from DB
- `FileHashes map[string]string` — replaced by DB node `content_hash` properties
- The cleanup loop (lines 414-440 in sync.go) — replaced by basename-scoped DB diff
- Periodic state saves during walk (no volatile data to persist)
- `state.FileTimes[relPath] = modTime` after indexing (DB handles via `AddNode`)

### Crash Resilience

Better than before. Currently, state saves every 10 files to a JSON file — crash between saves loses progress. With DB-only, each `AddNode` is a BadgerDB transaction, durable immediately.

### Performance

- One sequential scan of ~10K file-type nodes at sync start → milliseconds
- O(1) map lookups during filter phase — same as current state-based approach
- Non-parseable files (RAW, video) pass through filter and hit `ParserForFile` which returns false — a map lookup, negligible for ~22K files
- Deleted file cleanup: one DB query scoped by basename prefix, then set membership check

## Implementation Sequence

| Step | What | Files |
|------|------|-------|
| 1 | Backpop: basename-prefix migration | `internal/indexer/backpop_paths.go` (new) |
| 2 | Add `nonGitRoots` to Indexer | `internal/indexer/indexer.go` |
| 3 | Update `toRelativePath` for non-git roots | `internal/indexer/indexer.go` |
| 4 | Extend `loadIndexedFileTimes` to return hashes | `internal/indexer/backpop_updatedat.go` |
| 5 | Refactor `syncDirectory` filter phase | `internal/indexer/sync.go` |
| 6 | Add basename-scoped deleted file cleanup | `internal/indexer/sync.go` |
| 7 | Remove `FileTimes`/`FileHashes` from SyncState | `internal/indexer/syncstate.go` |
| 8 | Update CLI sync orchestrator to pass nonGitRoots | `internal/cli/sync.go` |
| 9 | Tests | `internal/indexer/sync_test.go`, `backpop_paths_test.go` |

## Verification

1. `make test-fast` — all tests pass
2. `make lint` — clean
3. Backpop: run on home dir DB, verify node count and edge count unchanged
4. Manual: `codeeagle sync` on home dir — verify 0 files to index (incremental), no cross-directory deletion
5. Manual: delete a file from Pictures, re-sync — verify only that node is removed
