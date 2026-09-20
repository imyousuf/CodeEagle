# jev

A Go client for [TypeSafe Jev](https://api.typesafe.ai/docs), a decision model
that answers typed questions about a piece of state instead of generating
text. One request carries one state and any number of questions; every
question is answered in the same pass, with calibrated probabilities rather
than prose to parse.

```
go get github.com/imyousuf/CodeEagle/pkg/jev
```

The module depends on the standard library only, and needs Go 1.22.

> The import path may move once, to `github.com/imyousuf/jev`, when a second
> external consumer appears or at v1.0.0 — whichever comes first. See
> [RELEASING.md](RELEASING.md).

## Asking

```go
client, err := jev.New(os.Getenv("TYPESAFE_API_KEY"))
if err != nil {
	return err
}

resp, err := client.Ask(ctx, map[string]string{
	"command":           "git push --force origin main",
	"working_directory": "/app/service-payments",
}, jev.Questions{
	"destructive": jev.NoulWithCriteria(
		"Does this command irreversibly overwrite remote history or delete resources?",
		"Rewrites history, drops data, or bypasses a safety mechanism",
		"Performs safe local work or an additive operation"),
	"route": jev.Choice("Which safety queue should process this command?", map[string]string{
		"auto_execute": "Read-only inspection or a non-destructive operation",
		"peer_review":  "Alters a shared environment or force-updates remote history",
		"hard_block":   "Leaks credentials or destroys production state",
		"unrecognized": "Ambiguous or obfuscated",
	}),
	"blast_radius": jev.Score("Score the blast radius of this command.", []string{
		"Strictly read-only and local",
		"Alters local repository state or unmerged history",
		"Mutates canonical shared history or production state",
	}),
})
if err != nil {
	return err
}

p, _ := resp.Answers.Noul("destructive")            // 0.87
route, confidence, _ := resp.Answers.Choice("route") // "peer_review", 1.00
blast, _, _ := resp.Answers.Score("blast_radius")    // 1.99 on a 0..2 rubric
```

Three primitives, deliberately narrow:

| Constructor | Asks | Answers with |
|---|---|---|
| `Noul` / `NoulWithCriteria` | whether something is true | the probability that it is; 0.5 means the state does not decide it |
| `Choice` | which of a named set applies | the winner, the mass on every option, and how concentrated it is |
| `Score` | where the state sits on an ordered rubric of 2–10 levels | a continuous position, so 1.84 on three levels is "mostly the top one" |

Always give a `Choice` an option for "none of these". Without one the model has
to spread its belief over answers it has rejected, and the confidence figure
stops meaning anything. Runnable examples are in
[example_test.go](example_test.go); they replay recorded exchanges and never
call the service.

## What the model cannot do

It does not do arithmetic, does not compare dates, does not follow chains of
deduction, and does not write prose. Count the items, compute the elapsed
days, split a compound question into atomic ones, and pass the results in as
fields of the state.

Whatever a field asserts is believed. Name each field as exactly what it is —
describing a count as something it is not reliably flips the answer — and keep
text from users, logs or web pages in a field of its own, never mixed into the
instructions.

## Errors

Every failure matches a sentinel through `errors.Is`, and the concrete type is
reachable through `errors.As`:

| Sentinel | Meaning | Retried |
|---|---|---|
| `ErrInvalidRequest` | refused, or rejected here before sending; change the request | no |
| `ErrTooLarge` | state plus longest question past the ceiling; trim and ask again | no |
| `ErrUnauthorized` | key missing (403), invalid (401), or rejected | no |
| `ErrRateLimited` | 429; `Retry-After` honoured | yes |
| `ErrOverloaded` | 529, or any 5xx | yes |
| `ErrUnavailable` | transport failure: DNS, TLS, reset, per-attempt timeout | yes |
| `ErrMalformedResponse` | the reply did not answer what was asked; a proxy rewrote it | no |

`*APIError` carries the status, the service's `error_type`, its message, the
individual schema violations for a 422, and the request id. `*TransportError`
wraps the underlying `*url.Error`. Retries use jittered exponential backoff
and stop the moment the caller's context ends.

## Size, and who trims

The service accepts about 32,900 input tokens for the state plus the longest
question (measured: 32,882 accepted, roughly 33,100 refused) and 64,000 for the
request as a whole. It refuses rather than truncates, so the caller trims —
`MaxStateTokens` is the safe bound, and `ErrTooLarge` is the signal to trim
further. Measured character-per-token rates: prose 5.3, diarized transcript
3.8, Go source 3.4, JSON 2.4.

## Answers are not bit-stable

Identical requests do not return identical numbers. Five identical fan-outs
in the recorded corpus put the same noul at 0.73, 0.76, 0.73, 0.76 and 0.76;
across more states the movement has reached 0.09. Do not tune a threshold to
two decimals, do not treat one run as a measurement, and gate on a margin —
`Answers.Ranked` gives the runner-up as well as the winner.

## What the vendor documentation gets wrong

Verified against the live service on 2026-09-19 (`jev-1.13.0`, API 0.2.0),
and recorded under `testdata/recordings/`:

| Claim in circulation | What the service does |
|---|---|
| Keys begin `ts-live-` | They begin `apikey_` |
| Validation errors are HTTP 422 | Both: 400 with `detail` a string or an object from the service's own checks; 422 with `detail` an *array* of `{loc, msg, type}` from the schema layer |
| A rubric must have 2–10 levels | One level is accepted and answered `score 0.0, confidence 1.0`; eleven are refused with 400 |
| A choice takes 1–255 options | One option is accepted and answered at confidence 1.0; 256 are refused with 400 |
| `output_tokens` is always 0 | It is not (20 for a single noul); output is "currently" unbilled |
| `jev-latest` is a rolling alias | It and `jev-preview` both resolve to `jev-1.13.0` today; pin the release |
| The ceiling is 32,000 tokens | 32,882 accepted, ~33,100 refused; refused outright, never truncated |
| Median latency 0.23–0.26 s | 0.28–0.71 s end to end; the server's own time is about 70 ms |
| A bad key is 401 | Invalid is 401; a *missing* `Authorization` header is 403 |
| Answers are deterministic | They move by 0.01–0.09 between identical requests |
| (not documented) | `x-typesafe-request-id` on every reply; `GET /v1/models`; `/openapi.json`; instructions and criteria may be JSON objects or arrays |

This package validates locally what the service accepts and answers
meaninglessly: a one-level rubric, a one-option choice, an empty option
description, an unknown question type, and a state that is not a string,
object or array.

## Testing code that depends on this package

Depend on `jev.Asker`, the one-method interface `*jev.Client` satisfies.

`jevtest` replays exchanges recorded from the live service: offline, exact,
and loud. A request with no recording fails the test rather than reaching for
the network, and a recording that no test asked for fails it too.

```go
rec := jevtest.NewRecorder(t, "guardrail")
client, _ := jev.New("apikey_test", jev.WithHTTPClient(rec.Client()))
```

The corpus under `testdata/recordings/` is real: 53 exchanges over realistic
states — a diarized transcript, a code diff, a build log, support tickets — at
every shape the service produces, including its refusals. Nothing in it was
written by hand, and none of it is private. Re-record it with
`make jev-record` (or `make jev-record GROUP=errors`) from the repository
root; it needs `TYPESAFE_API_KEY` or `JEV_KEYRING_ACCOUNT` and costs about
half a cent.

## License

Apache 2.0; see [LICENSE](LICENSE).
