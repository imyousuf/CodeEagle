# Tokens-to-answer benchmark

Measures what it costs an agent to answer a question about a team's specs
and meetings with different tools, and whether the answer is right. It is the
measurement behind `docs/artifact-relations.md` and the `mcp.tools` default.

Nothing here contains data. Questions, answers and grades live in a bench
directory outside the repository (`--bench-dir`, or `RELATION_BENCH_DIR`).

## Arms

| arm | tools |
|---|---|
| `grep` | Read, Grep, Glob over the spec files and transcripts |
| `mcp` | CodeEagle's MCP tools as they were before the verbs (find, brief and show denied) |
| `verbs` | find, brief and show only |
| `all` | Read, Grep, Glob and every CodeEagle tool |
| `oracle` | as `all`, on a stronger model with many more turns; drafts reference answers |

Every run is `claude -p` in restricted mode with only the arm's MCP server
(`--restricted --strict-mcp-config`), so no personal CLAUDE.md, hook or
plugin reaches the agent. All arms get the same prompt, which says where the
specs and transcripts are.

## Protocol

1. Index a corpus in its own project directory (`codeeagle sync`, `meetings
   sync` or `meetings migrate`, then `codeeagle relations`), and stop any
   sync while measuring: a sync holds the graph, and the MCP arms wait for it.
2. Write `questions.jsonl` in the bench directory, one object per line:
   `{"id": "q01", "question": "..."}`. Mix questions a relation answers
   (the state of a spec, what a meeting changed in it), questions about
   meetings, and questions the sources cannot answer.
3. Draft references: `bench.py --arms oracle ...`. Read every draft against
   the sources, correct it, and copy it into the question's `reference`.
4. Collect answers: `bench.py --arms grep,mcp,verbs,all --reps 3 ...`.
   Answers already on disk are not asked again, so a run can be resumed or
   widened.
5. Grade: `grade.py`. Each answer is graded on its own against the
   reference, in shuffled order, by a judge that never sees the arm. It
   writes `report.md` and `spotcheck.md`, a sample of graded answers with
   the arms hidden; check the judge against it.

```
export RELATION_BENCH_DIR=/path/to/bench
scripts/bench-relations/bench.py --corpus /path/to/project --codeeagle "$(which codeeagle)" \
    --sessions ~/.local/share/tomoe/sessions --arms oracle
scripts/bench-relations/bench.py --corpus /path/to/project --codeeagle "$(which codeeagle)" \
    --sessions ~/.local/share/tomoe/sessions --reps 3
scripts/bench-relations/grade.py
```

## What is recorded

Per answer, from `claude -p --output-format json`: input tokens including
cache reads and writes, output tokens, turns, the reported cost, and wall
time. The report gives medians per arm and each arm's input tokens as a
multiple of `grep`'s, beside accuracy (mean grade over 2), the share fully
right, answers that present superseded information as current, claims that
contradict the reference, and the share that cite sources.

`--max-budget` (default $1) caps any one answer, and `--max-turns` (default
15) its turns; an answer that hits either is recorded as it stood.
