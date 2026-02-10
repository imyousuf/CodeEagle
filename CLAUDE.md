# CodeEagle

## Project Vision

CodeEagle is a CLI tool that watches codebases (monorepos, multi-repo setups, or combinations), builds a **knowledge graph** of source code and code-related documentation, and exposes **non-coding AI agents** for planning, designing, and code review — all grounded in deep codebase understanding.

## Goals

### 1. Codebase Indexing & Knowledge Graph

Build and maintain a rich knowledge graph that captures:

**Source Code Entities**
- Repositories, services/modules, packages
- Files (source, config, infra-as-code, CI/CD workflows)
- Functions, methods, classes, structs, types, interfaces, enums
- Constants, exported variables
- API endpoints (REST routes, gRPC services, GraphQL schemas)
- Database models, schema migrations
- Dependencies (go.mod, package.json, pyproject.toml, requirements.txt, pom.xml, build.gradle)

**Documentation Entities**
- READMEs, tech specs, design docs, ADRs
- Inline doc comments (godoc, JSDoc, Python docstrings)
- Architecture diagrams (reference/link tracking)
- CLAUDE.md and similar development guideline files

**Relationships**
- `CONTAINS` — repo -> service -> package -> file -> symbol
- `IMPORTS` / `DEPENDS_ON` — inter-package, inter-service, external deps
- `CALLS` — function call graph (intra-service)
- `IMPLEMENTS` — struct -> interface, class -> abstract class
- `EXPOSES` — service -> API endpoint
- `CONSUMES` — service -> external API / other service endpoint
- `DOCUMENTS` — doc file -> code entity
- `TESTS` — test file/function -> source file/function
- `MIGRATES` — migration -> database schema
- `CONFIGURES` — config file -> service/deployment

**Code Quality Metrics** (attached to graph nodes)
- Cyclomatic complexity per function
- Lines of code per file/package/service
- Test coverage percentage
- Linting issue counts (by severity)
- Dependency freshness / known vulnerabilities
- TODO/FIXME/HACK counts

### 2. File Watching & Incremental Updates

- Watch configured repositories for file changes using filesystem events
- Incrementally update the knowledge graph on change (not full rebuild)
- Support git-aware change detection (branch tracking, diff-based updates)
- Handle multi-language codebases: Go, Python, TypeScript, JavaScript, Java, Rust, C#, Ruby, HTML, Markdown, Makefile, Shell, Terraform, YAML, and extensible to others
- Respect `.gitignore` and configurable exclude patterns

### 3. CLI Interface

```
codeeagle init                          # Initialize project config
codeeagle sync [--full]                 # Sync knowledge graph (incremental or full)
codeeagle watch                         # Start watching and building/updating the knowledge graph
codeeagle status                        # Show indexing status, graph stats

codeeagle agent plan <query>            # Ask the planning agent a question
codeeagle agent design <query>          # Ask the design agent a question
codeeagle agent review <query>          # Ask the code review agent a question
codeeagle agent review --diff <ref>     # Review changes in a git diff/PR

codeeagle query [--type T] [--name N]   # Query the knowledge graph
codeeagle query symbols --file <path>   # List symbols in a file
codeeagle query interface --name <name> # Show interface and implementors
codeeagle query edges --node <name>     # Show relationships for a node
codeeagle query unused [--type T]       # Find potentially unused functions/methods
codeeagle query coverage [--level L]    # Show test coverage by file or function

codeeagle backpop [--all]               # Run linker phases on existing graph
codeeagle metrics [service|file|func]   # Show code quality metrics
codeeagle mcp serve                     # Start MCP server (stdio transport)
```

### 4. Non-Coding AI Agents

All agents are grounded in the knowledge graph — they do NOT modify code, they advise.

**Planning Agent**
- Impact analysis: "What would be affected if I change X?"
- Dependency mapping: "What depends on service Y?"
- Scope estimation: "What files/services does feature Z touch?"
- Change risk assessment based on complexity and test coverage

**Design Agent**
- Architecture pattern recognition: "How is auth handled across services?"
- API design review: "Is this new endpoint consistent with existing patterns?"
- Suggest patterns based on what the codebase already uses
- Cross-service consistency checks

