# CodeEagle

CodeEagle is a CLI tool that indexes codebases into a knowledge graph and exposes AI agents for planning, design review, and code review — all grounded in deep codebase understanding.

It supports monorepos, multi-repo setups, and multi-language codebases (Go, Python, TypeScript, JavaScript, Java, Rust, C#, Ruby, HTML, Markdown, Makefile, Shell, Terraform, YAML). No external database required — the embedded graph store runs locally with zero setup.

## Features

- **Knowledge graph** of source code entities (functions, classes, interfaces, packages, services) and their relationships (calls, imports, implements, tests, etc.)
- **15 language parsers**: Go (stdlib AST), Python, TypeScript, JavaScript, Java, Rust, C# (with ASP.NET), Ruby (with Rails), HTML, Markdown, Makefile, Shell, Terraform, YAML, plus a manifest parser (go.mod, package.json, pyproject.toml, requirements.txt)
- **Cross-service dependency analysis**: API endpoint extraction, HTTP client call detection, import-to-manifest linking, cross-file interface implements resolution
- **Test coverage mapping**: automatic test file/function detection across 8 languages with `EdgeTests` linking to source counterparts
- **Code quality metrics**: cyclomatic complexity, lines of code, TODO/FIXME counts
- **Graph analysis queries**: unused code detection and test coverage reporting
- **AI agents** for planning, design, code review, and freeform Q&A — read-only, advisory, never modify code
- **Git-aware incremental sync** with branch tracking and diff-based updates
- **MCP server** for integration with Claude Code and other MCP-compatible tools
- **LLM auto-summarization** of services and architectural patterns

## Installation

### Pre-built Binaries (Recommended)

Download the latest release for your platform from [GitHub Releases](https://github.com/imyousuf/CodeEagle/releases):

- Linux: `codeeagle-linux-amd64.tar.gz`, `codeeagle-linux-arm64.tar.gz`
- macOS: `codeeagle-darwin-amd64.tar.gz`, `codeeagle-darwin-arm64.tar.gz`

Extract and move to your PATH:

```bash
tar -xzf codeeagle-<platform>.tar.gz
sudo mv codeeagle /usr/local/bin/
```

To update to the latest version:

```bash
codeeagle update
```

### Build from Source

Requires Go 1.24+ and a C compiler (gcc or clang) — needed for [tree-sitter](https://tree-sitter.github.io/tree-sitter/) parsing via CGO.

```bash
go install github.com/imyousuf/CodeEagle/cmd/codeeagle@latest
```

Or clone and build:

```bash
git clone https://github.com/imyousuf/CodeEagle.git
cd CodeEagle
make build
# Binary: bin/codeeagle
```

## Quick Start

```bash
# 1. Initialize a project (creates .CodeEagle/ directory)
codeeagle init                 # quick setup with defaults
codeeagle init --interactive   # guided setup wizard

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
codeeagle init [--interactive|-i]            Initialize project config
codeeagle config                            View current configuration
codeeagle config edit                       Edit configuration interactively
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
codeeagle query edges --node <name>         Show relationships for a node
codeeagle query unused [--type T]           Find potentially unused functions/methods
codeeagle query coverage [--level L]        Show test coverage by file or function

codeeagle backpop [--all]                   Run linker phases on existing graph
codeeagle metrics [--file F] [--type T]     Show code quality metrics
codeeagle mcp serve                         Start MCP server (stdio transport)
codeeagle hook install                      Install git post-commit hook for auto-sync

codeeagle version                           Print version, commit, build date
codeeagle update [--check] [--force]        Check for and install updates
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
  - rust
  - csharp
  - ruby
  - html
  - markdown
  - makefile
  - shell
  - terraform
  - yaml

graph:
  storage: embedded

agents:
  llm_provider: claude-cli   # claude-cli, anthropic, or vertex-ai
  model: sonnet
  auto_link: true            # enable LLM-assisted cross-service edge detection
```

### LLM Providers

| Provider | Config | Auth |
|----------|--------|------|
| Claude CLI (default) | `llm_provider: claude-cli` | Claude Code installed and authenticated |
| Anthropic API | `llm_provider: anthropic` | `ANTHROPIC_API_KEY` env var |
| Vertex AI | `llm_provider: vertex-ai` | GCP Application Default Credentials + `project`, `location` |

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
| TestFile | Test file (detected by naming convention) |
| Function | Function or standalone method |
| Method | Method bound to a type/class |
| TestFunction | Test function (detected by naming convention or annotation) |
| Struct, Class | Data structures |
| Interface | Interface or abstract class (includes Python Protocol) |
| Enum, Constant | Enumerations, exported constants |
| Type | Type aliases and definitions |
| Module | Module (Ruby, Rust) |
| APIEndpoint | REST routes, gRPC services, ASP.NET endpoints, Rails routes |
| DBModel, DomainModel, ViewModel, DTO | Classified model types |
| Dependency | External dependency |
| Document | Documentation file (README, spec, etc.) |
| AIGuideline | AI-related guideline files (CLAUDE.md, etc.) |

### Edge Types

| Edge | Description |
|------|-------------|
| Contains | Parent contains child (Service -> File -> Function) |
| Imports | File/package imports a dependency |
| Calls | Function/method calls another (includes qualified callees like `Store.QueryNodes`) |
| Implements | Type implements interface (Go structural, Java/TS/C# nominal, Python Protocol) |
| DependsOn | Import-to-manifest linking, service-to-service dependencies |
| Tests | Test file/function tests a source file/function |
| Documents | Documentation file describes a code entity |
| Exposes | Service exposes an API endpoint |
| Consumes | Code makes HTTP client call to an API endpoint |
| Configures | Config file configures a service/deployment |
| Migrates | Migration file migrates a schema |
| References | General cross-reference |
| Embeds | Struct embeds another type |

### Storage

The embedded graph store uses [BadgerDB](https://github.com/dgraph-io/badger) with secondary indexes. Data is stored per-branch with fallback reads (current branch -> default branch). No external database required.

## Claude Code Integration

The recommended way to use CodeEagle with [Claude Code](https://docs.anthropic.com/en/docs/claude-code) is as a plugin. This gives Claude Code access to all CodeEagle commands — no MCP server configuration needed.

### Install as Plugin

```bash
# 1. Install CodeEagle CLI
go install github.com/imyousuf/CodeEagle/cmd/codeeagle@latest

# 2. Initialize and index your project
cd /path/to/your/project
codeeagle init
codeeagle sync

# 3. Add CodeEagle as a Claude Code plugin marketplace
/plugin marketplace add imyousuf/CodeEagle

# 4. Install the plugin
/plugin install codeeagle@imyousuf-CodeEagle
```

Once installed, Claude Code gains access to all CodeEagle skills. The skills teach Claude Code when and how to use each command — querying symbols, tracing dependencies, finding unused code, checking test coverage, running code review, etc.

### Available Skills

| Skill | What it does |
|-------|-------------|
| `/codeeagle:codeeagle` | Query the knowledge graph — symbols, interfaces, edges, unused code, coverage |
| `/codeeagle:codeeagle-sync` | Sync the graph with latest code changes, run linker phases |
| `/codeeagle:codeeagle-review` | Review code changes and diffs against codebase conventions |
| `/codeeagle:codeeagle-status` | Show indexing status and graph statistics |

### MCP Server (alternative)

For integration with other MCP-compatible tools, CodeEagle also exposes an MCP server:

```bash
codeeagle mcp serve
```

Available MCP tools: `get_graph_overview`, `search_nodes`, `get_node_details`, `get_node_edges`, `get_service_structure`, `get_file_symbols`, `search_edges`, `get_project_guidelines`, `query_file_symbols`, `query_interface_impl`, `query_node_edges`.

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
│   ├── linker/             Cross-service linker (7 phases: services, endpoints, API calls, deps, imports, implements, tests)
│   ├── parser/             Language parsers (Go, Python, TS, JS, Java, Rust, C#, Ruby, HTML, MD, + 5 more)
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
