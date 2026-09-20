package transcript

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/search"
)

// A question about meetings is nearly always "what was said about X, by
// whom, and when". Answering it used to take a chain of commands — a topic
// listing, a semantic search, an edge query, a meeting dump — and the chain
// broke whenever one link truncated or guessed. This search reads everything a
// meeting recorded in one pass and reports, per meeting, exactly where the
// question matched, so the answer and the evidence for it arrive together.
//
// It calls no model. Matching is on whole words, case-insensitively, with a
// word of four or more letters also matching the start of a longer one, so
// "AGI" finds "AGI feasibility debate" and nothing containing "agile".

// Query describes a search over indexed meetings.
type Query struct {
	// Text is matched against everything a meeting recorded: its title and
	// summary, the topics it is filed under, what each topic segment said,
	// its decisions and their quotes, its follow-ups, the systems it
	// mentioned and the people who attended. Empty means every meeting
	// that passes the other filters matches.
	Text string
	// Person keeps only meetings this person attended.
	Person string
	// Since keeps only meetings on or after this time.
	Since time.Time
	// Only restricts the evidence considered to one kind, so "every
	// decision since March" is a query with no text and Only set.
	Only MatchKind
	// Limit caps the hits returned; zero means all.
	Limit int
	// Breadth is how far a topic match reaches through the topics judged
	// related to it; BreadthDefault when empty.
	Breadth Breadth
}

// Breadth is how far a search follows the adjacency graph out from the
// topics whose labels matched.
//
// The topics meetings produce are specific and rarely shared, so the words
// of a question match one label and the meetings filed under it, while the
// same conversation under a differently worded label stays invisible. The
// adjacency graph records, per pair of labels, a decision model's
// probability that they are about one thing; breadth is the probability a
// search insists on before it follows such an edge, and how many it will
// follow in a row.
//
// Measured against 91 hand-labelled pairs: at 0.6 every pair admitted was
// related; at 0.5 six in seven were; no unrelated pair scored above 0.56.
// A second hop is what reaches a label that no signal proposed against the
// seed — the real corpus has one: two labels from different meetings under
// different parents, related through a third.
type Breadth string

const (
	// BreadthNone follows no edges: only the labels the words matched.
	BreadthNone Breadth = "none"
	// BreadthNarrow follows one edge, at 0.8 or above.
	BreadthNarrow Breadth = "narrow"
	// BreadthDefault follows one edge, at 0.6 or above.
	BreadthDefault Breadth = "default"
	// BreadthWide follows two edges, each at 0.5 or above.
	BreadthWide Breadth = "wide"
)

// Breadths lists the settings a search accepts.
var Breadths = []Breadth{BreadthNone, BreadthNarrow, BreadthDefault, BreadthWide}

// The edge probability each breadth insists on. Three bands rather than one
// tuned cut, because the model's probabilities move by up to a tenth
// between identical requests.
const (
	NarrowRelatedGate  = 0.8
	DefaultRelatedGate = 0.6
	WideRelatedGate    = 0.5
)

// minProbability is the edge probability a breadth insists on.
func (b Breadth) minProbability() float64 {
	switch b {
	case BreadthNarrow:
		return NarrowRelatedGate
	case BreadthWide:
		return WideRelatedGate
	default:
		return DefaultRelatedGate
	}
}

// hops is how many edges a breadth follows in a row.
func (b Breadth) hops() int {
	switch b {
	case BreadthNone:
		return 0
	case BreadthWide:
		return 2
	default:
		return 1
	}
}

// relatedHopDecay scales the credit a meeting earns for each edge between
// its label and the one the words matched, on top of the edge's own
// probability. The label the words matched earns full credit, so a meeting
// reached through a neighbour can never outrank one filed under the seed.
const relatedHopDecay = 0.5

// MatchKind says where inside a meeting a query matched.
type MatchKind string

const (
	MatchTitle       MatchKind = "title"
	MatchSummary     MatchKind = "summary"
	MatchTopic       MatchKind = "topic"
	MatchSegment     MatchKind = "segment"
	MatchDecision    MatchKind = "decision"
	MatchFollowUp    MatchKind = "follow-up"
	MatchMention     MatchKind = "mention"
	MatchParticipant MatchKind = "participant"
)

// MatchKinds lists the kinds a search can be restricted to.
var MatchKinds = []MatchKind{
	MatchTitle, MatchSummary, MatchTopic, MatchSegment,
	MatchDecision, MatchFollowUp, MatchMention, MatchParticipant,
}

