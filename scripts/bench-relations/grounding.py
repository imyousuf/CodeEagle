#!/usr/bin/env python3
"""Checks whether brief lines say what their quotes support.

Every line of a brief carries quotes that were verified to appear in the
sources, but a verified quote does not prove the sentence written around it.
This samples lines across every kind (headline, current, events, unresolved,
conflicts), shows a judge each line beside its quotes and nothing else, and
asks whether the quotes support it. A person should then read the sample in
grounding.md and check the judge.

  grounding.py --bench-dir DIR --corpus DIR --codeeagle PATH [--sample 60]

Writes briefs.jsonl (every brief, as brief --json prints it), grounding.jsonl
(one verdict per sampled line) and grounding.md into the bench directory.
With --context the judge also sees the passage around each quote, as show
prints it, and the results go to grounding-context.*: a line can be true to
its source while saying more than its short quote.
"""

import argparse
import concurrent.futures
import json
import os
import pathlib
import random
import re
import subprocess

JUDGE = """A summary of a document and the meetings that discussed it makes the \
claim below, and cites the quoted passages as its support. The quotes are \
verbatim from the sources.

Claim ({kind}): {text}

Quotes:
{quotes}

{context}Do the quotes support the claim? Judge only what the quotes{and_passages} say, \
not what you know.
- supported: the quotes state or directly imply everything the claim says.
- partial: the quotes support the gist, but the claim adds or changes a \
detail (a name, number, date, who decided, or which side said what).
- unsupported: the quotes do not support the claim, or contradict it.

Reply with only a JSON object: {{"verdict": "supported"|"partial"|"unsupported", \
"why": "one sentence"}}"""


def export(args, bench):
    out = bench / "briefs.jsonl"
    if out.exists():
        return [json.loads(l) for l in out.read_text().splitlines() if l.strip()]
    listing = subprocess.run([args.codeeagle, "query", "--type", "Relation"], cwd=args.corpus,
                             capture_output=True, text=True, check=True).stdout
    ids = [line.split()[0] for line in listing.splitlines() if " Relation " in line]
    briefs = []
    for rid in ids:
        p = subprocess.run([args.codeeagle, "brief", rid[:12], "--json"], cwd=args.corpus, capture_output=True, text=True)
        try:
            b = json.loads(p.stdout)
        except json.JSONDecodeError:
            continue
        if b.get("brief"):
            briefs.append(b)
    out.write_text("".join(json.dumps(b) + "\n" for b in briefs))
    return briefs


def lines_of(b):
    br = b["brief"]
    ev = {e["id"]: e for e in b.get("evidence") or []}
    def item(kind, text, cites):
        return {"subject": b["subject"], "kind": kind, "text": text,
                "quotes": [{"id": c, "kind": ev[c]["kind"], "quote": ev[c]["quote"]} for c in cites if c in ev]}
    out = []
    if br.get("headline", {}).get("text"):
        out.append(item("headline", br["headline"]["text"], br["headline"].get("cites") or []))
    for l in br.get("current") or []:
        out.append(item("current", l["text"], l.get("cites") or []))
    for e in br.get("events") or []:
        out.append(item("event", f"{e.get('date', '')}: {e['text']}", e.get("cites") or []))
    for u in br.get("unresolved") or []:
        out.append(item("unresolved", u["text"], u.get("cites") or []))
    for c in br.get("conflicts") or []:
        out.append(item("conflict", f"the document says {c['doc_says']}, but a meeting on {c.get('date', '')} said {c['discussion_says']}",
                        [c["doc_cite"], c["said_cite"]]))
    return out


def expand(args, cite):
    """The passage around a quote, as show prints it."""
    p = subprocess.run([args.codeeagle, "show", cite], cwd=args.corpus, capture_output=True, text=True)
    return (p.stdout or p.stderr)[:2500]


