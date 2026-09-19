#!/usr/bin/env python3
"""Records the fixture corpus under recordings/ from the live Jev service.

    python3 record_corpus.py            # every group
    python3 record_corpus.py errors     # one group, leaving the rest as they are

Run through `make jev-record` from the repository root. Each interaction is
written as recordings/<group>/<case>-NN.json — the request as sent, the status,
an allow-list of response headers, and the body — and recordings/manifest.json
records provenance and totals. A group's directory is emptied before it is
re-recorded, so a case that was removed here does not linger there.

Nothing in this file is private: every person, ticket, diff and log is
invented. Keep it that way, because the recordings are committed.

The API key is read from TYPESAFE_API_KEY (or JEV_API_KEY), or from the
keyring account named by JEV_KEYRING_ACCOUNT. It is sent only in the
Authorization header and never written anywhere: request headers are not
recorded at all, and response headers are copied from an allow-list.

Standard library only.
"""
import hashlib
import json
import os
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone

BASE = "https://api.typesafe.ai"
OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "recordings")
MODEL = "jev-1.13.0"
HEADER_ALLOWLIST = {"content-type", "x-typesafe-request-id", "retry-after", "x-envoy-upstream-service-time"}
PRICE_PER_MTOK = 0.042


def api_key():
    key = os.environ.get("TYPESAFE_API_KEY") or os.environ.get("JEV_API_KEY")
    if key:
        return key
    account = os.environ.get("JEV_KEYRING_ACCOUNT")
    if account:
        return subprocess.check_output(["keyring", "get", "typesafe.ai", account]).decode().strip()
    sys.exit("record_corpus: set TYPESAFE_API_KEY, or JEV_KEYRING_ACCOUNT to read the key from the keyring")


KEY = api_key()


# ---------------------------------------------------------------------------
# Recording.
# ---------------------------------------------------------------------------

def canonical(obj):
    return json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def fixture_key(method, path, body):
    material = method + " " + path + "\n" + (canonical(body) if body is not None else "")
    return "sha256:" + hashlib.sha256(material.encode()).hexdigest()


def call(method, path, body=None, auth=KEY):
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Content-Type": "application/json"}
    if auth is not None:
        headers["Authorization"] = "Bearer " + auth
    req = urllib.request.Request(BASE + path, data=data, method=method, headers=headers)
    t0 = time.time()
    try:
        with urllib.request.urlopen(req, timeout=120) as r:
            status, hdrs, raw = r.status, dict(r.headers.items()), r.read()
    except urllib.error.HTTPError as e:
        status, hdrs, raw = e.code, dict(e.headers.items()), e.read()
    latency = int((time.time() - t0) * 1000)
    hdrs = {k.lower(): v for k, v in hdrs.items() if k.lower() in HEADER_ALLOWLIST}
    try:
        parsed = json.loads(raw.decode())
    except Exception:
        parsed = None
    return status, hdrs, parsed, raw.decode(errors="replace"), latency


class Session:
    """One recording run: writes fixtures and accumulates the manifest."""

    def __init__(self, api_version):
        self.api_version = api_version
        self.seq = {}
        self.fixtures = []
        self.probes = []

    def record(self, group, case, method, path, body=None, note="", auth=KEY):
        status, hdrs, parsed, text, latency = call(method, path, body, auth)
        n = self.seq[(group, case)] = self.seq.get((group, case), 0) + 1
        name = f"{group}/{case}-{n:02d}"
        fx = {
            "schema": 1,
            "name": name,
            "note": note,
            "recorded_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
            "api_version": self.api_version,
            "model_requested": body.get("model") if body else None,
            "model_answered": parsed.get("model") if isinstance(parsed, dict) else None,
            "request": {"method": method, "path": path, **({"body": body} if body is not None else {})},
            "response": {"status": status, "headers": hdrs,
                         **({"body": parsed} if parsed is not None else {"body_text": text})},
            "latency_ms": latency,
            "key": fixture_key(method, path, body),
        }
        os.makedirs(os.path.join(OUT, group), exist_ok=True)
        with open(os.path.join(OUT, f"{name}.json"), "w") as f:
            json.dump(fx, f, indent=2, ensure_ascii=False)
            f.write("\n")
        usage = parsed.get("usage", {}) if isinstance(parsed, dict) else {}
        summary = summarize(parsed, status)
        self.fixtures.append({"name": name, "status": status, "summary": summary,
                              "input_tokens": usage.get("input_tokens", 0),
                              "output_tokens": usage.get("output_tokens", 0),
                              "model_answered": fx["model_answered"]})
        print(f"{name:52s} http {status} {latency:4d}ms  {summary}")
        return status, parsed

    def probe(self, body):
        """A request whose reply is noted in the manifest but not kept as a fixture."""
        status, _, parsed, _, _ = call("POST", "/v1/systemone", body)
        toks = parsed.get("usage", {}).get("input_tokens") if isinstance(parsed, dict) else None
        detail = parsed.get("detail") if isinstance(parsed, dict) else None
        self.probes.append({"status": status, "input_tokens": toks, "detail": detail})
        return status, toks, detail


