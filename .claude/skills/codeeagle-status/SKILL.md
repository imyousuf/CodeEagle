---
name: codeeagle-status
description: Show CodeEagle knowledge graph indexing status and statistics including node/edge counts, types, and git branch info.
allowed-tools: Bash(codeeagle *)
---

# CodeEagle Status

Show the current indexing status and statistics for the CodeEagle knowledge graph.

## Usage

Use the Bash tool to run:
```
codeeagle status
```

## Prerequisites
- CodeEagle must be installed and on PATH
- Project must be initialized (`codeeagle init`)

## Notes
- Displays node and edge counts by type
- Shows indexed files, packages, and services
- Reports current git branch information
- Indicates whether the graph is up to date
