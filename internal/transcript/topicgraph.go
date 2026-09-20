package transcript

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// Every meeting names its subject in its own words, so a corpus of six
// hundred meetings carries two thousand topic labels of which most are used
// exactly once. "AGI feasibility debate", "AI alignment and superintelligence
// risk" and "Recursive self improvement and model regression" are one
// conversation spread over three labels, and a search for any one of them
// finds one meeting. Merging the labels would fuse three distinct discussions;
// leaving them apart hides two of them. The tree tried to settle this by
// giving each label one parent, which cannot express that a label is both a
// facet of one concept and adjacent to another.
//
// The answer is to keep every label as it is and put the commonality on
// edges between them. What follows finds the pairs worth asking about: an
// exhaustive comparison is N² questions nobody needs answered, since almost
// every pair of topics is unrelated. Three signals each propose a few
// neighbours per topic — the nearest labels by embedding, the topics the
// same meetings discussed, and the topics filed under the same parent — and
// their union is what gets judged.

// TopicProfile is what is known about one topic when relating it to others.
type TopicProfile struct {
	Node *graph.Node
	// Meetings are the session identifiers of the meetings filed under the
	// topic directly.
	Meetings map[string]bool
	// Summaries are what topic segments filed under it said, most recent
	// first, capped at maxProfileSummaries.
	Summaries []string
	// Quote is a verbatim quote from a decision filed under the topic, when
	// one was verified against its transcript.
	Quote string
	// Parents are the ids of the topics this one sits under in the tree.
	Parents []string
	// Vector is the stored embedding of the label, when the topic is
	// indexed for vector search.
	Vector []float32
	// SegmentIDs are the segments the Summaries came from, and Said their
	// embeddings when the index has them: what was said, as opposed to
	// what it was called.
	SegmentIDs []string
	Said       [][]float32
}

// ID is the topic's node id.
func (p *TopicProfile) ID() string { return p.Node.ID }

// maxProfileSummaries caps how much of what was said travels with a topic.
// Three meetings' worth is enough to see what a label means without the
// state growing with every meeting that reuses it.
const maxProfileSummaries = 3

// TopicProfiles holds every topic meetings were filed under.
type TopicProfiles struct {
	byID map[string]*TopicProfile
	// cooccur counts, per unordered pair, the meetings filed under both.
	cooccur map[pairKey]int
	// counted names the meetings already folded into cooccur.
	//
	// A meeting reaches this twice: loading counts every meeting already in
	// the graph, and indexing one then reports it again. Without a record of
	// which have been counted, the first meeting of every run is counted
	// twice, and re-indexing a whole corpus doubles all of them -- while
	// Meetings, being a set, stays right. The judge is then shown a pair
	// filed under both of two topics more often than either topic was filed
	// under at all, which cannot happen, on one of the few pieces of
	// evidence it is given.
	counted map[string]bool
	// adjacent counts, per unordered pair, the times the two were
	// consecutive segments of one meeting.
	adjacent map[pairKey]int
}

type pairKey struct{ a, b string }

func keyOf(a, b string) pairKey {
	if a > b {
		a, b = b, a
	}
	return pairKey{a, b}
}

// Get returns a topic's profile, or nil.
func (tp *TopicProfiles) Get(id string) *TopicProfile { return tp.byID[id] }

// Len reports how many topics have profiles.
func (tp *TopicProfiles) Len() int { return len(tp.byID) }