def summarize(parsed, status):
    if not isinstance(parsed, dict):
        return "(non-JSON body)"
    if status != 200:
        d = parsed.get("detail")
        kind = "array" if isinstance(d, list) else "object" if isinstance(d, dict) else "string"
        return f"detail={kind}: {json.dumps(d)[:90]}"
    if "models" in parsed:
        return "models=" + ",".join(m["name"] for m in parsed["models"])
    parts = []
    for name, a in parsed.get("answers", {}).items():
        if a["type"] == "noul":
            parts.append(f"{name}=noul {a['noul']:.2f}")
        elif a["type"] == "choice":
            top = sorted(a["probabilities"].items(), key=lambda kv: -kv[1])[:2]
            parts.append(f"{name}={a['choice']}@{a['confidence']:.2f} top2={[(k, round(v, 2)) for k, v in top]}")
        else:
            parts.append(f"{name}=score {a['score']:.2f}@{a['confidence']:.2f}")
    return "; ".join(parts) + f"  tok={parsed['usage']['input_tokens']}"


def ask(state, questions, model=MODEL):
    return {"model": model, "state": state, "questions": questions}


def noul(instr, crit=None):
    q = {"type": "noul", "instructions": instr}
    if crit:
        q["criteria"] = crit
    return q


def choice(instr, opts):
    return {"type": "choice", "instructions": instr, "criteria": opts}


def score(instr, levels):
    return {"type": "score", "instructions": instr, "criteria": levels}


# ---------------------------------------------------------------------------
# Content. Every person, ticket, diff and log is fictional.
# ---------------------------------------------------------------------------

OWNER = "Nadia Rahman"
COLLEAGUES = ["Kevin Mitchell", "Priya Nair", "Omar Haddad", "Tom Becker", "Saki Ito", "Lena Fischer",
              "Marcus Ellis", "Diego Alvarez", "Hana Kobayashi", "Rupak Sen", "Ana Costa", "Wei Zhang"]
MENTIONED = ["Kevin", "Priya", "Tom", "Omar"]

TRANSCRIPT = """You (00:00): Okay, I think we're recording. Let's get started, it's the gateway sync.
Person 1 (00:06): Hi all, Kevin here. I'm on the train so if I drop, that's why.
You (00:11): No problem. So the plan for today: status on the gateway migration, then the schema change for the sessions table, then whatever's left.
Person 1 (00:22): On the gateway, the routing layer is done. All the read endpoints go through it now. Writes are still on the old path because of the idempotency key thing we discussed last week.
You (00:36): Right. Is that blocked on us or on the platform team?
Person 1 (00:40): On us. I need to decide whether the key lives in the header or in the body, and I wanted a second opinion before I commit to it.
You (00:48): Priya, you did the same thing on the billing service. Can you take the schema migration and also look at Kevin's idempotency question?
Person 2 (00:57): Yeah, I can do both. For billing we put it in the header, Idempotency-Key, and the body was untouched. It worked fine with the retries.
Person 1 (01:08): That's what I was leaning toward. Okay, header it is.
Person 3 (01:11): Mm-hmm.
You (01:13): Good. Priya, when can the sessions migration land? We have the release on Thursday.
Person 2 (01:19): The migration itself is small, it's adding the expires_at column and a backfill. I'd say Wednesday, but the backfill needs to run in batches or it'll lock the table.
Person 4 (01:31): We had exactly that problem on the events table last quarter. Ten thousand rows per batch was fine, a hundred thousand was not.
You (01:40): Thanks, Tom. Let's use ten thousand then, Priya.
Person 2 (01:43): Will do.
Person 3 (01:44): Yeah.
You (01:46): Omar, what do you think about the timeline? You're the one who has to ship it.
Person 4 (01:52): Thursday is tight but doable if the migration is in by Wednesday morning. If it slips to the afternoon I'd rather push the release a day.
You (02:01): Fair. Let's say Wednesday morning is the hard cutoff.
Person 1 (02:05): One more thing on the gateway. The health check is returning 200 even when the upstream is down, because it only checks that the process is up. I'll fix it, but it's the kind of thing that'll bite us during the release if we forget.
You (02:18): Put it in the release checklist. Anything else?
Person 2 (02:21): Nope.
Person 3 (02:22): Mm-hmm, no.
You (02:24): Okay, thanks everyone. Kevin, enjoy the train.
Person 1 (02:27): Ha, thanks. Bye all.
"""