**Code Review Agent**
- Review diffs against codebase conventions and patterns
- Flag deviations from established patterns
- Identify missing tests for changed code paths
- Highlight complexity hotspots in modified code
- Security pattern checks (auth, input validation, secrets)

### 5. Multi-Language Support

Language parsing and graph extraction:
- **Go** — AST via `go/ast`, `go/parser`; struct field type resolution for deeper call graphs
- **Python** — tree-sitter; Protocol detection (`typing.Protocol` -> NodeInterface)
- **TypeScript** — tree-sitter; test detection (`.test.ts`, `.spec.ts`)
- **JavaScript** — tree-sitter (separate grammar from TypeScript, covers CommonJS/ESM)
- **Java** — tree-sitter (classes, interfaces, annotations, packages, Maven/Gradle deps)
- **Rust** — tree-sitter; traits, impls, modules, test detection (`#[test]`, `test_` prefix)
- **C# / ASP.NET** — tree-sitter; attributes, route annotations (`[HttpGet]`, `[Route]`), test detection (`[Fact]`, `[Test]`)
- **Ruby / Rails** — tree-sitter; modules, Rails routes (`routes.rb`), controllers, test detection (`_spec.rb`, `_test.rb`)
- **HTML / Templates** — `golang.org/x/net/html`; component references, includes, template variables
- **Markdown** — line-based parsing (headings, links, code blocks, front matter); cross-reference links to source files and other docs
- **Makefile** — line-based parsing of targets, variables, includes, .PHONY declarations
- **Shell** (bash/sh) — tree-sitter bash grammar; functions, variables, exports, source imports, shebang detection
- **Terraform** (HCL) — tree-sitter HCL grammar; resources, data sources, modules, variables, outputs, providers, locals
- **YAML** — content-aware dialect detection for GitHub Actions workflows, Ansible playbooks/roles, and generic YAML configs
- **Manifest** — FilenameParser for `go.mod`, `package.json`, `pyproject.toml`, `requirements.txt`
- Extensible parser interface for adding new languages

### 6. Configuration

Project config lives in `.codeeagle.yaml` (or similar) at the project root:

```yaml
project:
  name: "opal-app"

repositories:
  - path: /home/user/projects/opal-app
    type: monorepo
  - path: /home/user/projects/shared-lib
    type: single

watch:
  exclude:
    - "**/node_modules/**"
    - "**/.git/**"
    - "**/vendor/**"
    - "**/__pycache__/**"
    - "**/dist/**"
    - "**/build/**"

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
  storage: embedded  # embedded (BadgerDB)

agents:
  llm_provider: claude-cli  # claude-cli, anthropic, or vertex-ai
  model: sonnet
  auto_link: true           # LLM-assisted cross-service edge detection
  # api_key: sk-...          # for direct Anthropic API
  # project: my-gcp-project  # for Vertex AI
  # location: us-central1    # for Vertex AI
```

## Architecture

