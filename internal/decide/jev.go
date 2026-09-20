package decide

import (
	"context"
	"fmt"
	"sync"

	"github.com/imyousuf/CodeEagle/pkg/jev"
)

// JevJudge answers questions with the TypeSafe Jev decision model.
type JevJudge struct {
	client *jev.Client

	mu    sync.Mutex
	usage Usage
}

// NewJevJudge builds a judge backed by Jev.
//
// The model is pinned rather than tracked. Confidence thresholds are tuned
// against a particular set of weights, and a rolling alias can move the
// distributions without changing anything visible: the same inputs keep
// producing answers, just on the other side of the line.
func NewJevJudge(apiKey, model string) (*JevJudge, error) {
	var opts []jev.Option
	if model != "" {
		opts = append(opts, jev.WithModel(model))
	}
	client, err := jev.New(apiKey, opts...)
	if err != nil {
		return nil, err
	}
	return &JevJudge{client: client}, nil
}

// Name identifies the judge and the exact model behind it.
func (j *JevJudge) Name() string { return "jev/" + j.client.Model() }

// Usage reports what this judge has spent so far.
func (j *JevJudge) Usage() Usage {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.usage
}

// Decide answers every question in one round trip.
func (j *JevJudge) Decide(
	ctx context.Context, state any, questions map[string]Question,
) (map[string]Verdict, error) {
	if len(questions) == 0 {
		return map[string]Verdict{}, nil
	}

	asked := make(jev.Questions, len(questions))
	for name, q := range questions {
		asked[name] = jev.Choice(q.Ask, q.Options)
	}

	resp, err := j.client.Ask(ctx, state, asked)
	if err != nil {
		return nil, err
	}

	j.mu.Lock()
	j.usage.Requests++
	j.usage.InputTokens += resp.Usage.InputTokens
	j.usage.OutputTokens += resp.Usage.OutputTokens
	j.mu.Unlock()

	out := make(map[string]Verdict, len(questions))
	for name := range questions {
		choice, confidence, err := resp.Answers.Choice(name)
		if err != nil {
			// Sampling is bounded to the question asked, so a missing answer
			// means the request and the reply disagree about what was asked —
			// worth reporting rather than defaulting.
			return nil, fmt.Errorf("decide %q: %w", name, err)
		}
		verdict := Verdict{Choice: choice, Confidence: confidence}
		if ranked, err := resp.Answers.Ranked(name); err == nil && len(ranked) > 1 {
			verdict.RunnerUp = ranked[1].Option
			verdict.RunnerUpShare = ranked[1].Probability
		}
		out[name] = verdict
	}
	return out, nil
}
