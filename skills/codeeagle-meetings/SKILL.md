---
name: codeeagle-meetings
description: Search indexed meeting transcripts for what was discussed, decided, and committed to — by topic, by person, or by date. Use when a question is about what someone said, why a decision was made, who owns a follow-up, or what a meeting concluded, rather than about what the code does.
allowed-tools: Bash(codeeagle *)
---

# CodeEagle — Meeting Transcripts

Meetings hold the part of a project's reasoning that never reaches the
codebase: why a design was chosen, what was ruled out, and who owns what next.
Use this skill when the question is about what people *said*, not what the code
does.

## Finding meetings

```bash
codeeagle meetings list --limit 20            # most recent first
codeeagle meetings list --person "Kevin"      # meetings someone attended
codeeagle meetings list --since 2026-03-01    # meetings after a date
codeeagle meetings list --who                 # show attendee names, not a count
codeeagle meetings list --json                # machine-readable
```

Then read one in full — participants with speaking time, topics with their own
summaries and timestamps, decisions with supporting quotes, and follow-ups:

```bash
codeeagle meetings show <meeting-id>
codeeagle meetings show "auth" --json          # partial title match also works
```

## People and commitments

```bash
codeeagle meetings people                      # who appears, how often, what they own
codeeagle meetings actions --person "Kevin"    # what someone committed to
codeeagle meetings actions --unassigned        # follow-ups nobody owns
codeeagle meetings topics                      # what gets discussed most
```

## Indexing

```bash
codeeagle meetings sync --dry-run              # scope and token cost, no model call
codeeagle meetings sync                        # enrich and index; skips unchanged
codeeagle meetings watch                       # index new recordings as they appear
```

## Reading the output honestly

Two things in this data need care:

- **Speakers may be unidentified.** Recordings are diarized but anonymous, and
  a name is only asserted when the transcript supports it — leaving a speaker
  unnamed is a deliberate outcome, not a bug. `codeeagle meetings identify`
  lists what is unresolved, and `codeeagle meetings label "Person 3" Kevin
  --meeting <id>` assigns one by hand. Never infer who an unidentified speaker
  was and present it as fact.
- **Quotes are marked when they do not check out.** Every decision and
  follow-up carries a verbatim quote, and whether that quote actually appears
  in the transcript is recorded. `meetings show` flags failures with `?`. Do
  not repeat a flagged quote as something that was said.

Attribution matters here in a way it does not for code: these are real people's
words. When reporting what someone said or agreed to, say how confident the
record is, and prefer quoting the meeting over paraphrasing an attribution.

## Prerequisites

- CodeEagle installed and on PATH
- `codeeagle meetings sync` run at least once. Transcripts are found in the
  directories named by `transcripts.sessions_dir`, and among the documents
  `codeeagle sync` already indexed, so neither setting is strictly required
