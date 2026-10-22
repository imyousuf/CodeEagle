package relations

import (
	"context"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// LoadRelations reads every relation in the graph, keyed by subject path.
func LoadRelations(ctx context.Context, store graph.Store) (map[string]*Relation, error) {
	nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeRelation})
	if err != nil {
		return nil, err
	}
	out := make(map[string]*Relation, len(nodes))
	for _, n := range nodes {
		r, err := FromNode(n)
		if err != nil {
			return nil, err
		}
		out[r.SubjectPath] = r
	}
	return out, nil
}

// JudgedBy returns the test Select uses to set aside pairs already judged
// under the same signatures.
func JudgedBy(rels map[string]*Relation) func(*Candidate) bool {
	return func(c *Candidate) bool {
		r, ok := rels[c.Subject.Path]
		return ok && r.Judged(c.Meeting.SessionID, c.SigDoc(), c.SigMeeting())
	}
}

// Coverage reports how much of the corpus the said signal can see: sections
// and segments with a vector, out of all of them.
func (p *Proposer) Coverage() (sectionsWith, sections, segmentsWith, segments int) {
	p.prepare()
	for _, s := range p.subjects {
		sections += len(s.Sections)
	}
	for _, m := range p.meetings {
		segments += len(m.Segments)
	}
	return len(p.secVecs), sections, len(p.segVecs), segments
}

// Judging cost model. A request carries one document card and the segment
// cards of its pairs; these sizes are what the cards are capped to, in
// tokens, so the estimate errs high.
const (
	estDocCardTokens  = 700
	estPairCardTokens = 320
	estPairsPerCall   = 10
)

// EstimateJudging returns how many decision-model requests and input tokens
// judging the candidates would take.
func EstimateJudging(cands []*Candidate) (requests, tokens int) {
	perSubject := make(map[string]int)
	for _, c := range cands {
		perSubject[c.Subject.Path]++
	}
	for _, n := range perSubject {
		calls := (n + estPairsPerCall - 1) / estPairsPerCall
		requests += calls
		tokens += calls*estDocCardTokens + n*estPairCardTokens
	}
	return requests, tokens
}
