// Package decide answers bounded questions about a piece of state, returning
// a choice and a calibrated measure of how sure it is.
//
// It exists to separate two things a language model is asked to do at once.
// Writing a summary is generation: open-ended, priced per token produced, and
// judged by whether it reads well. Deciding which of five colleagues a voice
// belongs to is not generation at all — the set of answers is known before the
// question is asked, and what matters is whether the number attached to the
// answer means anything.
//
// That distinction has teeth here because this codebase gates on the number.
// A speaker is written into the graph when confidence clears a threshold, so a
// model that reports 0.9 whether or not it is right will quietly fill the
// graph with confident mistakes. A decision model trained to calibrate — where
// answers given at 0.8 are right about 80% of the time — makes the threshold
// mean what it was always assumed to mean.
//
// Generation stays where it was. Nothing in this package writes prose.
package decide

import "context"

// Question is one decision to make: what to ask, and the answers allowed.
type Question struct {
	// Ask is the question in plain words.
	Ask string
	// Options maps each allowed answer to a description of when it applies.
	// The descriptions are what the judgment is made against, so they carry
	// more weight than the keys.
	Options map[string]string
}

// Verdict is one answer, with enough of the distribution to see how close it
// was.
type Verdict struct {
	// Choice is the option chosen.
	Choice string
	// Confidence is how concentrated the distribution was, 0..1.
	Confidence float64
	// RunnerUp is the next most likely option, and RunnerUpShare the mass on
	// it. A winner at 0.51 against one rival is a different situation from a
	// winner at 0.51 against five, and the choice alone does not say which.
	RunnerUp      string
	RunnerUpShare float64
}

// Judge answers questions about a state.
//
// Implementations are safe for concurrent use.
type Judge interface {
	// Decide answers every question about one state. Implementations answer
	// them together where the underlying service allows it, so asking about
	// ten speakers costs little more than asking about one.
	Decide(ctx context.Context, state any, questions map[string]Question) (map[string]Verdict, error)

	// Name identifies the judge for logging, e.g. "jev/jev-1.13.0".
	Name() string
}

// Usage reports what a judgment cost, for callers that track spend.
type Usage struct {
	Requests     int
	InputTokens  int
	OutputTokens int
}
