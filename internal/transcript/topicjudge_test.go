package transcript

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/pkg/jev"
)

// scriptedTopicAsker answers the two questions about each pair from a table
// keyed by the pair's labels, and can refuse the first request as too large.
type scriptedTopicAsker struct {
	related    map[string]float64
	same       map[string]float64
	refuseOnce bool
	calls      int
	states     []relateState
}

func labelsKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

func (s *scriptedTopicAsker) Ask(_ context.Context, state any, questions jev.Questions) (*jev.Response, error) {
	s.calls++
	raw, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	var st relateState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	s.states = append(s.states, st)
	if s.refuseOnce {
		s.refuseOnce = false
		return nil, jev.ErrTooLarge
	}
	labelOf := make(map[string]string)
	for _, t := range st.Topics {
		labelOf[t.ID] = t.Label
	}
	answers := jev.Answers{}
	for _, p := range st.Pairs {
		key := labelsKey(labelOf[p.A], labelOf[p.B])
		for _, prefix := range []string{relatedQuestion, sameQuestion} {
			name := prefix + p.Pair
			if _, asked := questions[name]; !asked {
				return nil, fmt.Errorf("no question %q", name)
			}
		}
		related, ok := s.related[key]
		if !ok {
			related = 0.1
		}
		answers[relatedQuestion+p.Pair] = jev.Answer{Type: jev.TypeNoul, Noul: related}
		answers[sameQuestion+p.Pair] = jev.Answer{Type: jev.TypeNoul, Noul: s.same[key]}
	}
	return &jev.Response{Model: "scripted", Answers: answers, Usage: jev.Usage{InputTokens: 100 * len(st.Pairs)}}, nil
}

// profileNamed builds a bare profile for a label.
func profileNamed(name string, meetings int) *TopicProfile {
	p := &TopicProfile{
		Node:     &graph.Node{ID: graph.NewNodeID(string(graph.NodeTopic), "", name), Type: graph.NodeTopic, Name: name},
		Meetings: make(map[string]bool),
	}
	for i := range meetings {
		p.Meetings[fmt.Sprintf("m%d", i)] = true
	}
	return p
}

func pairOf(a, b *TopicProfile) *TopicPair {
	if a.ID() > b.ID() {
		a, b = b, a
	}
	return &TopicPair{A: a, B: b}
}

func TestTopicJudgeBatchesPairsAndKeepsTheirOrder(t *testing.T) {
	asker := &scriptedTopicAsker{related: map[string]float64{}, same: map[string]float64{}}
	var pairs []*TopicPair
	for i := range 25 {
		a, b := profileNamed(fmt.Sprintf("topic %02d", i), 1), profileNamed(fmt.Sprintf("other %02d", i), 2)
		asker.related[labelsKey(a.Node.Name, b.Node.Name)] = float64(i) / 100
		pairs = append(pairs, pairOf(a, b))
	}
	verdicts, report, err := NewTopicJudge(asker).Judge(context.Background(), pairs)
	if err != nil {
		t.Fatal(err)
	}
	if asker.calls != 3 {
		t.Errorf("calls = %d, want 3 for 25 pairs at %d per request", asker.calls, maxPairsPerRequest)
	}
	if report.Requests != 3 || report.InputTokens != 2500 || report.Model != "scripted" {
		t.Errorf("report = %+v", report)
	}
	if len(verdicts) != 25 {
		t.Fatalf("%d verdicts", len(verdicts))
	}
	for i, v := range verdicts {
		if v.Pair != pairs[i] {
			t.Errorf("verdict %d is for another pair", i)
		}
		if want := float64(i) / 100; v.Related != want {
			t.Errorf("verdict %d related = %v, want %v", i, v.Related, want)
		}
	}
}

func TestTopicJudgeHalvesABatchTheServiceRefuses(t *testing.T) {
	asker := &scriptedTopicAsker{refuseOnce: true, related: map[string]float64{}}
	var pairs []*TopicPair
	for i := range 10 {
		pairs = append(pairs, pairOf(profileNamed(fmt.Sprintf("a%d", i), 1), profileNamed(fmt.Sprintf("b%d", i), 1)))
	}
	verdicts, report, err := NewTopicJudge(asker).Judge(context.Background(), pairs)
	if err != nil {
		t.Fatal(err)
	}
	if asker.calls != 3 || report.Requests != 2 {
		t.Errorf("calls = %d, requests = %d; want one refusal then two halves", asker.calls, report.Requests)
	}
	if len(verdicts) != 10 {
		t.Errorf("%d verdicts", len(verdicts))
	}
}

// The model believes the state, so every field must be exactly what its
// name says: a count of meetings, what was said, and nothing that presumes
// the answer.
func TestTopicJudgeStateNamesFactsOnly(t *testing.T) {
	a := profileNamed("schema migration", 3)
	a.Summaries = []string{"The migration ran overnight.", strings.Repeat("long ", 200)}
	a.Quote = "we ship once the client is updated"
	a.Node.Properties = map[string]string{graph.PropAliases: "database migration"}
	b := profileNamed("client rollout", 1)
	p := pairOf(a, b)
	p.SharedMeetings, p.Adjacent = 2, 1

	state, questions := buildRelateState([]*TopicPair{p})
	raw, _ := json.Marshal(state)
	text := string(raw)
	for _, want := range []string{
		`"meetings_filed_under":3`, `"meetings_filed_under_both":2`, `"times_consecutive_segments_of_one_meeting":1`,
		`"other_wordings_meetings_used":["database migration"]`, `"verbatim_quote_from_a_decision_filed_under_it"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("state lacks %s:\n%s", want, text)
		}
	}
	for _, forbidden := range []string{"related", "similar", "same"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Errorf("state implies the answer with %q:\n%s", forbidden, text)
		}
	}
	for _, card := range state.Topics {
		for _, s := range card.WhatWasSaid {
			if len([]rune(s)) > maxSummaryChars {
				t.Errorf("summary not clipped: %d runes", len([]rune(s)))
			}
		}
	}
	names := make([]string, 0, len(questions))
	for name := range questions {
		names = append(names, name)
	}
	sort.Strings(names)
	if got := strings.Join(names, ","); got != "related_p0,same_p0" {
		t.Errorf("questions = %s", got)
	}
	for _, q := range questions {
		if q.Type != jev.TypeNoul {
			t.Errorf("question is a %s, want a noul", q.Type)
		}
	}
}
