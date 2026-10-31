#!/usr/bin/env python3
"""The verbs arm on models Claude Code cannot drive: any OpenAI-compatible API.

Each answer runs its own `codeeagle mcp serve --tools verbs`, as bench.py's
verbs arm does, so the agent sees the same three tools, the same tool
descriptions and the same server instructions. What differs is the loop
around them: a minimal system prompt instead of Claude Code's, and this
script's tool calling. The question prompt is bench.py's, word for word, and
the limits are the same: 15 model calls and $0.10 per answer, after which the
answer is lost, as Claude Code loses it.

Cost is computed from each response's token counts and the provider's prices:
Baseten publishes them in /v1/models; other providers' are given here.

  bench_openai.py --bench-dir DIR --corpus DIR --codeeagle PATH \\
      --arm glm-5.3=baseten:zai-org/GLM-5.3 --arm flash-lite=vertex:google/gemini-3.5-flash-lite \\
      [--reps 1] [--concurrency 4]

Keys: Baseten's from BASETEN_API_KEY, or the command in --baseten-key-cmd;
Vertex AI's from Application Default Credentials (gcloud) for --gcp-project.

Writes runs/<arm>/<id>.r<rep>.json in bench.py's shape, so grade.py grades
them like any other arm. Resumable: an answer on disk is not asked again.
"""

import argparse
import concurrent.futures
import importlib.util
import json
import os
import pathlib
import subprocess
import threading
import time
import urllib.error
import urllib.request

HERE = pathlib.Path(__file__).resolve().parent

SYSTEM = """You answer questions about a team's work using the tools you are given. \
Use them to find what the documents and meetings say, then write your answer \
as plain text. When you have enough to answer, stop calling tools and answer."""

# Prices in dollars per token (input, cached input, output) for providers that
# do not publish them through their API. Gemini 3.5 Flash-Lite: $0.30, $0.03
# and $2.50 per million (ai.google.dev pricing, standard tier, 2026-10-23).
PRICES = {
    "vertex:google/gemini-3.5-flash-lite": (0.30e-6, 0.03e-6, 2.50e-6),
    "vertex:google/gemini-3.1-flash-lite": (0.25e-6, 0.025e-6, 1.50e-6),
    "vertex:google/gemini-2.5-flash-lite": (0.10e-6, 0.01e-6, 0.40e-6),
}


def load_prompt():
    spec = importlib.util.spec_from_file_location("bench", HERE / "bench.py")
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod.PROMPT


class Provider:
    def __init__(self, kind, args):
        self.kind = kind
        self.lock = threading.Lock()
        self.token, self.token_at = None, 0.0
        if kind == "baseten":
            key = os.environ.get("BASETEN_API_KEY") or subprocess.run(
                args.baseten_key_cmd, shell=True, capture_output=True, text=True, check=True).stdout.strip()
            self.url = "https://inference.baseten.co/v1"
            self.header = "Api-Key " + key
        elif kind == "vertex":
            self.url = (f"https://aiplatform.googleapis.com/v1/projects/{args.gcp_project}"
                        "/locations/global/endpoints/openapi")
            self.header = None
        else:
            raise SystemExit(f"unknown provider {kind}")

    def auth(self):
        if self.header:
            return self.header
        with self.lock:
            if not self.token or time.time() - self.token_at > 1800:
                self.token = subprocess.run(["gcloud", "auth", "application-default", "print-access-token"],
                                            capture_output=True, text=True, check=True).stdout.strip()
                self.token_at = time.time()
            return "Bearer " + self.token

    def post(self, path, body, timeout=600):
        data = json.dumps(body).encode()
        for attempt in range(4):
            req = urllib.request.Request(self.url + path, data=data, method="POST",
                                         headers={"Authorization": self.auth(), "Content-Type": "application/json"})
            try:
                with urllib.request.urlopen(req, timeout=timeout) as resp:
                    out = json.loads(resp.read())
                    return out[0] if isinstance(out, list) else out
            except urllib.error.HTTPError as e:
                detail = e.read().decode(errors="replace")[:300]
                if e.code in (429, 500, 502, 503, 504) and attempt < 3:
                    time.sleep(5 * (attempt + 1))
                    continue
                raise RuntimeError(f"HTTP {e.code}: {detail}") from None
            except (urllib.error.URLError, TimeoutError) as e:
                if attempt < 3:
                    time.sleep(5 * (attempt + 1))
                    continue
                raise RuntimeError(str(e)) from None

    def prices(self, model):
        if self.kind != "baseten":
            return PRICES[f"{self.kind}:{model}"]
        req = urllib.request.Request(self.url + "/models", headers={"Authorization": self.auth()})
        with urllib.request.urlopen(req, timeout=60) as resp:
            for m in json.loads(resp.read()).get("data", []):
                if m.get("id") == model:
                    p = m.get("pricing") or {}
                    prompt = float(p.get("prompt", 0))
                    return prompt, float(p.get("input_cache_read", prompt)), float(p.get("completion", 0))
        raise SystemExit(f"{model}: not listed by Baseten")


