# Models CodeEagle uses

Every model this tool can call, what it is used for, and where the default is
written. When a vendor releases a new version, this page is the checklist: each
row names the file to edit, so nothing is missed and no default quietly goes
stale.

Last reviewed: 2026-09-20.

---

## At a glance

| Job | Provider | Default model | Set in |
|---|---|---|---|
| Agents (plan, design, review, ask) | `claude-cli` | whatever `claude` resolves | `internal/config/config.go` |
| Agents, direct API | `anthropic` | `claude-sonnet-5` | `internal/llm/anthropic.go` |
| Agents, on GCP | `vertex-ai` | `gemini-3.8-flash` | `internal/llm/vertexai.go` |
| Meeting enrichment | `baseten` | `deepseek-ai/DeepSeek-V4.1-Flash` | `internal/config/config.go` |
| Speaker adjudication, topic relating, search re-ranking | Jev | `jev-1.13.0` | `pkg/jev/client.go` |
| Document and image description | `ollama` | `qwen3.5:9b` | `internal/docs/ollama.go` |
| Document and image description, hosted | `baseten` | `zai-org/GLM-5.3-Flash` | `internal/docs/baseten.go` |
| Document and image description, on GCP | `vertex-ai` | `gemini-3.8-flash` | `internal/docs/vertex.go` |
| Embeddings (semantic search) | `ollama` | `nomic-embed-text-v2-moe` | `internal/embedding/ollama.go` |
| Embeddings, on GCP | `vertex-ai` | `gemini-embedding-001` | `internal/embedding/vertex.go` |

Nothing here is required. With no model configured at all, indexing, `query`,
`meetings search` and the graph work; what you lose is summarisation,
description, semantic search and the judged features.

---

## Language models

These generate text: meeting summaries, topic labels, decisions, follow-ups,
and the agents' answers.

### Anthropic (`agents.llm_provider: anthropic`)

Current family. `claude-sonnet-5` is the default and the right starting point.

| Model | Identifier | When to pick it |
|---|---|---|
| Opus 5 | `claude-opus-5` | The hardest reasoning. Slower and dearer. |
| Sonnet 5 | `claude-sonnet-5` | **Default.** The balance most work wants. |
| Haiku 4.5 | `claude-haiku-4-5-20251001` | Fastest and cheapest. Good for bulk. |

There is no Haiku 5 at the time of writing; 4.5 is the current Haiku.

The short aliases `sonnet`, `opus` and `haiku` also work, and any identifier
beginning `claude-sonnet`, `claude-opus` or `claude-haiku` is routed correctly
(`internal/llm/claude_cli.go`), so a version bump does not need that file
touched.

### Claude CLI (`agents.llm_provider: claude-cli`)

Shells out to the `claude` binary and uses whatever that is configured with, so
there is no version to maintain here. No API key needed. The simplest option if
you already have Claude Code installed.

### Vertex AI (`agents.llm_provider: vertex-ai`)

Runs Gemini on your own GCP project. Default `gemini-3.8-flash`. Requires
Application Default Credentials — see `installation.md`.

**Gemini 3.8 Flash is the only Gemini chat model this project uses.** Earlier
releases are not a supported fallback and should not be configured; if 3.8
Flash is unavailable to you, that is worth fixing rather than working around.

**Location matters as much as the model.** Newer Gemini releases are served
from `global` and are absent from regional endpoints, so the default location
is `global`. `gemini-3.8-flash` returns 404 in `us-central1` while working
perfectly there. If a model you can see in the catalogue returns "was not
found", try `location: global` before concluding the identifier is wrong --
that mistake cost an afternoon here.

**Claude is not reachable here.** Anthropic publishes `claude-opus-5`,
`claude-sonnet-5` and `claude-haiku-4-5` on Vertex, and they appear in the
catalogue, but this client is built on Google's `genai` SDK, which addresses
`publishers/google` and speaks Gemini's API. Claude on Vertex needs the
Anthropic messages API. Use the `anthropic` provider or the Claude CLI
instead.

### Baseten (`transcripts.provider: baseten`)

OpenAI-compatible, at `https://inference.baseten.co/v1`. This is what enriches
meetings, where the work is bulk summarisation over hundreds of transcripts and
cost dominates.

| Model | Identifier | Notes |
|---|---|---|
| DeepSeek V4.1 Flash | `deepseek-ai/DeepSeek-V4.1-Flash` | **Default.** Chosen on measured cost against quality. |
| GLM 5.3 Flash | `zai-org/GLM-5.3-Flash` | Comparable on text. **Accepts images**, which most
  open-weight models on Baseten do not — so it is also the default for the hosted
  document and image description provider. Verified against the live API 2026-09-21. |