```
codeeagle/
├── cmd/codeeagle/          # CLI entry point
├── internal/
│   ├── agents/             # AI agents (planner, designer, reviewer, asker) + MCP query tools
│   ├── cli/                # Cobra command definitions (sync, watch, query, backpop, etc.)
│   ├── config/             # Configuration loading and validation (viper)
│   ├── gitutil/            # Git operations (branch detection, diffs)
│   ├── graph/              # Knowledge graph interface + embedded store (BadgerDB)
│   ├── indexer/            # Orchestrates parsing -> graph updates + LLM summarization
│   ├── linker/             # Cross-service linker (7 phases: services, endpoints, API calls, deps, imports, implements, tests)
│   ├── llm/                # LLM provider implementations (Anthropic, Vertex AI, Claude CLI)
│   ├── mcp/                # MCP server (JSON-RPC over stdio)
│   ├── metrics/            # Code quality metric calculators
│   ├── parser/             # Language parsers
│   │   ├── parser.go       # Parser + FilenameParser interfaces
│   │   ├── golang/         # Go parser (stdlib go/ast, struct field type resolution)
│   │   ├── python/         # Python parser (tree-sitter, Protocol detection)
│   │   ├── typescript/     # TypeScript parser (tree-sitter)
│   │   ├── javascript/     # JavaScript parser (tree-sitter)
│   │   ├── java/           # Java parser (tree-sitter)
│   │   ├── rust/           # Rust parser (tree-sitter)
│   │   ├── csharp/         # C# parser (tree-sitter, ASP.NET support)
│   │   ├── ruby/           # Ruby parser (tree-sitter, Rails support)
│   │   ├── html/           # HTML parser (golang.org/x/net/html)
│   │   ├── markdown/       # Markdown parser (line-based)
│   │   ├── makefile/       # Makefile parser (line-based, FilenameParser)
│   │   ├── shell/          # Shell parser (tree-sitter bash)
│   │   ├── terraform/      # Terraform parser (tree-sitter HCL)
│   │   ├── yaml/           # YAML parser (GHA, Ansible, generic)
│   │   └── manifest/       # Manifest parser (go.mod, package.json, pyproject.toml, requirements.txt)
│   └── watcher/            # Filesystem watcher (fsnotify + gitignore)
├── pkg/llm/                # Public LLM client interface + provider registry
├── testdata/               # Test fixtures
├── go.mod
├── go.sum
├── Makefile
└── CLAUDE.md               # This file
```

## Tech Stack

- **Language:** Go 1.24+
- **CLI Framework:** cobra
- **File Watching:** fsnotify
- **Go AST Parsing:** stdlib `go/ast`, `go/parser`, `go/types`
- **Tree-sitter:** for Python, TypeScript, JavaScript, Java, Rust, C#, Ruby, Shell, Terraform parsing (via `github.com/smacker/go-tree-sitter` bindings)
- **Graph Storage:** Embedded (BadgerDB with secondary indexes), branch-aware with fallback reads
- **LLM Integration:** Anthropic API (direct) + Vertex AI (Claude & Gemini on GCP), extensible to others
- **Config:** viper (YAML config loading)
- **Testing:** stdlib `testing` + testify

## Development Guidelines

### Build & Test
```bash
make build          # Build the CLI binary
make test           # Run all tests
make test-fast      # Run tests without race detector
make lint           # Run golangci-lint
make fmt            # Format code
```

### Test Ground
Use `/home/imyousuf/projects/opal-app` as the primary test codebase for integration testing and validating the knowledge graph. This is a large multi-language monorepo (Go, Python, TypeScript) with 45+ services, extensive docs, and complex inter-service dependencies — representative of real-world usage.

### Reference Project

Use [agentic-test-runner](https://github.com/imyousuf/agentic-test-runner) (`/home/imyousuf/projects/gopath/src/github.com/imyousuf/agentic-test-runner`) as architectural inspiration. It demonstrates the Go patterns to follow in this project:

- **Project layout:** `cmd/` entry point, `internal/` for implementation, `pkg/` for public interfaces
- **CLI framework:** cobra with persistent flags, subcommand registration, viper flag binding
- **Config management:** viper with defaults -> config file -> env vars -> CLI flags hierarchy, struct-based config with `Unmarshal`
- **LLM integration:** provider-agnostic `pkg/llm.Client` interface with a provider registry pattern (`RegisterProvider` + factory functions), support for multiple backends (API and CLI)
- **Agent loop:** conversation history management, tool execution with results fed back, iteration limits, timeout via context, metrics tracking (tokens, tool calls, duration)
- **Tool registry:** interface-driven tool system (`Name()`, `Description()`, `Parameters()`, `Execute()`) with a central registry
- **Error handling:** wrapped errors with `fmt.Errorf("context: %w", err)`, early returns, no panic
- **Testing:** stdlib `testing` (no testify), table-driven tests
- **Makefile:** `build`, `test`, `lint`, `fmt`, `tidy` targets with `LDFLAGS="-s -w"`

### Principles
- Incremental graph updates over full rebuilds
- Parsers are pluggable — adding a new language should only require implementing the parser interface
- Agents are read-only — they query the graph and advise, never modify code
- Embedded storage by default — no external DB dependency for basic usage
- CLI-first — no web UI (keep it terminal-native)
