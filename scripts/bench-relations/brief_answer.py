#!/usr/bin/env python3
"""Grades the index rather than an agent: each question's answer is the briefs
of the documents it needs, as one multi-ID brief call returns them.

What an agent can answer from is bounded by what the index can hand it, so
grading the briefs themselves (grade.py --arms brief) shows the ceiling per
category without paying for an agent. Questions that name no documents are
answered by brief on the question's own words, as an agent's first call
would be.

  brief_answer.py --bench-dir DIR --corpus DIR --codeeagle PATH [--budget 2000]

Writes runs/brief/<id>.r1.json in the shape bench.py writes, so grade.py
reads them like any arm.
"""

import argparse
import hashlib
import json
import os
import pathlib
import subprocess
import time


def relation_id(path):
    """graph.NewNodeID("Relation", "", "doc:"+path)."""
    return hashlib.sha256(f"Relation::doc:{path}".encode()).hexdigest()[:24]


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--bench-dir", default=os.environ.get("RELATION_BENCH_DIR"), required=not os.environ.get("RELATION_BENCH_DIR"))
    ap.add_argument("--corpus", required=True)
    ap.add_argument("--codeeagle", required=True)
    ap.add_argument("--budget", type=int, default=2000)
    args = ap.parse_args()
    bench = pathlib.Path(args.bench_dir)
    out_dir = bench / "runs" / "brief"
    out_dir.mkdir(parents=True, exist_ok=True)
    for line in (bench / "questions.jsonl").read_text().splitlines():
        if not line.strip():
            continue
        q = json.loads(line)
        docs = (q.get("sources") or {}).get("docs") or []
        target = ", ".join(relation_id(d)[:12] for d in docs) if docs else q["question"]
        started = time.time()
        p = subprocess.run([args.codeeagle, "brief", target, "--budget", str(args.budget)], cwd=args.corpus,
                           capture_output=True, text=True, errors="replace", timeout=300)
        record = {"arm": "brief", "id": q["id"], "rep": 1, "started": started, "wall_s": round(time.time() - started, 1),
                  "exit": p.returncode, "claude": {"result": p.stdout.strip(), "num_turns": 0, "total_cost_usd": 0,
                                                   "usage": {}}}
        (out_dir / f"{q['id']}.r1.json").write_text(json.dumps(record, indent=1))
        print(q["id"], len(p.stdout), "chars", flush=True)


if __name__ == "__main__":
    main()