// All returns the profiles in a stable order.
func (tp *TopicProfiles) All() []*TopicProfile {
	out := make([]*TopicProfile, 0, len(tp.byID))
	for _, p := range tp.byID {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// SharedMeetings reports how many meetings are filed under both topics.
func (tp *TopicProfiles) SharedMeetings(a, b string) int { return tp.cooccur[keyOf(a, b)] }

// AdjacentSegments reports how often the two topics were consecutive
// segments of a meeting.
func (tp *TopicProfiles) AdjacentSegments(a, b string) int { return tp.adjacent[keyOf(a, b)] }

// LoadTopicProfiles reads the topics meetings were filed under, with what
// was said under each and which meetings share them.
//
// Only topics reached from a meeting count. The graph also holds topics that
// document indexing extracted, and relating a meeting's subject to a README's
// would answer a question nobody asked.
func LoadTopicProfiles(ctx context.Context, store graph.Store) (*TopicProfiles, error) {
	meetings, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeMeeting})
	if err != nil {
		return nil, fmt.Errorf("query meetings: %w", err)
	}
	tp := &TopicProfiles{
		byID:     make(map[string]*TopicProfile),
		cooccur:  make(map[pairKey]int),
		adjacent: make(map[pairKey]int),
		counted:  make(map[string]bool),
	}

	profile := func(id string) (*TopicProfile, error) {
		if p, ok := tp.byID[id]; ok {
			return p, nil
		}
		n, err := store.GetNode(ctx, id)
		if err != nil || n == nil || n.Type != graph.NodeTopic {
			return nil, err
		}
		p := &TopicProfile{Node: n, Meetings: make(map[string]bool)}
		tp.byID[id] = p
		return p, nil
	}

	// The meetings filed under each topic, and the pairs of topics one
	// meeting was filed under.
	byRecording := make(map[string]*graph.Node, len(meetings))
	for _, m := range meetings {
		byRecording[recordingKey(m.FilePath, m.QualifiedName)] = m
		edges, err := store.GetEdges(ctx, m.ID, graph.EdgeHasTopic)
		if err != nil {
			return nil, err
		}
		var ids []string
		for _, e := range edges {
			if e.SourceID != m.ID {
				continue
			}
			p, err := profile(e.TargetID)
			if err != nil || p == nil {
				continue
			}
			if !p.Meetings[m.QualifiedName] {
				p.Meetings[m.QualifiedName] = true
				ids = append(ids, p.ID())
			}
		}
		sort.Strings(ids)
		tp.counted[m.QualifiedName] = true
		for i := range ids {
			for j := i + 1; j < len(ids); j++ {
				tp.cooccur[keyOf(ids[i], ids[j])]++
			}
		}
	}

	// What was said under each topic, and which topics followed which.
	segments, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeTopicSegment})
	if err != nil {
		return nil, fmt.Errorf("query segments: %w", err)
	}
	type placed struct {
		start   float64
		topicID string
		when    int64
	}
	order := make(map[string][]placed)
	for _, s := range segments {
		key := recordingKey(s.FilePath, s.Properties[graph.PropMeetingID])
		if byRecording[key] == nil {
			continue
		}
		edges, err := store.GetEdges(ctx, s.ID, graph.EdgeHasTopic)
		if err != nil {
			return nil, err
		}
		for _, e := range edges {
			if e.SourceID != s.ID {
				continue
			}
			p, err := profile(e.TargetID)
			if err != nil || p == nil {
				continue
			}
			if summary := strings.TrimSpace(s.Properties[graph.PropSummary]); summary != "" {
				p.Summaries = append(p.Summaries, summary)
				p.SegmentIDs = append(p.SegmentIDs, s.ID)
			}
			start, _ := strconv.ParseFloat(s.Properties[graph.PropStartTime], 64)
			order[key] = append(order[key], placed{start: start, topicID: p.ID(), when: s.UpdatedAt.Unix()})
		}
	}
	for _, segs := range order {
		sort.Slice(segs, func(i, j int) bool { return segs[i].start < segs[j].start })
		for i := 1; i < len(segs); i++ {
			if segs[i-1].topicID != segs[i].topicID {
				tp.adjacent[keyOf(segs[i-1].topicID, segs[i].topicID)]++
			}
		}
	}
	for _, p := range tp.byID {
		p.trimSaid()
	}

	// A verified quote from a decision, where a topic has one.
	decisions, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeDecision})
	if err != nil {
		return nil, fmt.Errorf("query decisions: %w", err)
	}
	for _, d := range decisions {
		if d.Properties["quote_verified"] != "true" {
			continue
		}
		quote := strings.TrimSpace(d.Properties[graph.PropQuote])
		if quote == "" {
			continue
		}
		edges, err := store.GetEdges(ctx, d.ID, graph.EdgeHasTopic)
		if err != nil {
			return nil, err
		}
		for _, e := range edges {
			if e.SourceID != d.ID {
				continue
			}
			if p := tp.byID[e.TargetID]; p != nil && p.Quote == "" {
				p.Quote = quote
			}
		}
	}

	// Where each topic sits in the tree.
	for _, p := range tp.byID {
		parents, err := store.GetNeighbors(ctx, p.ID(), graph.EdgeContains, graph.Incoming)
		if err != nil {
			continue
		}
		for _, parent := range parents {
			if parent.Type == graph.NodeTopic {
				p.Parents = append(p.Parents, parent.ID)
			}
		}
		sort.Strings(p.Parents)
	}
	return tp, nil
}

