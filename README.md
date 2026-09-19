# CodeEagle

CodeEagle is a CLI tool that indexes codebases into a knowledge graph and exposes AI agents for planning, design review, and code review — all grounded in deep codebase understanding.

It supports monorepos, multi-repo setups, and multi-language codebases (Go, Python, TypeScript, JavaScript, Java, Rust, C#, Ruby, HTML, Markdown, Makefile, Shell, Terraform, YAML). No external database required — the embedded graph store runs locally with zero setup.

## Features

- **Knowledge graph** of source code entities (functions, classes, interfaces, packages, services) and their relationships (calls, imports, implements, tests, etc.)
- **15 language parsers**: Go (stdlib AST), Python, TypeScript, JavaScript, Java, Rust, C# (with ASP.NET), Ruby (with Rails), HTML, Markdown, Makefile, Shell, Terraform, YAML, plus a manifest parser (go.mod, package.json, pyproject.toml, requirements.txt)
- **Document format extraction**: Text extraction from DOCX, PPTX, XLSX, ODT, ODS, ODP (pure Go, stdlib only) and PDF (`dslipak/pdf`). Documents are indexed, topic-extracted via LLM, and semantically searchable
- **Non-code file indexing**: Changelogs, design docs, CSVs, images, config templates — all indexed as Document nodes with optional LLM-based topic extraction and image description
- **Meeting transcripts**: Diarized recordings are indexed with speaker identification, topic segmentation, per-topic summaries, decisions, and follow-ups. Speakers arrive anonymous ("Person 1", "Person 2") and are resolved to durable people, shared with face recognition
- **Face detection & recognition** (optional, `-tags faces`): OpenCV DNN-based face detection with 128-dim embeddings, agglomerative clustering, KNN classification, person management, and EXIF metadata extraction
- **Cross-service dependency analysis**: API endpoint extraction, HTTP client call detection, import-to-manifest linking, cross-file interface implements resolution
- **Test coverage mapping**: automatic test file/function detection across 8 languages with `EdgeTests` linking to source counterparts
- **Code quality metrics**: cyclomatic complexity, lines of code, TODO/FIXME counts
- **Graph analysis queries**: unused code detection and test coverage reporting
- **AI agents** for planning, design, code review, and freeform Q&A — read-only, advisory, never modify code
- **Temporal tracking**: Every file node records `UpdatedAt` with Year/Month/Date graph nodes for date-based queries (e.g., "files modified in March 2024")
- **Duplicate file detection**: Content-hash-based identification of identical files across different paths, with `DuplicateOf` graph edges and `query duplicates` CLI command
- **Symlink tracking**: Automatic detection of symbolic links with `SymLink` graph edges pointing to resolved targets
- **Git-aware incremental sync** with branch tracking and diff-based updates
- **Smart non-git sync**: Skips unchanged files by comparing mtime against DB, with crash-resilient periodic state saving
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

Requires Go 1.27+ and a C compiler (gcc or clang) — needed for [tree-sitter](https://tree-sitter.github.io/tree-sitter/) parsing via CGO.

```bash
go install github.com/imyousuf/CodeEagle/cmd/codeeagle@latest
```

Or clone and build:

```bash
git clone https://github.com/imyousuf/CodeEagle.git
cd CodeEagle
make build           # Auto-detects OpenCV for face support
make build-faces     # Explicitly enable face detection (requires libopencv-dev)
make build-minimal   # Skip face detection
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
codeeagle query duplicates [--json]         Find duplicate files by content hash

codeeagle backpop [--all]                   Run linker phases on existing graph
codeeagle metrics [--file F] [--type T]     Show code quality metrics
codeeagle mcp serve                         Start MCP server (stdio transport)
codeeagle hook install                      Install git post-commit hook for auto-sync

codeeagle faces scan [dirs...]              Detect, cluster, assign faces in images
codeeagle faces clusters                    View/manage face clusters
codeeagle faces label <id> <name>           Assign person name to a cluster
codeeagle faces search <query>              Search by person or image
codeeagle faces merge/split                 Merge or split face clusters
codeeagle faces unlabeled                   Show unassigned clusters
codeeagle faces suggest                     Auto-suggest face assignments
codeeagle faces person [...]                Person CRUD (add, list, edit, delete)

codeeagle version                           Print version, commit, build date
codeeagle update [--check] [--force]        Check for and install updates
```

> **Note:** `codeeagle faces` commands require building with `-tags faces` and OpenCV 4 (`libopencv-dev`).

Global flags: `--config <path>`, `--db-path <path>`, `-p <project-name>`, `-v` (verbose).

### Meeting transcripts

```bash
codeeagle meetings sync                    # Enrich transcripts and index them
codeeagle meetings sync --dry-run          # Report scope and token cost, call no model
codeeagle meetings sync --limit 20         # Process a subset
codeeagle meetings sync --force            # Re-enrich already-indexed recordings
codeeagle meetings watch                   # Index new recordings as they appear

codeeagle meetings list                    # List indexed meetings
codeeagle meetings list --person Kevin     # Meetings a person attended
codeeagle meetings list --since 2026-03-01 # Meetings after a date
codeeagle meetings show <meeting-id>       # Participants, topics, decisions, follow-ups
codeeagle meetings people                  # People, with speaking time and follow-up counts
codeeagle meetings topics                  # Topics discussed, by meeting count
codeeagle meetings topics --themes         # The induced topic hierarchy
codeeagle meetings taxonomy                # Group topics into concepts (re-runnable)
codeeagle meetings taxonomy --rebuild      # Group from scratch instead of extending
codeeagle meetings migrate --from <branch> # Move a corpus indexed by an older version
codeeagle meetings actions --person Kevin  # Follow-ups owned by someone
codeeagle meetings actions --unassigned    # Follow-ups nobody owns

codeeagle meetings identify                # Review speakers that could not be identified
codeeagle meetings label "Person 3" Kevin --meeting <id>   # Assign one by hand
```

Recordings already indexed and unchanged are skipped on a content hash, so
re-running after a few new meetings costs almost nothing. `meetings watch`
sweeps on an interval for the same reason — an unchanged recording costs a file
read and no model call — and leaves a transcript alone until it has been idle
for a moment, so a meeting still being recorded is not indexed half-complete.

#### Supported formats

| Format | Written by | Speakers |
|--------|-----------|----------|
| `session.json` | local diarizing recorder | anonymous (`Person 1`, `You`) |
| `.vtt` (WebVTT) | Zoom, Teams live captions | named |
| `.srt` | Zoom and most recorders | named, when the tool writes them |
| `.docx` | Teams "Meeting Recording" transcript export | named |

Discovery walks the configured directory, so a folder of loose downloads and a
folder of per-session directories both work. Files that turn out to be
something else are counted and skipped, not reported as failures.

Where a transcript names its speakers, identification has nothing to work out
and the model call is skipped entirely — it would cost money to produce a worse
answer than the file already contains. Those identities are recorded as
resolved by the transcript rather than by inference, and the speaking-time
threshold is dropped for them: it exists to filter diarization debris, which a
named transcript does not have, and a colleague who said one word was still in
the meeting.

One meeting is often exported twice — a caption file and a Word document of the
same call. Duplicates are dropped, keeping the format that carries more, but
only when the dates agree as well as the names: a weekly standup exports to the
same filename every week.

#### How speakers are identified

Recording software separates voices but does not know whose they are, so it
labels them `Person 1`, `Person 2`, and those labels mean nothing outside a
single recording. Three signals resolve them:

1. **Microphone audio is the recording's owner**, by construction. This is
   structural rather than inferred, needs no model call, and is never wrong.
2. **People say each other's names**, and each usage points somewhere.
   "Kevin, what do you think?" names the next speaker; "Thanks, Kevin" names
   the previous one; "this is Saki" names the speaker. Resolving direction
   against turn order yields weighted votes for specific labels — computed
   without a model, so it is free and reproducible.
3. **A model adjudicates** the remaining ambiguity, given those votes and the
   transcript. Leaving a speaker unidentified is an expected outcome: a wrong
   name silently attributes one person's words to another, so the model is
   instructed to return nothing when the evidence is thin, and
   `meetings identify` lists what is left for a human.

Identity is resolved across recordings too. A transcriber spells the same name
differently between meetings ("Imran" and "Imron"), so variants are folded in
as aliases rather than creating a second person. Names that merely resemble one
another are kept apart.

Where both names carry a surname, the surname decides: "Chris Banner" and
"Christopher Stookey" are two colleagues, not one spelled two ways. Where one
name has no surname — all a diarized recording ever offers — the given name
settles it. Names written surname-first, as some directories export them, are
recognized as the same person.

People discovered in earlier meetings are fed back as known names for later
ones, so recordings are processed in the order the meetings happened.

#### How topics become a hierarchy

A meeting names its subject in whatever words suited that conversation, so a
flat vocabulary never converges: four meetings on one subject produce four
labels, each used once, and `HasTopic` indexes nothing.

Merging those labels into each other is the obvious fix and the wrong one. Fuse
"MCP server vs OAuth architecture" with "OBO token concern" and two different
discussions are misrepresented; leave them apart and neither is findable. The
dilemma only exists because one label is being asked to serve as both the
precise description and the searchable subject.

So the specific phrases stay as leaves, and each is placed under the concept it
is a facet of:

```
MCP (4)
  MCP authentication (3)
    - OAuth token revocation      1
    - OAuth token lifetimes       1
  - MCP tool access               3
```

Three mechanisms build this, and the third is what makes it hold:

1. Topic labels are asked for as reusable subjects — two to four words — with
   everything specific to the meeting going in the topic's summary.
2. A registry canonicalizes wordings, so "authentication for MCP" and "MCP
   authentication" resolve to one node.
3. **Each meeting is shown the current hierarchy** and names the concept its
   topics sit under. The tree therefore grows while meetings are indexed rather
   than needing a bulk rebuild, and a model extending a structure it can see
   produces a far better one than a model naming things blind.

`meetings taxonomy` also groups in bulk, applying one operation repeatedly:
group what is ungrouped, then group the groups. For large corpora it proposes
the concepts from a sample and then places every label against that fixed list
in batches, because a single request must name every label it places and its
output would otherwise truncate mid-answer.

It defaults to two rounds of grouping. A third measurably made things worse on a
real corpus: two rounds produced concepts worth searching by ("Model routing",
"Agent memory", "Tenant isolation"), while forcing a further pass to reach a
handful of top-level headings fused unrelated work. Use `--depth` for a corpus
that wants more.

#### Recurring meetings

Standing meetings are threaded together, so `meetings show` points at the
previous and next instance and an agent can follow a thread backwards. The
thread is inferred from who was in the room rather than from titles: a model's
titles vary between instances of one standing meeting, while the set of people
recurs reliably. A series needs at least two shared participants, and a gap of
more than two months breaks the chain rather than inventing continuity across
it.

#### Verifying what was extracted

Every decision and follow-up carries a verbatim quote, and whether that quote
really appears in the transcript is recorded on the node. `meetings show` marks
the ones that fail with `?`, and the agent tools say so in as many words — an
unverified quote is the one claim that should not be taken on trust.

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

docs:
  # provider: ollama          # auto-detected if omitted (ollama -> vertex-ai -> disabled)
  # model: qwen3.5:9b         # Ollama model for topic extraction
  # max_image_resolution: 1024
  # context_window: 120000
  exclude_extensions:
    - ".lock"
    - ".min.js"
    - ".min.css"
    - ".map"
    - ".wasm"
    - ".pb.go"
```

### Meeting transcripts

```yaml
transcripts:
  enabled: true
  sessions_dir: ~/.local/share/tomoe/sessions   # one directory per recording
  owner: "Your Name"              # microphone audio is always this person
  owner_aliases: ["Yourname"]     # spellings the transcriber produces

  provider: baseten               # baseten | ollama | anthropic | vertex-ai
  model: deepseek-ai/DeepSeek-V4.1-Flash
  api_key_command: "keyring get baseten.co you@example.com"
  # api_key_env: BASETEN_API_KEY  # or an environment variable
  # api_key: ...                  # or a literal, though config files get committed

  reasoning_effort: low           # see the note below
  max_tokens: 65536
  min_confidence: 0.70            # bar for automatic identification
  concurrency: 8

  roster:                         # optional, and markedly improves accuracy:
    - Kevin                       # it turns an open guess into a choice
    - Mona                        # among known colleagues

  exclude_names:                  # terms that read like names in conversation
    - Acme
    - Opal
```

Supplying a `roster` is the single most effective setting: it both raises
recall on people who are never introduced by name and settles on one spelling
for each of them.

`reasoning_effort` and `max_tokens` matter more than they look. A reasoning
model spends its token budget deliberating *before* emitting any answer, so too
small a `max_tokens` produces an **empty** reply rather than a short one — on a
32-minute transcript one model consumed 30,000 tokens reasoning and returned
nothing. Low effort measurably reduces cost with no loss of identification
quality; switching reasoning off entirely does hurt it, so that is used only as
an automatic fallback when a request exhausts its budget.

`meetings sync --dry-run` reports how many recordings, hours of speech, and
prompt tokens a run involves before any of it is spent.

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
| Document | Documentation file, office document (DOCX, PPTX, XLSX, ODT, ODS, ODP, PDF), or other non-code file |
| Directory | Directory in the file hierarchy |
| Topic | A subject, either as a meeting named it or as an induced concept grouping several (`topic_level`, `topic_depth`) |
| Person | Named person, identified by voice in meetings and/or by face in images |
| AIGuideline | AI-related guideline files (CLAUDE.md, etc.) |
| Year | Calendar year node (e.g., "2024") — part of date hierarchy |
| Month | Calendar month node (e.g., "2024-03") — part of date hierarchy |
| Date | Calendar date node (e.g., "2024-03-15") — part of date hierarchy |
| Meeting | A recorded meeting, with title, summary, duration, and participants |
| Speaker | A per-meeting diarization label; meaningful only once linked to a Person |
| TopicSegment | A span of one meeting about one topic, with its own summary |
| Decision | A choice a meeting settled on, with a supporting quote |
| ActionItem | A follow-up arising from a meeting, with owner and due date |

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
| HasTopic | Document has an extracted topic |
| AppearsIn | Person appears in an image |
| UpdatedOn | File node linked to its last-modified Date node |
| DuplicateOf | File has identical content (same content_hash + mime_type) as canonical file |
| SymLink | Symbolic link points to resolved target file |
| Attended | Person participated in a meeting |
| IdentifiedAs | Speaker resolves to a Person, with confidence, evidence, and method |
| AssignedTo | Action item is owned by a person |
| RaisedBy | Decision or action item was raised by a person |
| Mentions | Meeting or topic segment refers to a code entity or person |
| FollowsUp | Action item implements a decision, or a recurring meeting follows its previous instance |
| References | General cross-reference |
| Embeds | Struct embeds another type |

### Meeting storage scope

The graph partitions keys by git branch, so indexing a feature branch does not
disturb main's view of the code. Meetings are exempt: a meeting happened, and it
belongs to no branch. They are written under a fixed scope and included as a
fallback on the read path, so they are visible whichever branch is checked out —
otherwise switching branches would hide the entire history and the next sync
would re-index everything.

A corpus indexed by an older version moves across with
`codeeagle meetings migrate --from <branch>`. The scope appears only in the key
and never in the stored value, so it is a key rename rather than a re-index.

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
| `/codeeagle:codeeagle-meetings` | Search meetings for what was discussed, decided, and committed to |

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
│   ├── docs/              Document content extraction providers (Ollama, Vertex AI)
│   ├── indexer/            Orchestrates parsing -> graph updates
│   ├── llm/               LLM provider implementations
│   ├── mcp/               MCP server (JSON-RPC over stdio)
│   ├── metrics/            Code quality metric calculators
│   ├── linker/             Cross-service linker (11 phases: services, endpoints, API calls, deps, imports, implements, tests, calls, documents, duplicates, symlinks)
│   ├── faces/              Face detection & recognition (OpenCV DNN, clustering, KNN classification)
│   ├── queue/              Async job queue (face detection, clustering, document enrichment)
│   ├── parser/             Language parsers + generic fallback (document formats, images, text files)
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
