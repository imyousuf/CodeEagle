package jev

import (
	"fmt"
	"sort"
)

// QuestionType names one of the three primitives.
type QuestionType string

const (
	// TypeNoul is a binary condition, answered with a probability.
	TypeNoul QuestionType = "noul"
	// TypeChoice is a categorical classification over named options.
	TypeChoice QuestionType = "choice"
	// TypeScore is a position on an ordered rubric.
	TypeScore QuestionType = "score"
)

// Limits the service imposes, or that it does not impose and should.
const (
	// MinChoiceOptions and MaxChoiceOptions bound a Choice.
	//
	// The upper bound the service enforces. The lower one it does not: a
	// single-option choice is accepted and answered with that option at
	// confidence 1.0 -- the same false certainty a one-level rubric produces,
	// and for the same reason, since there was nothing to choose between.
	// A caller who reaches one candidate has an answer already and does not
	// need to pay to be told so.
	MinChoiceOptions = 2
	MaxChoiceOptions = 255
	// MinScoreLevels and MaxScoreLevels bound a Score rubric.
	//
	// The service does not enforce this: a one-level rubric is accepted and
	// answered with score 0.0 at confidence 1.0, which reads as certainty
	// about a question that had only one possible answer. This package
	// refuses it instead.
	MinScoreLevels = 2
	MaxScoreLevels = 10
	// MaxStateTokens is the approximate ceiling on the state plus the longest
	// question, measured against the live service. Exceeding it is refused
	// with [ErrTooLarge] rather than truncated, so a caller that trims its own
	// input knows when to trim further.
	MaxStateTokens = 32000
)

// Question is one thing to decide about the state.
//
// Build one with [Noul], [Choice] or [Score] rather than by hand: they set the
// type and shape the criteria correctly for each primitive.
type Question struct {
	Type QuestionType `json:"type"`
	// Instructions is the question itself, in plain words.
	Instructions string `json:"instructions,omitempty"`
	// Criteria narrows what each answer means. Its shape depends on the type:
	// a two-key map for Noul, an option-to-description map for Choice, an
	// ordered list of levels for Score.
	Criteria any `json:"criteria,omitempty"`
}

// Noul asks whether a proposition holds.
//
// The answer is the probability that it does, so 0.5 is not a "maybe" to be
// rounded but a statement that the state does not decide the question.
func Noul(instructions string) Question {
	return Question{Type: TypeNoul, Instructions: instructions}
}

// NoulWithCriteria asks whether a proposition holds, spelling out what each
// answer would mean.
//
// Worth the extra words: against the live service, naming the two cases moved
// a borderline judgment from 0.73 to 0.87 on the same state.
func NoulWithCriteria(instructions, whenTrue, whenFalse string) Question {
	return Question{
		Type:         TypeNoul,
		Instructions: instructions,
		Criteria:     map[string]string{"true": whenTrue, "false": whenFalse},
	}
}

// Choice picks one option from a named set.
//
// Always offer an option for "none of these". Without one the model must
// spread its belief across answers it has already rejected, which inflates the
// entropy of every other option and makes the confidence figure mean less. It
// is also how declining becomes a valid answer: a pipeline identifying voices
// in a transcript offers "unresolved", and treats it as correct rather than as
// a failure.
func Choice(instructions string, options map[string]string) Question {
	return Question{Type: TypeChoice, Instructions: instructions, Criteria: options}
}

// Score places the state on an ordered rubric of two to ten levels.
//
// The answer is continuous: 1.84 on a three-level rubric means the state sits
// close to the top level while keeping something of the middle one. Levels
// must describe concrete situations — "the command rewrites shared history"
// rather than "high" — because the model reads them as descriptions and not as
// labels on a scale.
func Score(instructions string, levels []string) Question {
	return Question{Type: TypeScore, Instructions: instructions, Criteria: levels}
}

// validate checks a question against limits the service enforces and limits it
// does not.
func (q Question) validate(name string) error {
	if name == "" {
		return fmt.Errorf("question has no name")
	}

	switch q.Type {
	case TypeNoul:
		if q.Instructions == "" && q.Criteria == nil {
			return fmt.Errorf("question %q: a noul needs instructions or criteria", name)
		}

	case TypeChoice:
		options, ok := q.Criteria.(map[string]string)
		if !ok || len(options) == 0 {
			return fmt.Errorf("question %q: a choice needs options", name)
		}
		if len(options) < MinChoiceOptions || len(options) > MaxChoiceOptions {
			return fmt.Errorf("question %q: %d options, want %d to %d",
				name, len(options), MinChoiceOptions, MaxChoiceOptions)
		}
		for key, desc := range options {
			if key == "" {
				return fmt.Errorf("question %q: an option has no name", name)
			}
			if desc == "" {
				return fmt.Errorf("question %q: option %q has no description; "+
					"the description is what the model matches against", name, key)
			}
		}

	case TypeScore:
		levels, ok := q.Criteria.([]string)
		if !ok {
			return fmt.Errorf("question %q: a score needs an ordered list of levels", name)
		}
		if len(levels) < MinScoreLevels || len(levels) > MaxScoreLevels {
			return fmt.Errorf("question %q: %d rubric levels, want %d to %d",
				name, len(levels), MinScoreLevels, MaxScoreLevels)
		}
		for i, level := range levels {
			if level == "" {
				return fmt.Errorf("question %q: rubric level %d is empty", name, i)
			}
		}

	default:
		return fmt.Errorf("question %q: unknown type %q", name, q.Type)
	}
	return nil
}

// Questions is a set of questions about one state, keyed by the name each
// answer comes back under.
type Questions map[string]Question

// validate checks every question, reporting them in a stable order so a
// caller fixing several sees the same first error each run.
func (qs Questions) validate() error {
	if len(qs) == 0 {
		return fmt.Errorf("no questions asked")
	}
	names := make([]string, 0, len(qs))
	for name := range qs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := qs[name].validate(name); err != nil {
			return err
		}
	}
	return nil
}