def judge(item, args):
    quotes = "\n".join(f"- [{q['kind']}] \"{q['quote']}\"" for q in item["quotes"]) or "(none)"
    context, and_passages = "", ""
    if args.context:
        passages = "\n\n".join(f"Passage around quote {i + 1}:\n{expand(args, q['id'])}" for i, q in enumerate(item["quotes"]))
        context = passages + "\n\n"
        and_passages = " and the passages around them"
    prompt = JUDGE.format(kind=item["kind"], text=item["text"], quotes=quotes, context=context, and_passages=and_passages)
    proc = subprocess.run(
        ["claude", "-p", prompt,
         "--output-format", "json", "--no-session-persistence", "--restricted", "--strict-mcp-config",
         "--tools", "", "--model", args.judge, "--max-turns", "1"],
        capture_output=True, text=True, timeout=300, cwd="/tmp")
    result = json.loads(proc.stdout).get("result", "")
    m = re.search(r"\{.*\}", result, re.S)
    return json.loads(m.group(0)) if m else {"verdict": "error", "why": result[:200]}


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--bench-dir", default=os.environ.get("RELATION_BENCH_DIR"), required=not os.environ.get("RELATION_BENCH_DIR"))
    ap.add_argument("--corpus", required=True)
    ap.add_argument("--codeeagle", required=True)
    ap.add_argument("--sample", type=int, default=60)
    ap.add_argument("--judge", default="opus")
    ap.add_argument("--seed", type=int, default=11)
    ap.add_argument("--concurrency", type=int, default=4)
    ap.add_argument("--only-model", help="sample only briefs written by a model whose name contains this")
    ap.add_argument("--context", action="store_true",
                    help="show the judge the passage around each quote, as show prints it, not only the quote")
    args = ap.parse_args()
    bench = pathlib.Path(args.bench_dir)

    briefs = export(args, bench)
    if args.only_model:
        briefs = [b for b in briefs if args.only_model in (b.get("model") or "")]
    items = [it for b in briefs for it in lines_of(b)]
    rng = random.Random(args.seed)
    # Stratified: conflicts and headlines are few but matter most, so each
    # kind gets its share of the sample rather than its share of the lines.
    by_kind = {}
    for it in items:
        by_kind.setdefault(it["kind"], []).append(it)
    per_kind = max(1, args.sample // len(by_kind))
    sample = []
    for kind, its in sorted(by_kind.items()):
        sample += rng.sample(its, min(per_kind, len(its)))

    with concurrent.futures.ThreadPoolExecutor(args.concurrency) as pool:
        verdicts = list(pool.map(lambda it: judge(it, args), sample))
    rows = [it | {"judged": v} for it, v in zip(sample, verdicts)]
    name = "grounding-context" if args.context else "grounding"
    (bench / f"{name}.jsonl").write_text("".join(json.dumps(r) + "\n" for r in rows))

    counts = {}
    for r in rows:
        k = (r["kind"], r["judged"].get("verdict"))
        counts[k] = counts.get(k, 0) + 1
    kinds = sorted({r["kind"] for r in rows})
    table = ["| kind | lines | supported | partial | unsupported |", "|---|---|---|---|---|"]
    for kind in kinds + ["all"]:
        sel = [r for r in rows if kind == "all" or r["kind"] == kind]
        n = len(sel)
        c = {v: sum(r["judged"].get("verdict") == v for r in sel) for v in ("supported", "partial", "unsupported")}
        table.append(f"| {kind} | {n} | {c['supported']} ({c['supported'] / n:.0%}) | {c['partial']} | {c['unsupported']} |")
    detail = []
    for r in rows:
        if r["judged"].get("verdict") != "supported":
            qs = "\n".join(f"  - \"{q['quote']}\"" for q in r["quotes"])
            detail.append(f"- **{r['judged'].get('verdict')}** ({r['kind']}, {r['subject']}): {r['text']}\n{qs}\n  judge: {r['judged'].get('why')}")
    report = (f"{len(briefs)} briefs, {len(items)} lines; {len(rows)} sampled.\n\n" + "\n".join(table) +
              "\n\n## Lines not fully supported\n\n" + "\n".join(detail) + "\n")
    (bench / f"{name}.md").write_text(report)
    print("\n".join(table))


if __name__ == "__main__":
    main()