// trimSaid keeps the most recent summaries, and their segments and
// embeddings with them.
func (p *TopicProfile) trimSaid() {
	if n := len(p.Summaries); n > maxProfileSummaries {
		p.Summaries = p.Summaries[n-maxProfileSummaries:]
	}
	if n := len(p.SegmentIDs); n > maxProfileSummaries {
		p.SegmentIDs = p.SegmentIDs[n-maxProfileSummaries:]
	}
	if n := len(p.Said); n > maxProfileSummaries {
		p.Said = p.Said[n-maxProfileSummaries:]
	}
}

// WithVectors attaches stored embeddings to the profiles that have one: the
// label's, and those of the segments that said something under it. Returns
// how many topics got a label vector.
func (tp *TopicProfiles) WithVectors(lookup func(nodeID string) ([]float32, bool)) int {
	n := 0
	for _, p := range tp.byID {
		if v, ok := lookup(p.ID()); ok {
			p.Vector = v
			n++
		}
		if len(p.Said) == 0 {
			for _, id := range p.SegmentIDs {
				if v, ok := lookup(id); ok {
					p.Said = append(p.Said, v)
				}
			}
		}
	}
	return n
}

// ensure returns the profile for a topic node, creating one for a topic the
// load did not see. The node is copied so that later reads do not touch a
// map another goroutine may be writing.
func (tp *TopicProfiles) ensure(n *graph.Node) *TopicProfile {
	if p, ok := tp.byID[n.ID]; ok {
		return p
	}
	copied := *n
	copied.Properties = make(map[string]string, len(n.Properties))
	for k, v := range n.Properties {
		copied.Properties[k] = v
	}
	p := &TopicProfile{Node: &copied, Meetings: make(map[string]bool)}
	tp.byID[n.ID] = p
	return p
}

// noteMeeting records that one meeting was filed under all of these topics.
//
// Ignored for a meeting already counted, which is the common case: loading
// the profiles counts everything the graph holds, and the meeting just
// written is part of that by the time this is called.
func (tp *TopicProfiles) noteMeeting(session string, members []*TopicProfile) {
	if session != "" {
		if tp.counted[session] {
			return
		}
		tp.counted[session] = true
	}
	for i := range members {
		for j := i + 1; j < len(members); j++ {
			if members[i] != members[j] {
				tp.cooccur[keyOf(members[i].ID(), members[j].ID())]++
			}
		}
	}
}

// pair builds a candidate for two topics, in canonical order, with the
// evidence the profiles hold about them.
func (tp *TopicProfiles) pair(a, b *TopicProfile) *TopicPair {
	if a.ID() > b.ID() {
		a, b = b, a
	}
	p := &TopicPair{A: a, B: b, Cosine: math.NaN()}
	if a.Vector != nil && b.Vector != nil {
		p.Cosine = cosine(a.Vector, b.Vector)
	}
	key := keyOf(a.ID(), b.ID())
	p.SharedMeetings = tp.cooccur[key]
	p.Adjacent = tp.adjacent[key]
	return p
}

