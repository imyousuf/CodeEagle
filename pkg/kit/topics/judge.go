package topics

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/imyousuf/CodeEagle/pkg/kit/decide"
)

// A pair of topics is judged with two yes/no questions rather than a choice
// between relation types. That was measured, not assumed: over 91
// hand-labelled pairs from a real corpus, a six-way choice (same subject,
// facet of, sibling, co-occurring, unrelated) named the labelled relation 51%
// of the time and confused sibling with co-occurring in both directions -- a
// boundary the person labelling could not hold consistently either. The mass
// the same answers put on "unrelated", though, separated related pairs from
// unrelated ones with an AUC of 0.92. Asked directly as a probability, it did
// the same, and was conservative in the way that matters: no unrelated pair
// scored above 0.56, so a gate at 0.6 admitted 22 pairs and every one of them
// was related.
//
// Direction -- which of two topics is the narrower -- agreed with the labels
// on four pairs of seven and was refused for most siblings, so it is not
// asked.

// Verdict is one judged pair.
type Verdict struct {
	Pair *Pair
	// Related is the probability that something filed under either topic
	// is worth showing to someone asking about the other: the two name one
	// subject, one is part of the other, or both are parts of one broader
	// subject.
	Related float64
	// Same is the probability that the two labels are two wordings of one
	// subject. Faint in practice -- a registry merges rewordings before a
	// pair reaches here, so the survivors are the arguable ones -- and
	// recorded rather than gated on.
	Same float64
}

// Judging reports what a round of judging cost.
type Judging struct {
	// Requests counts the judge calls that were answered, including one
	// whose reply could not be read when a Meter recorded it.
	Requests int
	// InputTokens is what those calls read, where the judge reports it
	// through a Meter.
	InputTokens int
	// Latency is the time spent waiting, refused calls included.
	Latency time.Duration
	// Model names what answered: as a Meter reported it, else the judge's
	// Name.
	Model string
}

// Add accumulates another round.
func (j *Judging) Add(o Judging) {
	j.Requests += o.Requests
	j.InputTokens += o.InputTokens
	j.Latency += o.Latency
	if o.Model != "" {
		j.Model = o.Model
	}
}

// Meter collects what one judge call cost, for a judge -- or whatever it
// asks -- that can say. A decide.Judge returns verdicts and nothing else, so
// a PairJudge puts a fresh Meter in the context of every Decide call, and
// anything down the chain that knows the cost records it there.
//
// A nil *Meter records nothing, so a recorder need not check.
type Meter struct {
	mu          sync.Mutex
	inputTokens int
	model       string
	recorded    bool
}

type meterKey struct{}

// MeterFrom returns the Meter of the judge call ctx belongs to, or nil.
func MeterFrom(ctx context.Context) *Meter {
	m, _ := ctx.Value(meterKey{}).(*Meter)
	return m
}

// Record adds an answer's input tokens and names the model that gave it.
func (m *Meter) Record(inputTokens int, model string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inputTokens += inputTokens
	m.model = model
	m.recorded = true
}

func (m *Meter) read() (inputTokens int, model string, recorded bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inputTokens, m.model, m.recorded
}

// Question names. Pair i of a batch is named PairName(i), and asked
// RelatedQuestion+name and SameQuestion+name.
const (
	RelatedQuestion = "related_"
	SameQuestion    = "same_"
)

// PairName names the pair at index i of a batch, as the state and the
// questions refer to it.
func PairName(i int) string { return fmt.Sprintf("p%d", i) }

// Card renders a batch of pairs as the state a judge reads. Pair i is named
// PairName(i). A judge believes what the state says, so every field should
// be exactly what its name says, and nothing in it should presume the
// answer.
type Card func(pairs []*Pair) any

// Questions are the two questions asked about each pair, given its name.
// Spell out both answers: against a live decision model, naming the two
// cases moved a borderline judgment measurably, and the false case is what
// lets the model decline.
type Questions struct {
	Related func(pair string) decide.Question
	Same    func(pair string) decide.Question
}

// For renders the questions for a batch of n pairs, keyed by name.
func (q Questions) For(n int) map[string]decide.Question {
	out := make(map[string]decide.Question, 2*n)
	for i := range n {
		name := PairName(i)
		out[RelatedQuestion+name] = q.Related(name)
		out[SameQuestion+name] = q.Same(name)
	}
	return out
}

// DefaultQuestions asks whether what was said under two topics is about one
// thing, and whether their labels name one subject.
var DefaultQuestions = Questions{
	Related: func(pair string) decide.Question {
		return decide.YesNo(
			fmt.Sprintf("In the pair whose id is %q, is what was said under topic a about the same thing as what was said under topic b, or about a part of it, or is a part of it?", pair),
			"a and b name one subject, or one is a part of the other, or both are parts of one broader subject; a person asking about either would want to see what was filed under the other",
			"a and b are about different things; a person asking about one would not want what was filed under the other, even if one item happened to discuss both",
		)
	},
	Same: func(pair string) decide.Question {
		return decide.YesNo(
			fmt.Sprintf("In the pair whose id is %q, do the labels of topic a and topic b name one subject in different words?", pair),
			"the two labels are two wordings of one subject; anything filed under one belongs under the other",
			"the two labels name different subjects, however closely related",
		)
	},
}

// Bounds on what the default card shows of a topic.
const (
	cardSummaryChars = 300
	cardQuoteChars   = 200
)

// defaultTopicCard is what the default card shows of one topic.
type defaultTopicCard struct {
	ID            string   `json:"id"`
	Label         string   `json:"label"`
	OtherWordings []string `json:"other_wordings,omitempty"`
	// TimesFiled is how many things were filed under the label.
	TimesFiled  int      `json:"times_filed_under"`
	WhatWasSaid []string `json:"what_was_said_under_it,omitempty"`
	Quote       string   `json:"verbatim_quote_filed_under_it,omitempty"`
}