class MCP:
    """A `codeeagle mcp serve` process, spoken to over newline-delimited JSON-RPC."""

    def __init__(self, codeeagle, corpus, log):
        self.proc = subprocess.Popen([codeeagle, "mcp", "serve", "--tools", "verbs", "--log", str(log)], cwd=corpus,
                                     stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                                     text=True, errors="replace")
        self.next_id = 0
        init = self.call("initialize", {"protocolVersion": "2024-11-05", "capabilities": {},
                                        "clientInfo": {"name": "bench_openai", "version": "1"}})
        self.instructions = init.get("instructions", "")
        self.send({"jsonrpc": "2.0", "method": "notifications/initialized"})
        self.tools = self.call("tools/list", {}).get("tools", [])

    def send(self, msg):
        self.proc.stdin.write(json.dumps(msg) + "\n")
        self.proc.stdin.flush()

    def call(self, method, params):
        self.next_id += 1
        self.send({"jsonrpc": "2.0", "id": self.next_id, "method": method, "params": params})
        while True:
            line = self.proc.stdout.readline()
            if not line:
                raise RuntimeError("mcp server exited")
            msg = json.loads(line)
            if msg.get("id") == self.next_id:
                if "error" in msg:
                    raise RuntimeError(msg["error"].get("message", "mcp error"))
                return msg.get("result") or {}

    def tool(self, name, arguments):
        try:
            res = self.call("tools/call", {"name": name, "arguments": arguments})
        except RuntimeError as e:
            return f"error: {e}"
        return "\n".join(c.get("text", "") for c in res.get("content", []) if c.get("type") == "text")

    def close(self):
        try:
            self.proc.stdin.close()
            self.proc.wait(timeout=10)
        except Exception:
            self.proc.kill()


