package decide

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"sync"

	"github.com/imyousuf/CodeEagle/pkg/llm"
)

// ErrNoClient is returned by an LLMJudge built without a client.
var ErrNoClient = errors.New("decide: no language model client")

// Combine says how an LLMJudge merges the probabilities several passes gave
// one item.
type Combine int

const (
	// Mean averages the passes.
	Mean Combine = iota
	// Max takes the highest: an item counts as yes when any pass said so.
	Max
)

// DefaultLLMSystemPrompt is what an LLMJudge tells the model unless
// WithSystemPrompt replaces it.
const DefaultLLMSystemPrompt = `You judge yes/no questions about a piece of state.

The user message is a JSON document with "state" and "items". Each item has an
"id", a "question", and optionally "yes_when" and "no_when" saying what each
answer means. Judge every item from the state alone.

For each item give "p", the probability from 0 to 1 that the answer is yes.
Mean it: 0.5 says you cannot tell, values near 0 or 1 say the state settles
it. Give every id exactly once and nothing else.`

// LLMJudge answers yes/no questions with a language model that states a
// probability for each item through structured output.
//
// A model's stated probability moves with the items around it and with their
// order, so an LLMJudge can split a batch (WithBatchSize) and ask again in a
// shuffled order (WithPasses), merging the passes (WithCombine). The numbers
// are the model's own estimates, not calibrated probabilities: choose any
// threshold on labelled examples.
//
// An item the model leaves out is asked about again on its own; if it is
// still missing, Decide fails naming it rather than inventing an answer.
type LLMJudge struct {
	client      llm.StructuredClient
	batchSize   int
	passes      int
	combine     Combine
	system      string
	seed        int64
	concurrency int

	mu    sync.Mutex
	usage Usage
}

// LLMOption configures an LLMJudge.
type LLMOption func(*LLMJudge)

// WithBatchSize caps the items in one request; zero or less sends a batch
// whole.
func WithBatchSize(n int) LLMOption { return func(j *LLMJudge) { j.batchSize = n } }

// WithPasses asks about every item n times, each pass in a different order.
func WithPasses(n int) LLMOption {
	return func(j *LLMJudge) {
		if n > 0 {
			j.passes = n
		}
	}
}

// WithCombine sets how passes are merged; the default is Mean.
func WithCombine(c Combine) LLMOption { return func(j *LLMJudge) { j.combine = c } }

// WithSystemPrompt replaces DefaultLLMSystemPrompt. The user message and the
// response schema stay the same, so a replacement must keep asking for one
// "p" per "id".
func WithSystemPrompt(s string) LLMOption { return func(j *LLMJudge) { j.system = s } }

// WithSeed fixes the shuffles, so the same batch is asked in the same orders.
func WithSeed(seed int64) LLMOption { return func(j *LLMJudge) { j.seed = seed } }

// WithConcurrency sends up to n requests at once; the default is one.
func WithConcurrency(n int) LLMOption {
	return func(j *LLMJudge) {
		if n > 0 {
			j.concurrency = n
		}
	}
}

// NewLLMJudge builds a judge over client. The client's own configuration
// (model, temperature, token limit) applies to every request.
func NewLLMJudge(client llm.StructuredClient, opts ...LLMOption) *LLMJudge {
	j := &LLMJudge{client: client, passes: 1, system: DefaultLLMSystemPrompt, seed: 1, concurrency: 1}
	for _, opt := range opts {
		opt(j)
	}
	return j
}

// Name identifies the judge and the model behind it.
func (j *LLMJudge) Name() string {
	if j == nil || j.client == nil {
		return "llm"
	}
	return "llm/" + j.client.Provider() + "/" + j.client.Model()
}

// Usage reports what this judge has spent so far.
func (j *LLMJudge) Usage() Usage {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.usage
}

// llmItem is one question as the model sees it.
type llmItem struct {
	ID       string `json:"id"`
	Question string `json:"question"`
	YesWhen  string `json:"yes_when,omitempty"`
	NoWhen   string `json:"no_when,omitempty"`
}

type llmScores struct {
	Scores []struct {
		ID string   `json:"id"`
		P  *float64 `json:"p"`
	} `json:"scores"`
}

var llmScoreSchema = &llm.JSONSchema{
	Name:   "yes_no_scores",
	Strict: true,
	Schema: map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"scores"},
		"properties": map[string]any{
			"scores": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"id", "p"},
					"properties": map[string]any{
						"id": map[string]any{"type": "string"},
						"p":  map[string]any{"type": "number", "minimum": 0, "maximum": 1},
					},
				},
			},
		},
	},
}

