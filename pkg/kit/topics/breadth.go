package topics

import (
	"context"
	"strings"

	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
	"github.com/imyousuf/CodeEagle/pkg/kit/search"
)

// Breadth is how far a search follows the adjacency graph out from the
// topics whose labels matched.
//
// Topics are specific and rarely shared, so the words of a question match
// one label and what is filed under it, while the same subject under a
// differently worded label stays invisible. The adjacency graph records, per
// pair of labels, a judge's probability that they are about one thing;
// breadth is the probability a search insists on before it follows such an
// edge, and how many it will follow in a row.
//
// Measured against 91 hand-labelled pairs and a decision model: at 0.6 every
// pair admitted was related; at 0.5 six in seven were; no unrelated pair
// scored above 0.56. A second hop is what reaches a label that no signal
// proposed against the seed. Another judge needs its own measurement before
// these gates mean the same thing for it.
type Breadth string

const (
	// BreadthNone follows no edges: only the labels the words matched.
	BreadthNone Breadth = "none"
	// BreadthNarrow follows one edge, at NarrowRelatedGate or above.
	BreadthNarrow Breadth = "narrow"
	// BreadthDefault follows one edge, at DefaultRelatedGate or above.
	BreadthDefault Breadth = "default"
	// BreadthWide follows two edges, each, and their product, at
	// WideRelatedGate or above.
	BreadthWide Breadth = "wide"
)

// Breadths lists the settings, narrowest first.
var Breadths = []Breadth{BreadthNone, BreadthNarrow, BreadthDefault, BreadthWide}

// The edge probability each breadth insists on. Three bands rather than one
// tuned cut, because a model's probabilities move by up to a tenth between
// identical requests.
const (
	NarrowRelatedGate  = 0.8
	DefaultRelatedGate = 0.6
	WideRelatedGate    = 0.5
)

// HopDecay scales the credit for each edge between a topic and the seed the
// words matched, on top of the edge's own probability. A seed earns full
// credit, so something reached through a neighbour can never outrank what
// is filed under the seed.
const HopDecay = 0.5

// Valid reports whether b is one of Breadths.
func (b Breadth) Valid() bool {
	for _, known := range Breadths {
		if known == b {
			return true
		}
	}
	return false
}

// MinProbability is the edge probability a breadth insists on, and the
// floor for the product along a path. An unknown breadth is treated as
// BreadthDefault.
func (b Breadth) MinProbability() float64 {
	switch b {
	case BreadthNarrow:
		return NarrowRelatedGate
	case BreadthWide:
		return WideRelatedGate
	default:
		return DefaultRelatedGate
	}
}

// Hops is how many edges a breadth follows in a row.
func (b Breadth) Hops() int {
	switch b {
	case BreadthNone:
		return 0
	case BreadthWide:
		return 2
	default:
		return 1
	}
}

// Seed is a topic a search matched, to expand from.
type Seed struct {
	Topic *graph.Node
	// Whole reports that the topic's label covers the whole query (see
	// Covers). Only such seeds are expanded: a label that matched one word
	// of "sticky board" -- "HITL vs sticky notes" -- carries evidence for
	// that word alone, and following its neighbours would spread a
	// half-match over everything about HITL. Measured on a real corpus that
	// one seed pulled in nineteen meetings.
	Whole bool
}

// Reach is a topic found by following related edges from a seed.
type Reach struct {
	Topic *graph.Node
	// Seed is the index, in the seeds given, of the seed the path starts
	// from.
	Seed int
	// Path is the topics followed, the seed first and Topic last.
	Path []*graph.Node
	// Probability is the product of the edge probabilities along Path.
	Probability float64
	// Weight is the credit Topic earns: the product, over the edges, of
	// each one's probability times HopDecay.
	Weight float64
}

// Via names the path as labels joined with " ~ ".
func (r Reach) Via() string {
	names := make([]string, len(r.Path))
	for i, n := range r.Path {
		names[i] = n.Name
	}
	return strings.Join(names, " ~ ")
}

// Expand follows RelatedTo edges out from the seeds that cover the whole
// query, as far as breadth allows, and calls reached for each topic it
// credits, in order.
//
// A path is followed while every edge and the product of its probabilities
// stay at or above the breadth's gate, not merely each edge: two edges at
// 0.6 compound to a relation the judge would have put at 0.36, and a second
// hop through a broad topic otherwise reaches everything under it. A topic
// for which credited reports true -- the words matched it, or it sits under
// a theme they matched -- has full credit already and is never reached. A
// topic reached again is reported again only along a path that earns it
// more credit than before.
func Expand(ctx context.Context, store graph.Store, seeds []Seed, breadth Breadth, credited func(topicID string) bool, reached func(Reach)) error {
	type step struct {
		topic *graph.Node
		seed  int
		path  []*graph.Node
		p, w  float64
	}
	var frontier []step
	for i, s := range seeds {
		if s.Whole && s.Topic != nil {
			frontier = append(frontier, step{topic: s.Topic, seed: i, path: []*graph.Node{s.Topic}, p: 1, w: 1})
		}
	}

	minP, hops := breadth.MinProbability(), breadth.Hops()
	best := make(map[string]float64)
	for hop := 1; hop <= hops && len(frontier) > 0; hop++ {
		var next []step
		for _, s := range frontier {
			neighbours, err := RelatedTopics(ctx, store, s.topic.ID, minP)
			if err != nil {
				return err
			}
			for _, n := range neighbours {
				if credited != nil && credited(n.Topic.ID) {
					continue
				}
				p := s.p * n.Probability
				if p < minP {
					continue
				}
				w := s.w * (n.Probability * HopDecay)
				if w <= best[n.Topic.ID] {
					continue
				}
				best[n.Topic.ID] = w
				path := append(append(make([]*graph.Node, 0, len(s.path)+1), s.path...), n.Topic)
				reached(Reach{Topic: n.Topic, Seed: s.seed, Path: path, Probability: p, Weight: w})
				next = append(next, step{topic: n.Topic, seed: s.seed, path: path, p: p, w: w})
			}
		}
		frontier = next
	}
	return nil
}

// Covers reports whether text accounts for the whole of query: the query
// verbatim when it has more than one word, or a word from every group of its
// terms -- an initialism and its expansion being one group -- as kit/search
// tokenizes and matches them, a word of four or more letters also matching
// the start of a longer one. Use it to decide which matched topics are
// whole seeds.
func Covers(query, text string) bool {
	terms := search.QueryTerms(query)
	if len(terms) == 0 {
		return false
	}
	tokens := search.Tokenize(text)
	if words := search.Tokenize(query); len(words) > 1 {
		if strings.Contains(" "+strings.Join(tokens, " ")+" ", " "+strings.Join(words, " ")+" ") {
			return true
		}
	}
	matched := make(map[string]bool, len(terms))
	for _, t := range terms {
		if search.TermCredit(t, tokens) > 0 {
			matched[t] = true
		}
	}
	for _, g := range search.TermGroups(terms) {
		found := false
		for _, i := range g {
			if matched[terms[i]] {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