EVIDENCE = {
    "Person 1": 'Kevin (strength 3.0) — this speaker introduced themselves by this name: "Hi all, Kevin here."',
    "Person 2": 'Priya (strength 2.0) — addressed by this name, and answered in the turn that follows: '
                '"Priya, you did the same thing on the billing service. Can you take the schema migration"',
    "Person 4": 'Tom (strength 1.5) — thanked by this name, having just finished speaking: "Thanks, Tom."; '
                'Omar (strength 2.0) — addressed by this name, and answered in the turn that follows: '
                '"Omar, what do you think about the timeline?"',
}


def identity_state(transcript, mentioned, evidence=None):
    state = {
        "meeting": "Gateway sync",
        "date": "2026-09-15 10:00",
        "host": f'The speaker labelled "You" is {OWNER}, who made the recording. No other speaker is {OWNER}.',
        "known_colleagues": ", ".join(COLLEAGUES),
        "names_mentioned": ", ".join(mentioned),
        "transcript": transcript,
    }
    if evidence:
        state["candidate_evidence"] = evidence
        state["candidate_evidence_note"] = (
            "Produced by a pattern matcher reading direct address, self-introduction, thanks and "
            "hand-offs, and resolved against the order the turns actually occurred in. Where a quote "
            "and the direction given for it fit that order, this is reliable. It misses people who "
            "are never addressed by name, and it errs in four ways worth rejecting: a product, model "
            "or company name read as a person; a voice from a recording playing in the room; a name "
            "said in negation or doubt; and a common word the transcriber capitalised. Judge the "
            "candidate on whether it names a participant in this meeting.")
    return state


def identity_options(label):
    opts = {name: f"The transcript establishes that {label} is {name}" for name in COLLEAGUES}
    opts["unresolved"] = f"The transcript does not establish who {label} is"
    return opts


def identity_question(label):
    return choice(f'Which person is the speaker labelled "{label}"? Answer "unresolved" unless the '
                  "transcript itself establishes who they are.", identity_options(label))


VULNERABLE_DIFF = r"""diff --git a/internal/api/users.go b/internal/api/users.go
index 3f1c2aa..9b7d0e4 100644
--- a/internal/api/users.go
+++ b/internal/api/users.go
@@ -41,6 +41,27 @@ func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request) {
 	writeJSON(w, http.StatusOK, user)
 }

+// handleSearchUsers returns users whose name matches the q parameter.
+func (s *Server) handleSearchUsers(w http.ResponseWriter, r *http.Request) {
+	q := r.URL.Query().Get("q")
+	query := fmt.Sprintf("SELECT id, name, email FROM users WHERE name LIKE '%%%s%%'", q)
+	rows, err := s.db.QueryContext(r.Context(), query)
+	if err != nil {
+		http.Error(w, err.Error(), http.StatusInternalServerError)
+		return
+	}
+	defer rows.Close()
+	var out []User
+	for rows.Next() {
+		var u User
+		if err := rows.Scan(&u.ID, &u.Name, &u.Email); err != nil {
+			http.Error(w, err.Error(), http.StatusInternalServerError)
+			return
+		}
+		out = append(out, u)
+	}
+	writeJSON(w, http.StatusOK, out)
+}
+
 func (s *Server) routes() {
 	s.mux.HandleFunc("GET /users/{id}", s.handleGetUser)
+	s.mux.HandleFunc("GET /users/search", s.handleSearchUsers)
 }
"""

BENIGN_DIFF = r"""diff --git a/internal/api/users.go b/internal/api/users.go
index 3f1c2aa..1a2b3c4 100644
--- a/internal/api/users.go
+++ b/internal/api/users.go
@@ -12,9 +12,9 @@ import (

 // handleGetUser returns one user by ID.
 func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request) {
-	usr, err := s.store.User(r.Context(), r.PathValue("id"))
+	user, err := s.store.User(r.Context(), r.PathValue("id"))
 	if err != nil {
 		http.Error(w, "not found", http.StatusNotFound)
 		return
 	}
-	writeJSON(w, http.StatusOK, usr)
+	writeJSON(w, http.StatusOK, user)
 }
"""

REVIEW = {"file_path": "internal/api/users.go", "diff": VULNERABLE_DIFF, "author": "d.alvarez", "lines_modified": 24}
BENIGN = {"file_path": "internal/api/users.go", "diff": BENIGN_DIFF, "author": "d.alvarez", "lines_modified": 4}

SUBSYSTEMS = {
    "database": "Schema migrations, SQL queries, ORM models, transaction handling",
    "authentication": "Sessions, tokens, password handling, access control",
    "api_handlers": "HTTP routing, request parsing, response encoding",
    "frontend": "UI components, styling, client-side rendering",
    "business_logic": "Domain rules, calculations, workflow orchestration",
    "infrastructure": "Deployment, containers, CI pipelines, infrastructure as code",
    "observability": "Logging, metrics, tracing, alerting",
    "messaging": "Queues, event publishing, webhooks",
    "storage": "Object storage, file handling, caching layers",
    "testing": "Test code, fixtures, mocks",
    "documentation": "READMEs, comments, API docs",
    "build_tooling": "Makefiles, build scripts, dependency manifests",
    "unclassified": "Does not fit any category above",
}

