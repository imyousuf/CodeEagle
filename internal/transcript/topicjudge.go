package transcript

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/imyousuf/CodeEagle/pkg/jev"
)

// A pair of topics is judged with two yes/no questions rather than a choice
// between relation types. That was measured, not assumed: over 91 hand-labelled
// pairs from the real corpus, a six-way choice (same subject, facet of, sibling,
// co-occurring, unrelated) named the labelled relation 51% of the time and
// confused sibling with co-occurring in both directions — a boundary the person
// labelling could not hold consistently either. The mass the same answers put
// on "unrelated", though, separated related pairs from unrelated ones with an
// AUC of 0.92. Asked directly as a probability, it did the same, and was
// conservative in the way that matters: no unrelated pair scored above 0.56,
// so a gate at 0.6 admitted 22 pairs and every one of them was related.
//
// Direction — which of two topics is the narrower — agreed with the labels on
// four pairs of seven and was refused for most siblings, so it is not asked.

// TopicVerdict is one judged pair.
type TopicVerdict struct {
	Pair *TopicPair
	// Related is the probability that a meeting filed under either topic is
	// worth showing to someone asking about the other: the two name one
	// subject, one is part of the other, or both are parts of one broader
	// subject.
	Related float64
	// Same is the probability that the two labels are two wordings of one
	// subject. Faint on the real corpus — the registry already merges
	// rewordings before a pair reaches here, so the survivors are the
	// arguable ones — and recorded rather than gated on.
	Same float64
}

// TopicJudging reports what a round of judging cost.
type TopicJudging struct {
	Requests    int
	InputTokens int
	Latency     time.Duration
	Model       string
}

// Add accumulates another round.
func (j *TopicJudging) Add(o TopicJudging) {
	j.Requests += o.Requests
	j.InputTokens += o.InputTokens
	j.Latency += o.Latency
	if o.Model != "" {
		j.Model = o.Model
	}
}

// TopicJudge decides how pairs of topics relate with a decision model.
type TopicJudge struct {
	asker jev.Asker
}

// NewTopicJudge builds a judge over a decision model.
func NewTopicJudge(asker jev.Asker) *TopicJudge {
	return &TopicJudge{asker: asker}
}

// Bounds on one request. Ten pairs share one state, so a request carries at
// most twenty topics and stays a few thousand tokens — well inside what the
// service accepts — while the questions about them are answered together
// and billed once.
const (
	maxPairsPerRequest = 10
	maxSummaryChars    = 300
	maxQuoteChars      = 200
)

// topicCard is what the model sees of one topic. Field names say exactly
// what each value is, because the model believes them: a count of meetings
// described as anything other than a count of meetings would be read as
// that other thing.
type topicCard struct {
	ID            string   `json:"id"`
	Label         string   `json:"label"`
	OtherWordings []string `json:"other_wordings_meetings_used,omitempty"`
	// MeetingsFiledUnder is how many meetings were filed under the label.
	MeetingsFiledUnder int `json:"meetings_filed_under"`
	// WhatWasSaid is what the segments filed under the label said.
	WhatWasSaid []string `json:"what_meetings_filed_under_it_said,omitempty"`
	// DecisionQuote is a verbatim quote from a decision filed under it.
	DecisionQuote string `json:"verbatim_quote_from_a_decision_filed_under_it,omitempty"`
}

// pairCard names two topics to relate and the facts known about the pair.
type pairCard struct {
	Pair string `json:"pair"`
	A    string `json:"a"`
	B    string `json:"b"`
	// MeetingsFiledUnderBoth is how many meetings were filed under both
	// labels. It is a count and nothing more: two labels one meeting used
	// are by construction not the same wording, and may or may not be the
	// same subject.
	MeetingsFiledUnderBoth int `json:"meetings_filed_under_both"`
	// ConsecutiveSegments is how many times the two were consecutive
	// segments of one meeting.
	ConsecutiveSegments int `json:"times_consecutive_segments_of_one_meeting"`
}

type relateState struct {
	Topics []topicCard `json:"topics"`
	Pairs  []pairCard  `json:"pairs"`
}

