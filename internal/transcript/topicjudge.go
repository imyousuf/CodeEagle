package transcript

import (
	"context"
	"fmt"
	"strings"

	"github.com/imyousuf/CodeEagle/pkg/jev"
	"github.com/imyousuf/CodeEagle/pkg/kit/decide"
	"github.com/imyousuf/CodeEagle/pkg/kit/topics"
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

// A pair of topics is judged by pkg/kit/topics, with two yes/no questions
// put to the decision model. What is meeting-specific is here: what the model
// is shown of a topic, and the questions' wording.

type (
	// TopicVerdict is one judged pair.
	TopicVerdict = topics.Verdict
	// TopicJudging reports what a round of judging cost.
	TopicJudging = topics.Judging
	// TopicJudge decides how pairs of topics relate with a decision model.
	TopicJudge = topics.PairJudge
)

// NewTopicJudge builds a judge over a decision model. A nil asker gives a
// judge that answers nothing.
func NewTopicJudge(asker jev.Asker) *TopicJudge {
	return topics.NewPairJudge(topicDecider(asker), meetingJudgeOptions()...)
}

// topicDecider asks asker through decide.JevJudge, or gives no judge at all
// for no asker -- nil, or a nil *jev.Client -- so relating asserts nothing.
func topicDecider(asker jev.Asker) decide.Judge {
	if c, ok := asker.(*jev.Client); asker == nil || (ok && c == nil) {
		return nil
	}
	return decide.NewJevJudge(meteredAsker{asker})
}

// meteredAsker reports each reply's input tokens and model to the judge call
// it answers. A decide.Judge returns verdicts alone, while the relater's
// report and the judge recorded on every edge come from the reply.
type meteredAsker struct{ jev.Asker }

func (m meteredAsker) Ask(ctx context.Context, state any, questions jev.Questions) (*jev.Response, error) {
	resp, err := m.Asker.Ask(ctx, state, questions)
	if err == nil && resp != nil {
		topics.MeterFrom(ctx).Record(resp.Usage.InputTokens, resp.Model)
	}
	return resp, err
}

// Model names the asker's pinned model, for decide.JevJudge's Name.
func (m meteredAsker) Model() string {
	if named, ok := m.Asker.(interface{ Model() string }); ok {
		return named.Model()
	}
	return ""
}

// meetingJudgeOptions shape the kit's judge to what has always been asked:
// meeting cards, the meeting wording, ten pairs to a request.
func meetingJudgeOptions() []topics.Option {
	return []topics.Option{
		topics.WithCard(func(pairs []*topics.Pair) any { return relateCard(pairs) }),
		topics.WithQuestions(meetingQuestions),
		topics.WithBatchSize(maxPairsPerRequest),
	}
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

// The questions asked about pair p0 are related_p0 and same_p0.
const (
	relatedQuestion = topics.RelatedQuestion
	sameQuestion    = topics.SameQuestion
)

// buildRelateState renders a batch as one state and two questions per pair.
func buildRelateState(pairs []*TopicPair) (*relateState, map[string]decide.Question) {
	return relateCard(pairs), meetingQuestions.For(len(pairs))
}

// relateCard renders a batch as one state: each topic once, then each pair
// by name with what is known about it.
func relateCard(pairs []*TopicPair) *relateState {
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
			MeetingsFiledUnder: len(p.Uses),
		}
		for _, s := range p.Summaries {
			card.WhatWasSaid = append(card.WhatWasSaid, clipRunes(strings.Join(strings.Fields(s), " "), maxSummaryChars))
		}
		if p.Quote != "" {
			card.DecisionQuote = clipRunes(strings.Join(strings.Fields(p.Quote), " "), maxQuoteChars)
		}
		st.Topics = append(st.Topics, card)
	}
	for i, p := range pairs {
		add(p.A)
		add(p.B)
		st.Pairs = append(st.Pairs, pairCard{
			Pair:                   topics.PairName(i),
			A:                      p.A.ID(),
			B:                      p.B.ID(),
			MeetingsFiledUnderBoth: p.Shared,
			ConsecutiveSegments:    p.Adjacent,
		})
	}
	return st
}

// meetingQuestions are the two questions asked about each pair. Both answers
// are spelled out as situations: against the live service, naming the two
// cases moves a borderline judgment measurably, and the false case is what
// lets the model decline.
var meetingQuestions = topics.Questions{
	Related: func(name string) decide.Question {
		return decide.YesNo(
			fmt.Sprintf("In the pair whose id is %q, is what was said under topic a about the same thing as what was said under topic b, or about a part of it, or is a part of it?", name),
			"a and b name one subject, or one is a part of the other, or both are parts of one broader subject; a person asking about either would want to see meetings filed under the other",
			"a and b are about different things; a person asking about one would not want meetings filed under the other, even if a meeting happened to discuss both",
		)
	},
	Same: func(name string) decide.Question {
		return decide.YesNo(
			fmt.Sprintf("In the pair whose id is %q, do the labels of topic a and topic b name one subject in different words?", name),
			"the two labels are two wordings of one subject; any meeting filed under one belongs under the other",
			"the two labels name different subjects, however closely related",
		)
	},
}