COMPLEXITY = [
    "Trivial: a rename or comment change with no behavioural effect",
    "Small: a local logic change covered by existing tests",
    "Moderate: new behaviour that needs new tests and one reviewer familiar with the area",
    "Large: touches a shared boundary such as a database or public API and needs cross-team review",
    "Critical: changes security-sensitive or data-integrity code and needs a security review before merge",
]

VULNERABILITY = noul("Does this diff introduce an injection, insecure deserialization, or unhandled-input vulnerability?",
                     {"true": "User-controlled input reaches a query, command, or deserializer without validation or parameterization",
                      "false": "Input is validated or parameterized, or the change does not touch input handling"})

SYNTAX_LOG = """$ go build ./...
# github.com/acme/gateway/internal/router
internal/router/router.go:88:2: syntax error: unexpected newline in composite literal; possibly missing comma or }
internal/router/router.go:91:1: syntax error: non-declaration statement outside function body
FAIL	github.com/acme/gateway/internal/router [build failed]
make: *** [Makefile:42: build] Error 1
"""

AMBIGUOUS_LOG = """[12:04:18] Running step: test
[12:04:19] > make test
[12:07:52] Error: Process completed with exit code 1.
"""

CATEGORIES = {
    "compiler_syntax": "Compilation or syntax parser failure",
    "type_error": "Type mismatch or unresolved symbol at compile time",
    "test_assertion": "Unit, integration, or end-to-end assertion failure",
    "test_timeout": "A test exceeded its time limit",
    "dependency_resolution": "Missing package or version conflict",
    "linter": "Lint or formatting check failed",
    "infrastructure": "Out of memory, disk exhaustion, or container runtime error",
    "network": "DNS, TLS, or connection failure to an external service",
    "permissions": "Credential, token, or filesystem permission failure",
    "configuration": "Missing or malformed environment or config value",
    "flaky": "Non-deterministic failure with no code cause",
    "unknown": "The log does not contain enough to categorize the failure",
}

TICKET_BILLING = {
    "subject": "Charged twice for September",
    "message": ("Hi, I just checked my card statement and there are two charges from you on 3 September, "
                "both for $49. I only have one subscription. Can you refund the second one? Order number "
                "is in my account. Thanks, Marta"),
    "channel": "email",
    "customer_since": "2024-11",
}

TICKET_VAGUE = {
    "subject": "not working",
    "message": "the app doesnt work anymore since yesterday. please fix",
    "channel": "chat",
}

TONE = {"angry": "An upset or hostile message", "calm": "A neutral or polite message",
        "confused": "The customer does not understand what happened", "urgent": "The customer needs a fast answer",
        "other": "None of the above"}

COMMAND = {"command": "git push --force origin main", "working_directory": "/app/service-payments",
           "session_context": "Refactoring migration files"}
READ_ONLY = {"command": "git status", "working_directory": "/app/service-payments"}

DESTRUCTIVE = "Does this command irreversibly overwrite remote version control history or delete resources?"
DESTRUCTIVE_CRITERIA = {"true": "Command forcibly rewrites history, drops data, or bypasses safety mechanisms",
                        "false": "Command performs safe local work or standard additive operations"}
ROUTING = choice("Which automated safety queue should process this command?", {
    "auto_execute": "Read-only inspection or standard non-destructive operations",
    "require_peer_approval": "Commands that alter shared environments or force-update remote history",
    "hard_block": "Commands leaking credentials, writing to root, or destroying production state",
    "unrecognized": "Syntactically ambiguous or obfuscated command"})
BLAST = score("Score the organizational and technical blast radius of this terminal command.", [
    "Zero impact: Command is strictly read-only and local",
    "Moderate impact: Command alters local repository state or unmerged branch history",
    "Severe impact: Command mutates canonical production history or core system state"])


def ceiling_transcript(chars):
    """A transcript-shaped text of about the requested size, built from the
    fictional meeting so the content stays realistic rather than random."""
    lines = TRANSCRIPT.strip().split("\n")
    out, i = [], 0
    while sum(len(l) + 1 for l in out) < chars:
        label, rest = lines[i % len(lines)].split(" (", 1)
        _, text = rest.split("): ", 1)
        secs = i * 9
        out.append(f"{label} ({secs // 60:02d}:{secs % 60:02d}): {text}")
        i += 1
    return "\n".join(out)[:chars]


# ---------------------------------------------------------------------------
# The groups. A test function owns each one.
# ---------------------------------------------------------------------------