// siblings returns the other children of a topic's parents, for parents
// whose family is small enough to mean something.
func (tp *TopicProfiles) siblings(p *TopicProfile) []*TopicProfile {
	families := tp.families()
	var out []*TopicProfile
	seen := map[string]bool{p.ID(): true}
	for _, parent := range p.Parents {
		family := families[parent]
		if len(family) > maxSiblingFamily {
			continue
		}
		for _, s := range family {
			if !seen[s.ID()] {
				seen[s.ID()] = true
				out = append(out, s)
			}
		}
	}
	return out
}

// families groups the topics by parent.
func (tp *TopicProfiles) families() map[string][]*TopicProfile {
	byParent := make(map[string][]*TopicProfile)
	for _, p := range tp.All() {
		for _, parent := range p.Parents {
			byParent[parent] = append(byParent[parent], p)
		}
	}
	return byParent
}

// CandidateSource says which signal proposed a pair.
type CandidateSource uint8

const (
	// SourceNearest means one topic is among the other's nearest labels by
	// embedding.
	SourceNearest CandidateSource = 1 << iota
	// SourceSameMeeting means at least one meeting is filed under both.
	SourceSameMeeting
	// SourceAdjacent means the two were consecutive segments of a meeting.
	SourceAdjacent
	// SourceSharedParent means the two sit under the same parent in the tree.
	SourceSharedParent
	// SourceSaid means what a meeting said under one topic embeds close to
	// what a meeting said under the other, whatever the labels.
	SourceSaid
)

// String names the sources set, for reports.
func (s CandidateSource) String() string {
	var parts []string
	if s&SourceNearest != 0 {
		parts = append(parts, "nearest")
	}
	if s&SourceSaid != 0 {
		parts = append(parts, "said")
	}
	if s&SourceSameMeeting != 0 {
		parts = append(parts, "same-meeting")
	}
	if s&SourceAdjacent != 0 {
		parts = append(parts, "adjacent")
	}
	if s&SourceSharedParent != 0 {
		parts = append(parts, "shared-parent")
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, "+")
}

// TopicPair is two topics proposed as possibly related, with the evidence
// that proposed them.
type TopicPair struct {
	A, B    *TopicProfile
	Sources CandidateSource
	// Cosine is the similarity of the two labels' embeddings, or NaN when
	// either has none.
	Cosine float64
	// NearestRank is the better of the two ranks each holds in the other's
	// nearest list, 1 being the nearest; 0 when neither lists the other.
	NearestRank int
	// SharedMeetings and Adjacent are the co-occurrence counts.
	SharedMeetings int
	Adjacent       int
}

// Key identifies the pair regardless of order.
func (p *TopicPair) Key() pairKey { return keyOf(p.A.ID(), p.B.ID()) }

// DefaultNearestK is how many embedding neighbours each topic proposes by
// its label.
const DefaultNearestK = 10

// saidK is how many neighbours each topic proposes by what was said under
// it. Fewer than by label: a summary embeds richly enough that its nearest
// few are already specific.
const saidK = 5

