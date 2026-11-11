package decide

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/llm"
)

// scriptedLLM answers each ChatJSON call with reply(callIndex, ids asked) and
// records what it was asked.
type scriptedLLM struct {
	mu      sync.Mutex
	reply   func(call int, ids []string) string
	calls   int
	asked   [][]string
	systems []string
	schemas []*llm.JSONSchema
	states  []string
}

func (s *scriptedLLM) Chat(context.Context, string, []llm.Message) (*llm.Response, error) {
	return nil, errors.New("not used")
}
func (s *scriptedLLM) Model() string    { return "glm-test" }
func (s *scriptedLLM) Provider() string { return "scripted" }
func (s *scriptedLLM) Close() error     { return nil }

func (s *scriptedLLM) ChatJSON(_ context.Context, system string, msgs []llm.Message, schema *llm.JSONSchema) (*llm.Response, error) {
	var req struct {
		State json.RawMessage `json:"state"`
		Items []llmItem       `json:"items"`
	}
	if err := json.Unmarshal([]byte(msgs[0].Content), &req); err != nil {
		return nil, err
	}
	ids := make([]string, len(req.Items))
	for i, it := range req.Items {
		ids[i] = it.ID
	}
	s.mu.Lock()
	call := s.calls
	s.calls++
	s.asked = append(s.asked, ids)
	s.systems = append(s.systems, system)
	s.schemas = append(s.schemas, schema)
	s.states = append(s.states, string(req.State))
	s.mu.Unlock()
	return &llm.Response{Content: s.reply(call, ids), Usage: llm.TokenUsage{InputTokens: 100, OutputTokens: 10}}, nil
}

// scoresFor replies with p(id) for every id asked.
func scoresFor(p func(id string) float64) func(int, []string) string {
	return func(_ int, ids []string) string {
		var parts []string
		for _, id := range ids {
			parts = append(parts, fmt.Sprintf(`{"id":%q,"p":%v}`, id, p(id)))
		}
		return `{"scores":[` + strings.Join(parts, ",") + `]}`
	}
}

func facts(n int) map[string]Question {
	qs := make(map[string]Question, n)
	for i := 0; i < n; i++ {
		qs[fmt.Sprintf("f%d", i)] = YesNo("Would knowing this change the task?", "It changes a choice.", "It does not bear on it.")
	}
	return qs
}

func TestLLMJudgeScoresABatchInOneRequest(t *testing.T) {
	client := &scriptedLLM{reply: scoresFor(func(id string) float64 {
		return map[string]float64{"f0": 0.9, "f1": 0.1, "f2": 0.5}[id]
	})}
	judge := NewLLMJudge(client)
	got, err := judge.Decide(context.Background(), map[string]string{"task": "email the vendor"}, facts(3))
	if err != nil {
		t.Fatal(err)
	}
	if client.calls != 1 {
		t.Errorf("requests = %d, want 1", client.calls)
	}
	for id, p := range map[string]float64{"f0": 0.9, "f1": 0.1, "f2": 0.5} {
		if got[id] != YesNoVerdict(p) {
			t.Errorf("%s = %+v, want P %v", id, got[id], p)
		}
	}
	if client.systems[0] != DefaultLLMSystemPrompt || client.schemas[0] != llmScoreSchema {
		t.Error("the default prompt and schema were not used")
	}
	if client.states[0] != `{"task":"email the vendor"}` {
		t.Errorf("state sent = %s", client.states[0])
	}
	if u := judge.Usage(); u != (Usage{Requests: 1, InputTokens: 100, OutputTokens: 10}) {
		t.Errorf("usage = %+v", u)
	}
	if got := judge.Name(); got != "llm/scripted/glm-test" {
		t.Errorf("Name() = %q", got)
	}
}

func TestLLMJudgeSplitsBatchesAndShufflesLaterPasses(t *testing.T) {
	client := &scriptedLLM{reply: scoresFor(func(string) float64 { return 0.5 })}
	judge := NewLLMJudge(client, WithBatchSize(4), WithPasses(3), WithSeed(7))
	if _, err := judge.Decide(context.Background(), nil, facts(10)); err != nil {
		t.Fatal(err)
	}
	if client.calls != 9 {
		t.Fatalf("requests = %d, want 3 passes of 3 batches", client.calls)
	}
	for _, ids := range client.asked {
		if len(ids) > 4 {
			t.Errorf("a batch of %d exceeds the cap of 4", len(ids))
		}
	}
	order := func(pass int) string {
		var all []string
		for _, ids := range client.asked[pass*3 : pass*3+3] {
			all = append(all, ids...)
		}
		return strings.Join(all, ",")
	}
	if order(0) != "f0,f1,f2,f3,f4,f5,f6,f7,f8,f9" {
		t.Errorf("first pass order = %s, want sorted", order(0))
	}
	if order(1) == order(0) || order(2) == order(1) {
		t.Errorf("later passes did not reorder: %s / %s / %s", order(0), order(1), order(2))
	}

	again := &scriptedLLM{reply: scoresFor(func(string) float64 { return 0.5 })}
	if _, err := NewLLMJudge(again, WithBatchSize(4), WithPasses(3), WithSeed(7)).Decide(context.Background(), nil, facts(10)); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(again.asked) != fmt.Sprint(client.asked) {
		t.Error("the same seed did not give the same orders")
	}
}

