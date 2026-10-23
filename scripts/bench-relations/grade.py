#!/usr/bin/env python3
"""Grades the benchmark's answers blind and reports cost against accuracy.

Each answer is graded on its own against the question's checked key points by
a judge that never sees which tools produced it, in shuffled order. The judge
lists the answer's claims with a verdict each and says how fully each key
point is covered. From that:

  correctness   1 - (contradicted + stale + fabricated) / claims: is what the
                answer says true, current and grounded?
  completeness  (full + 0.5 * partial) / required key points: how much of what
                a correct answer needs does it hold?
  grade         0-2 as before: 2 agrees on every point that matters

For an unanswerable question the key point is that the sources do not record
it, so declining with a reason is complete and anything asserted in its place
is fabricated. The report adds the answered rate, false declines (declining
what the sources answer) and hallucinations (answering what they do not).

  grades/<arm>/<id>.r<rep>.json
  report.md       per arm, and per arm and category
  spotcheck.md    a sample of graded answers, arms hidden

  grade.py --bench-dir DIR [--judge opus] [--arms verbs,all,grep]

questions.jsonl records: {"id", "category", "question", "answerable",
"key_points": [{"id", "text", "optional"}], "reference"}. Older records with
only "reference" are graded against it as a single key point.
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

JUDGE = """You are grading an answer to a question about a team's documents and \
meetings, against key points a person has checked against the sources.

Question: {question}
{answerability}
Key points a correct answer needs:
{key_points}

Answer to grade:
{answer}

Do three things.
1. List every factual claim the answer makes (skip hedges and pointers). For \
each give a verdict: "supported" (agrees with the key points or is a detail \
consistent with them), "extra_ok" (not in the key points and not contradicted \
by them), "contradicted" (conflicts with a key point), "stale" (presents as \
current something the key points say was superseded or changed), \
"fabricated" (asserts something the key points say the sources do not \
contain, or invents a source).
2. For each key point, say whether the answer covers it "full", "partial" or \
"none". Optional key points are graded the same way but are not required.
3. Give "declined": true if the answer says it cannot answer or that the \
sources do not cover the question; "correct": 2 if it agrees with the key \
points on everything that matters, 1 if partly right, 0 if wrong or empty; \
"cited": true if its claims name their sources.

Reply with only a JSON object:
{{"claims": [{{"claim": "...", "verdict": "..."}}], \
"key_points": [{{"id": "...", "coverage": "full|partial|none"}}], \
"declined": true|false, "correct": 0|1|2, "cited": true|false, "why": "one sentence"}}"""

BAD = {"contradicted", "stale", "fabricated"}


def key_points(q):
    if q.get("key_points"):
        return q["key_points"]
    return [{"id": "k1", "text": q.get("reference", ""), "optional": False}]


def answer_text(record):
    c = record.get("claude") or {}
    if c.get("is_error") or record.get("exit"):
        return None
    return c.get("result") or None


def cost(record):
    c = record.get("claude") or {}
    u = c.get("usage") or {}
    return {
        "input": u.get("input_tokens", 0) + u.get("cache_creation_input_tokens", 0) + u.get("cache_read_input_tokens", 0),
        "output": u.get("output_tokens", 0),
        "turns": c.get("num_turns") or 0,
        "usd": c.get("total_cost_usd") or 0.0,
        "wall": record.get("wall_s", 0.0),
        "capped": c.get("subtype") in ("error_max_budget_usd", "error_max_turns"),
    }


def judge(prompt, model):
    proc = subprocess.run(
        ["claude", "-p", prompt, "--output-format", "json", "--no-session-persistence",
         "--restricted", "--strict-mcp-config", "--tools", "", "--model", model, "--max-turns", "1"],
        capture_output=True, text=True, timeout=600, cwd="/tmp")
    result = json.loads(proc.stdout).get("result", "")
    m = re.search(r"\{.*\}", result, re.S)
    if not m:
        raise ValueError(f"judge reply had no JSON: {result[:200]}")
    return json.loads(m.group(0))


def score(q, g, answered):
    """correctness, completeness from a judge's verdicts."""
    kps = key_points(q)
    required = [k["id"] for k in kps if not k.get("optional")]
    cov = {k.get("id"): k.get("coverage") for k in g.get("key_points", [])}
    if not answered:
        return None, 0.0
    claims = g.get("claims", [])
    bad = sum(c.get("verdict") in BAD for c in claims)
    correctness = 1.0 - bad / max(1, len(claims))
    if not required:
        completeness = 1.0
    else:
        completeness = sum(1.0 if cov.get(k) == "full" else 0.5 if cov.get(k) == "partial" else 0.0 for k in required) / len(required)
    return correctness, completeness


