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
}

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
		if err := matchTopics(ctx, store, m, byID, byRecording); err != nil {
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
func matchTopics(ctx context.Context, store graph.Store, m *matcher, byID, byRecording map[string]*Hit) error {
	topics, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeTopic})
	if err != nil {
		return err
	}
	visited := make(map[string]bool)
	var walk func(t *graph.Node, label string, ev Match, depth int)
	walk = func(t *graph.Node, label string, ev Match, depth int) {
		if depth > maxTaxonomyDepth+1 || visited[t.ID] {
			return
		}
		visited[t.ID] = true
		edges, err := store.GetEdges(ctx, t.ID, graph.EdgeHasTopic)
		if err == nil {
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
				ev.Label = label
				h.add(&ev)
			}
		}
		children, err := TopicChildren(ctx, store, t.ID)
		if err != nil {
			return
		}
		for _, c := range children {
			walk(c, label+" › "+c.Name, ev, depth+1)
		}
	}
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
				c := 1.0
				if strings.Contains(t, "*") {
					c = search.PrefixCredit
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
func (h *Hit) add(ev *Match) {
	if ev == nil {
		return
	}
	h.Matches = append(h.Matches, *ev)
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

func joinKinds() string {
	names := make([]string, len(MatchKinds))
	for i, k := range MatchKinds {
		names[i] = string(k)
	}
	return strings.Join(names, ", ")
}