def guardrail(s):
    s.record("guardrail", "force-push-noul-plain", "POST", "/v1/systemone",
             ask(COMMAND, {"destructive": noul(DESTRUCTIVE)}),
             note="Noul without criteria; expected near 0.95.")
    s.record("guardrail", "force-push-noul-criteria", "POST", "/v1/systemone",
             ask(COMMAND, {"destructive": noul(DESTRUCTIVE, DESTRUCTIVE_CRITERIA)}),
             note="Same question with explicit true/false criteria.")
    s.record("guardrail", "force-push-fanout", "POST", "/v1/systemone",
             ask(COMMAND, {"is_destructive": noul(DESTRUCTIVE, DESTRUCTIVE_CRITERIA),
                           "execution_routing": ROUTING, "blast_radius": BLAST}),
             note="Three-question fan-out over one state; small decisive choice; 3-level score.")
    s.record("guardrail", "git-status-noul-low", "POST", "/v1/systemone",
             ask(READ_ONLY, {"destructive": noul(DESTRUCTIVE, DESTRUCTIVE_CRITERIA)}),
             note="Noul expected near 0.05.")
    s.record("guardrail", "force-push-alias-latest", "POST", "/v1/systemone",
             ask(COMMAND, {"destructive": noul(DESTRUCTIVE)}, model="jev-latest"),
             note="Requested jev-latest; model_answered shows what the alias resolved to.")


def jitter(s):
    for _ in range(5):
        s.record("jitter", "force-push-fanout", "POST", "/v1/systemone",
                 ask(COMMAND, {"is_destructive": noul(DESTRUCTIVE), "execution_routing": ROUTING}),
                 note="Identical request repeated; the spread across -01..-05 is the measured non-determinism.")


def speaker_identity(s):
    st = identity_state(TRANSCRIPT, MENTIONED, EVIDENCE)
    four = {"speaker_0": identity_question("Person 1"), "speaker_1": identity_question("Person 2"),
            "speaker_2": identity_question("Person 3"), "speaker_3": identity_question("Person 4")}
    s.record("speaker-identity", "four-speakers-fanout", "POST", "/v1/systemone", ask(st, four),
             note="13-option choices over one transcript: self-introduced (decisive), addressed-then-answers, "
                  "a voice that only says mm-hmm (should decline), and conflicting evidence (Tom vs Omar).")
    s.record("speaker-identity", "kevin-alone-with-evidence", "POST", "/v1/systemone",
             ask(st, {"speaker_0": identity_question("Person 1")}),
             note="Single question over the same state, for the fan-out token comparison.")
    s.record("speaker-identity", "four-speakers-no-evidence", "POST", "/v1/systemone",
             ask(identity_state(TRANSCRIPT, MENTIONED), four),
             note="Same transcript without the deterministic candidate evidence.")
    s.record("speaker-identity", "mmhmm-only-declined", "POST", "/v1/systemone",
             ask(st, {"speaker_2": identity_question("Person 3")}),
             note="The declined/unresolved outcome the production gate depends on.")
    s.record("speaker-identity", "meeting-ended-on-time-uncertain", "POST", "/v1/systemone",
             ask(st, {"on_time": noul("Did the meeting end at its scheduled time?")}),
             note="The transcript does not say; a calibrated answer sits near 0.5.")
    undecidable = {
        "ships_thursday": noul("Will the release ship on Thursday as planned?"),
        "events_larger": noul("Does the events table hold more rows than the sessions table?"),
        "priya_last_week": noul("Did Priya attend last week's meeting?"),
        "remote_meeting": noul("Was this meeting held entirely remotely?"),
        "migration_lands_wednesday": noul("Will the sessions migration land by Wednesday morning?"),
        "health_check_fixed": noul("Has the gateway health check already been fixed?"),
    }
    s.record("speaker-identity", "undecidable-nouls-fanout", "POST", "/v1/systemone", ask(st, undecidable),
             note="Questions the transcript does not settle; the ones near 0.5 are the point.")
    for name in ("events_larger", "priya_last_week"):
        s.record("speaker-identity", f"uncertain-{name.replace('_', '-')}", "POST", "/v1/systemone",
                 ask(st, {name: undecidable[name]}),
                 note="Single undecidable noul, recorded on its own; expected near 0.5.")


def code_review(s):
    s.record("code-review", "sql-injection-fanout", "POST", "/v1/systemone",
             ask(REVIEW, {"introduces_vulnerability": VULNERABILITY,
                          "subsystem": choice("Which subsystem does this diff primarily modify?", SUBSYSTEMS),
                          "review_complexity": score("Rate the review effort this change needs.", COMPLEXITY),
                          "breaks_existing_clients": noul("Will existing API clients break because of this change?")}),
             note="Four-question fan-out; 13-option choice; 5-level rubric; last noul is genuinely uncertain.")
    s.record("code-review", "sql-injection-single", "POST", "/v1/systemone",
             ask(REVIEW, {"introduces_vulnerability": noul(VULNERABILITY["instructions"])}),
             note="One question over the same state, for the fan-out token comparison.")
    s.record("code-review", "rename-fanout", "POST", "/v1/systemone",
             ask(BENIGN, {"introduces_vulnerability": noul(VULNERABILITY["instructions"]),
                          "subsystem": choice("Which subsystem does this diff primarily modify?", SUBSYSTEMS),
                          "review_complexity": score("Rate the review effort this change needs.", COMPLEXITY)}),
             note="Benign rename: noul near 0.05, score at the bottom of the rubric.")
    s.record("code-review", "which-file-255-options", "POST", "/v1/systemone",
             ask(REVIEW, {"file": which_file(255)}),
             note="255 options, the documented maximum; the diff names the answer.")