def pct(xs, p):
    xs = sorted(xs)
    if not xs:
        return 0.0
    i = min(len(xs) - 1, int(round(p * (len(xs) - 1))))
    return xs[i]


def bootstrap(per_q, rng, n=2000):
    """95% interval of the mean over questions, resampling questions."""
    vals = [v for v in per_q if v is not None]
    if len(vals) < 2:
        return (float("nan"), float("nan"))
    means = sorted(statistics.mean(rng.choice(vals) for _ in vals) for _ in range(n))
    return means[int(0.025 * n)], means[int(0.975 * n)]


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
            if path.name.endswith(".mcp.log"):
                continue
            qid = path.name.split(".")[0]
            if qid not in questions:
                continue
            todo.append((arm, qid, path, bench / "grades" / arm / path.name))
    rng = random.Random(args.seed)
    rng.shuffle(todo)  # the judge sees answers in no order that reveals their arm

    def grade(item):
        arm, qid, path, out = item
        if out.exists():
            return
        record = json.loads(path.read_text())
        q = questions[qid]
        text = answer_text(record)
        g = {"claims": [], "key_points": [], "declined": False, "correct": 0, "cited": False,
             "why": "the run failed and produced no answer"}
        if text is not None:
            kps = "\n".join(f"- [{k['id']}]{' (optional)' if k.get('optional') else ''} {k['text']}" for k in key_points(q))
            answerability = "" if q.get("answerable", True) else (
                "The sources do NOT answer this question; a correct answer says so and asserts nothing in its place.\n")
            g = judge(JUDGE.format(question=q["question"], answerability=answerability, key_points=kps, answer=text), args.judge)
        correctness, completeness = score(q, g, text is not None)
        out.parent.mkdir(parents=True, exist_ok=True)
        out.write_text(json.dumps({"grade": g, "cost": cost(record), "answered": text is not None,
                                   "correctness": correctness, "completeness": completeness}, indent=1))

    with concurrent.futures.ThreadPoolExecutor(args.concurrency) as pool:
        for f in concurrent.futures.as_completed([pool.submit(grade, t) for t in todo]):
            try:
                f.result()
            except Exception as e:
                print(f"grading failed: {e}")

    def summarise(gs):
        """gs: list of (qid, grade-record). Per-question means first, so reps weigh equally."""
        by_q = {}
        for qid, g in gs:
            by_q.setdefault(qid, []).append(g)
        def qmean(key):
            vals = []
            for qid, rs in by_q.items():
                xs = [r[key] for r in rs if r[key] is not None]
                vals.append(statistics.mean(xs) if xs else None)
            return vals
        corr, comp = qmean("correctness"), qmean("completeness")
        grades = [r["grade"].get("correct", 0) for _, r in gs]
        answerable = [(qid, r) for qid, r in gs if questions[qid].get("answerable", True)]
        negative = [(qid, r) for qid, r in gs if not questions[qid].get("answerable", True)]
        costs = [r["cost"] for _, r in gs]
        cl, ch = bootstrap(comp, rng)
        rl, rh = bootstrap(corr, rng)
        return {
            "n": len(gs),
            "answered": sum(r["answered"] for _, r in gs) / len(gs),
            "correctness": statistics.mean([v for v in corr if v is not None]) if any(v is not None for v in corr) else 0.0,
            "corr_ci": (rl, rh),
            "completeness": statistics.mean([v if v is not None else 0.0 for v in comp]),
            "comp_ci": (cl, ch),
            "grade": statistics.mean(grades) / 2,
            "full": sum(g == 2 for g in grades) / len(grades),
            "false_decline": (sum(r["grade"].get("declined", False) for _, r in answerable) / len(answerable)) if answerable else 0.0,
            "hallucination": (sum(not r["grade"].get("declined", False) and r["answered"] for _, r in negative) / len(negative)) if negative else 0.0,
            "usd_med": statistics.median(c["usd"] for c in costs), "usd_mean": statistics.mean(c["usd"] for c in costs),
            "usd_p90": pct([c["usd"] for c in costs], 0.9),
            "wall_med": statistics.median(c["wall"] for c in costs), "wall_p90": pct([c["wall"] for c in costs], 0.9),
            "turns": statistics.median(c["turns"] for c in costs), "input": statistics.median(c["input"] for c in costs),
            "capped": sum(c["capped"] for c in costs),
        }

    head = ("| arm | n | answered | correctness (95% CI) | completeness (95% CI) | grade | fully right | false decline | "
            "hallucination | $ median / mean / p90 | wall s median / p90 | turns | input tokens | capped |")
    sep = "|" + "---|" * 14

    def row(name, s):
        return (f"| {name} | {s['n']} | {s['answered']:.0%} | {s['correctness']:.2f} ({s['corr_ci'][0]:.2f}-{s['corr_ci'][1]:.2f}) | "
                f"{s['completeness']:.2f} ({s['comp_ci'][0]:.2f}-{s['comp_ci'][1]:.2f}) | {s['grade']:.2f} | {s['full']:.0%} | "
                f"{s['false_decline']:.0%} | {s['hallucination']:.0%} | {s['usd_med']:.3f} / {s['usd_mean']:.3f} / {s['usd_p90']:.3f} | "
                f"{s['wall_med']:.0f} / {s['wall_p90']:.0f} | {s['turns']:.0f} | {s['input']:,.0f} | {s['capped']} |")

    graded = {}
    for arm in arms:
        gs = []
        for p in sorted((bench / "grades" / arm).glob("*.json")):
            qid = p.name.split(".")[0]
            if qid in questions:
                gs.append((qid, json.loads(p.read_text()) | {"name": p.name}))
        if gs:
            graded[arm] = gs

    lines = ["## By arm", "", head, sep]
    for arm, gs in graded.items():
        lines.append(row(arm, summarise(gs)))
    cats = sorted({questions[q].get("category", "uncategorised") for gs in graded.values() for q, _ in gs})
    for cat in cats:
        lines += ["", f"## {cat}", "", head, sep]
        for arm, gs in graded.items():
            sub = [(q, g) for q, g in gs if questions[q].get("category", "uncategorised") == cat]
            if sub:
                lines.append(row(arm, summarise(sub)))
    report = "\n".join(lines)
    (bench / "report.md").write_text("Per-question means; reps weigh equally. Correctness is over answered runs.\n\n" + report + "\n")
    print("\n".join(lines[:3 + len(graded) + 1]))

    flat = [(arm, q, g) for arm, gs in graded.items() for q, g in gs]
    spot = []
    for i, (arm, qid, g) in enumerate(rng.sample(flat, min(args.spotcheck, len(flat))), 1):
        record = json.loads((bench / "runs" / arm / g["name"]).read_text())
        spot.append(f"## {i}. {qid}\n\n**Question:** {questions[qid]['question']}\n\n"
                    f"**Answer:**\n\n{answer_text(record) or '(no answer)'}\n\n**Grade:** {json.dumps(g['grade'])[:1500]}\n")
    (bench / "spotcheck.md").write_text("Arms are hidden; the key is the grades/ directory.\n\n" + "\n".join(spot))


if __name__ == "__main__":
    main()
