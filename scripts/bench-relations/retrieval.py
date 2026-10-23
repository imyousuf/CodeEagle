#!/usr/bin/env python3
"""Measures retrieval without an agent: can the verbs reach what an answer needs?

For each question with sources, it asks the index directly:

  find@8        the share of the question's required documents that appear in
                find's lines for the question itself
  brief reach   the share of the question's required meetings (by date) and
                linked documents named in the briefs of its required
                documents: reachable in two calls, find then brief

It costs no model calls beyond the embedding of each query, so it can run at
every change. It bounds what an agent can find in two calls; it does not
grade answers.

  retrieval.py --bench-dir DIR --corpus DIR --codeeagle PATH

questions.jsonl records carry "sources": {"docs": [paths as indexed],
"meetings": ["YYYY-MM-DD", ...]}.
"""

import argparse
import json
import os
import pathlib
import re
import statistics
import subprocess


def run(args, *cmd):
    p = subprocess.run([args.codeeagle, *cmd], cwd=args.corpus, capture_output=True, text=True, timeout=300)
    return p.stdout


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--bench-dir", default=os.environ.get("RELATION_BENCH_DIR"), required=not os.environ.get("RELATION_BENCH_DIR"))
    ap.add_argument("--corpus", required=True)
    ap.add_argument("--codeeagle", required=True)
    args = ap.parse_args()
    bench = pathlib.Path(args.bench_dir)
    questions = [json.loads(l) for l in (bench / "questions.jsonl").read_text().splitlines() if l.strip()]

    rows, by_cat = [], {}
    for q in questions:
        src = q.get("sources") or {}
        docs, meetings = src.get("docs") or [], src.get("meetings") or []
        if not docs and not meetings:
            continue
        found = run(args, "find", q["question"])
        find_hit = [d for d in docs if d in found]
        briefs = "".join(run(args, "brief", d, "--budget", "2000") for d in docs)
        reach_meet = [m for m in meetings if m in briefs]
        linked = [d for d in docs[1:] if d in briefs]
        r = {
            "id": q["id"], "category": q.get("category", ""),
            "find8": len(find_hit) / len(docs) if docs else None,
            "reach": (len(reach_meet) + len(linked)) / (len(meetings) + max(0, len(docs) - 1)) if (meetings or len(docs) > 1) else None,
            "missing_find": [d for d in docs if d not in find_hit],
            "missing_meetings": [m for m in meetings if m not in reach_meet],
        }
        rows.append(r)
        by_cat.setdefault(r["category"], []).append(r)
        print(f"{q['id']} find@8 {r['find8'] if r['find8'] is not None else '-'} reach {r['reach'] if r['reach'] is not None else '-'}", flush=True)

    def mean(xs):
        xs = [x for x in xs if x is not None]
        return statistics.mean(xs) if xs else float("nan")

    lines = ["| category | questions | find@8 | brief reach |", "|---|---|---|---|"]
    for cat, rs in sorted(by_cat.items()):
        lines.append(f"| {cat} | {len(rs)} | {mean(r['find8'] for r in rs):.2f} | {mean(r['reach'] for r in rs):.2f} |")
    lines.append(f"| all | {len(rows)} | {mean(r['find8'] for r in rows):.2f} | {mean(r['reach'] for r in rows):.2f} |")
    (bench / "retrieval.md").write_text("\n".join(lines) + "\n")
    (bench / "retrieval.jsonl").write_text("".join(json.dumps(r) + "\n" for r in rows))
    print("\n".join(lines))


if __name__ == "__main__":
    main()
