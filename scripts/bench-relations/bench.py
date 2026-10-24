#!/usr/bin/env python3
"""Tokens-to-answer benchmark for CodeEagle's relation verbs.

Asks the same questions of an agent given different tools, and records what
each answer cost: input tokens (cache reads and writes included), output
tokens, turns, dollars and wall time. Grading is separate (grade.py), blind
to which tools produced an answer.

Arms:
  grep   Read, Grep and Glob over the files; no CodeEagle
  mcp    CodeEagle's MCP tools as they were before find, brief and show
  verbs  find, brief and show only
  all    Read, Grep, Glob and every CodeEagle tool
  oracle like all, on a stronger model with many more turns; drafts the
         reference answers a person then checks
  verbs-haiku  the three verbs on a cheaper model

Everything the benchmark reads and writes lives in the bench directory, never
in the repository:

  questions.jsonl   {"id": "q01", "question": "...", "reference": "..."}
  runs/<arm>/<id>.r<rep>.json

Runs are resumable: an answer already on disk is not asked again.

  bench.py --bench-dir DIR --corpus DIR --codeeagle PATH [--sessions DIR]
           [--docs DIR] [--arms grep,mcp,verbs,all] [--reps 1] [--model sonnet]
"""

import argparse
import concurrent.futures
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import time

VERBS = ["find", "brief", "show"]
FILE_TOOLS = "Read,Grep,Glob"

ARMS = {
    "grep": {"tools": FILE_TOOLS, "mcp": None},
    "mcp": {"tools": "", "mcp": "all", "deny": VERBS},
    "verbs": {"tools": "", "mcp": "verbs"},
    "all": {"tools": FILE_TOOLS, "mcp": "all"},
    "oracle": {"tools": FILE_TOOLS, "mcp": "all", "oracle": True},
    # The three verbs on a cheaper model: whether briefs carry enough that a
    # small model answers as well, for a fraction of the cost.
    "verbs-haiku": {"tools": "", "mcp": "verbs", "model": "haiku"},
}

PROMPT = """You are answering a question about a team's work. Its documents are \
under {docs}: tech specs, and under {docs}/home-slice work notes, \
incident write-ups, plans, PDFs, slide decks and a virtual teammate's \
workspace. Its meeting transcripts are under {sessions} (one directory per \
recording; session.json holds what was said, by whom, when). You may not \
have tools that read those files directly; use the tools you have.

Answer concisely. Cite the source of each claim: a document's path and \
section, or a meeting's date and title. Say what is current, and say so when \
something said in a meeting changed or contradicts a document. If the sources \
do not answer the question, say that rather than guessing.

Question: {question}"""

ORACLE_PROMPT = PROMPT + """

This answer will be the reference other answers are graded against, so be \
thorough: find every meeting that bears on the question, check what the \
document says now, and quote the passages your answer rests on."""


def mcp_config(path, codeeagle, tools, log):
    args = ["mcp", "serve", "--tools", tools, "--log", str(log)]
    path.write_text(json.dumps({"mcpServers": {"ce": {"command": codeeagle, "args": args}}}))
    return path


def command(arm, spec, prompt, args, mcp_dir, out):
    oracle = spec.get("oracle", False)
    cmd = [
        "claude", "-p", prompt,
        "--output-format", "json",
        "--no-session-persistence",
        # Restricted mode ignores user, project and local settings, and with
        # --strict-mcp-config nothing but the arm's own server: no personal
        # CLAUDE.md, hooks or plugins leak into what is measured.
        "--restricted", "--strict-mcp-config",
        "--permission-mode", "dontAsk",
        "--tools", spec["tools"],
        "--model", args.oracle_model if oracle else spec.get("model", args.model),
        "--max-turns", str(args.oracle_turns if oracle else args.max_turns),
    ]
    if args.max_budget and not oracle:
        cmd += ["--max-budget-usd", str(args.max_budget)]
    if spec["tools"] and args.sessions:
        cmd += ["--add-dir", args.sessions]
    if spec["tools"] and args.docs:
        cmd += ["--add-dir", args.docs]
    if spec["mcp"]:
        # Each answer gets its own server log: which tools it called, with
        # what, is half of explaining what it cost.
        cfg = mcp_config(mcp_dir / f"{out.stem}.{arm}.json", args.codeeagle, spec["mcp"], out.with_suffix(".mcp.log"))
        cmd += ["--mcp-config", str(cfg), "--allowedTools", "mcp__ce"]
        if spec.get("deny"):
            cmd += ["--disallowedTools", ",".join(f"mcp__ce__{t}" for t in spec["deny"])]
    return cmd


