# Artifact Relations: Discovered Links With Always-Current Briefs

## Problem

An agent working over a codebase, its documents and the meetings about it
spends most of its tokens in three places: finding the right material,
reading whole files to understand fragments, and then checking whether what
it read is still true. CodeEagle holds documents and meetings in one graph
but barely connects them:

- A markdown link becomes a `Dependency` node named after the target path
  and is never resolved to the target `Document`, so documents have no
  incoming edges from each other.
- Markdown sections carry a heading and a line number but no text, so
  markdown is never embedded and `rag` cannot find it by meaning.
- Documents and meetings share `Topic` nodes only when two different models
  happen to choose the same label, which in practice they do not: in the
  home graph "markbench" and "model routing" are linked only from documents
  although both are discussed in meetings.

So a tech spec and the four meetings that discussed it are unrelated in the
graph, and nothing records whether the spec still describes what was
decided. An agent asked "what is the current state of the retry design?"
has to find the spec, read it, search meetings by word, read transcripts,
and reconcile them itself, at tens of thousands of tokens, with no warning
when the spec is stale.

## Goals

1. **Discover** relations between artifacts that do not link to each other,
   starting with a document and the meeting discussion about it.
2. **Judge** each candidate with a yes/no decision model; never assert a
   relation the evidence does not support.
3. **Keep a brief on each relation** that is current at all times: what the
   document says, what was said about it and when, what changed since, what
   is unresolved, and where the document and the latest discussion
   disagree. Every line cites a verbatim quote that is checked.
4. **Serve agents token-efficiently**: one call of a few hundred tokens
   lands on the current state; evidence expands only on demand.
5. **Stay incremental and bounded**: work only on what changed, under
   per-run caps, with a cost line, so an automatic run can never run away.

## Non-goals (this round)

- Document ↔ document relations, and code entities as relation endpoints
  (spec ↔ code ↔ meeting). Both build on this round's model; document links
  are phase 2 (below), code endpoints are deferred.
- Verbatim text for non-markdown documents (PDF, DOCX): the graph held an
  LLM summary for them, not their text, so quotes could not be checked.
  Phase 2 keeps their text in sections.
- Deploying to a hosted personal-file store.

## Vocabulary

| Term | Meaning |
|---|---|
| **subject** | The artifact a relation is about. This round: a document (a markdown file) |
| **discussion** | Meeting content that may concern a subject: topic segments, decisions, action items, transcript turns |
| **candidate** | A (subject, meeting) pair some signal proposed |
| **verdict** | The decision model's probability that a candidate is a real relation, stored whether accepted or not |
| **link** | A candidate whose verdict passed the gate |
| **evidence** | A verbatim quote anchored to a place in a source: a document section or a span of a transcript |
| **relation** | The node that holds a subject's links, evidence, verdict ledger and brief |
| **brief** | The relation's current-state summary, regenerated when its inputs change |

## What is already measured, and what must be measured again

From the topic-relatedness work (2026-09-20, 91 hand-labelled topic pairs;
`docs/jev.md`):

- **Cosine similarity ranks candidates but cannot decide** (AUC 0.77; no cut
  better than precision 0.83 at recall 0.50). It is a proposer, never a gate.
- **A yes/no question to the decision model decides** (AUC 0.92; gate 0.6
  gave precision 1.00 at recall 0.58). A six-way typed relation from the
  same model failed (exact 0.51). So relatedness is a yes/no question, and
  the kind of relation is not asked of the judge.
- **What was said proposes best.** Segment-summary embeddings found the
  largest share of real pairs (43% of judged pairs at ≥ 0.6, against 26% for
  label embeddings).
- **Exact vector search is cheap** below 250k vectors (0.14–0.26 s over 51k);
  `rag`'s wall time is index load and keyword scan, not search.

**Measured for document ↔ discussion pairs (2026-10-22).** The gate was
recalibrated before anything ran automatically, on 160 candidate pairs from
the experiment corpus (460 tech specs, 744 meetings), stratified by signal
and similarity and labelled blind to any score (27 related, 133 unrelated;
nine labels spot-checked by hand, all agreed):

| gate | admitted | precision | recall |
|---|---|---|---|
| 0.6 | 41–42 | 0.61–0.62 | 0.93–0.96 |
| 0.7 | 29 | 0.76 | 0.81 |
| **0.8** | **21–22** | **0.86** | **0.67–0.70** |
| 0.9 | 4–6 | 1.00 | 0.15–0.22 |

(Two runs; the spread is the decision model's own drift between identical
requests.) The judge ranks these pairs better than it ranked topics (AUC
0.97), but its probabilities sit higher: the topic gate of 0.6 would have
admitted a wrong relation four times in ten. No gate reaches precision
0.95 without losing three quarters of the real relations, so the default
is **0.8**: precision 0.86, recall about two thirds. The pairs still wrongly
admitted are neighbouring subjects (a server spec and a meeting about the
same product's tokens; a directory spec and a meeting about publishing to
it), which are also where the labels were least certain. Raising
`relations.gate` to 0.9 trades most of the recall for precision.

A second yes/no question, whether what was said is about the specific
thing the document specifies rather than a neighbour of it, was measured
alongside and did not help: alone it reached the same precision at the
same recall (0.86 at 0.70), and combining the two added nothing. It is not
asked.

One proposal signal was measured to propose nothing useful: of the 32
pairs proposed by a name or an identifier alone, with nothing near in
meaning, none was related. Where a document's sections have vectors, a
pair now needs the meaning signal; name, identifiers and topics only add to
its rank. They still propose for a document with no vectors yet.