// Judge decides how each pair relates, in batches, and returns one verdict
// per pair in the order given.
func (j *TopicJudge) Judge(ctx context.Context, pairs []*TopicPair) ([]*TopicVerdict, TopicJudging, error) {
	var report TopicJudging
	if j == nil || j.asker == nil || len(pairs) == 0 {
		return nil, report, nil
	}
	out := make([]*TopicVerdict, 0, len(pairs))
	for start := 0; start < len(pairs); start += maxPairsPerRequest {
		end := min(start+maxPairsPerRequest, len(pairs))
		verdicts, err := j.judgeBatch(ctx, pairs[start:end], &report)
		if err != nil {
			return out, report, err
		}
		out = append(out, verdicts...)
	}
	return out, report, nil
}

func (j *TopicJudge) judgeBatch(ctx context.Context, pairs []*TopicPair, report *TopicJudging) ([]*TopicVerdict, error) {
	state, questions := buildRelateState(pairs)
	started := time.Now()
	resp, err := j.asker.Ask(ctx, state, questions)
	report.Latency += time.Since(started)
	if err != nil {
		// The size ceiling is the service's, and refusing is the only way
		// it reports it; halving is the remedy that does not guess at
		// tokens.
		if errors.Is(err, jev.ErrTooLarge) && len(pairs) > 1 {
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
		return nil, fmt.Errorf("relate topics: %w", err)
	}
	report.Requests++
	report.InputTokens += resp.Usage.InputTokens
	report.Model = resp.Model

	out := make([]*TopicVerdict, 0, len(pairs))
	for i, p := range pairs {
		name := pairName(i)
		related, err := resp.Answers.Noul(relatedQuestion + name)
		if err != nil {
			return nil, fmt.Errorf("relate topics: %w", err)
		}
		same, err := resp.Answers.Noul(sameQuestion + name)
		if err != nil {
			return nil, fmt.Errorf("relate topics: %w", err)
		}
		out = append(out, &TopicVerdict{Pair: p, Related: related, Same: same})
	}
	return out, nil
}

const (
	relatedQuestion = "related_"
	sameQuestion    = "same_"
)

func pairName(i int) string { return fmt.Sprintf("p%d", i) }

// buildRelateState renders a batch as one state and two questions per pair.
func buildRelateState(pairs []*TopicPair) (*relateState, jev.Questions) {
	st := &relateState{}
	seen := make(map[string]bool)
	add := func(p *TopicProfile) {
		if seen[p.ID()] {
			return
		}
		seen[p.ID()] = true
		card := topicCard{
			ID:                 p.ID(),
			Label:              p.Node.Name,
			OtherWordings:      aliasesOf(p.Node),
			MeetingsFiledUnder: len(p.Meetings),
		}
		for _, s := range p.Summaries {
			card.WhatWasSaid = append(card.WhatWasSaid, clipRunes(strings.Join(strings.Fields(s), " "), maxSummaryChars))
		}
		if p.Quote != "" {
			card.DecisionQuote = clipRunes(strings.Join(strings.Fields(p.Quote), " "), maxQuoteChars)
		}
		st.Topics = append(st.Topics, card)
	}
	questions := make(jev.Questions, 2*len(pairs))
	for i, p := range pairs {
		add(p.A)
		add(p.B)
		name := pairName(i)
		st.Pairs = append(st.Pairs, pairCard{
			Pair:                   name,
			A:                      p.A.ID(),
			B:                      p.B.ID(),
			MeetingsFiledUnderBoth: p.SharedMeetings,
			ConsecutiveSegments:    p.Adjacent,
		})
		// Both answers are spelled out as situations: against the live
		// service, naming the two cases moves a borderline judgment
		// measurably, and the false case is what lets the model decline.
		questions[relatedQuestion+name] = jev.NoulWithCriteria(
			fmt.Sprintf("In the pair whose id is %q, is what was said under topic a about the same thing as what was said under topic b, or about a part of it, or is a part of it?", name),
			"a and b name one subject, or one is a part of the other, or both are parts of one broader subject; a person asking about either would want to see meetings filed under the other",
			"a and b are about different things; a person asking about one would not want meetings filed under the other, even if a meeting happened to discuss both",
		)
		questions[sameQuestion+name] = jev.NoulWithCriteria(
			fmt.Sprintf("In the pair whose id is %q, do the labels of topic a and topic b name one subject in different words?", name),
			"the two labels are two wordings of one subject; any meeting filed under one belongs under the other",
			"the two labels name different subjects, however closely related",
		)
	}
	return st, questions
}