// Match is one place inside a meeting where the query matched.
type Match struct {
	Kind MatchKind
	// Node is the segment, decision or follow-up that matched; nil for a
	// title, summary, mention, topic or participant hit.
	Node *graph.Node
	// Label is the matched thing in short form: a topic label, a decision,
	// a follow-up, a person's name.
	Label string
	// Terms are the query words that matched here.
	Terms []string
	// Phrase reports that the whole query appeared verbatim here.
	Phrase bool
	// Via is set when the match came through the adjacency graph rather
	// than the words: the label the words matched, then each label followed
	// to reach this one, joined with " ~ ".
	Via string
	// Probability is the product of the edge probabilities along Via.
	Probability float64
	// Weight is the credit this match carries towards the score: 1 for a
	// match the words made, less for one reached through related topics.
	// Zero means one, so evidence built elsewhere needs no change.
	Weight float64
}

// credit is the match's weight, treating an unset one as full.
func (m *Match) credit() float64 {
	if m.Weight == 0 {
		return 1
	}
	return m.Weight
}

// Hit is a meeting that matched, with what matched inside it.
type Hit struct {
	Meeting      *graph.Node
	Participants []string
	Matches      []Match
	// Terms are the distinct query words matched anywhere in the meeting.
	Terms []string
	// Phrase reports that the whole query appeared verbatim somewhere.
	Phrase bool
	// Score orders hits: verbatim phrase hits first, then by how much of
	// the query the meeting covers with rare words counting for more, then
	// most recent first.
	Score float64
	// Relevance is a decision model's probability that the meeting answers
	// the question, set only when a Reranker judged it.
	Relevance float64
	Judged    bool
}

// Found is the outcome of a search.
type Found struct {
	Hits []*Hit
	// Total is how many meetings matched before Limit was applied.
	Total int
	// Complete is how many of those matched every query word, as the word
	// itself or the start of a longer one. A hit needs only one word, so
	// this is what "meetings about X and Y" honestly means.
	Complete int
	// Considered is how many meetings were searched after the person and
	// date filters.
	Considered int
	// Terms are the query words that were searched.
	Terms []string
	// Expanded is how many hits the words never matched: they were reached
	// only through topics judged related to one the words did match.
	Expanded int
}

// OnlyRelated reports whether every match on a hit came through the
// adjacency graph.
func (h *Hit) OnlyRelated() bool {
	if len(h.Matches) == 0 {
		return false
	}
	for _, m := range h.Matches {
		if m.Via == "" {
			return false
		}
	}
	return true
}