## Document substrate changes (prerequisites)

### Markdown sections carry their text

The markdown parser emits each section with:

- **EndLine**: the section's extent runs to the next heading of the same or
  a higher level, so it includes its subsections. This is what `show`
  displays.
- **DocComment**: the section's *own* text, up to the next heading of any
  level, capped at about 6k characters. Own text rather than the whole
  extent, because a parent carrying its children's words would embed the
  document a second time and would change whenever any subsection was
  edited. A DocComment alone makes markdown embeddable: `EmbeddableText`
  already embeds a node that has one.
- **heading_path** (`H1 > H2 > H3`, also the QualifiedName), **ordinal**
  (which occurrence of that heading text in the file it is), and
  **section_hash** (sha256 of the whitespace-collapsed own text, so a reflow
  is not a change but an edit is).
- The root document's DocComment is its front-matter title and description
  plus the text before the first heading.
- Headings inside fenced code blocks are not headings.

**Duplicate headings.** Section IDs are `NewNodeID(Document, path,
"section:"+title)`, so two sections with the same title in one file collide
today. The first keeps exactly that ID; later ones append `#n`. No existing
ID changes.

**Blast radius.** Additive fields on existing nodes; new vectors for
markdown in every project (on by default; `docs.markdown_sections: false`
stops storing section text and so stops embedding it, keeping the structural
fields). `rag` starts returning markdown sections, which
is the point, but saved queries should be re-run before and after.

The documents linker is kept away from markdown. It links a document to any
code entity whose name of three letters or more appears in its text as a
bare substring, so over every markdown section it would link prose to code on
coincidences of spelling ("new" in "renewal") and multiply its own cost.
Markdown had no text for it to read before, so excluding it keeps that phase
exactly as it was; linking prose to code is deferred to matching built for
prose (see Deferred).

### Backfill without a runaway

Existing markdown is re-parsed once by a backpop step gated on a
`SyncState.MarkdownSectionsVersion` flag, like `BackpopContentHash`. It
deliberately does not mark files as changed: a changed-file list triggers
summarization, the linker and the enrichment queue, and doing that for every
markdown file at once is the shape of the runaway-sync incident. It
re-parses only the scope being written, so a feature branch does not copy
the default branch's documents into its own scope.

### Incremental vectors

Today only a full rebuild embeds anything; incremental syncs and the
meetings pipeline embed nothing, so new documents and meetings are invisible
to vector search until `vectorindex --force`. After this change:

- a sync embeds the embeddable nodes of the files it changed;
- each sync backfills missing vectors and drops vectors of deleted nodes, up
  to `vector.backfill_limit` (default 2000) so a large gap closes over
  several runs instead of in one;
- after a meetings run, the meeting nodes the index lacks are embedded,
  best-effort; if the vector store is locked or does not exist yet it skips,
  and the next sync's backfill catches up. It never removes vectors: a store
  that sees only the meetings scope would take every other vector for a
  deleted node's.

The reconcile runs over a view of the graph that includes the meetings and
relations scopes as well as the branch. A code sync's own store reads only
branches, and through it every meeting vector would look stale.

`EmbeddableTextVersion` does not change: markdown was never embedded, so no
existing vector's text changes.

## Data model

### Where relations live

Relations live in their own reserved scope, **`@relations`**, alongside
`@meetings`:

- Meeting re-indexing deletes `@meetings` edges touching the meeting's
  nodes, and `meetings migrate` moves the meetings scope whole. Neither
  should touch relations.
- Relations are not branch data: a spec's discussion does not change when
  you switch branches. Branch cleanup already skips reserved scopes.
- Resetting is one scope deletion.

Readers that serve agents read `[current branch, default, @meetings,
@relations]`. The writer opens a view over the same database handle that
writes `@relations` and reads the others (Badger allows one writer handle
per directory, so a second store cannot be opened in the same process).

### The relation node

One `Relation` per subject document, not one per pair. A per-pair brief
cannot say "the spec says X; the latest of four meetings decided Y"; the
per-meeting verdicts live inside the relation instead.

- **ID**: `NewNodeID("Relation", "", "doc:"+subject_path)`. The empty file
  path means `DeleteByFile` never removes it when the document is re-indexed.
- **Edges**: `About` (Relation → the subject Document) and `DiscussedIn`
  (Relation → Meeting, accepted links only, carrying the probability).
  Edges are derived from the properties and rewritten on every refresh.
- **DocComment**: the brief's headline and current lines, so relations are
  embedded and `find` can land on them directly.

| Property | Content |
|---|---|
| `subject_path`, `subject_hash` | The document and its content hash at the last refresh |
| `sections_json` | `heading_path → {hash, first_seen}`: the document's change history, which the graph otherwise does not keep |
| `links_json` | The verdict ledger, one entry per meeting session (below) |
| `evidence_json` | Evidence anchors (below) |
| `brief_json` | The current brief (below) |
| `brief_inputs_hash`, `brief_model`, `briefed_at`, `revision` | Regeneration bookkeeping; `revision` increments on each new brief |

### The verdict ledger