Set `transcripts.max_tokens` generously — reasoning models spend their budget
on reasoning first, and too small a ceiling returns an empty reply rather than
an error. 65536 is a sane floor.

### Ollama (`docs.provider: ollama`)

Local, free, private. `qwen3.5:9b` for describing documents and images. Nothing
leaves the machine. `docs.context_window` defaults to 120000, which covers
documents up to roughly 336 KB.

---

## Decision model

### Jev (`transcripts.jev_api_key`)

Not a language model. It answers bounded questions with a **calibrated
probability** rather than generating prose. Three jobs use it: speaker
adjudication, topic relating, and search re-ranking. See `jev.md` for what it
is and how it is used.

| Version | Notes |
|---|---|
| `jev-1.13.0` | **Pinned default** (`pkg/jev/client.go`). |
| `jev-latest` | Resolves to the above today. Do not use as a default. |
| `jev-preview` | Also resolves to 1.13.0 today. |

**Pin the version, never track `latest`.** Confidence thresholds are tuned
against a specific version, and a silent weight change moves every gate. The
recorded test corpus stores the version that answered it, and the suite fails
if the pin changes without re-recording — see `pkg/jev/RELEASING.md`.

---

## Embedding models

These turn text into vectors for semantic search. They are **not**
interchangeable: vectors from different models are not comparable, and mixing
them ranks unrelated things against each other.

| Model | Dimensions | Provider |
|---|---|---|
| `nomic-embed-text-v2-moe` | 768 | Ollama (default, local, free) |
| `gemini-embedding-001` | 768 (via MRL) | Vertex AI |

Changing the embedding model invalidates the whole index. CodeEagle detects
this — the provider, the model and an embeddable-text version are recorded in
the index and compared on open — and forces a rebuild rather than mixing.
`codeeagle status` says when the index is behind.

Rebuilding is `codeeagle vectorindex`. On a corpus of ~54,000 nodes that is
about eight minutes against a local Ollama, and free.

---

## Updating a version

1. Change the default in the file named in the table above.
2. `grep -rn "<old-identifier>" --include=*.go --include=*.md .` — tests pin
   defaults, and a stale assertion is how a bump gets reverted by accident.
3. If an **embedding** model changed, bump `EmbeddableTextVersion` in
   `internal/vectorstore/chunk.go` so existing indexes rebuild instead of
   mixing.
4. If the **Jev** version changed, re-record the corpus with `make jev-record`
   before shipping, or `TestCorpusProvenance` fails — correctly, because the
   fixtures were answered by a version that no longer runs.
5. Re-run any confidence threshold you rely on. `transcripts.min_confidence`
   and the topic-relating gates were tuned by measurement against a specific
   model; a new version moves them.
6. Update the date at the top of this page.

---

## Verified

Every default in this document was exercised against the live provider on the
date above, not taken from a vendor page:

| Checked | Result |
|---|---|
| `vertex-ai` / `gemini-3.8-flash` (in `global`) | answered |
| `vertex-ai` with only a project set | answered — defaults resolve |
| `baseten` / `deepseek-ai/DeepSeek-V4.1-Flash` | answered |
| `ollama` / `qwen3.5:9b` | answered |
| `ollama` / `nomic-embed-text-v2-moe` | 768-dim vector returned |
| `claude-cli` | answered |
| `claude-sonnet-5`, `claude-opus-5`, `claude-haiku-4-5-20251001` | all answered |
| Jev `jev-1.13.0` | all three primitives answered |

`claude-haiku-5` was checked and **does not exist** — the catalogue rejects it.
Haiku 4.5 is the current Haiku.

Found wrong by this exercise and corrected: `gemini-2.0-flash`, which had been
the default, no longer resolves anywhere. `gemini-3.8-flash` does exist and is
now the only Gemini chat model used — it was briefly recorded here as
non-existent because it was only tried in `us-central1`, where it 404s. The
fault was the location, not the name, and the default location moved to
`global` as a result.

The Anthropic identifiers were checked against the Vertex catalogue, which
lists eleven: Opus 4.5 through 4.8 and 5, Sonnet 4.5, 4.6 and 5, Haiku 4.5, and
two Fable builds. The latest of each family is what this document names, and
there is no Haiku 5.

Also established: Claude models cannot be reached through the `vertex-ai`
provider at all, in any location, because the client speaks only Gemini's API.
That contradicted what the architecture notes claimed, and they have been
corrected.

Not verified: the direct `anthropic` provider, for want of an API key on the
machine used. Its identifiers are the ones both the Claude CLI and the Vertex
catalogue accept, so they are right; the code path is not exercised.

The lesson worth keeping: a default nobody calls goes stale silently, and a
model identifier is only true on the day it is tested. Re-run this table when
changing one.
