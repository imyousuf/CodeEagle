# CodeEagle

CodeEagle is a CLI tool that indexes codebases into a knowledge graph and exposes AI agents for planning, design review, and code review — all grounded in deep codebase understanding.

It supports monorepos, multi-repo setups, and multi-language codebases (Go, Python, TypeScript, JavaScript, Java, HTML, Markdown, Makefile, Shell, Terraform, YAML). No external database required — the embedded graph store runs locally with zero setup.

## Features

- **Knowledge graph** of source code entities (functions, classes, interfaces, packages, services) and their relationships (calls, imports, implements, tests, etc.)
- **11 language parsers**: Go (stdlib AST), Python, TypeScript, JavaScript, Java, HTML, Markdown (tree-sitter), Makefile, Shell (tree-sitter bash), Terraform (tree-sitter HCL), YAML (GitHub Actions, Ansible, generic)
- **Code quality metrics**: cyclomatic complexity, lines of code, TODO/FIXME counts
- **AI agents** for planning, design, code review, and freeform Q&A — read-only, advisory, never modify code
- **Git-aware incremental sync** with branch tracking and diff-based updates
- **MCP server** for integration with Claude Code and other MCP-compatible tools
- **LLM auto-summarization** of services and architectural patterns

## Installation

Requires Go 1.24+.

```bash
go install github.com/imyousuf/CodeEagle/cmd/codeeagle@latest
```

Or build from source:

```bash
git clone https://github.com/imyousuf/CodeEagle.git
cd CodeEagle
make build
# Binary: bin/codeeagle
```

## Quick Start

```bash
# 1. Initialize a project (creates .CodeEagle/ directory)
codeeagle init

# 2. Index the codebase
codeeagle sync

# 3. Check indexing status
codeeagle status

# 4. Ask an AI agent a question
codeeagle agent plan "What would be affected if I change the Store interface?"
codeeagle agent design "How is authentication handled across services?"
codeeagle agent review --diff HEAD~1
codeeagle agent ask "What are the largest packages by node count?"
```

## CLI Reference

```
codeeagle init                              Initialize project config
codeeagle sync [--full]                     Sync knowledge graph (incremental or full)
codeeagle sync --export                     Export graph to portable file
codeeagle sync --import                     Import a graph export
codeeagle watch                             Watch for file changes and sync continuously
codeeagle status                            Show indexing status and graph stats

codeeagle agent plan <query>                Impact analysis, dependency mapping, scope estimation
codeeagle agent design <query>              Architecture review and pattern recognition
codeeagle agent review <query>              Code review against codebase conventions
codeeagle agent review --diff <ref>         Review a git diff
codeeagle agent ask <query>                 Freeform Q&A about the codebase

codeeagle query [--type T] [--name N]       Query the knowledge graph
codeeagle query symbols --file <path>       List symbols in a file
codeeagle query interface --name <name>     Show interface and implementors
codeeagle query edges --id <node-id>        Show relationships for a node

codeeagle metrics [--file F] [--type T]     Show code quality metrics
codeeagle mcp serve                         Start MCP server (stdio transport)
codeeagle hook install                      Install git post-commit hook for auto-sync
```

Global flags: `--config <path>`, `--db-path <path>`, `-p <project-name>`, `-v` (verbose).

## Configuration

Project config lives in `.CodeEagle/config.yaml`:

```yaml
project:
  name: my-project

repositories:
  - path: /path/to/repo
    type: monorepo

watch:
  exclude:
    - "**/node_modules/**"
    - "**/.git/**"
    - "**/vendor/**"
    - "**/__pycache__/**"

languages:
  - go
  - python
  - typescript
  - javascript
  - java
  - html
  - markdown
  - makefile
  - shell
  - terraform
  - yaml

graph:
  storage: embedded

agents:
  llm_provider: anthropic   # anthropic, vertex-ai, or claude-cli
  model: claude-sonnet-4-5-20250929
```

### LLM Providers

| Provider | Config | Auth |
|----------|--------|------|
| Anthropic API | `llm_provider: anthropic` | `ANTHROPIC_API_KEY` env var |
| Vertex AI | `llm_provider: vertex-ai` | GCP Application Default Credentials + `project`, `location` |
| Claude CLI | `llm_provider: claude-cli` | Claude Code installed and authenticated |

### Multi-Project Registry

Register multiple projects in `~/.codeeagle.conf` to switch between them with `-p`:

```bash
codeeagle init  # registers the project automatically
codeeagle -p my-project status
```

## Knowledge Graph

### Node Types

| Type | Description |
|------|-------------|
| Repository | Top-level repository |
| Service | Service or module within a repo |
| Package | Language-level package/module |
| File | Source file |
| Function | Function or standalone method |
| Method | Method bound to a type/class |
| Struct, Class | Data structures |
| Interface | Interface or abstract class |
| Enum, Constant | Enumerations, exported constants |
| Type | Type aliases and definitions |
| APIEndpoint | REST routes, gRPC services |
| DBModel, DomainModel, ViewModel, DTO | Classified model types |
| Dependency | External dependency |
| Document | Documentation file (README, spec, etc.) |

### Edge Types

`Contains`, `Imports`, `Calls`, `Implements`, `DependsOn`, `Tests`, `Documents`, `Exposes`, `Configures`, `Migrates`, `References`, `Embeds`

### Storage

The embedded graph store uses [BadgerDB](https://github.com/dgraph-io/badger) with secondary indexes. Data is stored per-branch with fallback reads (current branch -> default branch). No external database required.

## MCP Server

CodeEagle exposes its knowledge graph as an [MCP](https://modelcontextprotocol.io/) server for integration with Claude Code and other LLM tools.

```bash
# Start directly (stdio transport)
codeeagle mcp serve

# Or configure as an MCP server in Claude Code settings:
# ~/.claude/settings.json
{
  "mcpServers": {
    "codeeagle": {
      "command": "codeeagle",
      "args": ["mcp", "serve", "--config", "/path/to/.CodeEagle/config.yaml"]
    }
  }
}
```

Available MCP tools: `get_graph_overview`, `search_nodes`, `get_node_details`, `get_node_edges`, `get_service_structure`, `get_file_symbols`, `search_edges`, `get_project_guidelines`.

## Architecture

```
codeeagle/
├── cmd/codeeagle/          CLI entry point
├── internal/
│   ├── agents/             AI agents (planner, designer, reviewer, asker)
│   ├── cli/                Cobra command definitions
│   ├── config/             Configuration loading (viper)
│   ├── gitutil/            Git operations (branch detection, diffs)
│   ├── graph/              Knowledge graph interface + embedded store
│   ├── indexer/            Orchestrates parsing -> graph updates
│   ├── llm/               LLM provider implementations
│   ├── mcp/               MCP server (JSON-RPC over stdio)
│   ├── metrics/            Code quality metric calculators
│   ├── parser/             Language parsers (Go, Python, TS, JS, Java, HTML, MD, Makefile, Shell, Terraform, YAML)
│   └── watcher/            Filesystem watcher (fsnotify)
└── pkg/llm/               Public LLM client interface + provider registry
```

## Development

```bash
make build       # Build binary to bin/codeeagle
make test        # Run tests with race detector
make test-fast   # Run tests without race detector
make lint        # Run golangci-lint
make fmt         # Format code with gofmt
make tidy        # Tidy go modules
make install     # Install to $GOPATH/bin
```

## License

Apache License 2.0. See [LICENSE](LICENSE).