func TestLLMJudgeCombinesPasses(t *testing.T) {
	perPass := []float64{0.2, 0.8, 0.5}
	reply := func(call int, ids []string) string {
		return scoresFor(func(string) float64 { return perPass[call] })(call, ids)
	}
	for _, tt := range []struct {
		combine Combine
		want    float64
	}{{Mean, 0.5}, {Max, 0.8}} {
		client := &scriptedLLM{reply: reply}
		got, err := NewLLMJudge(client, WithPasses(3), WithCombine(tt.combine)).Decide(context.Background(), nil, facts(1))
		if err != nil {
			t.Fatal(err)
		}
		if p := got["f0"].P; p < tt.want-1e-9 || p > tt.want+1e-9 {
			t.Errorf("combine %d: P = %v, want %v", tt.combine, p, tt.want)
		}
	}
}

func TestLLMJudgeAsksAgainAboutWhatAReplyLeftOut(t *testing.T) {
	// The batch reply drops f1, scores f2 out of range, and adds an id that
	// was never asked; the follow-ups answer f1 and f2 on their own.
	client := &scriptedLLM{reply: func(call int, ids []string) string {
		if call == 0 {
			return "```json\n" + `{"scores":[{"id":"f0","p":0.7},{"id":"f2","p":1.4},{"id":"zz","p":0.9}]}` + "\n```"
		}
		return scoresFor(func(string) float64 { return 0.3 })(call, ids)
	}}
	got, err := NewLLMJudge(client).Decide(context.Background(), nil, facts(3))
	if err != nil {
		t.Fatal(err)
	}
	if client.calls != 3 || fmt.Sprint(client.asked[1:]) != "[[f1] [f2]]" {
		t.Errorf("follow-ups = %v, want f1 and f2 asked alone", client.asked[1:])
	}
	if got["f0"].P != 0.7 || got["f1"].P != 0.3 || got["f2"].P != 0.3 {
		t.Errorf("verdicts = %+v", got)
	}
	if _, ok := got["zz"]; ok {
		t.Error("an id nobody asked about was returned")
	}
}

func TestLLMJudgeNamesWhatItCouldNotScore(t *testing.T) {
	client := &scriptedLLM{reply: func(int, []string) string { return "I would rather not say." }}
	_, err := NewLLMJudge(client).Decide(context.Background(), nil, facts(2))
	if err == nil || !strings.Contains(err.Error(), "f0, f1") {
		t.Errorf("err = %v, want both ids named", err)
	}
}

func TestLLMJudgeRefusesWhatItCannotAnswer(t *testing.T) {
	_, err := NewLLMJudge(&scriptedLLM{}).Decide(context.Background(), nil,
		map[string]Question{"who": {Ask: "Who?", Options: map[string]string{"a": "A", "b": "B"}}})
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("a choice question: %v, want ErrUnsupported", err)
	}
	_, err = NewLLMJudge(nil).Decide(context.Background(), nil, facts(1))
	if !errors.Is(err, ErrNoClient) {
		t.Errorf("no client: %v, want ErrNoClient", err)
	}
	if got, err := NewLLMJudge(nil).Decide(context.Background(), nil, nil); err != nil || len(got) != 0 {
		t.Errorf("no questions: %v, %v; want an empty answer", got, err)
	}
}

func TestLLMJudgeSendsBatchesConcurrently(t *testing.T) {
	client := &scriptedLLM{reply: scoresFor(func(string) float64 { return 0.6 })}
	judge := NewLLMJudge(client, WithBatchSize(2), WithConcurrency(4))
	got, err := judge.Decide(context.Background(), nil, facts(9))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 9 || client.calls != 5 {
		t.Errorf("verdicts %d from %d requests; want 9 from 5", len(got), client.calls)
	}
	if u := judge.Usage(); u.Requests != 5 || u.InputTokens != 500 {
		t.Errorf("usage = %+v", u)
	}
}
