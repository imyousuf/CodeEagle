package transcript

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/pkg/jev"
)

// scriptedAsker answers every noul with the probability scripted for its
// candidate title, and can refuse the first request as too large.
type scriptedAsker struct {
	byTitle    map[string]float64
	refuseOnce bool
	calls      int
	lastState  rerankState
}

func (s *scriptedAsker) Ask(_ context.Context, state any, questions jev.Questions) (*jev.Response, error) {
	s.calls++
	raw, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.lastState); err != nil {
		return nil, err
	}
	if s.refuseOnce {
		s.refuseOnce = false
		return nil, jev.ErrTooLarge
	}
	answers := jev.Answers{}
	for _, c := range s.lastState.Candidates {
		name := fmt.Sprintf("c%d", c.Index)
		if _, asked := questions[name]; !asked {
			return nil, fmt.Errorf("no question for candidate %d", c.Index)
		}
		answers[name] = jev.Answer{Type: jev.TypeNoul, Noul: s.byTitle[c.Title]}
	}
	return &jev.Response{Model: "scripted", Answers: answers, Usage: jev.Usage{InputTokens: 100 * len(answers)}}, nil
}

func rerankHits(titles ...string) []*Hit {
	var hits []*Hit
	for i, t := range titles {
		m := &graph.Node{ID: fmt.Sprintf("m%d", i), Type: graph.NodeMeeting, Name: t,
			QualifiedName: fmt.Sprintf("session-%d", i), UpdatedAt: time.Date(2026, 1, 1+i, 0, 0, 0, 0, time.UTC),
			Properties: map[string]string{graph.PropSummary: "summary of " + t}}
		seg := &graph.Node{ID: "s" + m.ID, Type: graph.NodeTopicSegment, Name: "seg",
			Properties: map[string]string{graph.PropSummary: "what was said in " + t}}
		hits = append(hits, &Hit{Meeting: m, Participants: []string{"Kevin"},
			Matches: []Match{{Kind: MatchTitle, Label: t}, {Kind: MatchSegment, Node: seg, Label: "seg"}}})
	}
	return hits
}

func TestRerankOrdersByProbabilityAndKeepsTiesInWordOrder(t *testing.T) {
	asker := &scriptedAsker{byTitle: map[string]float64{
		"passing mention": 0.10, "the real debate": 0.92, "also passing": 0.10, "a decision": 0.75,
	}}
	hits := rerankHits("passing mention", "the real debate", "also passing", "a decision")
	rep, err := NewReranker(asker).Rerank(context.Background(), "AGI", hits)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(hits))
	for i, h := range hits {
		got[i] = h.Meeting.Name
	}
	want := "the real debate, a decision, passing mention, also passing"
	if s := strings.Join(got, ", "); s != want {
		t.Errorf("order = %s\nwant    %s", s, want)
	}
	if rep.Judged != 4 || rep.InputTokens != 400 || rep.Model != "scripted" {
		t.Errorf("report = %+v", rep)
	}
	if !hits[0].Judged || hits[0].Relevance != 0.92 {
		t.Errorf("winner = %+v", hits[0])
	}

	// The state names each field for what it is, and carries the passages.
	c := asker.lastState.Candidates[1]
	if c.Title != "the real debate" || c.Date != "2026-01-02" || len(c.Passages) == 0 ||
		!strings.Contains(c.Passages[0], "what was said in the real debate") {
		t.Errorf("candidate sent = %+v", c)
	}
	if asker.lastState.Question != "AGI" {
		t.Errorf("question sent = %q", asker.lastState.Question)
	}
}

// TestRerankTreatsNoiseAsATie: the model's numbers move by up to a tenth
// between identical calls, so 0.79 against 0.73 says nothing, and word-match
// order — which put the meeting with every query word first — stands.
func TestRerankTreatsNoiseAsATie(t *testing.T) {
	asker := &scriptedAsker{byTitle: map[string]float64{
		"every word": 0.73, "one word": 0.79, "another": 0.61, "clearly better": 0.91,
	}}
	hits := rerankHits("every word", "one word", "another", "clearly better")
	if _, err := NewReranker(asker).Rerank(context.Background(), "q", hits); err != nil {
		t.Fatal(err)
	}
	got := []string{hits[0].Meeting.Name, hits[1].Meeting.Name, hits[2].Meeting.Name, hits[3].Meeting.Name}
	want := []string{"clearly better", "every word", "one word", "another"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestRerankHalvesWhenTooLargeAndLeavesTheTailInWordOrder(t *testing.T) {
	asker := &scriptedAsker{refuseOnce: true, byTitle: map[string]float64{"a": 0.2, "b": 0.9, "c": 0.5, "d": 0.99}}
	hits := rerankHits("a", "b", "c", "d")
	rep, err := NewReranker(asker).Rerank(context.Background(), "q", hits)
	if err != nil {
		t.Fatal(err)
	}
	if asker.calls != 2 || rep.Judged != 2 {
		t.Fatalf("calls=%d judged=%d, want a retry over the first half", asker.calls, rep.Judged)
	}
	// b (0.9) leads a (0.2); c and d were never judged and keep their place
	// after the judged ones, in word-match order, however well d would do.
	got := []string{hits[0].Meeting.Name, hits[1].Meeting.Name, hits[2].Meeting.Name, hits[3].Meeting.Name}
	if strings.Join(got, "") != "bacd" {
		t.Errorf("order = %v, want b a c d", got)
	}
	if hits[3].Judged {
		t.Error("an unjudged hit was marked judged")
	}
}

func TestRerankDoesNothingWithoutAModelOrACandidatePair(t *testing.T) {
	var none *Reranker
	hits := rerankHits("only")
	if rep, err := none.Rerank(context.Background(), "q", hits); err != nil || rep.Judged != 0 {
		t.Errorf("nil reranker: %+v, %v", rep, err)
	}
	asker := &scriptedAsker{}
	if rep, err := NewReranker(asker).Rerank(context.Background(), "q", hits); err != nil || rep.Judged != 0 || asker.calls != 0 {
		t.Errorf("one hit: %+v, %v, calls=%d", rep, err, asker.calls)
	}
}

func TestRerankReportsOtherErrors(t *testing.T) {
	failing := askerFunc(func(context.Context, any, jev.Questions) (*jev.Response, error) {
		return nil, jev.ErrUnauthorized
	})
	_, err := NewReranker(failing).Rerank(context.Background(), "q", rerankHits("a", "b"))
	if !errors.Is(err, jev.ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

type askerFunc func(context.Context, any, jev.Questions) (*jev.Response, error)

func (f askerFunc) Ask(ctx context.Context, s any, q jev.Questions) (*jev.Response, error) {
	return f(ctx, s, q)
}
