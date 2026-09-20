# Jev: judging, not generating

How CodeEagle uses TypeSafe Jev, what it is for, and how to use the Go client
in your own code.

For which version is pinned and how to bump it, see `models.md`. For setting
the key up, see `installation.md`.

---

## What it is

Jev answers a bounded question with a **calibrated probability**. It does not
write prose. You hand it some state and ask "is this true?", "which of these?",
or "where on this scale?", and it answers with a number that means what it
says.

That last part is the whole point. Ask a language model how confident it is and
you get a plausible-sounding number, because it is generating text that looks
like a confidence score. Measured over 85 speaker identifications, Jev's
probabilities came back sharply bimodal — 21 above 0.70, 54 below 0.10, almost
nothing in between. It knows when it knows.

It is also cheap: input is billed at $0.042 per million tokens and output is
not billed at all. Asking several questions about one shared state costs
almost nothing extra — three questions came to 937 input tokens against 680 for
one — so fanning out is the normal way to use it.

## Where the line is

**Jev is a verifier, not a solver.** This is the single most useful thing
measurement taught us, and it cost a wrong design to learn.

Asked an open question — "which of these eight people is Person 3?" against an
80,000-character transcript — it does poorly, because that is global
re-derivation. Hand it a concrete proposal to judge and it is excellent.

So every use follows the same shape:

> something cheap and deterministic **proposes**, Jev **verifies**, a threshold
> **gates**.

Where there is nothing to propose, Jev is the wrong tool. Where the judgment is
generation — write a summary, name a topic — it is also the wrong tool; that
work stays with a language model.

---

## What it does here

Three jobs. All three are optional: without `transcripts.jev_api_key` the
features degrade to their offline behaviour rather than failing.

### 1. Speaker adjudication (`internal/transcript/adjudicate.go`)

Transcripts arrive diarized but anonymous — "Person 1", "Person 2". A
deterministic pass extracts directional name evidence ("Thanks, Kevin" names
the previous speaker; "Kevin, what do you think?" the next), and Jev judges
which candidates are real.

Measured against a language model over the same corpus, Jev is markedly more
conservative and better at refusing: it correctly rejected a speaker named
"Mark" whose name came from *"a general purpose Mark One model"*, and another
taken from a television playing in the room. A wrong name corrupts the graph
silently; an unidentified speaker is a correct outcome.

### 2. Topic relating (`internal/transcript/topicrelate.go`)

1,716 of 2,007 topic labels are used by exactly one meeting, so the topic layer
indexes almost nothing. Four cheap generators propose topic pairs — nearest
labels and nearest segment summaries by embedding, same meeting, shared parent
— and Jev answers one yes/no per pair. The probability lands on a `RelatedTo`
edge, and `meetings search --breadth` walks those edges.

Cosine similarity alone was measured and rejected: the best single cut gives
half the recall at 83% precision. Good for proposing, poor for deciding.

### 3. Search re-ranking (`internal/transcript/rerank.go`)

Word matching finds the meetings; it cannot tell one that discussed a subject
from one that mentioned it in passing. Jev reorders the candidates — one
request, one question per candidate. On five real queries it improved three and
demoted no correct first result.

It **never adds a meeting the words did not reach**, so a wrong or unavailable
judgment can only leave the order as word matching had it.

---

## Four things measurement taught us

Write these on your wall before using it for anything new.

**A type you cannot apply, it cannot apply.** Six relation types
(same-subject / facet-of / sibling / discussed-together / unrelated) scored 51%
accurate. The same judge, same pairs, asked one yes/no question instead: 0.92
area under the curve. The option split was destroying the signal. If you cannot
label your own categories consistently by hand, do not ask for them.

**It believes what you assert.** State fields are taken as fact. Describing a
count as something it is not flipped answers the wrong way. Name every field as
exactly what it is, and never imply the conclusion you are asking for.

**Give it a way to decline.** An explicit "unresolved" or "none of these"
option gets used decisively rather than smearing probability across wrong
answers. Without one, it will pick something.

**Answers move.** Repeated identical calls shift a probability by up to 0.10.
Never tune a gate to two decimals, never assert an exact probability in a test,
and bucket before ordering if order matters.

---

## Using the Go client

`pkg/jev` is a standalone module with **no dependencies outside the standard
library**.

```
go get github.com/imyousuf/CodeEagle/pkg/jev
```

### The three primitives

```go
c, err := jev.New(os.Getenv("TYPESAFE_API_KEY"))

resp, err := c.Ask(ctx,
    map[string]any{
        "command":           "git push --force origin main",
        "working_directory": "/app/service-payments",
    },
    jev.Questions{
        "destructive": jev.Noul("Would running this lose work that is not recoverable?"),
        "area":        jev.Choice("Which subsystem does this touch?", map[string]string{
            "source_control": "the repository's own history",
            "deployment":     "what is running in production",
        }),
        "blast":       jev.Score("How wide is the effect?", []string{
            "one file", "one service", "the whole system",
        }),
    })

p, _ := resp.Answers.Noul("destructive")       // 0.0 to 1.0
who, conf, _ := resp.Answers.Choice("area")    // winner + confidence
level, _, _ := resp.Answers.Score("blast")     // position on the rubric
```

All three questions share one state and cost barely more than one. That is the
intended shape — batch, do not loop.

`NoulWithCriteria(instructions, whenTrue, whenFalse)` states explicitly what
true and false mean, and measurably beats a bare `Noul` on the same question
(0.87 against 0.75 in the recorded corpus).

### Options

`WithModel`, `WithBaseURL`, `WithTimeout`, `WithMaxRetries`, `WithHTTPClient`.
The timeout bounds each attempt via the request context, never a shared HTTP
client.

### Errors worth branching on

```go
errors.Is(err, jev.ErrTooLarge)      // trim your state and retry
errors.Is(err, jev.ErrUnavailable)   // transport or 5xx; transient
errors.Is(err, jev.ErrInvalidRequest)// your question set is wrong
errors.Is(err, jev.ErrUnauthorized)  // stop; retrying will not help
```

`ErrTooLarge` is the one to handle. The service takes roughly 32,000 tokens of
state plus the longest question and **refuses** rather than truncating, so the
caller trims. JSON tokenises at about 2.4 characters per token — not the
4 often assumed — so budget accordingly.

### Testing against it

Depend on `jev.Asker`, the one-method interface `*Client` satisfies, and
substitute a fake.

`pkg/jev/jevtest` replays a corpus recorded from the live service. Replay is
offline by construction: the transport has no network path, so a test cannot
quietly start making paid calls, and an unmatched request fails loudly naming
the command that re-records. `make jev-record` refreshes it.

Every recorded state is fictional, deliberately. **Never record real customer
or meeting content into a committed fixture** — those files are public.

---

## What it cannot do

No streaming. No batch endpoint — fanning out over one state *is* the batch. No
pagination. Exactly two endpoints exist: the question endpoint and a model
listing.

And it does not generate. If you want a sentence, you want a different model.
