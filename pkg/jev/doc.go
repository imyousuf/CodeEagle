// Package jev is a client for TypeSafe Jev, a decision model that answers
// typed questions about a state instead of generating text.
//
// A request carries one state — a struct, a map, a slice, or a string — and
// any number of questions about it. Every question is answered in the same
// pass, so asking ten costs about what asking one costs, and the answers come
// back as values with calibrated probabilities rather than prose to be parsed.
//
// The three question types are deliberately narrow:
//
//   - [Noul] asks whether something is true, and answers with the probability
//     that it is.
//   - [Choice] picks one of a set of options, and answers with the winner, the
//     distribution over all options, and how concentrated it is.
//   - [Score] places the state on an ordered rubric, and answers with a
//     continuous position between the levels.
//
// # What the model cannot do
//
// It does not do arithmetic, does not compare dates, and does not write prose.
// Count the items, compute the elapsed days, and pass the results in as part
// of the state; ask a generative model for the explanation. Chains of
// deduction and double negatives misfire too: split them into atomic questions
// and combine the answers in code.
//
// Sampling is bounded to the question, so an answer is always one of the
// options asked for. That is a guarantee about the shape of the answer and not
// about its truth: a confident wrong answer remains possible, which is what
// the confidence figures are for.
//
// # The state is believed
//
// Whatever a field asserts is taken as fact. Describing a count as something
// it is not — labelling a measure of who spoke next as "others replied to this
// voice" — reliably flips the answer, and the failure is the caller's
// assertion rather than the model's reading. Name each field as exactly what
// it is. Text from users, web pages or logs belongs in a field of its own,
// never mixed into the instructions, so that what it says cannot be mistaken
// for what is being asked: a comment in a diff telling the evaluator to mark
// the code safe is the obvious attack, and a named field is the defence,
// together with a human path for anything high-stakes.
//
// # Answers are not bit-stable
//
// Sending an identical request twice does not return identical numbers. Over
// five identical fan-outs recorded against jev-1.13.0, a noul moved between
// 0.73 and 0.76; across a wider set of states, probabilities have moved by up
// to 0.09, typically 0.02 to 0.03. A threshold tuned to two decimal places is
// therefore tuned to noise, and a single run is not a measurement. Gate on a
// margin — [Answers.Ranked] gives the runner-up as well as the winner — and
// leave headroom of about 0.05 around any threshold.
//
// # Size, and who trims
//
// The service accepts about 32,900 input tokens for the state plus the longest
// question — measured as 32,882 accepted and roughly 33,100 refused — and
// 64,000 for the request as a whole. An oversized request is refused with
// [ErrTooLarge], never truncated, so the caller decides what to cut: a
// transcript keeps its opening and its close, a log keeps its tail.
// [MaxStateTokens] is the safe bound. There is no tokenizer to consult;
// measured against the live service, prose runs at about 5.3 characters per
// token, a diarized transcript 3.8, Go source 3.4 and JSON 2.4 — so a JSON
// state needs roughly twice the headroom that a rule of four characters per
// token would give it.
//
// # Errors
//
// Refusals are [*APIError] values, transport failures [*TransportError]; both
// carry what the caller acts on and both match a sentinel through errors.Is:
// [ErrInvalidRequest], [ErrTooLarge], [ErrUnauthorized], [ErrRateLimited],
// [ErrOverloaded], [ErrUnavailable] and [ErrMalformedResponse]. Transient
// failures are retried with jittered backoff before any of them is returned.
// Every reply and every refusal carries the service's request id, which is the
// one thing the vendor can look up.
//
// # What the service does not offer
//
// There is no streaming, no batch endpoint, no pagination and no idempotency
// key. One request answers every question it carries, which is the batch; and
// the call has no side effects, so a retry after a transport failure is safe.
// Rate limits are documented as 1,200 requests a minute, with no header
// reporting the remaining allowance. The only other endpoint, GET /v1/models,
// lists the rolling aliases and not the pinned releases; it is not wrapped
// here.
//
// # Testing
//
// [Asker] is the seam a consumer depends on. The jevtest subpackage replays
// exchanges recorded from the live service — offline, exactly, and failing
// loudly when a request has no recording rather than reaching for the network.
package jev