One entry per meeting session ID: `p`, `accepted`, `judge` (model),
`signals` (which proposed it), best cosine, the segment time ranges judged,
`sig_doc` (hash of the subject's matched section hashes), `sig_meeting` (the
transcript's content hash) and `judged_at`.

**Rejected verdicts are kept.** A pair is never asked again while both
signatures are unchanged; a changed section or a changed transcript makes it
eligible again. Re-enriching a meeting does not change its transcript hash,
so it does not trigger re-judging.

### Evidence anchors

Meeting child node IDs are not stable: decisions and action items are
numbered by position, and segment IDs embed the model's topic name, so
re-enrichment changes them. Evidence therefore anchors to sources, not node
IDs:

| Kind | Anchor |
|---|---|
| `doc` | `{path, heading_path, ordinal, section_hash, quote}` |
| `said` | `{session_id, start, end, speaker, quote}` |
| `decided` | `{session_id, quote}` (the decision's own quote) |

On each refresh an anchor is re-resolved: a `doc` anchor by heading path and
a check that the quote is still in the section; a `said` anchor by loading
the transcript and finding the quote within ±60 s of the anchor, then
anywhere. An anchor that no longer resolves is kept and marked
`source_missing`, and is never cited in `current` or `conflicts`.

**Evidence IDs** are the first 6 hex characters of
`sha256(relation_id | kind | anchor | collapsed quote)`, so a regenerated
brief citing the same words keeps the same IDs.

## Proposal

Signals propose (subject, meeting) candidates. They are compared at the
section ↔ segment level and aggregated per pair.

| Signal | What it compares |
|---|---|
| `said` | Section vectors against segment-summary vectors, nearest 5 each way |
| `name` | The document's title (front matter, first H1, or file name with separators as spaces) and its distinctive headings, against segment summaries, keywords, the meeting's mentions and transcript turns. Token-set match, tolerating one edit in tokens of five or more letters, because transcription mangles names. Headings that occur in many documents ("Overview", "Goals") are discounted by IDF |
| `ids` | Shared identifiers: issue keys, URLs, PR and issue numbers, file names, multi-part identifiers |
| `topic` | A document topic equal to, or `RelatedTo` at the gate with, a segment's topic |

People and time (an editor of the document attended a meeting near the
edit) only boost ranking; they never propose on their own.

**Not subjects:** transcripts that are also indexed as documents (they would
"relate" to their own meeting), AI guideline files, documents under 400
characters of text, paths matching `relations.exclude`. Duplicate documents
collapse to their canonical copy.

**Caps:** at most 8 meetings per changed document, 8 documents per newly
written meeting, and 200 pairs per run. `codeeagle relations --plan` prints
the candidate counts per signal and the estimated tokens, and writes
nothing.

## Judging

One decision-model request per subject carries a document card (path,
title, outline of up to 20 headings, up to 3 matched excerpts of 500
characters) and up to 10 segment cards (meeting title and date, what the
segment said, a verbatim excerpt of up to 400 characters, one decision
quote). Each pair is one yes/no question with criteria:

- **true** when the segment discusses the work, design or decision this
  document describes, or changes or questions something in it;
- **false** when they share vocabulary or a broad area only.

Facts the judge needs are named literally in the state ("words of the
document title found in this segment: …") rather than described, because
the model believes what the state asserts. A request too large for the model
is split in half and retried; four requests run at a time. A failed request
writes no verdict, so the pair is retried on a later run, within the caps.

## Briefs and grounding

### Inputs

At most `relations.max_brief_input_tokens` (12k): the document's matched
sections first, then its outline, labelled D1…Dn; transcript slices for the
latest four linked meetings (from segment start and end times, with
timestamps and speakers), labelled M1…Mk; earlier meetings as their verified
evidence with dates; open action items from linked meetings; and the
previous brief's unresolved items and conflicts as hints. The language model
writes the brief; the decision model only judges.

### Output schema

```json
{
  "headline":   {"text": "...", "cites": ["D2", "M1"]},
  "current":    [{"text": "...", "cites": ["M1"]}],
  "events":     [{"date": "2026-10-14", "text": "...", "cites": ["M1"]}],
  "unresolved": [{"text": "...", "raised": "2026-10-14", "cites": ["M1"]}],
  "conflicts":  [{"doc_says": "...", "discussion_says": "...", "date": "2026-10-14",
                  "doc_cite": "D2", "said_cite": "M1"}],
  "quotes":     [{"label": "M1", "quote": "verbatim words"}]
}
```

### Grounding rules

- Every cited quote is checked against **its cited snippet only**, not the
  whole document or transcript, so a quote cannot be borrowed from
  elsewhere. A line with no verified citation is dropped, and the count is
  recorded as `unverified_dropped`.
- A conflict is shown only if the decision model confirms the two cited
  passages contradict each other (`relations.conflict_gate`, provisionally
  0.7). Otherwise it is kept as an event, not presented as a conflict.
- `delta` is not stored. It is derived when read, from `events` and from
  `sections_json` changes after the reader's `--since`.

### When a brief is regenerated

All three must hold:

1. the inputs hash changed: stage version, model, the resolved anchors, the
   subject's hash and the accepted meetings;
2. the subject has been unchanged for `relations.settle` (30 minutes),
   because working-tree indexing sees every save of a file being edited;
3. the run has not yet written `relations.max_briefs_per_run` (10) briefs.

## Freshness

Freshness is computed deterministically on every read, not stored, so it is
never out of date:

| Flag | Meaning |
|---|---|
| `doc_changed_after_discussion` | The document changed after the last linked meeting; nobody has discussed the new version |
| `discussed_after_doc_change` | Meetings discussed it after its last change; the document may not reflect them |
| `brief_pending` | Inputs changed and the brief has not been regenerated yet |
| `doc_differs_on_branch` | The current branch's copy differs from the one the brief was written from |
| `doc_missing` | The subject no longer exists |
| `quote_gone` | A cited section changed, so a cited quote may no longer be there |
| `source_missing` | An evidence anchor no longer resolves |

Briefs are written against the default branch (or the working tree of a
non-git project). A feature branch's difference is a flag, not a reason to
regenerate on every branch switch.

## Pipeline and bounds

- **Sync**: after vector indexing (so what changed can already be found by
  meaning), on the default branch or a non-git project. A feature branch
  never runs it: briefs describe the default branch's documents.
- **Meetings sync**: after the new meetings are embedded, when any were
  indexed. `meetings watch` does not run it; the next `sync` or `meetings
  sync` picks the meetings up.
- **Worker**: unchanged. It already runs `sync`, and relations run inside it.
- **Repair and bulk**: `codeeagle relations [--plan] [--doc PATH]
  [--meeting ID] [--limit N] [--briefs-only] [--reset]`, which proposes over
  the whole corpus rather than what is pending. `--plan` is the dry run;
  `--reset` deletes the relations scope whole, verdict ledger and scan
  ledger included.

### What one automatic run considers

A scan ledger (one `RelationScan` node in `@relations`) records, per
document, the content hash it was last considered under, and per meeting
its transcript's hash. A run considers what is unrecorded or changed, newest
first: at most 50 documents and 25 meetings. The same mechanism covers an
edited document, a new recording, a recording embedded after it was indexed,
and a corpus that existed before relations were turned on, which is worked
through a batch per run.

- **Vectors first.** A document or meeting with no vectors waits, and with no
  vectors at all nothing is considered. On the labelled pairs the name and
  identifier signals alone proposed no related pair, so judging without the
  meaning signal spends money to find nothing, and marking those items
  considered would stop the meaning signal from ever proposing for them.
- **Two kinds of cap.** The per-document and per-meeting caps pick the best
  few candidates for each; what they drop is not retried. The per-run cap
  only postpones: candidates it defers, and those the decision model failed
  to judge, keep the side that proposed them pending for the next run. A
  deferred candidate still counts toward its document's and meeting's few,
  so the per-run cap never widens them.
- **A fair split.** Pending meetings and pending documents each get at least
  half the per-run cap and either side what the other leaves. Selected as one
  pool, document candidates outscored meeting candidates on every run in the
  experiment corpus, and 609 waiting meetings did not move in three runs.
- **Whoever proposed it owns it.** A candidate deferred for a pending
  document does not hold back the meeting it names, and the reverse.

Measured on the experiment corpus (460 documents, 751 meetings), 2026-10-22:
one run judges 200 pairs in 60-110 decision-model requests (about 200k input
tokens) and writes up to 10 briefs, for 40-120 s of sync time; each run
drains about 10 documents and 13 meetings from the backlog. A bulk run of
`codeeagle relations` gets through a large backlog faster.

Every run with something to do prints one cost line: documents and meetings
considered; candidates proposed, judged, accepted; decision-model requests
and tokens; how many were deferred; briefs written and their tokens; and how
much waits for later runs. With no decision-model key, or `relations.enabled:
false`, nothing is read or written: a relation is never created on cosine
similarity alone.

## Agent verbs

Three verbs, as CLI commands and MCP tools. Output is compact text by
default and `--json` on request; every line carries an ID that the next verb
accepts.

### `find <words>`

Hybrid search (vector, keyword, centrality) over documents, meetings,
segments and relations. Hits fold up to their subject (a section to its
document, a document to its relation). At most 8 lines:

```
3fa9c2 doc docs/specs/retry-policy.md: retries capped at 5 with jitter (4 mtgs, last 2026-10-14) !doc_changed_after_discussion
81d0e4 mtg 2026-10-14 Platform sync: retry budget and backoff
```

### `brief <id|words> [--since T] [--budget N] [--json]`

Words resolve through `find`; a clear winner is briefed, otherwise the
candidates are returned, in the same call. Default budget 400 tokens,
maximum 2000. A sample (fictitious content), about 170 tokens:

```
docs/specs/retry-policy.md · 4 meetings (last 2026-10-14) · @r7
retries capped at 5 with jitter; spec still says 3 [e1a2b3]
current:
- cap is 5 retries, exponential backoff with full jitter [9c4d10]
- idempotency keys required on all write calls [77e0f2]
conflicts:
- spec §Limits says 3 retries; 2026-10-14 meeting agreed 5 [e1a2b3]
unresolved:
- who owns the retry-budget dashboard (raised 2026-10-14) [b51a09]
delta since 2026-10-07:
- 2026-10-14 cap raised from 3 to 5 after the load test [9c4d10]
fresh: discussed_after_doc_change
expand: show 9c4d10 · brief 3fa9c2 --since @r7
```

Order: header, headline, current, conflicts, unresolved, delta, freshness,
expand hints. Under a tight budget, lines are dropped in this order: delta,
unresolved beyond two, current beyond three, conflicts beyond two. The
header, headline and freshness are never dropped. With `--since`, delta
moves up to follow the headline. `--since` takes a date, an RFC 3339 time, a
duration (`7d`) or the `@rN` revision printed in the header.

### `show <id-prefix> [--from N]`

Expands an evidence ID or a node ID prefix (at least 4 hex characters). An
ambiguous prefix lists up to ten matches. A `doc` evidence shows its section
from disk, at most 60 lines or 2,500 characters, continuing with `--from`,
and says if the section changed since the brief. A `said` evidence shows the
transcript turns within ±15 s of the anchor, at most 1,500 characters. Never
a whole file.

### MCP

Three tools with short descriptions (every tool definition is sent with
every request, so fewer tools is itself a saving). `mcp.tools: verbs`, or
`mcp serve --tools verbs`, exposes only the three.

No tool holds the graph open between calls. A read-only Badger handle takes
a shared directory lock, and a writer an exclusive one, so a handle kept for
an agent's session would stop the worker syncing until the session ended,
and no brief could be refreshed under it. (The original design reopened the
store when the database changed; it could never have triggered, since the
worker could not write while the server held the store.) The verbs open the
graph for each call and keep the library over it until the database
changes; the other tools share a handle that closes after half a second
idle. The vector index is loaded once and its database closed, keeping the
key list in memory. While a sync holds the database, a call waits up to 3 s
and then `brief` and `show` answer from what was last read, while `find`
reports that the index is busy.

Token estimates use bytes / 3.5 until calibrated against measured usage.

## Configuration

```yaml
relations:
  # enabled: false            # unset = on when transcripts.jev_api_key is set;
  #                           # true cannot turn it on without the key
  gate: 0.8                   # measured 2026-10-22: precision 0.86, recall ~0.7
  conflict_gate: 0.7
  max_pairs_per_run: 200
  max_candidates_per_subject: 8
  max_candidates_per_meeting: 8
  max_briefs_per_run: 10
  max_brief_input_tokens: 12000
  settle: 30m
  # provider: baseten         # unset = transcripts.provider
  # model: ...                # unset = transcripts.model
  exclude: []

docs:
  markdown_sections: true     # false restores heading-only sections

vector:
  backfill_limit: 2000

mcp:
  tools: all                  # all | verbs
```

## Measurement

All data stays outside the repository; tests that use it are gated on
environment variables (`RELATION_MEASURE`, `RELATION_MEASURE_OUT`,
`RELATION_BENCH_DIR`).

**Corpus**: an experiment instance with its own index: transcripts imported
from a copy of the home graph's meetings scope (never the live database),
tech specs copied into a scratch folder, and personal files in the shape an
agent's workspace keeps (memories with front matter, per-source world and
journal files, digests and open loops).

**Labelled set**: about 200 stratified candidate pairs exported by
`relations --plan --sample-out`, labelled by a person.

**Metrics**:

- proposal recall per signal and combined, at the caps;
- judge AUC, and precision and recall at the gate (target precision ≥ 0.95);
- grounding: the share of brief lines supported by their cited quote,
  checked by a person (target ≥ 95%);
- **stale shown as current**: a `current` line contradicted by later
  evidence with no freshness flag (target 0);
- conflict precision.

**Tokens to answer**: about 20 real questions with reference answers, run
headless by an agent in four arms (file reading and search only; the
existing MCP tools; the three verbs only; everything), three repetitions
each, recording input tokens (including cache reads and writes), output
tokens, turns, cost and wall time. Answers are graded blind (arm labels
removed, order shuffled) for correctness, staleness and citation validity.
Target: 5–10× fewer tokens than file reading at equal or better accuracy.
Dated results go in this document and decide the `mcp.tools` default.

## Results (2026-10-22)

Measured on the experiment corpus: 460 tech specs, 744 meetings with 3,600
topic segments (recordings to 2026-10-16). Personal files are not in it yet;
that waits on an export. One repetition per arm; treat differences of a
tenth in accuracy as noise.

**Bulk relating.** `codeeagle relations` with the per-run cap lifted judged
3,130 pairs in 451 decision-model requests (2.4M input tokens, 14 s) and
accepted 303; briefs for the 165 documents with an accepted meeting take
about 8 minutes four at a time with GLM-5.3-Flash (1.3M input, 0.2M output
tokens).

**Grounding.** A blind judge (Opus) read 60 sampled brief lines, 12 of each
kind, beside their quotes:

| judged against | supported | partly | unsupported |
|---|---|---|---|
| the quotes alone | 17% | 78% | 5% |
| the passages around the quotes, as `show` prints them | 42-48% | 50-57% | 2% |

"Partly" is mostly detail beyond the quoted words: a rationale, a number, a
second meeting. What it should not include, and still does with
GLM-5.3-Flash after the writing rules were tightened: a proposal written as
a decision, a claim that something was never recorded, a follow-up called
unassigned that someone took. A conflict is now shown in its two verified
quotes rather than the model's paraphrase, which had put the meeting's
position on the document's side; conflict and event dates now come from the
evidence's meeting. With DeepSeek-V4.1-Flash writing instead
(53 lines from 60 briefs): 57% supported, 42% partly, 2% unsupported, and
3 unverified lines dropped in 60 briefs against GLM's 39 in 165, at about
five times the cost per brief (about a cent). It is the better writer;
where it is the transcripts model, briefs use it unless `relations.model`
says otherwise.

**Tokens to answer.** 20 questions (13 on where a spec stands, 2 on
meetings, 3 on specs no meeting was judged to discuss, 2 the sources cannot
answer), answered by Sonnet under `claude -p` in restricted mode with at most
15 turns and $1, graded blind by Opus against references Opus drafted with
every tool and a separate agent then checked against the sources (it
corrected a material error in 7 of the 20 drafts; a person has not yet
reviewed them). Medians per answer:

| arm | accuracy | fully right | input tokens | turns | cost | hit the turn cap |
|---|---|---|---|---|---|---|
| grep (Read, Grep, Glob) | 0.42 | 5% | 382k | 15 | $0.41 | 3 |
| existing MCP tools | 0.42 | 5% | 99k (3.9x less) | 8 | $0.15 | 0 |
| find, brief, show | 0.50 | 15% | 81k (4.7x less) | 14 | $0.09 | 3 |
| everything | 0.57 | 15% | 125k (3.0x less) | 9 | $0.15 | 0 |

The first run of the verbs scored 0.30 and hit the turn cap on 11 of 20:
`brief` on a meeting listed only the documents it discussed and `find` gave
a meeting as a title, so agents probed with `show` until they ran out of
turns. With a meeting's brief carrying its decisions, topics and follow-ups,
`show` on a topic printing what was said, and `find` naming the part that
matched, the verbs reached the numbers above.

`mcp.tools` stays `all`: it is the most accurate, at a third of grep's
tokens. `verbs` is the cheapest and still beats grep and the existing tools
on accuracy, for clients that pay per tool definition. Accuracy is low
across the board because the references synthesise several meetings and few
answers get everything in 15 turns; the comparison between arms is the
point. The two unanswerable questions were answered correctly by the verbs
and by everything; grep spent its whole dollar searching for one of them
and returned nothing.

**Conflicts.** A blind judge read every conflict in the corpus's briefs,
as its two verified quotes with the turns around the meeting's words. With
the decision model asked whether the meeting "decided or stated" something
incompatible: 14 conflicts, 8 real, 2 not, 4 unclear, mostly one person's
proposal set against the document. Asked whether the meeting "decided or
settled" it: 7 conflicts, 6 real, none wrong, 1 unclear on a garbled
transcript. Some real conflicts now appear as events instead; a conflict
sends a reader to fix a document, so precision wins.

**Recall, end to end.** The checked references name the meetings that
discussed each of 16 specs. Of 80 (spec, meeting date) pairs they name, the
specs' relations held 16 (20%). The references include loosely related
meetings, so this understates recall, but not by enough to excuse it.
`relations --plan --doc` lists every candidate for one document and what
became of it, and on the Python SDK spec it shows where the rest go: pairs
labelled related during calibration at 0.88 were judged in the bulk run at
0.66-0.78, below the gate, since a pair's probability moves with the other
candidates judged beside it; and many named meetings were proposed but
past the per-document cap of eight. The gate on the labelled pairs:

| gate | precision | recall |
|---|---|---|
| 0.70 | 0.76 | 0.81 |
| 0.75 | 0.78 | 0.78 |
| 0.80 | 0.86 | 0.67 |
| 0.85 | 0.93 | 0.52 |

Agents answering with every tool make up for missing relations by searching
meetings directly, which is part of why that arm leads.

Not yet measured: stale-shown-as-current.

## Improvement research (2026-10-22)

Where answers lose points, from the benchmark's blind grades, in every arm
alike: meetings missed (about 9 answers in 20), the document's own content
left out (5-12), a confident claim that no meeting discussed something
(2-4), and a superseded document presented as current (1-2).

Where relation recall is lost, for the 80 (spec, meeting) pairs the checked
references name: 15 accepted, 28 rejected by the judge (14 of them at
0.60-0.79), 22 proposed but past the per-document cap or not yet reached,
5 proposed by title alone and set aside, 10 never proposed, several of
which are dates of documents or commits rather than meetings.

**Tested.** Labelled-set numbers are at gate 0.8 on the 160 pairs; the
repeat noise there is about ±0.05. End-to-end is accepted pairs of the 80.

| hypothesis | result |
|---|---|
| the judge's score moves with the pairs batched beside it | confirmed: calibration and bulk differ by 0.085 on average, up to 0.39; 16 of 25 related pairs cleared the gate once, 11 the other time |
| one pair per request steadies it | precision 0.86 → 0.95, recall unchanged |
| the judge sees a long segment's opening, not where the document comes up | confirmed: showing the stretch that shares the document's words raises recall 0.67 → 0.81-0.85 at precision 0.88 |
| both, with 1,500 characters of speech | AUC 0.971 → 0.987; precision 0.89-0.96, recall 0.89 (two runs); about twice the decision-model tokens, under a dollar for the corpus |
| deeper caps (8 → 30 per document and meeting) | end to end 15 → 17 with the old judge, 16 → 23 with the new; a blind check found 24 of 30 added relations related, against 6 of 10 the old configuration accepted |
| also show the stretch of the whole meeting richest in the document's words | rejected: 841 → 600 accepted, 23 → 17 end to end |
| weight the document's words by rarity within the meeting | no effect; not kept |
| count progress reports, demos and shipping as discussion | no effect (21 against 23) |
| keyword search finds the never-proposed meetings | the meeting index found none; the raw transcripts held 3 of the 10 |
| the decision model can check a brief line against its passages | AUC 0.75 on 53 lines: keeping lines at 0.5 or above lifts the fully supported share 57% → 90% but drops 11 of 30 good lines; it caught the one unsupported line at 0.07. A flag or a reason to rewrite, not a filter |

The labelled set overstates precision: a blind check put the old
configuration's accepted relations at 6 of 10 against 0.86 on the labels.
More labels, drawn from deep candidates too and by a second labeller, are
needed before any gate is tuned to two decimals.

**Not yet tested, in order of expected value:**

1. Adopt one pair per request, the relevant stretch at 1,500 characters and
   caps of 20-30 as defaults, and rerun the benchmark's verbs and
   everything arms on the deeper graph.
2. Say what is not confirmed instead of implying absence: a brief or `find`
   line with no accepted meeting names the closest candidates below the gate
   as unconfirmed, rather than "no meeting has been judged to discuss it".
   19 of the 35 references the new judge rejects score 0.54-0.79.
3. Breadth in one call: a compact timeline of the meetings about a subject,
   one line each with the passage that matched. The verbs take a median 14
   calls; everything gets breadth from `query_meetings` at about 35 KB a
   call.
4. The document's own state in its brief: status and date from front matter
   and a two-line gist, since answers leave the document out.
5. Document-to-document supersession (a redesign replacing a spec), judged
   as a yes/no question, to stop superseded documents reading as current.
6. Faithful briefs: DeepSeek writes 57% fully supported lines against
   GLM's 42-48%; headlines are weakest at 25-33%. Flag lines the decision
   model does not support, and build headlines from verified lines.
7. Judge pairs proposed by title alone (5 of the 80) now that the judge is
   steadier, checked first against the 32 such labelled pairs.
8. CLI latency: expand credentials only when used (0.5 s of 2.3 s) and keep
   a library snapshot keyed by the database's state (most of the rest).

## Phase 2: every document, typed, linked and dated

Phase 1 related markdown specs to meetings and briefed only the specs a
meeting was confirmed to discuss. Graded as the answer, a brief scored 0.47
and none was fully right; the causes were missed meetings, no brief at all
for a document without a confirmed meeting, no links between documents, no
dated record of decisions, and only markdown being a subject at all.

### A brief for every document, shaped by its type

`EnsureRelations` gives every eligible subject a relation, so the brief
writer runs whether or not a meeting was confirmed; with none, the brief is
the document's own state.

A document's type is decided deterministically from the words of its path
(counted double, since authors name files for what they are) and its
headings: `spec`, `adr`, `incident`, `meeting_notes`, `notes`, `report`,
`reference`, or `other` when nothing wins outright. A slide deck (`.pptx`,
`.odp`) is a `deck` by its format, whatever it is about. Status and date are
read from the document's opening (`**Status:** Draft, 2026-10-04`, `Last
updated:`), never written by the model. Besides the common parts (headline,
the document's own points, current state, decisions, unresolved, conflicts,
timeline) each type has its own, and the writer is told which it may use:

| type | parts |
|---|---|
| adr | context, decision, consequences |
| incident | impact, root_cause, incident_timeline, follow_ups |
| meeting_notes | agreed, follow_ups |
| notes | entries, open_loops |
| report | findings, recommendations |
| deck | purpose, claims, asks |
| reference | purpose, how_to |

Parts under any other name are dropped after grounding.

### References are facts

Links, backtick paths and bare paths in a document's sections are resolved
relative to the document first, then by the unique longest suffix across the
corpus (a Companions block names `docs/tech-spec/...` repository paths); an
ambiguous basename resolves to nothing rather than to a guess. Resolved
references are stored on the relation (`refs_json`) with the sentence they
appear in, written as `References` edges, and shown as "links to" and
"linked from". No judge is involved.

### Document ↔ document links

Four kinds, each asked as its own yes/no question, because a six-way relation
type failed when measured (51%):

| kind | meaning | direction |
|---|---|---|
| changes | A changes part of what B specifies | A → B |
| replaces | B as a whole is no longer the account to follow; implies changes | A → B |
| records | A is an account of something that happened (incident, review, meeting, conversation) involving B | A → B |
| companion | related, and no directional kind accepted | symmetric |

Pairs are proposed by resolved references, title mentions in order, version
and rename patterns in file names, shared rare identifiers, and section
vectors at cosine 0.75 or above (3 nearest), at most 12 per document and 60
per run, with the deferred-versus-cut semantics of meeting pairs. Each pair
is judged in its own request: two document cards (type, status, date,
outline, excerpts) and the literal facts that proposed it, asked whether the
two are related and, in both directions, whether one changes, replaces or
records the other. Default gates: related 0.8, changes 0.85, replaces 0.95,
records 0.85. Where both directions clear a gate, the later stated date wins;
with no dates to tell them apart neither direction is kept. `replaces` needs
`changes` in the same direction. Every tie falls back to the weaker claim,
because a wrong "replaced by" hides a document someone needs.

The verdict is stored once, on the relation of the lower path
(`doc_links_json`), keyed by the other path and the content hashes of both,
so a pair is asked about again only when one side changes. Accepted kinds are
written as `Changes`, `Supersedes`, `Records` and `CompanionOf` edges. Briefs
gain `superseded_by` and `amended_by` freshness flags, computed when read and
never trimmed, and a related-documents part.

`relations --docs --plan` lists the proposals; `--sample-out-docs DIR`
writes a stratified sample for labelling; `--measure-docs labels.jsonl`
reports related AUC, precision and recall per gate, and per-kind precision
and recall with direction included. The automatic pass judges document
pairs only with `relations.auto_doc_links: true`, off until the gates are
calibrated on labelled pairs and a blind check of accepted links. With it on,
each sync considers the documents whose content changed since their pairs
were last considered (newest first, at most 50), proposes and judges within
the per-run cap, and marks a document considered only when none of its pairs
was deferred or failed.

### A dated decision ledger

Each relation keeps `decisions_json`, entries kept by ID across brief
rewrites so a decision recorded once stays in the history:

- **meetings**, deterministically: a meeting decision whose topic is the
  topic of a stretch the judge accepted, dated by the meeting and cited by
  its verbatim quote;
- **the document**: dated lines in its decision, changelog, history or
  revision sections, dated as stated;
- **the writer**: decisions found in the snippets, kept only with a verified
  quote and a date that appears in the quote or its snippet header, and
  checked by the decision model as settled or merely proposed.

Each entry records its date basis (stated, meeting, undated) and status
(decided, proposed). A brief shows the ledger newest first, with the
decisions of documents that change this one inherited and marked as such;
`brief --since D` lists those after D, and the latest three are in the
relation's searchable text.

### Non-markdown documents

PDFs, decks, word-processor files and plain text were one node each, holding
a model's two-sentence summary that also overwrote the text. Now:

- **Sections.** The generic parser always extracts the text of prose formats
  (`.pdf .docx .pptx .odt .odp .txt .rst .adoc .org` and similar wiki
  markup) and stores it in child section nodes, like markdown's: PDF pages,
  with pages under 1,500 characters merged into the next; slides, titled by
  their first line; otherwise runs of paragraphs of about 4,000 characters.
  At most 200 sections per file and 6,000 characters per section. Ligatures
  and words hyphenated across a line end are repaired once, where sections
  are made, so stored text and the quotes checked against it agree.
  Spreadsheets, data, configuration and code get no sections and are never
  subjects. The file's own node keeps its summary, for search.
- **The summary no longer clobbers the text.** Enrichment updated every node
  with the file's path; it now leaves sections alone. A cached summary no
  longer skips text extraction either.
- **Backfill.** Documents indexed before are re-parsed quietly (not reported
  as changed, so nothing downstream is set off), in the scope being written
  only, at most 200 files or two minutes per sync, resuming where the last
  sync stopped, under a `DocumentSectionsVersion` flag kept per branch. A
  cached summary is reused, so no model is called again.
- **Subjects.** Such a document is a subject like a markdown one. Its title
  is its file name; its opening is its first page, slide or part, never the
  summary. `show` and `brief` read the indexed text, since the file cannot
  be read as lines, and name the page or slide instead of line numbers.

### Chat logs

A conversation held in a chat tool is a record of decisions like a meeting,
but arrives as a file. `internal/parser/chatlog` recognizes, by shape rather
than by name:

- role/content JSONL, as assistant chat histories store it, dated from the
  file name where the messages carry no time;
- an assistant session's JSONL, whose records wrap messages with
  timestamps, skipping tool calls and their results;
- a ChatGPT `conversations.json`, one thread per conversation;
- a Slack channel's day file, and a Teams (Graph) message list;
- a conversation pasted into text, a PDF or a word-processor file:
  `[10:32] Kevin: ...`, `2026-06-01 10:32 Kevin: ...`, or the name-and-time
  header lines copying from Slack or Teams leaves, with date-only lines
  separating days.

Pasted text is a chat only when message lines are at least 30% of its
non-empty lines, it holds at least four messages, and at least two people
speak; an application log, whose "speakers" are levels such as `INFO`, is
not one. An email quoting a line of chat stays an email.

A chat's sections are days, or a day of one thread where a log holds
several, rendered `15:04 Speaker: text`; a day too long for one section
continues in the next. Its type is `chat` whatever it is about, with parts
threads, agreed and open_questions, and the writer reads its newest days
first. A decision drawn from it is dated by the day its messages were sent
(`date_basis: message`) and recorded as proposed unless the decision model
finds that what was said settled it, as for speech in a meeting. When the
decision model reads two documents as each recording the other, a chat,
incident write-up, notes, meeting notes or report is taken as the record;
when neither or both are, no `records` link is kept.

### Meeting recall: what was not pursued

A proposer over the raw words of transcripts (60-second windows scored
against a document's title and distinctive headings) was planned for the
meetings no signal proposes. It is deferred: the meeting index keeps
summaries rather than what was said, so the proposer would parse 2 GB of
transcripts per run, and the measured ceiling is low. The raw transcripts
held only 3 of the 10 never-proposed reference meetings, which is the most
it could find, at the edge of the bar set for adopting it.

### Readers do not fetch credentials

Configuration values may run a command, typically to read a key from the
keyring. `find`, `brief`, `show` and the MCP server only read the index, so
they leave values under keys ending in `api_key`, `token`, `secret` or
`password` unexpanded rather than spend half a second per call fetching a
credential they never use.

## Deferred

- Code endpoints: spec ↔ code ↔ meeting, through existing `Documents` and
  `Mentions` edges.
- One subject per conversation in an export that holds many (a ChatGPT
  history is one subject today, its conversations its threads).
- Images and spreadsheets as subjects; PDF transcripts as meetings.
- Rename detection by content hash, so a moved document keeps its relation.
- Document deltas from git history.
- The people-and-time proposer.
- Serving a hosted personal-file store.

## Implementation sequence

| Milestone | Delivers |
|---|---|
| M1 | Markdown section text, duplicate-heading IDs, backfill, incremental vectors, store view and prefix lookup |
| M2 | Hybrid search extracted from `rag` into a reusable function |
| M3 | Relation schema, proposal signals, `relations --plan` (read-only) |
| M4 | Judging, verdict ledger, gate calibration |
| M5 | Brief writer, grounding, freshness |
| M6 | Sync and meetings hooks, configuration, cost line |
| M7 | `find`, `brief`, `show` as CLI and MCP tools |
| M8 | Measurement report |
