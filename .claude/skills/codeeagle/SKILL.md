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
| Function call graph (who calls what) | `codeeagle query edges --node <name> --type Calls` |
| What a function depends on | `codeeagle query edges --node <name> --type Calls --direction out` |
| Who calls a function | `codeeagle query edges --node <name> --type Calls --direction in` |
| Cross-service API dependencies | `codeeagle query edges --node <name> --type Consumes` |
| Import-to-manifest dep tracing | `codeeagle query edges --node <name> --type DependsOn` |
| Service endpoints | `codeeagle query edges --node <name> --type Exposes` |
| All relationships for a symbol | `codeeagle query edges --node <name>` |
| Search nodes by type/name/package | `codeeagle query --type X --name Y` |
| Impact analysis ("what breaks if I change X?") | `codeeagle agent plan "<question>"` |
| Design patterns, API consistency | `codeeagle agent design "<question>"` |
| General "how does X work?" questions | `codeeagle agent ask "<question>"` |

## Node Types

`File`, `Package`, `Service`, `Function`, `Method`, `Struct`, `Class`, `Interface`, `Enum`,
`Variable`, `Constant`, `Type`, `Dependency`, `APIEndpoint`, `Document`, `Module`, `DTO`, `AIGuideline`

## Edge Types

| Edge | Meaning | Example |
|------|---------|---------|
| `Calls` | Function/method calls another | `extract() -> extractImports()` |
| `Contains` | Parent contains child | `Service -> File -> Function` |
| `Imports` | File/package imports dependency | `parser.go -> go/ast` |
| `DependsOn` | Dependency relationship | import node -> manifest dep, service -> service |
| `Implements` | Type implements interface | `GoParser -> Parser` |
| `Exposes` | Service exposes endpoint | `backend -> GET /api/users` |
| `Consumes` | API call targets endpoint | `fetch /api/users -> GET /api/users` |
| `Documents` | Doc describes code entity | `README -> Service` |

## Structured Queries (fast, machine-friendly)

### List symbols in a file
```
codeeagle query symbols --file <path>
```
Returns all functions, structs, interfaces, methods with signatures, line numbers,
and export status. **Use this instead of reading a source file.**

### Find interface definitions and implementors
```
codeeagle query interface --name <name>
codeeagle query interface --name "Store"
```
Returns the interface location, signature, and all types that implement it.

### Show edges for a symbol
```
codeeagle query edges --node <name-or-id>
codeeagle query edges --node "RunAll" --type Calls
codeeagle query edges --node "RunAll" --type Calls --direction out
```
Returns all incoming/outgoing relationships for a node. Filter by `--type` and `--direction`.

### Trace function call chains
```
codeeagle query edges --node "extractFunctionCalls" --type Calls --direction out
codeeagle query edges --node "extractFunctionCalls" --type Calls --direction in
```
Outgoing = what this function calls. Incoming = who calls this function.

### Trace import to manifest dependency
```
codeeagle query edges --node "github.com/dgraph-io/badger/v4" --type DependsOn
```
Shows import -> manifest dep links (kind=import_to_manifest).

### Find API endpoints and their callers
```
codeeagle query --type APIEndpoint
codeeagle query edges --node "GET /api/users" --type Consumes --direction in
```

### General node search
```
codeeagle query --type Function --name "New*" --package embedded
codeeagle query --type Struct --language go
codeeagle query --type Dependency --name "axios"
codeeagle query --type Service
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
3. `codeeagle query edges --node <name> --type Calls` — map the call graph
4. `codeeagle query edges --node <name> --type DependsOn` — trace dependency chains
5. `codeeagle agent plan "<what you plan to change>"` — assess impact and risk

## Prerequisites
- CodeEagle must be installed and on PATH
- Project must be initialized (`codeeagle init`) with an indexed graph (`codeeagle sync`)

## Tips
- Structured queries are instant; AI agents take 10-20 seconds
- Use `--type Calls --direction out` to see what a function depends on
- Use `--type Calls --direction in` to find all callers of a function
- Use `--type DependsOn` to trace imports through to manifest dependencies
- Prefer structured queries for implementation planning, AI agents for understanding
