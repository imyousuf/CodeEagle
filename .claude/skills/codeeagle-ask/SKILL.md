---
name: codeeagle-ask
description: Ask questions about the indexed codebase using CodeEagle's knowledge graph and AI agent. Use when the user wants to understand code architecture, find dependencies, or get answers about the codebase.
allowed-tools: Bash(codeeagle *)
---

# CodeEagle Ask

Run the CodeEagle ask agent to answer questions about the indexed codebase.

## Usage

Use the Bash tool to run:
```
codeeagle agent ask "$ARGUMENTS"
```

## Prerequisites
- CodeEagle must be installed and on PATH
- Project must be initialized (`codeeagle init`) with an indexed graph (`codeeagle sync`)

## Notes
- The agent queries the knowledge graph built from the codebase
- Answers are grounded in actual code structure
