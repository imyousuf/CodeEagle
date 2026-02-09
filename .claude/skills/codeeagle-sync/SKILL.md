---
name: codeeagle-sync
description: Sync the CodeEagle knowledge graph with latest code changes. Supports diff-aware incremental sync and full re-index.
allowed-tools: Bash(codeeagle *)
---

# CodeEagle Sync

Sync the CodeEagle knowledge graph with the latest code changes.

## Usage

Use the Bash tool to run:
```
codeeagle sync
```

For a full re-index (ignoring incremental state):
```
codeeagle sync --full
```

To export the graph after syncing:
```
codeeagle sync --export
```

## Prerequisites
- CodeEagle must be installed and on PATH
- Project must be initialized (`codeeagle init`)

## Notes
- By default performs a diff-aware incremental sync (only processes changed files)
- Use `--full` to rebuild the entire knowledge graph from scratch
- Use `--export` to export the graph data after syncing
