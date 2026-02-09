---
name: codeeagle-design
description: Review architecture patterns, API design consistency, and cross-service patterns using CodeEagle's knowledge graph. Use for design reviews and pattern recognition.
allowed-tools: Bash(codeeagle *)
---

# CodeEagle Design

Run the CodeEagle design agent to review architecture and design patterns.

## Usage

Use the Bash tool to run:
```
codeeagle agent design "$ARGUMENTS"
```

## Prerequisites
- CodeEagle must be installed and on PATH
- Project must be initialized (`codeeagle init`) with an indexed graph (`codeeagle sync`)

## Notes
- Recognizes architecture patterns: how is auth handled across services?
- Reviews API design: is this new endpoint consistent with existing patterns?
- Suggests patterns based on what the codebase already uses
- Checks cross-service consistency