// CoversAll reports whether a hit matched every query word.
func (h *Hit) CoversAll(terms []string) bool {
	for _, t := range terms {
		found := false
		for _, have := range h.Terms {
			if have == t || have == t+"*" {
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

// FindMeetings searches the indexed meetings.
func FindMeetings(ctx context.Context, store graph.Store, q Query) (*Found, error) {
	terms := search.QueryTerms(q.Text)
	if len(terms) == 0 && q.Person == "" && q.Since.IsZero() && q.Only == "" {
		return nil, errors.New("nothing to search for: give some words, a person, a date, or a kind of evidence")
	}
	if q.Only != "" && !validKind(q.Only) {
		return nil, fmt.Errorf("unknown evidence kind %q; one of %s", q.Only, joinKinds())
	}
	if q.Breadth == "" {
		q.Breadth = BreadthDefault
	}
	if !validBreadth(q.Breadth) {
		return nil, fmt.Errorf("unknown breadth %q; one of %s", q.Breadth, joinBreadths())
	}
	m := newMatcher(q.Text, terms)

	ix, err := LoadMeetingIndex(ctx, store)
	if err != nil {
		return nil, err
	}

	// Meetings that pass the filters, keyed two ways: by node id for edges
	// that point at the meeting, and by recording for the segments,
	// decisions and follow-ups that carry only the session identifier.
	byID := make(map[string]*Hit)
	byRecording := make(map[string]*Hit)
	var considered []*Hit
	for _, mt := range ix.All() {
		if !q.Since.IsZero() && mt.UpdatedAt.Before(q.Since) {
			continue
		}
		names, err := Attendees(ctx, store, mt.ID)
		if err != nil {
			return nil, err
		}
		if q.Person != "" && !attended(names, q.Person) {
			continue
		}
		h := &Hit{Meeting: mt, Participants: names}
		byID[mt.ID] = h
		byRecording[recordingKey(mt.FilePath, mt.QualifiedName)] = h
		considered = append(considered, h)
	}

	only := func(kind MatchKind) bool { return q.Only == "" || q.Only == kind }

	for _, h := range considered {
		mt := h.Meeting
		if only(MatchTitle) {
			h.add(m.match(MatchTitle, nil, mt.Name, mt.Name))
		}
		if only(MatchSummary) {
			h.add(m.match(MatchSummary, nil, mt.Properties[graph.PropSummary], mt.Name))
		}
		if only(MatchMention) {
			for _, sys := range strings.Split(mt.Properties["mentions"], ",") {
				sys = strings.TrimSpace(sys)
				if sys != "" {
					h.add(m.match(MatchMention, nil, sys, sys))
				}
			}
		}
		if only(MatchParticipant) {
			for _, who := range h.Participants {
				h.add(m.match(MatchParticipant, nil, who, who))
			}
		}
	}

	// The children are queried by type through the index rather than
	// walked meeting by meeting, which is the difference between three
	// index scans and six hundred neighbour lookups.
	if only(MatchSegment) {
		segments, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeTopicSegment})
		if err != nil {
			return nil, err
		}
		for _, s := range segments {
			h := byRecording[recordingKey(s.FilePath, s.Properties[graph.PropMeetingID])]
			if h == nil {
				continue
			}
			text := s.Name + "\n" + s.Properties[graph.PropSummary] + "\n" + s.Properties["keywords"]
			h.add(m.match(MatchSegment, s, text, s.Name))
		}
	}
	if only(MatchDecision) {
		decisions, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeDecision})
		if err != nil {
			return nil, err
		}
		for _, d := range decisions {
			h := byRecording[recordingKey(d.FilePath, d.Properties[graph.PropMeetingID])]
			if h == nil {
				continue
			}
			text := d.Properties[graph.PropSummary] + "\n" + d.Properties[graph.PropQuote] + "\n" + d.Properties["rationale"]
			h.add(m.match(MatchDecision, d, text, d.Properties[graph.PropSummary]))
		}
	}
	if only(MatchFollowUp) {
		actions, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeActionItem})
		if err != nil {
			return nil, err
		}
		for _, a := range actions {
			h := byRecording[recordingKey(a.FilePath, a.Properties[graph.PropMeetingID])]
			if h == nil {
				continue
			}
			text := a.Properties[graph.PropSummary] + "\n" + a.Properties[graph.PropQuote] + "\n" + a.Properties[graph.PropAssignee]
			h.add(m.match(MatchFollowUp, a, text, a.Properties[graph.PropSummary]))
		}
	}
	if only(MatchTopic) && len(terms) > 0 {
		if err := matchTopics(ctx, store, m, byID, byRecording, q.Breadth); err != nil {
			return nil, err
		}
	}

	found := &Found{Considered: len(considered), Terms: terms}
	var hits []*Hit
	for _, h := range considered {
		if len(h.Matches) > 0 {
			hits = append(hits, h)
			if h.Phrase || h.CoversAll(terms) {
				found.Complete++
			}
			if h.OnlyRelated() {
				found.Expanded++
			}
		}
	}
	score(hits, terms, m.groups)
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Meeting.UpdatedAt.After(hits[j].Meeting.UpdatedAt)
	})
	found.Total = len(hits)
	if q.Limit > 0 && len(hits) > q.Limit {
		hits = hits[:q.Limit]
	}
	found.Hits = hits
	return found, nil
}