def run_one(arm, spec, q, rep, args, out, mcp_dir):
    template = ORACLE_PROMPT if spec.get("oracle") else PROMPT
    prompt = template.format(docs=args.docs or os.path.join(args.corpus, "specs"), sessions=args.sessions or "(not available)",
                             question=q["question"])
    started = time.time()
    proc = subprocess.run(command(arm, spec, prompt, args, mcp_dir, out), cwd=args.corpus,
                          capture_output=True, text=True, timeout=args.timeout)
    record = {"arm": arm, "id": q["id"], "rep": rep, "started": started,
              "wall_s": round(time.time() - started, 1), "exit": proc.returncode}
    try:
        record["claude"] = json.loads(proc.stdout)
    except json.JSONDecodeError:
        record["raw"] = proc.stdout[-4000:]
        record["stderr"] = proc.stderr[-4000:]
    tmp = out.with_suffix(".tmp")
    tmp.write_text(json.dumps(record, indent=1))
    tmp.rename(out)
    return record


def summary(record):
    c = record.get("claude") or {}
    u = c.get("usage") or {}
    tokens_in = u.get("input_tokens", 0) + u.get("cache_creation_input_tokens", 0) + u.get("cache_read_input_tokens", 0)
    return (f"{record['arm']:6} {record['id']} r{record['rep']} exit={record['exit']} turns={c.get('num_turns')} "
            f"in={tokens_in} out={u.get('output_tokens', 0)} ${c.get('total_cost_usd', 0):.3f} {record['wall_s']}s")


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--bench-dir", default=os.environ.get("RELATION_BENCH_DIR"), required=not os.environ.get("RELATION_BENCH_DIR"))
    ap.add_argument("--corpus", required=True, help="the indexed project's root; agents run here")
    ap.add_argument("--codeeagle", required=True, help="the codeeagle binary the MCP arms use")
    ap.add_argument("--sessions", help="meeting transcripts directory, for the file tools")
    ap.add_argument("--docs", help="the documents directory, when it is not <corpus>/specs (the index's project "
                                   "root can live apart from the files it indexes)")
    ap.add_argument("--arms", default="grep,mcp,verbs,all")
    ap.add_argument("--only", help="comma-separated question ids")
    ap.add_argument("--reps", type=int, default=1)
    ap.add_argument("--model", default="sonnet")
    ap.add_argument("--max-turns", type=int, default=15)
    ap.add_argument("--max-budget", type=float, default=0.10, help="dollars per answer; 0 for none")
    ap.add_argument("--oracle-model", default="opus")
    ap.add_argument("--oracle-turns", type=int, default=60)
    ap.add_argument("--concurrency", type=int, default=3)
    ap.add_argument("--timeout", type=int, default=900)
    args = ap.parse_args()

    bench = pathlib.Path(args.bench_dir)
    args.corpus = str(pathlib.Path(args.corpus).resolve())
    questions = [json.loads(line) for line in (bench / "questions.jsonl").read_text().splitlines() if line.strip()]
    if args.only:
        keep = set(args.only.split(","))
        questions = [q for q in questions if q["id"] in keep]

    jobs = []
    for arm in args.arms.split(","):
        if arm not in ARMS:
            sys.exit(f"unknown arm {arm!r}; want one of {', '.join(ARMS)}")
        (bench / "runs" / arm).mkdir(parents=True, exist_ok=True)
        reps = 1 if ARMS[arm].get("oracle") else args.reps
        for q in questions:
            for rep in range(1, reps + 1):
                out = bench / "runs" / arm / f"{q['id']}.r{rep}.json"
                if not out.exists():
                    jobs.append((arm, q, rep, out))
    print(f"{len(jobs)} answers to collect", flush=True)

    with tempfile.TemporaryDirectory() as mcp_dir, \
            concurrent.futures.ThreadPoolExecutor(args.concurrency) as pool:
        futures = [pool.submit(run_one, arm, ARMS[arm], q, rep, args, out, pathlib.Path(mcp_dir))
                   for arm, q, rep, out in jobs]
        for f in concurrent.futures.as_completed(futures):
            try:
                print(summary(f.result()), flush=True)
            except Exception as e:  # one failed answer must not stop the rest
                print(f"failed: {e}", flush=True)


if __name__ == "__main__":
    main()