// Decide answers every yes/no question about state. A choice question is
// ErrUnsupported.
func (j *LLMJudge) Decide(ctx context.Context, state any, questions map[string]Question) (map[string]Verdict, error) {
	if len(questions) == 0 {
		return map[string]Verdict{}, nil
	}
	if j == nil || j.client == nil {
		return nil, ErrNoClient
	}
	ids := make([]string, 0, len(questions))
	for id, q := range questions {
		if !q.IsYesNo() {
			return nil, fmt.Errorf("%w: %q is a choice", ErrUnsupported, id)
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("decide: encode state: %w", err)
	}

	scores := make(map[string][]float64, len(ids))
	for pass := 0; pass < j.passes; pass++ {
		order := append([]string(nil), ids...)
		// The first pass keeps a stable order; later passes shuffle, which is
		// what makes them worth asking.
		if pass > 0 {
			rng := rand.New(rand.NewSource(j.seed + int64(pass)))
			rng.Shuffle(len(order), func(a, b int) { order[a], order[b] = order[b], order[a] })
		}
		got, err := j.pass(ctx, stateJSON, questions, order)
		if err != nil {
			return nil, err
		}
		for id, p := range got {
			scores[id] = append(scores[id], p)
		}
	}

	out := make(map[string]Verdict, len(ids))
	for _, id := range ids {
		ps := scores[id]
		p := ps[0]
		for _, q := range ps[1:] {
			switch j.combine {
			case Max:
				p = math.Max(p, q)
			default:
				p += q
			}
		}
		if j.combine != Max {
			p /= float64(len(ps))
		}
		out[id] = YesNoVerdict(p)
	}
	return out, nil
}

// pass asks about every id once, in order, in batches, and asks again on its
// own about any id a reply left out.
func (j *LLMJudge) pass(ctx context.Context, state []byte, questions map[string]Question, order []string) (map[string]float64, error) {
	var batches [][]string
	size := j.batchSize
	if size <= 0 {
		size = len(order)
	}
	for start := 0; start < len(order); start += size {
		batches = append(batches, order[start:min(start+size, len(order))])
	}

	got := make(map[string]float64, len(order))
	var mu sync.Mutex
	errs := make([]error, len(batches))
	sem := make(chan struct{}, j.concurrency)
	var wg sync.WaitGroup
	for i, batch := range batches {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			ps, err := j.ask(ctx, state, questions, batch)
			mu.Lock()
			defer mu.Unlock()
			errs[i] = err
			for id, p := range ps {
				got[id] = p
			}
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	var missing []string
	for _, id := range order {
		if _, ok := got[id]; ok {
			continue
		}
		ps, err := j.ask(ctx, state, questions, []string{id})
		if err != nil {
			return nil, err
		}
		if p, ok := ps[id]; ok {
			got[id] = p
		} else {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("decide: %s gave no usable score for %s", j.Name(), strings.Join(missing, ", "))
	}
	return got, nil
}

// ask sends one request and returns the scores it got for the ids asked;
// anything else in the reply, and any score outside 0..1, is ignored.
func (j *LLMJudge) ask(ctx context.Context, state []byte, questions map[string]Question, ids []string) (map[string]float64, error) {
	items := make([]llmItem, len(ids))
	for i, id := range ids {
		q := questions[id]
		items[i] = llmItem{ID: id, Question: q.Ask, YesWhen: q.WhenTrue, NoWhen: q.WhenFalse}
	}
	body, err := json.Marshal(struct {
		State json.RawMessage `json:"state"`
		Items []llmItem       `json:"items"`
	}{state, items})
	if err != nil {
		return nil, fmt.Errorf("decide: encode request: %w", err)
	}
	resp, err := j.client.ChatJSON(ctx, j.system, []llm.Message{{Role: llm.RoleUser, Content: string(body)}}, llmScoreSchema)
	if err != nil {
		return nil, fmt.Errorf("decide: %s: %w", j.Name(), err)
	}
	j.mu.Lock()
	j.usage.Requests++
	j.usage.InputTokens += resp.Usage.InputTokens
	j.usage.OutputTokens += resp.Usage.OutputTokens
	j.mu.Unlock()

	var parsed llmScores
	if err := json.Unmarshal([]byte(jsonObject(resp.Content)), &parsed); err != nil {
		// An unparseable reply scores nothing; the ids are asked again on
		// their own, and reported if that fails too.
		return map[string]float64{}, nil
	}
	asked := make(map[string]bool, len(ids))
	for _, id := range ids {
		asked[id] = true
	}
	out := make(map[string]float64, len(ids))
	for _, s := range parsed.Scores {
		if !asked[s.ID] || s.P == nil || math.IsNaN(*s.P) || *s.P < 0 || *s.P > 1 {
			continue
		}
		if _, dup := out[s.ID]; !dup {
			out[s.ID] = *s.P
		}
	}
	return out, nil
}

// jsonObject trims anything around the outermost JSON object, for providers
// that wrap structured output in a code fence or a sentence.
func jsonObject(s string) string {
	start, end := strings.IndexByte(s, '{'), strings.LastIndexByte(s, '}')
	if start < 0 || end < start {
		return s
	}
	return s[start : end+1]
}