// matchTopics finds the topics whose label or alternate wordings match, and
// credits every meeting filed under them. A topic that is a theme credits the
// meetings under its descendants too, labelled with the path down, so
// "AI strategy" reaches a meeting filed under "AGI feasibility debate" and
// says which leaf it was.
//
// Then, within the breadth asked for, the topics judged related to a matched
// one are credited too, at a discount that compounds with each edge
// followed. That is how "AGI" reaches the meeting filed under "Recursive
// self improvement and model regression", whose label contains none of the
// words.
func matchTopics(ctx context.Context, store graph.Store, m *matcher, byID, byRecording map[string]*Hit, breadth Breadth) error {
	topics, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeTopic})
	if err != nil {
		return err
	}
	// creditTopic gives every meeting filed under a topic the evidence.
	creditTopic := func(t *graph.Node, ev Match) {
		edges, err := store.GetEdges(ctx, t.ID, graph.EdgeHasTopic)
		if err != nil {
			return
		}
		credited := make(map[*Hit]bool)
		for _, e := range edges {
			if e.TargetID != t.ID {
				continue
			}
			h := byID[e.SourceID]
			if h == nil {
				// The topic may be reached only through a segment of
				// the meeting, when the meeting-level edge predates
				// the current labels.
				src, err := store.GetNode(ctx, e.SourceID)
				if err != nil || src == nil || src.Type != graph.NodeTopicSegment {
					continue
				}
				h = byRecording[recordingKey(src.FilePath, src.Properties[graph.PropMeetingID])]
			}
			if h == nil || credited[h] {
				continue
			}
			credited[h] = true
			h.add(&ev)
		}
	}

	visited := make(map[string]bool)
	var walk func(t *graph.Node, label string, ev Match, depth int)
	walk = func(t *graph.Node, label string, ev Match, depth int) {
		if depth > maxTaxonomyDepth+1 || visited[t.ID] {
			return
		}
		visited[t.ID] = true
		ev.Label = label
		creditTopic(t, ev)
		children, err := TopicChildren(ctx, store, t.ID)
		if err != nil {
			return
		}
		for _, c := range children {
			walk(c, label+" › "+c.Name, ev, depth+1)
		}
	}

	// The frontier holds the seeds to expand from: only topics whose label
	// covered the whole query. A label that matched one word of "sticky
	// board" — "HITL vs sticky notes" — carries evidence for that word
	// alone, and following its neighbours would spread a half-match over
	// every meeting about HITL. Measured on the real corpus that one seed
	// pulled in nineteen meetings.
	type seed struct {
		topic *graph.Node
		ev    Match
	}
	var frontier []seed
	for _, t := range topics {
		text := t.Name
		if aliases := t.Properties[graph.PropAliases]; aliases != "" {
			text += "\n" + aliases
		}
		ev := m.match(MatchTopic, nil, text, t.Name)
		if ev == nil {
			continue
		}
		walk(t, t.Name, *ev, 0)
		if !m.covers(ev) {
			continue
		}
		ev.Via, ev.Probability, ev.Weight = t.Name, 1, 1
		frontier = append(frontier, seed{t, *ev})
	}

	// A path is followed while the product of its edge probabilities stays
	// above the gate, not merely each edge. Two edges at 0.6 compound to a
	// relation the judge would have put at 0.36, and a second hop through a
	// broad topic — "AGI feasibility debate ~ AI strategy ~ AI model
	// training" — otherwise reaches every meeting under it.
	minP, hops := breadth.minProbability(), breadth.hops()
	best := make(map[string]float64)
	for hop := 1; hop <= hops && len(frontier) > 0; hop++ {
		var next []seed
		for _, s := range frontier {
			neighbours, err := RelatedTopics(ctx, store, s.topic.ID, minP)
			if err != nil {
				return err
			}
			for _, n := range neighbours {
				if visited[n.Topic.ID] {
					// The words matched it, or a theme the words matched
					// contains it: it has full credit already.
					continue
				}
				ev := s.ev
				ev.Probability *= n.Probability
				if ev.Probability < minP {
					continue
				}
				ev.Weight *= n.Probability * relatedHopDecay
				if ev.Weight <= best[n.Topic.ID] {
					continue
				}
				best[n.Topic.ID] = ev.Weight
				ev.Label = n.Topic.Name
				ev.Via = s.ev.Via + " ~ " + n.Topic.Name
				ev.Phrase = false
				creditTopic(n.Topic, ev)
				next = append(next, seed{n.Topic, ev})
			}
		}
		frontier = next
	}
	return nil
}

