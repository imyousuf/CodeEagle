package jev

import (
	"fmt"
	"sort"
)

// Answer is one decision, as the service returned it.
//
// Which fields carry meaning depends on the question that produced it, so
// prefer the accessors on [Answers], which check the type before reading.
type Answer struct {
	Type QuestionType `json:"type"`
	// Noul is the probability that a binary proposition holds.
	Noul float64 `json:"noul"`
	// Choice is the winning option's key.
	Choice string `json:"choice"`
	// Score is the continuous position on the rubric.
	Score float64 `json:"score"`
	// Confidence is how concentrated the distribution is, for Choice and
	// Score. A Noul has none: its probability already expresses the whole of
	// its uncertainty.
	Confidence float64 `json:"confidence"`
	// Probabilities is the mass on every option or rubric level.
	Probabilities map[string]float64 `json:"probabilities"`
	// Legend maps a Score's level indices back to their descriptions.
	Legend map[string]string `json:"legend"`
}

// Answers holds every answer in a reply, keyed by question name.
type Answers map[string]Answer

// Usage reports what the request cost.
//
// Only input tokens are billed. Output tokens are reported and are not always
// zero, despite documentation to the contrary, but they are not charged for —
// which is what makes asking many questions at once worthwhile.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Response is a complete reply.
type Response struct {
	// Model is the version that answered. With a rolling alias this is how to
	// find out what actually ran.
	Model   string  `json:"model"`
	Answers Answers `json:"answers"`
	Usage   Usage   `json:"usage"`
}

// Noul returns the probability that the named proposition holds.
//
// The error distinguishes "no such answer" from "an answer of another type",
// because both mean the caller and the request have drifted apart.
func (a Answers) Noul(name string) (float64, error) {
	ans, err := a.typed(name, TypeNoul)
	if err != nil {
		return 0, err
	}
	return ans.Noul, nil
}

// Choice returns the winning option and how concentrated the distribution was.
func (a Answers) Choice(name string) (choice string, confidence float64, err error) {
	ans, err := a.typed(name, TypeChoice)
	if err != nil {
		return "", 0, err
	}
	return ans.Choice, ans.Confidence, nil
}

// Score returns the rubric position and how concentrated the distribution was.
func (a Answers) Score(name string) (score, confidence float64, err error) {
	ans, err := a.typed(name, TypeScore)
	if err != nil {
		return 0, 0, err
	}
	return ans.Score, ans.Confidence, nil
}

// Probability returns the mass the model put on one option or rubric level.
//
// Useful when the runner-up matters as much as the winner: a choice at 0.51
// against a 0.49 is a different situation from one at 0.51 against five
// options at 0.098 each, and the winning key alone does not say which it was.
func (a Answers) Probability(name, option string) (float64, error) {
	ans, ok := a[name]
	if !ok {
		return 0, fmt.Errorf("no answer named %q", name)
	}
	p, ok := ans.Probabilities[option]
	if !ok {
		return 0, fmt.Errorf("answer %q has no option %q", name, option)
	}
	return p, nil
}

// Ranked returns a Choice's options from most to least likely.
//
// Ties break on the option name, so the order is the same on every run.
func (a Answers) Ranked(name string) ([]RankedOption, error) {
	ans, err := a.typed(name, TypeChoice)
	if err != nil {
		return nil, err
	}
	out := make([]RankedOption, 0, len(ans.Probabilities))
	for option, p := range ans.Probabilities {
		out = append(out, RankedOption{Option: option, Probability: p})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Probability != out[j].Probability {
			return out[i].Probability > out[j].Probability
		}
		return out[i].Option < out[j].Option
	})
	return out, nil
}

// RankedOption is one option and the mass on it.
type RankedOption struct {
	Option      string
	Probability float64
}

// typed fetches an answer and checks it came from the question type expected.
func (a Answers) typed(name string, want QuestionType) (Answer, error) {
	ans, ok := a[name]
	if !ok {
		return Answer{}, fmt.Errorf("no answer named %q", name)
	}
	if ans.Type != want {
		return Answer{}, fmt.Errorf("answer %q is a %s, not a %s", name, ans.Type, want)
	}
	return ans, nil
}