def run_one(arm, provider, model, prices, q, rep, args, prompt_tmpl, out):
    started = time.time()
    mcp = MCP(args.codeeagle, args.corpus, out.with_suffix(".mcp.log"))
    tools = [{"type": "function", "function": {"name": t["name"], "description": t.get("description", ""),
                                               "parameters": t.get("inputSchema") or {"type": "object"}}}
             for t in mcp.tools]
    system = SYSTEM + ("\n\n" + mcp.instructions if mcp.instructions else "")
    user = prompt_tmpl.format(docs=args.docs, sessions=args.sessions, question=q["question"])
    messages = [{"role": "system", "content": system}, {"role": "user", "content": user}]
    p_in, p_cache, p_out = prices
    usage = {"input_tokens": 0, "cache_read_input_tokens": 0, "output_tokens": 0, "reasoning_tokens": 0}
    cost, turns, result, subtype, error = 0.0, 0, None, None, None
    try:
        while True:
            if turns >= args.max_turns:
                subtype = "error_max_turns"
                break
            body = {"model": model, "messages": messages, "tools": tools, "max_tokens": args.max_tokens}
            if args.reasoning_effort and provider.kind == "baseten":
                body["reasoning_effort"] = args.reasoning_effort
            resp = provider.post("/chat/completions", body)
            turns += 1
            u = resp.get("usage") or {}
            prompt_t = u.get("prompt_tokens", 0)
            cached = ((u.get("prompt_tokens_details") or {}).get("cached_tokens")) or 0
            completion = u.get("completion_tokens", 0)
            usage["input_tokens"] += prompt_t - cached
            usage["cache_read_input_tokens"] += cached
            usage["output_tokens"] += completion
            usage["reasoning_tokens"] += ((u.get("completion_tokens_details") or {}).get("reasoning_tokens")) or 0
            cost += (prompt_t - cached) * p_in + cached * p_cache + completion * p_out
            if args.max_budget and cost > args.max_budget:
                subtype = "error_max_budget_usd"
                break
            msg = resp["choices"][0]["message"]
            calls = msg.get("tool_calls") or []
            # The reasoning is not sent back: some providers refuse it, and it
            # is the model's, not part of the conversation.
            kept = {"role": "assistant", "content": msg.get("content") or ""}
            if calls:
                kept["tool_calls"] = calls
            messages.append(kept)
            if not calls:
                text = (msg.get("content") or "").strip()
                if text:
                    result, subtype = text, "success"
                else:
                    subtype = "error_empty_reply"
                break
            for call in calls:
                fn = call.get("function") or {}
                try:
                    arguments = json.loads(fn.get("arguments") or "{}")
                except json.JSONDecodeError:
                    arguments = {}
                messages.append({"role": "tool", "tool_call_id": call.get("id", ""),
                                 "content": mcp.tool(fn.get("name", ""), arguments)})
    except Exception as e:  # noqa: BLE001 - recorded with the answer
        subtype, error = "error_exception", str(e)[:500]
    finally:
        mcp.close()
    record = {"arm": arm, "id": q["id"], "rep": rep, "model": model, "started": started,
              "wall_s": round(time.time() - started, 1), "exit": 0 if result else 1,
              "claude": {"result": result, "num_turns": turns, "total_cost_usd": round(cost, 6), "usage": usage,
                         "subtype": subtype, "is_error": result is None, "error": error}}
    tmp = out.with_suffix(".tmp")
    tmp.write_text(json.dumps(record, indent=1))
    tmp.rename(out)
    return record


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--bench-dir", default=os.environ.get("RELATION_BENCH_DIR"), required=not os.environ.get("RELATION_BENCH_DIR"))
    ap.add_argument("--corpus", required=True)
    ap.add_argument("--codeeagle", required=True)
    ap.add_argument("--docs", required=True)
    ap.add_argument("--sessions", default="(not available)")
    ap.add_argument("--arm", action="append", required=True, help="name=provider:model; provider is baseten or vertex")
    ap.add_argument("--only")
    ap.add_argument("--reps", type=int, default=1)
    ap.add_argument("--max-turns", type=int, default=15)
    ap.add_argument("--max-budget", type=float, default=0.10)
    ap.add_argument("--max-tokens", type=int, default=32768, help="per call; reasoning is spent from it first")
    ap.add_argument("--reasoning-effort", default="low", help="for Baseten models; empty to leave it to the model")
    ap.add_argument("--concurrency", type=int, default=4)
    ap.add_argument("--baseten-key-cmd", default="keyring get baseten.co imran.yousuf@optimizely.com")
    ap.add_argument("--gcp-project", default=os.environ.get("GOOGLE_CLOUD_PROJECT", ""))
    args = ap.parse_args()

    bench = pathlib.Path(args.bench_dir).resolve()
    questions = [json.loads(l) for l in (bench / "questions.jsonl").read_text().splitlines() if l.strip()]
    if args.only:
        keep = set(args.only.split(","))
        questions = [q for q in questions if q["id"] in keep]
    prompt_tmpl = load_prompt()
    providers, jobs = {}, []
    for spec in args.arm:
        name, _, target = spec.partition("=")
        kind, _, model = target.partition(":")
        provider = providers.setdefault(kind, Provider(kind, args))
        prices = provider.prices(model)
        (bench / "runs" / name).mkdir(parents=True, exist_ok=True)
        for q in questions:
            for rep in range(1, args.reps + 1):
                out = bench / "runs" / name / f"{q['id']}.r{rep}.json"
                if not out.exists():
                    jobs.append((name, provider, model, prices, q, rep, out))
    print(f"{len(jobs)} answers to collect", flush=True)
    with concurrent.futures.ThreadPoolExecutor(args.concurrency) as pool:
        futures = [pool.submit(run_one, *j[:6], args, prompt_tmpl, j[6]) for j in jobs]
        for f in concurrent.futures.as_completed(futures):
            r = f.result()
            c = r["claude"]
            u = c["usage"]
            print(f"{r['arm']:14} {r['id']} r{r['rep']} {c['subtype']} turns={c['num_turns']} "
                  f"in={u['input_tokens'] + u['cache_read_input_tokens']} out={u['output_tokens']} "
                  f"${c['total_cost_usd']:.4f} {r['wall_s']}s" + (f" ({c['error'][:80]})" if c.get("error") else ""),
                  flush=True)


if __name__ == "__main__":
    main()
