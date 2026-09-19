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
- Office documents: DOCX, PPTX, XLSX (text extracted from ZIP/XML, pure Go stdlib)
- OpenDocument files: ODT, ODS, ODP (text extracted from content.xml)
- PDF files (text extracted via `github.com/dslipak/pdf`, BSD-3, pure Go)
- Images: PNG, JPG, GIF, WebP, BMP, TIFF (LLM-described when provider available)
- Inline doc comments (godoc, JSDoc, Python docstrings)
- Architecture diagrams (reference/link tracking)
- CLAUDE.md and similar development guideline files

**Meeting Transcript Entities**
- Meetings (diarized recordings), with LLM-generated titles and summaries
- Speakers (per-recording diarization labels), resolved to Person nodes
- Topic segments — spans of a meeting about one subject, each summarized from
  that subject's point of view
- Decisions, with rationale and a verbatim supporting quote
- Action items / follow-ups, with owner and due date

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
- `HAS_TOPIC` — document -> extracted topic (via LLM)
- `APPEARS_IN` — person -> image
- `UPDATED_ON` — file -> date node (Year/Month/Date hierarchy)
- `DUPLICATE_OF` — file -> canonical file with identical content (same content_hash + mime_type)
- `SYM_LINK` — symlink file -> resolved target file
- `ATTENDED` — person -> meeting
- `IDENTIFIED_AS` — speaker -> person (carries confidence, evidence, method)
- `ASSIGNED_TO` — action item -> person
- `RAISED_BY` — decision/action item -> person
- `MENTIONS` — meeting/topic segment -> code entity or person
- `FOLLOWS_UP` — action item -> decision; recurring meeting -> its previous instance
  (inferred from the participant set, which recurs more reliably than a title)

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
- **Temporal tracking**: Every node records `UpdatedAt` (file modification time). Year/Month/Date nodes form a date hierarchy linked via `UpdatedOn` edges, enabling queries like "files modified in March 2024"
- **Smart sync for non-git directories**: Skips unchanged files by comparing file mtime against DB's `UpdatedAt`, avoiding brute-force re-indexing
- **Auto-backpop**: On first sync after upgrade, automatically populates `UpdatedAt` for existing nodes from file mtimes
- **Crash-resilient sync state**: Sync state is saved periodically (every 10 files) during directory walks to minimize progress loss on interruption

### 3. CLI Interface