// Candidates proposes the pairs worth judging: the union of each topic's k
// nearest labels by embedding, the topics whose segments said the nearest
// things, the topics its meetings also discussed, and the topics under the
// same parent when the family is small.
//
// Each signal is blind somewhere. Embeddings of labels compare a few words:
// measured, the three AGI labels sit at cosine 0.60-0.64 from each other
// while each one's ten nearest labels sit at 0.70 and above, so none of the
// three pairs is proposed that way. What the meetings said under two of
// them embeds as each other's nearest segment, which is the fourth signal;
// the third pair shared a meeting. Co-occurrence in turn cannot connect two
// meetings that never shared a label; embeddings can. None knows what the
// tree already grouped.
func (tp *TopicProfiles) Candidates(k int) []*TopicPair {
	if k <= 0 {
		k = DefaultNearestK
	}
	pairs := make(map[pairKey]*TopicPair)
	get := func(a, b *TopicProfile) *TopicPair {
		key := keyOf(a.ID(), b.ID())
		if p, ok := pairs[key]; ok {
			return p
		}
		p := tp.pair(a, b)
		pairs[key] = p
		return p
	}

	all := tp.All()
	for _, a := range all {
		for _, n := range tp.nearest(a, all, k) {
			p := get(a, n.profile)
			p.Sources |= SourceNearest
			if p.NearestRank == 0 || n.rank < p.NearestRank {
				p.NearestRank = n.rank
			}
		}
		for _, n := range tp.nearestSaid(a, all, saidK) {
			get(a, n.profile).Sources |= SourceSaid
		}
	}
	for key := range tp.cooccur {
		a, b := tp.byID[key.a], tp.byID[key.b]
		if a != nil && b != nil {
			get(a, b).Sources |= SourceSameMeeting
		}
	}
	for key := range tp.adjacent {
		a, b := tp.byID[key.a], tp.byID[key.b]
		if a != nil && b != nil {
			get(a, b).Sources |= SourceAdjacent
		}
	}
	for _, family := range tp.families() {
		if len(family) > maxSiblingFamily {
			continue
		}
		for i := range family {
			for j := i + 1; j < len(family); j++ {
				get(family[i], family[j]).Sources |= SourceSharedParent
			}
		}
	}

	out := make([]*TopicPair, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, p)
	}
	sortPairs(out)
	return out
}

// sortPairs orders pairs by their keys, so a run is reproducible.
func sortPairs(pairs []*TopicPair) {
	sort.Slice(pairs, func(i, j int) bool {
		ki, kj := pairs[i].Key(), pairs[j].Key()
		if ki.a != kj.a {
			return ki.a < kj.a
		}
		return ki.b < kj.b
	})
}

type ranked struct {
	profile *TopicProfile
	cosine  float64
	rank    int
}

// nearest returns the k topics whose labels embed closest to one.
func (tp *TopicProfiles) nearest(a *TopicProfile, all []*TopicProfile, k int) []ranked {
	if a.Vector == nil {
		return nil
	}
	var out []ranked
	for _, b := range all {
		if b == a || b.Vector == nil {
			continue
		}
		out = append(out, ranked{profile: b, cosine: cosine(a.Vector, b.Vector)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].cosine != out[j].cosine {
			return out[i].cosine > out[j].cosine
		}
		return out[i].profile.ID() < out[j].profile.ID()
	})
	if len(out) > k {
		out = out[:k]
	}
	for i := range out {
		out[i].rank = i + 1
	}
	return out
}

// nearestSaid returns the k topics under which a meeting said something
// closest to what was said under this one: the best cosine between any of
// the two topics' segment embeddings.
func (tp *TopicProfiles) nearestSaid(a *TopicProfile, all []*TopicProfile, k int) []ranked {
	if len(a.Said) == 0 {
		return nil
	}
	var out []ranked
	for _, b := range all {
		if b == a || len(b.Said) == 0 {
			continue
		}
		best := -1.0
		for _, av := range a.Said {
			for _, bv := range b.Said {
				best = math.Max(best, cosine(av, bv))
			}
		}
		out = append(out, ranked{profile: b, cosine: best})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].cosine != out[j].cosine {
			return out[i].cosine > out[j].cosine
		}
		return out[i].profile.ID() < out[j].profile.ID()
	})
	if len(out) > k {
		out = out[:k]
	}
	for i := range out {
		out[i].rank = i + 1
	}
	return out
}

// cosine is the cosine similarity of two vectors, 0 when either is zero or
// the dimensions differ.
func cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
