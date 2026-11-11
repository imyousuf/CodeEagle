package decide

import (
	"context"
	"fmt"
	"sync"

	"github.com/imyousuf/CodeEagle/pkg/jev"
)

// JevJudge answers questions with the TypeSafe Jev decision model, through
// any jev.Asker: a *jev.Client, or a caller's own asker that adds quota
// checks or usage records around one.
//
// A choice becomes jev.Choice, and a yes/no question jev.NoulWithCriteria, or
// jev.Noul when it states no criteria. Errors from the asker come back
// unwrapped, so errors.Is(err, jev.ErrTooLarge) still tells a caller to trim
// its state.
type JevJudge struct {
	asker jev.Asker
	model string

	mu    sync.Mutex
	usage Usage
}

// JevOption configures a JevJudge.
type JevOption func(*JevJudge)

// WithModelName names the model behind an asker that cannot say, for Name.
// An asker with a Model() method, such as *jev.Client, is asked instead.
func WithModelName(model string) JevOption {
	return func(j *JevJudge) { j.model = model }
}

// NewJevJudge builds a judge that asks asker. A nil asker gives a judge whose
// Decide returns ErrNoAsker; use Optional to get no judge instead.
//
// Pin the model rather than track it. Confidence thresholds are tuned
// against a particular set of weights, and a rolling alias can move the
// distributions without changing anything visible: the same inputs keep
// producing answers, just on the other side of the line.
func NewJevJudge(asker jev.Asker, opts ...JevOption) *JevJudge {
	j := &JevJudge{asker: present(asker)}
	for _, opt := range opts {
		opt(j)
	}
	if m, ok := j.asker.(interface{ Model() string }); ok && m.Model() != "" {
		j.model = m.Model()
	}
	if j.model == "" {
		j.model = jev.DefaultModel
	}
	return j
}

// Optional returns a JevJudge over asker, or no judge at all when asker is
// nil, so that a caller's "judge == nil" fallback still runs.
func Optional(asker jev.Asker, opts ...JevOption) Judge {
	if present(asker) == nil {
		return nil
	}
	return NewJevJudge(asker, opts...)
}

// present treats a typed nil inside the interface, such as a nil *jev.Client,
// as no asker.
func present(asker jev.Asker) jev.Asker {
	if c, ok := asker.(*jev.Client); ok && c == nil {
		return nil
	}
	return asker
}

// Name identifies the judge and the exact model behind it.
func (j *JevJudge) Name() string { return "jev/" + j.model }

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
	if j == nil || j.asker == nil {
		return nil, ErrNoAsker
	}

	asked := make(jev.Questions, len(questions))
	for name, q := range questions {
		switch {
		case !q.IsYesNo():
			asked[name] = jev.Choice(q.Ask, q.Options)
		case q.WhenTrue != "" || q.WhenFalse != "":
			asked[name] = jev.NoulWithCriteria(q.Ask, q.WhenTrue, q.WhenFalse)
		default:
			asked[name] = jev.Noul(q.Ask)
		}
	}

	resp, err := j.asker.Ask(ctx, state, asked)
	if err != nil {
		return nil, err
	}

	j.mu.Lock()
	j.usage.Requests++
	j.usage.InputTokens += resp.Usage.InputTokens
	j.usage.OutputTokens += resp.Usage.OutputTokens
	j.mu.Unlock()

	out := make(map[string]Verdict, len(questions))
	for name, q := range questions {
		if q.IsYesNo() {
			p, err := resp.Answers.Noul(name)
			if err != nil {
				return nil, fmt.Errorf("decide %q: %w", name, err)
			}
			out[name] = YesNoVerdict(p)
			continue
		}
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