```
codeeagle init [--interactive|-i]       # Initialize project config
codeeagle config                        # View current configuration
codeeagle config edit                    # Edit configuration interactively
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
codeeagle query duplicates [--json]     # Find duplicate files by content hash

codeeagle rag <query>                   # Semantic search over the knowledge graph
codeeagle backpop [--all]               # Run linker phases on existing graph
codeeagle metrics [service|file|func]   # Show code quality metrics
codeeagle mcp serve                     # Start MCP server (stdio transport)

codeeagle faces scan [dirs...]         # Detect, cluster, and assign faces in images
codeeagle faces clusters               # View face clusters
codeeagle faces label <id> <name>      # Assign person name to a cluster
codeeagle faces search <query>         # Search by person or image
codeeagle faces merge <ids...>         # Merge face clusters
codeeagle faces split <id> [...]       # Split a face cluster
codeeagle faces unlabeled              # Show unassigned clusters
codeeagle faces suggest                # Auto-suggest face assignments
codeeagle faces person [...]           # Person CRUD (add, list, edit, delete)

codeeagle meetings sync [--dry-run] [--limit N] [--force]   # Index transcripts
codeeagle meetings watch [--interval D] [--settle D]       # Index new recordings live
codeeagle meetings list [--person P] [--since DATE]        # List meetings
codeeagle meetings show <id>            # Participants, topics, decisions, follow-ups
codeeagle meetings people               # People, speaking time, follow-up counts
codeeagle meetings topics [--themes]    # Topics, flat or as the induced hierarchy
codeeagle meetings taxonomy [--rebuild] [--depth N]   # Group topics into concepts
codeeagle meetings migrate --from <branch>            # Move an older corpus into scope
codeeagle meetings actions [--person P] [--unassigned]     # Follow-ups
codeeagle meetings identify             # Review unidentified speakers
codeeagle meetings label <label> <name> --meeting <id>     # Assign by hand

codeeagle version                       # Print version, commit, build date
codeeagle update [--check] [--force]    # Check for and install updates
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

### 4a. Transcript Formats

Meetings are recorded by whatever tool the participants used, so indexing reads
several layouts through one `transcript.Format` interface: the local recorder's
diarized `session.json`, WebVTT, SRT, and the Word document Teams exports for a
meeting recording. Discovery walks for any of them, so loose downloads and
per-session directories both work.

The distinction that matters is whether the file names its speakers. A local
recording hears distinct voices and calls them "Person 1"; a conferencing
platform knows who was in the call. Where names are present the identification
pass is skipped entirely — it would cost a model call to produce a worse answer
than the file already contains — and those identities are marked as resolved by
the transcript. The speaking-time threshold is also dropped for them, since it
exists to filter diarization debris that a named transcript does not have.

### 4b. Meeting Transcript Understanding

Transcripts arrive diarized but anonymous: voices are labelled "Person 1",
"Person 2", and those labels are meaningful only within one recording.
Resolving them is most of the work, and it rests on three signals:

1. **The microphone anchor.** Mic audio is by construction whoever made the
   recording. This is structural, costs no model call, and cannot be wrong.
   In the reference corpus it holds without exception and covers half of all
   speech.
2. **Directional name evidence, computed deterministically.** People say each
   other's names constantly, and each usage points somewhere: "Kevin, what do
   you think?" names the next speaker, "Thanks, Kevin" the previous one, "this
   is Saki" the speaker themselves. Resolving direction against turn order
   yields weighted votes for specific labels. This is done without a model — it
   is free, reproducible, and a small model asked to track twenty anonymous
   labels across an hour of talk loses the thread.
3. **A model adjudicates what is left**, given those votes and the transcript.
   Its job is the narrow one it is good at: judging which candidates are real
   and breaking ties.

Design constraints that came out of measuring the real corpus:

- **Most speaker labels are noise.** 85% carry under five seconds of speech —
  diarization hands a fresh label to every "mm-hmm" — while the eight busiest
  labels in a meeting hold over 99% of what was said. Participants are
  therefore separated from debris by speaking time before anything else
  happens, or the graph fills with thousands of phantom people.
- **Transcription mangles names.** One person appears as both "Imran" and
  "Imron"; honorifics get absorbed ("Rupak bhai" becomes "Rupad Bai"). Matching
  tolerates those edits, but refuses to merge names that merely look alike:
  conflating two people silently misattributes one person's words, which is
  worse than recording one person under two spellings.
- **Unidentified is a valid answer.** A wrong name is worse than no name, so
  the model is instructed to return nothing when evidence is thin, and
  `meetings identify` lists what remains for a human.
- **Claims are checkable.** Every decision and follow-up carries a verbatim
  quote, and whether that quote is really in the transcript is recorded on the
  node. The CLI and agent tools flag the ones that fail.
- **A flat topic vocabulary never converges.** Each meeting names its subject in
  its own words, so labels almost never collide and `HasTopic` indexes nothing.
  Merging the labels is the wrong fix: it fuses distinct discussions. Instead the
  phrases stay as leaves under the concept they are facets of, and each meeting
  is shown the current hierarchy so it can place its topics inside it. Structure
  therefore forms while indexing, not only in a bulk rebuild.
- **More hierarchy is not better hierarchy.** Two rounds of grouping produced
  concepts worth searching by; a third, chasing a tidy top level, mis-filed them
  while still reading plausibly. Depth is a choice, defaulting to two.
- **Meetings are not branch-scoped.** They live outside the per-branch key scopes
  and are added as a fallback read, so they survive branch switches.
- **Compare surnames when both names have one.** Matching on given names alone
  was right for diarized transcripts, which offer nothing else, but against full
  names from a conferencing platform it merged distinct colleagues who happened
  to share a first name.
- **A file that is not a transcript is not a failure.** Scanning a folder of
  mixed downloads is normal; unrecognized files are counted and skipped.

Enrichment runs as two passes: identity first, then content read with real
names substituted in. The order matters — "Person 3 will update the schema" is
not an assignable follow-up, while "Kevin will update the schema" is.

People identified in earlier meetings are fed back as known names for later
ones, so recordings are processed in the order the meetings happened.

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

transcripts:
  enabled: true
  sessions_dir: ~/.local/share/tomoe/sessions
  owner: "Your Name"          # microphone audio is always this person
  owner_aliases: ["Yourname"] # spellings the transcriber produces
  provider: baseten           # baseten | ollama | anthropic | vertex-ai
  model: deepseek-ai/DeepSeek-V4.1-Flash
  api_key_command: "keyring get baseten.co you@example.com"
  reasoning_effort: low       # low cuts cost without hurting identification
  max_tokens: 65536           # must be generous: reasoning is spent first
  min_confidence: 0.70
  concurrency: 8
  roster: ["Kevin", "Mona"]   # optional, and markedly improves accuracy
  exclude_names: ["Acme"]     # terms that read like names in conversation

docs:
  # provider: ollama          # auto-detected if omitted (ollama -> vertex-ai -> disabled)
  # model: qwen3.5:9b         # Ollama model for topic extraction
  # max_image_resolution: 1024
  # context_window: 120000
  # disable_thinking: false
  exclude_extensions:
    - ".lock"
    - ".min.js"
    - ".min.css"
    - ".map"
    - ".wasm"
    - ".pb.go"
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
│   ├── docs/               # Document content extraction providers (Ollama, Vertex AI) with topic extraction + caching
│   ├── linker/             # Cross-service linker (11 phases: services, endpoints, API calls, deps, imports, implements, tests, calls, documents, duplicates, symlinks)
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
│   │   ├── generic/        # Generic fallback parser for non-code files (text, images, directories, document formats)
│   │   └── manifest/       # Manifest parser (go.mod, package.json, pyproject.toml, requirements.txt)
│   ├── faces/              # Face detection & recognition (OpenCV DNN, Caffe SSD + ONNX SFace, agglomerative clustering, KNN classification)
│   ├── queue/              # Async job queue with worker pool (face detection, clustering, document enrichment)
│   ├── transcript/         # Meeting transcripts: loading, speaker identification, enrichment, graph projection
│   └── watcher/            # Filesystem watcher (fsnotify + gitignore)
├── pkg/llm/                # Public LLM client interface + provider registry
├── testdata/               # Test fixtures
├── go.mod
├── go.sum
├── Makefile
└── CLAUDE.md               # This file
```

