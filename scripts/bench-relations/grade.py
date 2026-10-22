#!/usr/bin/env python3
"""Grades the benchmark's answers blind and reports cost against accuracy.

Each answer is graded on its own against the question's reference answer by a
judge that is never told which tools produced it, in shuffled order. Then the
grades are joined back to their arms:

  grades/<arm>/<id>.r<rep>.json
  report.md        per arm: accuracy, staleness, and the median cost of an
                   answer, with each arm's cost as a multiple of grep's
  spotcheck.md     a random sample of answers and their grades, arms hidden,
                   for a person to check the judge

  grade.py --bench-dir DIR [--judge opus] [--arms grep,mcp,verbs,all]
"""

import argparse
import concurrent.futures
import json
import os
import pathlib
import random
import re
import statistics
import subprocess

JUDGE = """You are grading an answer to a question about a team's tech specs and \
meetings, against a reference answer a person has checked.

Question: {question}

Reference answer:
{reference}

Answer to grade:
{answer}

Grade it:
- correct: 2 if it agrees with the reference on every point that matters, 1 if \
it is partly right or misses a key point, 0 if it is wrong, empty, or declines \
when the reference has an answer. Extra detail the reference lacks is not an \
error unless it contradicts the reference. Facts the reference marks as coming \
from a meeting that is not in the index, or unindexed, are optional: an answer \
is not marked down for leaving them out.
- stale: true if it presents as current something the reference says was \
superseded or changed.
- contradictions: how many of its claims contradict the reference.
- cited: true if its claims name their sources (a document and section, or a \
meeting's date or title).

Reply with only a JSON object: {{"correct": 0|1|2, "stale": true|false, \
"contradictions": N, "cited": true|false, "why": "one sentence"}}"""


def answer_text(record):
    c = record.get("claude") or {}
    if c.get("is_error") or record.get("exit"):
        return c.get("result") or "(no answer: the run failed)"
    return c.get("result") or "(no answer)"


def cost(record):
    c = record.get("claude") or {}
    u = c.get("usage") or {}
    return {
        "input": u.get("input_tokens", 0) + u.get("cache_creation_input_tokens", 0) + u.get("cache_read_input_tokens", 0),
        "output": u.get("output_tokens", 0),
        "turns": c.get("num_turns") or 0,
        "usd": c.get("total_cost_usd") or 0.0,
        "wall": record.get("wall_s", 0.0),
    }


def judge(prompt, model):
    proc = subprocess.run(
        ["claude", "-p", prompt, "--output-format", "json", "--no-session-persistence",
         "--restricted", "--strict-mcp-config", "--tools", "", "--model", model, "--max-turns", "1"],
        capture_output=True, text=True, timeout=300, cwd="/tmp")
    result = json.loads(proc.stdout).get("result", "")
    m = re.search(r"\{.*\}", result, re.S)
    if not m:
        raise ValueError(f"judge reply had no JSON: {result[:200]}")
    return json.loads(m.group(0))


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--bench-dir", default=os.environ.get("RELATION_BENCH_DIR"), required=not os.environ.get("RELATION_BENCH_DIR"))
    ap.add_argument("--arms", default="grep,mcp,verbs,all")
    ap.add_argument("--judge", default="opus")
    ap.add_argument("--concurrency", type=int, default=4)
    ap.add_argument("--spotcheck", type=int, default=12)
    ap.add_argument("--seed", type=int, default=7)
    args = ap.parse_args()

    bench = pathlib.Path(args.bench_dir)
    questions = {q["id"]: q for q in (json.loads(l) for l in (bench / "questions.jsonl").read_text().splitlines() if l.strip())}
    arms = args.arms.split(",")

    todo = []
    for arm in arms:
        for path in sorted((bench / "runs" / arm).glob("*.json")):
            qid = path.name.split(".")[0]
            if qid not in questions or not questions[qid].get("reference"):
                continue
            out = bench / "grades" / arm / path.name
            todo.append((arm, qid, path, out))
    rng = random.Random(args.seed)
    rng.shuffle(todo)  # the judge sees answers in no order that reveals their arm

    def grade(item):
        arm, qid, path, out = item
        if out.exists():
            return
        record = json.loads(path.read_text())
        q = questions[qid]
        g = judge(JUDGE.format(question=q["question"], reference=q["reference"], answer=answer_text(record)), args.judge)
        out.parent.mkdir(parents=True, exist_ok=True)
        out.write_text(json.dumps({"grade": g, "cost": cost(record)}, indent=1))

    with concurrent.futures.ThreadPoolExecutor(args.concurrency) as pool:
        for f in concurrent.futures.as_completed([pool.submit(grade, t) for t in todo]):
            try:
                f.result()
            except Exception as e:
                print(f"grading failed: {e}")

    rows = {}
    graded = []
    for arm in arms:
        gs = [json.loads(p.read_text()) | {"name": p.name} for p in sorted((bench / "grades" / arm).glob("*.json"))]
        if not gs:
            continue
        graded += [(arm, g) for g in gs]
        med = lambda k: statistics.median(g["cost"][k] for g in gs)
        rows[arm] = {
            "n": len(gs),
            "correct": statistics.mean(g["grade"]["correct"] for g in gs) / 2,
            "full": sum(g["grade"]["correct"] == 2 for g in gs) / len(gs),
            "stale": sum(bool(g["grade"]["stale"]) for g in gs),
            "contra": sum(int(g["grade"].get("contradictions", 0)) for g in gs),
            "cited": sum(bool(g["grade"]["cited"]) for g in gs) / len(gs),
            "input": med("input"), "output": med("output"), "turns": med("turns"),
            "usd": med("usd"), "wall": med("wall"),
        }

    base = rows.get("grep")
    lines = ["| arm | n | accuracy | fully right | stale | contradictions | cited | input tokens | output | turns | $ | wall s |",
             "|---|---|---|---|---|---|---|---|---|---|---|---|"]
    for arm, r in rows.items():
        ratio = f" ({base['input'] / r['input']:.1f}x less)" if base and arm != "grep" and r["input"] else ""
        lines.append(f"| {arm} | {r['n']} | {r['correct']:.2f} | {r['full']:.0%} | {r['stale']} | {r['contra']} | {r['cited']:.0%} | "
                     f"{r['input']:,.0f}{ratio} | {r['output']:,.0f} | {r['turns']:.0f} | {r['usd']:.3f} | {r['wall']:.0f} |")
    report = "\n".join(lines)
    (bench / "report.md").write_text("Medians per answer; accuracy is the mean grade over 2.\n\n" + report + "\n")
    print(report)

    sample = rng.sample(graded, min(args.spotcheck, len(graded)))
    spot = []
    for i, (arm, g) in enumerate(sample, 1):
        qid, rep = g["name"].split(".")[0], g["name"].split(".")[1]
        record = json.loads((bench / "runs" / arm / g["name"]).read_text())
        spot.append(f"## {i}. {qid} ({rep})\n\n**Question:** {questions[qid]['question']}\n\n"
                    f"**Answer:**\n\n{answer_text(record)}\n\n**Grade:** {json.dumps(g['grade'])}\n")
    (bench / "spotcheck.md").write_text("Arms are hidden; the key is the grades/ directory.\n\n" + "\n".join(spot))


if __name__ == "__main__":
    main()