def which_file(n):
    dirs = ["internal/api", "internal/auth", "internal/db", "internal/router", "internal/store", "internal/cache",
            "internal/queue", "internal/metrics", "internal/config", "cmd/gateway", "cmd/migrate", "pkg/client",
            "pkg/errors", "web/src/components", "web/src/pages", "web/src/hooks", "deploy/k8s", "deploy/terraform"]
    names = ["users", "sessions", "tokens", "routes", "handlers", "middleware", "migrations", "models", "queries",
             "cache", "events", "webhooks", "metrics", "logging", "config"]
    paths = [f"{d}/{name}.go" if not d.startswith("web") else f"{d}/{name}.tsx" for d in dirs for name in names]
    return choice("Which file does this diff modify?", {p: f"The diff modifies {p}" for p in paths[:n]})


def build_triage(s):
    s.record("build-triage", "syntax-error-fanout", "POST", "/v1/systemone",
             ask({"log_excerpt": SYNTAX_LOG, "environment": "ci", "retry_count": 0}, {
                 "is_transient": noul("Is this failure caused by a transient network issue, timeout, or external resource lock?",
                                      {"true": "503s, socket hang-ups, external gateway timeouts, or lock contention",
                                       "false": "Syntax errors, failed test assertions, or missing dependencies"}),
                 "category": choice("Identify the root cause of the build failure.", CATEGORIES),
                 "urgency": score("Score the urgency of escalating this failure to on-call engineering.", [
                     "Low: blocked local developer run or non-critical feature branch",
                     "Medium: failed staging integration build requiring developer triage",
                     "High: shared trunk pipeline failure blocking other developers",
                     "Critical: production deployment blocked"])}),
             note="Decisive 12-option choice; transient noul near 0.05; 4-level rubric.")
    s.record("build-triage", "ambiguous-log-flat", "POST", "/v1/systemone",
             ask({"log_excerpt": AMBIGUOUS_LOG, "environment": "ci", "retry_count": 1},
                 {"category": choice("Identify the root cause of the build failure.", CATEGORIES)}),
             note="The log says almost nothing: a large option set with a near-flat distribution, or a decisive 'unknown'.")
    languages = {l: f"The project is written mainly in {l}" for l in
                 ["Go", "Python", "TypeScript", "JavaScript", "Java", "Rust", "C#", "Ruby", "Kotlin", "Swift", "PHP", "Scala"]}
    s.record("build-triage", "ambiguous-log-language-flat", "POST", "/v1/systemone",
             ask({"log_excerpt": AMBIGUOUS_LOG, "environment": "ci"},
                 {"language": choice("Which language is the project written in?", languages)}),
             note="A log that only says `make test` failed; expected near-flat over 12 options.")