// score ranks hits by how much of the query each covers. Each term weighs
// by its rarity across the matched meetings, and an initialism shares its
// weight with the words it expands to, so "AGI" and "artificial general
// intelligence" ask for the same thing. A verbatim phrase hit outranks any
// coverage.
func score(hits []*Hit, terms []string, groups [][]int) {
	if len(terms) == 0 {
		return
	}
	df := make([]int, len(terms))
	index := make(map[string]int, len(terms))
	for i, t := range terms {
		index[t] = i
	}
	credits := make([][]float64, len(hits))
	for k, h := range hits {
		credits[k] = make([]float64, len(terms))
		for _, ev := range h.Matches {
			for _, t := range ev.Terms {
				i, ok := index[t]
				if !ok {
					continue
				}
				c := ev.credit()
				if strings.Contains(t, "*") {
					c *= search.PrefixCredit
				}
				if c > credits[k][i] {
					credits[k][i] = c
				}
			}
		}
		for i, c := range credits[k] {
			if c > 0 {
				df[i]++
			}
		}
	}
	weights := make([]float64, len(terms))
	for i := range terms {
		weights[i] = math.Log(1 + float64(len(hits))/float64(df[i]+1))
	}
	for k, h := range hits {
		h.Score = search.ShareOf(credits[k], weights, groups)
		if h.Phrase {
			h.Score += 1
		}
	}
}

// add records evidence on a hit, merging the terms it matched.
//
// Evidence that arrived through the adjacency graph is kept but does not
// count towards the words the meeting itself matched: it earns the meeting
// a place in the results, not a claim to have said what was asked.
func (h *Hit) add(ev *Match) {
	if ev == nil {
		return
	}
	h.Matches = append(h.Matches, *ev)
	if ev.Via != "" {
		return
	}
	if ev.Phrase {
		h.Phrase = true
	}
	for _, t := range ev.Terms {
		found := false
		for _, have := range h.Terms {
			if have == t {
				found = true
				break
			}
		}
		if !found {
			h.Terms = append(h.Terms, t)
		}
	}
}

// matcher tests texts against a query's terms and phrase.
type matcher struct {
	terms  []string
	groups [][]int
	// phrase is the normalized query, checked verbatim when it has more
	// than one word; a single word is a term already.
	phrase string
}

func newMatcher(text string, terms []string) *matcher {
	m := &matcher{terms: terms, groups: search.TermGroups(terms)}
	if words := search.Tokenize(text); len(words) > 1 {
		m.phrase = " " + strings.Join(words, " ") + " "
	}
	return m
}

// match reports how a text matches: nil when it does not. With no terms
// every text matches, which is how a kind-only query lists everything.
func (m *matcher) match(kind MatchKind, node *graph.Node, text, label string) *Match {
	ev := &Match{Kind: kind, Node: node, Label: label}
	if len(m.terms) == 0 {
		return ev
	}
	tokens := search.Tokenize(text)
	for _, t := range m.terms {
		switch search.TermCredit(t, tokens) {
		case 1:
			ev.Terms = append(ev.Terms, t)
		case search.PrefixCredit:
			// A prefix hit is recorded as such, so ranking can prefer the
			// meeting that said the word itself.
			ev.Terms = append(ev.Terms, t+"*")
		}
	}
	if len(ev.Terms) == 0 {
		return nil
	}
	if m.phrase != "" && strings.Contains(" "+strings.Join(tokens, " ")+" ", m.phrase) {
		ev.Phrase = true
	}
	return ev
}

// covers reports whether a match accounts for the whole query: the phrase,
// or a word from every group, an initialism and its expansion being one
// group.
func (m *matcher) covers(ev *Match) bool {
	if ev.Phrase {
		return true
	}
	matched := make(map[string]bool, len(ev.Terms))
	for _, t := range ev.Terms {
		matched[strings.TrimSuffix(t, "*")] = true
	}
	for _, g := range m.groups {
		any := false
		for _, i := range g {
			if matched[m.terms[i]] {
				any = true
				break
			}
		}
		if !any {
			return false
		}
	}
	return true
}

// attended reports whether a person is among the names, tolerating the
// spelling variants transcription produces.
func attended(names []string, person string) bool {
	for _, n := range names {
		if SameName(n, person) {
			return true
		}
	}
	return false
}

func recordingKey(path, session string) string { return path + "\x00" + session }

func validKind(k MatchKind) bool {
	for _, kind := range MatchKinds {
		if kind == k {
			return true
		}
	}
	return false
}

func validBreadth(b Breadth) bool {
	for _, known := range Breadths {
		if known == b {
			return true
		}
	}
	return false
}

func joinBreadths() string {
	names := make([]string, len(Breadths))
	for i, b := range Breadths {
		names[i] = string(b)
	}
	return strings.Join(names, ", ")
}

func joinKinds() string {
	names := make([]string, len(MatchKinds))
	for i, k := range MatchKinds {
		names[i] = string(k)
	}
	return strings.Join(names, ", ")
}
