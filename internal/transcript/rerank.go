package transcript

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/pkg/jev"
)

// Word matching decides which meetings contain a question's words; it cannot
// tell a meeting that discussed the subject from one that mentioned it in
// passing, and among meetings that match equally well it can only prefer the
// more recent. A decision model can make that judgment cheaply: one request
// carries the question and every candidate, with one yes/no question per
// candidate, and answers with a calibrated probability for each. It is used
// only when a key is configured, and only to reorder what word matching
// already found — it never adds a meeting the words did not reach.

// Reranker orders search hits by how well each answers the question.
type Reranker struct {
	asker jev.Asker
}

// NewReranker builds a reranker over a decision model.
func NewReranker(asker jev.Asker) *Reranker {
	return &Reranker{asker: asker}
}

// Reranking reports what a rerank did and cost.
type Reranking struct {
	// Judged is how many hits were sent to the model; the rest kept their
	// place at the tail in word-match order.
	Judged int
	// InputTokens is what the request cost.
	InputTokens int
	// Latency is the round trip.
	Latency time.Duration
	// Model is the version that answered.
	Model string
}

// Bounds on what one request carries. The service takes about 32,000 tokens
// of state; JSON runs at roughly 2.4 characters per token, so 60,000
// characters leaves headroom for the questions.
const (
	maxRerankCandidates = 40
	maxRerankStateChars = 60000
	maxPassageChars     = 400
	maxPassages         = 4
)

// rerankCandidate is what the model sees of one meeting. Field names say
// exactly what each value is, because the model believes them.
type rerankCandidate struct {
	Index        int      `json:"index"`
	Date         string   `json:"date"`
	Title        string   `json:"title"`
	Participants []string `json:"participants_identified,omitempty"`
	// Passages are the parts of the meeting's record in which the question's
	// words occur, each prefixed with what kind of record it came from.
	Passages []string `json:"passages_containing_question_words"`
}

type rerankState struct {
	Question   string            `json:"question"`
	Candidates []rerankCandidate `json:"candidate_meetings"`
}

// Rerank reorders hits by the model's probability that each answers the
// question, most likely first, keeping word-match order among equals. Hits
// beyond what fits in one request keep their place after the judged ones.
func (r *Reranker) Rerank(ctx context.Context, question string, hits []*Hit) (*Reranking, error) {
	if r == nil || r.asker == nil || len(hits) < 2 {
		return &Reranking{}, nil
	}
	n := min(len(hits), maxRerankCandidates)
	state := buildRerankState(question, hits[:n])
	// Trim from the tail until the state fits: the tail is what word
	// matching ranked lowest, so it is the cheapest to leave unjudged.
	for len(state.Candidates) > 2 && stateSize(state) > maxRerankStateChars {
		state.Candidates = state.Candidates[:len(state.Candidates)-1]
	}

	var resp *jev.Response
	var latency time.Duration
	for {
		questions := make(jev.Questions, len(state.Candidates))
		for _, c := range state.Candidates {
			questions[fmt.Sprintf("c%d", c.Index)] = jev.NoulWithCriteria(
				fmt.Sprintf("Does the candidate meeting whose index is %d answer the question in `question`?", c.Index),
				"its passages discuss the subject the question asks about, at length or as a decision or follow-up",
				"the question's words occur only in passing, in another sense, or in passages about something else",
			)
		}
		start := time.Now()
		var err error
		resp, err = r.asker.Ask(ctx, state, questions)
		latency = time.Since(start)
		if err == nil {
			break
		}
		// The size ceiling is the service's, and ErrTooLarge is the only
		// authority on it: halve and retry rather than guess at tokens.
		if errors.Is(err, jev.ErrTooLarge) && len(state.Candidates) > 2 {
			state.Candidates = state.Candidates[:len(state.Candidates)/2]
			continue
		}
		return nil, fmt.Errorf("rerank: %w", err)
	}

	judged := len(state.Candidates)
	for _, c := range state.Candidates {
		p, err := resp.Answers.Noul(fmt.Sprintf("c%d", c.Index))
		if err != nil {
			return nil, fmt.Errorf("rerank: %w", err)
		}
		hits[c.Index].Relevance = p
		hits[c.Index].Judged = true
	}
	// Judged hits first by probability; unjudged keep word-match order
	// behind them. The model's probabilities move by up to a tenth between
	// identical requests, so a difference smaller than that is not evidence:
	// hits are ordered by decile, and within a decile word matching decides,
	// which keeps a meeting that contains every query word ahead of one at
	// the same level that contains fewer.
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Judged != hits[j].Judged {
			return hits[i].Judged
		}
		return decile(hits[i].Relevance) > decile(hits[j].Relevance)
	})
	return &Reranking{
		Judged:      judged,
		InputTokens: resp.Usage.InputTokens,
		Latency:     latency,
		Model:       resp.Model,
	}, nil
}

// decile buckets a probability to the tenth it falls in.
func decile(p float64) int { return int(math.Floor(p*10 + 1e-9)) }

func buildRerankState(question string, hits []*Hit) *rerankState {
	st := &rerankState{Question: question}
	for i, h := range hits {
		st.Candidates = append(st.Candidates, rerankCandidate{
			Index:        i,
			Date:         h.Meeting.UpdatedAt.Format("2006-01-02"),
			Title:        h.Meeting.Name,
			Participants: h.Participants,
			Passages:     passagesOf(h),
		})
	}
	return st
}

// passagesOf renders a hit's matches as short labelled passages, the
// substantive ones first: what a segment said, what was decided, what was
// promised, then the labels that merely matched.
func passagesOf(h *Hit) []string {
	var out []string
	add := func(kind, text string) {
		if len(out) >= maxPassages || strings.TrimSpace(text) == "" {
			return
		}
		out = append(out, "["+kind+"] "+clipRunes(strings.Join(strings.Fields(text), " "), maxPassageChars))
	}
	for _, m := range h.Matches {
		if m.Node == nil {
			continue
		}
		switch m.Kind {
		case MatchSegment:
			add("topic segment "+m.Label, m.Node.Properties[graph.PropSummary])
		case MatchDecision:
			add("decision", m.Node.Properties[graph.PropSummary])
		case MatchFollowUp:
			add("follow-up", m.Node.Properties[graph.PropSummary])
		}
	}
	for _, m := range h.Matches {
		if m.Node != nil {
			continue
		}
		switch m.Kind {
		case MatchTitle:
			add("title", h.Meeting.Name)
		case MatchSummary:
			add("meeting summary", h.Meeting.Properties[graph.PropSummary])
		case MatchTopic:
			add("topic label", m.Label)
		case MatchMention:
			add("system mentioned", m.Label)
		case MatchParticipant:
			add("participant name", m.Label)
		}
	}
	return out
}

func stateSize(st *rerankState) int {
	b, err := json.Marshal(st)
	if err != nil {
		return 0
	}
	return len(b)
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