## Tech Stack

- **Language:** Go 1.27+
- **CLI Framework:** cobra
- **File Watching:** fsnotify
- **Go AST Parsing:** stdlib `go/ast`, `go/parser`, `go/types`
- **Tree-sitter:** for Python, TypeScript, JavaScript, Java, Rust, C#, Ruby, Shell, Terraform parsing (via `github.com/smacker/go-tree-sitter` bindings)
- **Document Extraction:** OOXML/ODF via stdlib `archive/zip` + `encoding/xml`; PDF via `github.com/dslipak/pdf` (pure Go)
- **Image Processing:** Downscaling (aspect-ratio preserving, max 1024px), LLM-based image description (Ollama/Vertex AI)
- **Face Detection:** OpenCV DNN (Caffe SSD detector + ONNX SFace recognizer) via `gocv.io/x/gocv`; 128-dim L2-normalized embeddings; requires `-tags faces` build and `libopencv-dev`
- **Face Classification:** KNN-based with temporal decay, agglomerative hierarchical clustering, majority voting, auto-assignment at high confidence
- **Graph Storage:** Embedded (BadgerDB with secondary indexes), branch-aware with fallback reads; separate face.db for face embeddings/clusters
- **LLM Integration:** Anthropic API (direct), Vertex AI (Claude & Gemini on GCP), Baseten (OpenAI-compatible: GLM, DeepSeek, Kimi), Ollama, Claude CLI — extensible via a provider registry
- **Structured output:** Providers that can enforce a JSON Schema do so (`llm.StructuredClient`); those that cannot are asked for JSON and their reply is salvaged
- **Meeting transcripts:** Diarized JSON, content-sniffed; deterministic name-hint extraction with Jaro-Winkler variant matching, LLM adjudication, cross-recording identity resolution
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
- Never assert an identity the evidence does not support — an unidentified
  speaker is a correct outcome, a misattributed one corrupts the graph silently
- Ground extracted claims in verbatim quotes, and record whether the quote
  checks out rather than assuming it does