def support(s):
    s.record("support", "double-charge-fanout", "POST", "/v1/systemone",
             ask(TICKET_BILLING, {
                 "billing": noul("Is this message about billing?"),
                 "tone": choice("What is the tone of this message?", TONE),
                 "urgency": score("How urgent is this message?",
                                  ["Can wait", "Needs attention this week", "Needs attention today", "Needs attention now"]),
                 "enterprise_plan": noul("Is the customer on an enterprise plan?")}),
             note="Billing noul near 0.95; the enterprise-plan noul is unanswerable from the state and should sit near 0.5.")
    s.record("support", "vague-ticket-flat", "POST", "/v1/systemone",
             ask(TICKET_VAGUE, {"category": choice("Which team should handle this ticket?", {
                 "billing": "Charges, invoices, refunds", "login": "Passwords, sign-in, sessions",
                 "bug": "Something in the product is broken", "performance": "Slowness or timeouts",
                 "feature_request": "Asking for something new", "unknown": "Cannot tell from the message"})}),
             note="Near-flat small choice, or a decisive 'unknown'.")
    weekdays = {d: f"The ticket was sent on a {d}" for d in
                ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"]}
    s.record("support", "vague-ticket-weekday-flat", "POST", "/v1/systemone",
             ask(TICKET_VAGUE, {"weekday": choice("On which day of the week was this ticket sent?", weekdays)}),
             note="Nothing in the state answers this; expected near-flat over 7 options.")


def quirks(s):
    s.record("quirks", "score-one-level", "POST", "/v1/systemone",
             ask(COMMAND, {"blast": score("Score the blast radius of this command.", ["Severe impact"])}),
             note="Accepted by the server; returns score 0.0 at confidence 1.0. Why MinScoreLevels exists.")
    s.record("quirks", "choice-one-option", "POST", "/v1/systemone",
             ask(COMMAND, {"route": choice("Which queue?", {"require_peer_approval": "Commands that alter shared environments"})}),
             note="Accepted by the server; returns confidence 1.0. Why MinChoiceOptions exists.")
    s.record("quirks", "choice-names-only", "POST", "/v1/systemone",
             ask(COMMAND, {"route": {"type": "choice", "instructions": "Which safety queue should process this command?",
                                     "criteria": {"auto_execute": None, "require_peer_approval": None,
                                                  "hard_block": None, "unrecognized": None}}}),
             note="Null descriptions: the documented name-only form.")
    s.record("quirks", "noul-criteria-only", "POST", "/v1/systemone",
             ask(COMMAND, {"destructive": {"type": "noul", "criteria": {
                 "true": "Rewrites shared history or deletes data", "false": "Safe local or additive work"}}}),
             note="No instructions at all; criteria carry the question.")
    s.record("quirks", "structured-instructions", "POST", "/v1/systemone",
             ask(COMMAND, {"destructive": {"type": "noul", "instructions": {
                 "task": "Decide whether the command is destructive",
                 "definition": "Destructive means it rewrites shared history or deletes data that cannot be recovered"}}}),
             note="Instructions as a JSON object, which the OpenAPI schema permits.")
    s.record("quirks", "structured-score-levels", "POST", "/v1/systemone",
             ask(COMMAND, {"blast": score("Score the blast radius of this command.", [
                 {"level": "none", "meaning": "strictly read-only and local"},
                 {"level": "local", "meaning": "alters local repository state"},
                 {"level": "shared", "meaning": "mutates canonical shared history or production state"}])}),
             note="Object rubric levels; the legend comes back as objects, which a map[string]string cannot decode.")
    s.record("quirks", "unicode-question-name", "POST", "/v1/systemone",
             ask(COMMAND, {"är det säkert?": noul("Is this command safe to run unattended?")}),
             note="Question names are free text.")


def errors(s):
    s.record("errors", "400-object-unknown-type", "POST", "/v1/systemone",
             ask(COMMAND, {"destructive": {"type": "boolean", "instructions": "Is it destructive?"}}),
             note="detail is an object: api_usage_error with a message.")
    s.record("errors", "400-object-unknown-model", "POST", "/v1/systemone",
             ask(COMMAND, {"destructive": noul("Is it destructive?")}, model="jev-9.99.9"),
             note="detail is an object with a specific message.")
    s.record("errors", "400-string-empty-question-name", "POST", "/v1/systemone",
             ask(COMMAND, {"": noul("Is it destructive?")}), note="detail is a bare string.")
    s.record("errors", "400-string-too-many-levels", "POST", "/v1/systemone",
             ask(COMMAND, {"blast": score("Blast radius?", [f"level {i}" for i in range(11)])}),
             note="detail is a bare string; the upper rubric bound IS enforced server-side.")
    s.record("errors", "400-string-noul-without-anything", "POST", "/v1/systemone",
             ask(COMMAND, {"q": {"type": "noul"}}), note="detail is a bare string naming the question.")
    s.record("errors", "256-options", "POST", "/v1/systemone",
             ask(REVIEW, {"file": which_file(256)}),
             note="One over the documented maximum. Whether the server enforces it.")
    s.record("errors", "422-array-missing-state", "POST", "/v1/systemone",
             {"model": MODEL, "questions": {"q": noul("Is it destructive?")}},
             note="Schema validation: detail is an array of {loc, msg, type, input}.")
    s.record("errors", "422-array-state-number", "POST", "/v1/systemone",
             {"model": MODEL, "state": 42, "questions": {"q": noul("Is it even?")}},
             note="State must be a string, object, or array.")
    s.record("errors", "422-array-empty-questions", "POST", "/v1/systemone",
             {"model": MODEL, "state": COMMAND, "questions": {}}, note="Schema validation on minProperties.")
    s.record("errors", "401-invalid-key", "POST", "/v1/systemone",
             ask(COMMAND, {"q": noul("Is it destructive?")}), auth="apikey_invalid",
             note="Sent with the literal key apikey_invalid.")
    s.record("errors", "401-missing-key", "POST", "/v1/systemone",
             ask(COMMAND, {"q": noul("Is it destructive?")}), auth=None,
             note="No Authorization header at all. The service answers 403, not 401.")
    s.record("errors", "404-unknown-path", "GET", "/v1/nope", note="Routing miss; detail is a bare string.")
    s.record("errors", "405-get-systemone", "GET", "/v1/systemone", note="Wrong method on the decision endpoint.")
    s.record("errors", "models-401-invalid-key", "GET", "/v1/models", auth="apikey_invalid", note="Models() error path.")
    s.record("errors", "models-403-missing-key", "GET", "/v1/models", auth=None, note="Models() with no key at all.")


def models(s):
    s.record("models", "list", "GET", "/v1/models",
             note="The only other endpoint. Aliases only; pinned releases are not listed.")


def ceiling(s):
    """Bisects the size ceiling, then keeps the closest accepted and refused
    requests as fixtures. Each probe is a real request; only the pair is kept."""
    print("\nceiling search:")
    lo, hi = 100_000, 150_000  # chars of transcript; accepted at lo, refused at hi
    q = {"speaker_0": identity_question("Person 1")}
    while hi - lo > 1500:
        mid = (lo + hi) // 2
        status, toks, detail = s.probe(ask(identity_state(ceiling_transcript(mid), MENTIONED), q))
        print(f"  {mid:7d} chars -> http {status}  tokens={toks}  detail={json.dumps(detail)[:60] if detail else ''}")
        if status == 200:
            lo = mid
        else:
            hi = mid
    s.record("ceiling", "near-limit-accepted", "POST", "/v1/systemone",
             ask(identity_state(ceiling_transcript(lo), MENTIONED), q),
             note=f"Largest accepted state found by bisection: {lo} chars of transcript. usage.input_tokens is the real ceiling.")
    s.record("ceiling", "just-over-refused", "POST", "/v1/systemone",
             ask(identity_state(ceiling_transcript(hi), MENTIONED), q),
             note=f"Smallest refused state found by bisection: {hi} chars. A genuine max_tokens_exceeded with no message.")


GROUPS = {
    "guardrail": guardrail,
    "jitter": jitter,
    "speaker-identity": speaker_identity,
    "code-review": code_review,
    "build-triage": build_triage,
    "support": support,
    "quirks": quirks,
    "errors": errors,
    "models": models,
    "ceiling": ceiling,
}


# ---------------------------------------------------------------------------
# Manifest.
# ---------------------------------------------------------------------------

def load_manifest():
    path = os.path.join(OUT, "manifest.json")
    if os.path.exists(path):
        with open(path) as f:
            return json.load(f)
    return {"schema": 1, "fixtures": [], "ceiling_search": []}


def write_manifest(manifest, session, groups):
    kept = [fx for fx in manifest.get("fixtures", []) if fx["name"].split("/")[0] not in groups]
    fixtures = sorted(kept + session.fixtures, key=lambda fx: fx["name"])
    probes = session.probes if "ceiling" in groups else manifest.get("ceiling_search", [])
    answered = {}
    for fx in fixtures:
        if fx.get("model_answered"):
            answered[fx["model_answered"]] = answered.get(fx["model_answered"], 0) + 1
    input_tokens = sum(fx["input_tokens"] for fx in fixtures) + sum(p["input_tokens"] or 0 for p in probes)
    accepted = [p["input_tokens"] for p in probes if p["status"] == 200 and p["input_tokens"]]
    out = {
        "schema": 1,
        "recorded_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
        "api_version": session.api_version,
        "model_requested": MODEL,
        "models_answered": answered,
        "recorder": "testdata/record_corpus.py",
        "requests": len(fixtures) + len(probes),
        "input_tokens": input_tokens,
        "output_tokens": sum(fx.get("output_tokens", 0) for fx in fixtures),
        "cost_usd": round(input_tokens * PRICE_PER_MTOK / 1e6, 4),
        "ceiling_tokens_accepted": max(accepted) if accepted else manifest.get("ceiling_tokens_accepted"),
        "ceiling_search": probes,
        "fixtures": fixtures,
    }
    with open(os.path.join(OUT, "manifest.json"), "w") as f:
        json.dump(out, f, indent=2, ensure_ascii=False)
        f.write("\n")
    return out


def main(argv):
    groups = argv or list(GROUPS)
    unknown = [g for g in groups if g not in GROUPS]
    if unknown:
        sys.exit(f"record_corpus: unknown group(s) {unknown}; known: {', '.join(GROUPS)}")

    with urllib.request.urlopen(BASE + "/openapi.json", timeout=20) as r:
        api_version = json.load(r)["info"]["version"]
    print(f"recording against {BASE}, api {api_version}, model {MODEL}, groups: {', '.join(groups)}\n")

    session = Session(api_version)
    for group in groups:
        shutil.rmtree(os.path.join(OUT, group), ignore_errors=True)
        GROUPS[group](session)

    m = write_manifest(load_manifest(), session, set(groups))
    print(f"\nthis run: {len(session.fixtures)} fixtures, {len(session.probes)} probes; corpus: "
          f"requests={m['requests']} input_tokens={m['input_tokens']} cost=${m['cost_usd']} "
          f"models={m['models_answered']}")


if __name__ == "__main__":
    main(sys.argv[1:])