type defaultPairCard struct {
	Pair string `json:"pair"`
	A    string `json:"a"`
	B    string `json:"b"`
	// FiledUnderBoth is a count and nothing more: two labels one thing used
	// are by construction not the same wording, and may or may not be the
	// same subject.
	FiledUnderBoth int `json:"times_filed_under_both"`
	Adjacent       int `json:"times_consecutive_parts_of_one_thing"`
}

type defaultState struct {
	Topics []defaultTopicCard `json:"topics"`
	Pairs  []defaultPairCard  `json:"pairs"`
}

// DefaultCard shows each topic once -- its label, other wordings, how often
// it was used, up to three things said under it and a quote -- and each
// pair by name with its co-occurrence counts.
func DefaultCard(pairs []*Pair) any {
	st := &defaultState{}
	seen := make(map[string]bool)
	add := func(p *Profile) {
		if seen[p.ID()] {
			return
		}
		seen[p.ID()] = true
		card := defaultTopicCard{
			ID:            p.ID(),
			Label:         p.Node.Name,
			OtherWordings: Aliases(p.Node),
			TimesFiled:    len(p.Uses),
		}
		for _, s := range p.Summaries {
			card.WhatWasSaid = append(card.WhatWasSaid, clip(strings.Join(strings.Fields(s), " "), cardSummaryChars))
		}
		if p.Quote != "" {
			card.Quote = clip(strings.Join(strings.Fields(p.Quote), " "), cardQuoteChars)
		}
		st.Topics = append(st.Topics, card)
	}
	for i, p := range pairs {
		add(p.A)
		add(p.B)
		st.Pairs = append(st.Pairs, defaultPairCard{
			Pair: PairName(i), A: p.A.ID(), B: p.B.ID(),
			FiledUnderBoth: p.Shared, Adjacent: p.Adjacent,
		})
	}
	return st
}

// clip shortens s to n runes, marking the cut with an ellipsis.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// DefaultBatchSize is how many pairs share one state. Ten pairs carry at
// most twenty topics, a few thousand tokens -- well inside what a decision
// model accepts -- while the questions about them are answered together.
const DefaultBatchSize = 10

// PairJudge decides how pairs of topics relate, by putting two yes/no
// questions about each to a decide.Judge.
//
// A nil decide.Judge is allowed and answers nothing: a caller without a
// judge asserts no relation rather than failing. Build one with
// NewPairJudge.
type PairJudge struct {
	judge decide.Judge
	cfg   config
}

// NewPairJudge builds a pair judge over judge, which may be nil. Options
// that concern the relater are ignored.
func NewPairJudge(judge decide.Judge, opts ...Option) *PairJudge {
	return &PairJudge{judge: judge, cfg: newConfig(opts)}
}

// Request renders what one batch sends: the state and the questions.
func (j *PairJudge) Request(pairs []*Pair) (state any, questions map[string]decide.Question) {
	return j.cfg.card(pairs), j.cfg.questions.For(len(pairs))
}

// Judge decides how each pair relates, in batches, and returns one verdict
// per pair in the order given. On an error the verdicts of the batches
// answered before it are returned with it.
func (j *PairJudge) Judge(ctx context.Context, pairs []*Pair) ([]*Verdict, Judging, error) {
	var report Judging
	if j == nil || j.judge == nil || len(pairs) == 0 {
		return nil, report, nil
	}
	out := make([]*Verdict, 0, len(pairs))
	for start := 0; start < len(pairs); start += j.cfg.batch {
		end := min(start+j.cfg.batch, len(pairs))
		verdicts, err := j.judgeBatch(ctx, pairs[start:end], &report)
		if err != nil {
			return out, report, err
		}
		out = append(out, verdicts...)
	}
	return out, report, nil
}

func (j *PairJudge) judgeBatch(ctx context.Context, pairs []*Pair, report *Judging) ([]*Verdict, error) {
	state, questions := j.Request(pairs)
	meter := &Meter{}
	started := time.Now()
	answers, err := j.judge.Decide(context.WithValue(ctx, meterKey{}, meter), state, questions)
	report.Latency += time.Since(started)
	if err != nil {
		// The size ceiling is the judge's, and refusing is the only way it
		// reports it; halving is the remedy that does not guess at tokens.
		if errors.Is(err, decide.ErrTooLarge) && len(pairs) > 1 {
			half := len(pairs) / 2
			first, err := j.judgeBatch(ctx, pairs[:half], report)
			if err != nil {
				return nil, err
			}
			second, err := j.judgeBatch(ctx, pairs[half:], report)
			if err != nil {
				return nil, err
			}
			return append(first, second...), nil
		}
	}
	// A call is counted when it was answered: always on success, and on a
	// failure when a Meter shows a reply came back -- one that could not be
	// read still cost what it cost.
	if tokens, model, recorded := meter.read(); err == nil || recorded {
		report.Requests++
		report.InputTokens += tokens
		if !recorded {
			model = j.judge.Name()
		}
		report.Model = model
	}
	if err != nil {
		return nil, fmt.Errorf("relate topics: %w", err)
	}

	out := make([]*Verdict, 0, len(pairs))
	for i, p := range pairs {
		name := PairName(i)
		related, ok := answers[RelatedQuestion+name]
		if !ok {
			return nil, fmt.Errorf("relate topics: no answer to %q", RelatedQuestion+name)
		}
		same, ok := answers[SameQuestion+name]
		if !ok {
			return nil, fmt.Errorf("relate topics: no answer to %q", SameQuestion+name)
		}
		out = append(out, &Verdict{Pair: p, Related: related.P, Same: same.P})
	}
	return out, nil
}
