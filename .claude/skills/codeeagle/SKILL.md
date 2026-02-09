---
name: codeeagle
description: Query and analyze the codebase using CodeEagle's knowledge graph. Provides structured code data (symbols, interfaces, edges), impact analysis, design review, and general Q&A — all grounded in an indexed knowledge graph. Use this FIRST before reading source files.
allowed-tools: Bash(codeeagle *)
---

# CodeEagle — Codebase Intelligence

Use CodeEagle to understand the codebase without reading source files directly.
Pick the right command based on what you need:

## Decision Matrix

| Need | Command |
|------|---------|
| Function signatures, types in a file | `codeeagle query symbols --file <path>` |
| Interface definition + implementors | `codeeagle query interface --name <name>` |
| Callers, dependencies, relationships | `codeeagle query edges --node <name>` |
| Search nodes by type/name/package | `codeeagle query --type X --name Y` |
| Impact analysis ("what breaks if I change X?") | `codeeagle agent plan "<question>"` |
| Design patterns, API consistency | `codeeagle agent design "<question>"` |
| General "how does X work?" questions | `codeeagle agent ask "<question>"` |

## Structured Queries (fast, machine-friendly)

### List symbols in a file
```
codeeagle query symbols --file <path>
codeeagle query symbols --file <path> --json
```
Returns all functions, structs, interfaces, methods with signatures, line numbers,
and export status. **Use this instead of reading a source file.**

### Find interface definitions and implementors
```
codeeagle query interface --name <name>
codeeagle query interface --name "Store" --json
```
Returns the interface location, signature, and all types that implement it.

### Show edges (callers, dependencies, implementors) for a symbol
```
codeeagle query edges --node <name-or-id>
codeeagle query edges --node "BranchStore" --type Calls --direction in
codeeagle query edges --node "BranchStore" --json
```
Returns all incoming/outgoing relationships for a node.

### General node search
```
codeeagle query --type Function --name "New*" --package embedded
codeeagle query --type Struct --language go
```

## AI Agents (slower, prose answers)

### Impact analysis and planning
```
codeeagle agent plan "What would be affected if I change the Store interface?"
```
Use for: impact analysis, dependency mapping, scope estimation, risk assessment.

### Design review
```
codeeagle agent design "Is this new endpoint consistent with existing API patterns?"
```
Use for: pattern recognition, API consistency, cross-service design checks.

### General questions
```
codeeagle agent ask "How does the branch-aware store work?"
```
Use for: high-level understanding, "how does X work?" questions.

## Workflow for Implementation Planning

1. `codeeagle query symbols --file <path>` — get exact signatures in files you'll modify
2. `codeeagle query interface --name <name>` — understand contracts and implementors
3. `codeeagle query edges --node <name>` — map dependencies and callers
4. `codeeagle agent plan "<what you plan to change>"` — assess impact and risk

## Prerequisites
- CodeEagle must be installed and on PATH
- Project must be initialized (`codeeagle init`) with an indexed graph (`codeeagle sync`)

## Tips
- Use `--json` for machine-readable output from query subcommands
- Structured queries are instant; AI agents take 10-20 seconds
- Prefer structured queries for implementation planning, AI agents for understanding
