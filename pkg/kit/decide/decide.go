// Package decide answers bounded questions about a piece of state, returning
// a choice and a measure of how sure it is.
//
// It exists to separate two things a language model is asked to do at once.
// Writing a summary is generation: open-ended, priced per token produced, and
// judged by whether it reads well. Deciding which of five colleagues a voice
// belongs to is not generation at all — the set of answers is known before the
// question is asked, and what matters is whether the number attached to the
// answer means anything.
//
// That distinction has teeth because callers gate on the number. A speaker is
// written into a graph when confidence clears a threshold, so a model that
// reports 0.9 whether or not it is right will quietly fill the graph with
// confident mistakes. A decision model trained to calibrate — where answers
// given at 0.8 are right about 80% of the time — makes the threshold mean
// what it was always assumed to mean. JevJudge is backed by one. LLMJudge
// asks a language model to state a probability instead, which is cheaper to
// stand up and steadier when averaged over passes, but not calibrated: tune
// its thresholds on labelled examples of your own.
//
// A question is either a choice among named options or a yes/no question. A
// batch is one Decide call: one shared state and one question per item.
//
// Generation stays elsewhere. Nothing in this package writes prose.
package decide

import (
	"context"
	"errors"

	"github.com/imyousuf/CodeEagle/pkg/jev"
)

// Question is one decision to make: what to ask, and the answers allowed.
type Question struct {
	// Ask is the question in plain words.
	Ask string
	// Options maps each allowed answer to a description of when it applies.
	// The descriptions are what the judgment is made against, so they carry
	// more weight than the keys. A question without options is a yes/no
	// question.
	Options map[string]string
	// WhenTrue and WhenFalse spell out what yes and no mean, for a yes/no
	// question. Both are optional, but stating them sharpens the judgment
	// measurably over the bare question. Left out of JSON when empty, so a
	// choice encodes exactly as it did before yes/no questions existed.
	WhenTrue  string `json:",omitempty"`
	WhenFalse string `json:",omitempty"`
}

// YesNo builds a yes/no question; whenTrue and whenFalse may be empty.
func YesNo(ask, whenTrue, whenFalse string) Question {
	return Question{Ask: ask, WhenTrue: whenTrue, WhenFalse: whenFalse}
}

// IsYesNo reports whether q is a yes/no question rather than a choice.
func (q Question) IsYesNo() bool { return len(q.Options) == 0 }

// Verdict is one answer, with enough of the distribution to see how close it
// was.
type Verdict struct {
	// Choice is the option chosen; "true" or "false" for a yes/no question.
	Choice string
	// Confidence is how concentrated the distribution was, 0..1.
	Confidence float64
	// RunnerUp is the next most likely option, and RunnerUpShare the mass on
	// it. A winner at 0.51 against one rival is a different situation from a
	// winner at 0.51 against five, and the choice alone does not say which.
	RunnerUp      string
	RunnerUpShare float64
	// P is the probability that the answer to a yes/no question is yes. It
	// is the number to gate and rank on; Choice and Confidence restate it.
	P float64
}

// Answers to a yes/no question, as Verdict.Choice names them.
const (
	True  = "true"
	False = "false"
)

// YesNoVerdict describes a yes/no answer whose probability of yes is p.
func YesNoVerdict(p float64) Verdict {
	v := Verdict{P: p, Choice: True, Confidence: p, RunnerUp: False, RunnerUpShare: 1 - p}
	if p < 0.5 {
		v.Choice, v.Confidence, v.RunnerUp, v.RunnerUpShare = False, 1-p, True, p
	}
	return v
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

var (
	// ErrNoAsker is returned by a judge that has nothing to ask. Prefer
	// Optional, which gives no judge at all, so callers take their fallback
	// path instead of failing.
	ErrNoAsker = errors.New("decide: no decision model to ask")
	// ErrUnsupported is returned for a kind of question a judge cannot
	// answer.
	ErrUnsupported = errors.New("decide: question not supported by this judge")
	// ErrTooLarge is a judge's way of saying the state is more than it can
	// take: trim it, or split the questions over smaller states, and ask
	// again. It is jev.ErrTooLarge, so errors.Is matches either name.
	ErrTooLarge = jev.ErrTooLarge
)
