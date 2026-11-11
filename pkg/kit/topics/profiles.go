package topics

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
)

// Things name their subjects in their own words, so a corpus of six hundred
// meetings carries two thousand topic labels of which most are used exactly
// once. "AGI feasibility debate", "AI alignment and superintelligence risk"
// and "Recursive self improvement and model regression" are one conversation
// spread over three labels, and a search for any one of them finds one
// meeting. Merging the labels would fuse three distinct discussions; leaving
// them apart hides two of them. A tree cannot settle it either, since it
// gives each label one parent and so cannot say that a label is both a facet
// of one concept and adjacent to another.
//
// The answer is to keep every label as it is and put the commonality on
// edges between them. What follows finds the pairs worth asking about: an
// exhaustive comparison is N² questions nobody needs answered, since almost
// every pair of topics is unrelated. Each Generator proposes a few
// neighbours per topic -- the nearest labels by embedding, the topics whose
// uses said the nearest things, the topics filed under the same use, and the
// topics under the same parent -- and their union is what gets judged.

// Profile is what is known about one topic when relating it to others.
type Profile struct {
	Node *graph.Node
	// Uses identifies the things filed under the topic directly -- meetings,
	// documents, sections -- as a set.
	Uses map[string]bool
	// Summaries are what was said under the topic, in the order seen; the
	// latest MaxSummaries are kept.
	Summaries []string
	// Quote is a verbatim quote filed under the topic, when one is known to
	// be genuine.
	Quote string
	// Parents are the ids of the topics this one sits under in the tree.
	Parents []string
	// Vector is the embedding of the label, when it has one.
	Vector []float32
	// SaidIDs are the nodes the Summaries came from, and Said their
	// embeddings where known: what was said, as opposed to what it was
	// called.
	SaidIDs []string
	Said    [][]float32
}

// ID is the topic's node id.
func (p *Profile) ID() string { return p.Node.ID }

// MaxSummaries caps how much of what was said travels with a topic. Three
// uses' worth is enough to see what a label means without the judge's state
// growing with every use of it.
const MaxSummaries = 3

// TrimSaid keeps the latest MaxSummaries summaries, and their nodes and
// embeddings with them.
func (p *Profile) TrimSaid() {
	if n := len(p.Summaries); n > MaxSummaries {
		p.Summaries = p.Summaries[n-MaxSummaries:]
	}
	if n := len(p.SaidIDs); n > MaxSummaries {
		p.SaidIDs = p.SaidIDs[n-MaxSummaries:]
	}
	if n := len(p.Said); n > MaxSummaries {
		p.Said = p.Said[n-MaxSummaries:]
	}
}

// PairKey identifies an unordered pair of topics.
type PairKey struct{ A, B string }

// KeyOf returns the key for two topic ids, in either order.
func KeyOf(a, b string) PairKey {
	if a > b {
		a, b = b, a
	}
	return PairKey{a, b}
}

// Profiles holds the topics being related, and what they share.
//
// Profiles is not safe for concurrent use; a Relater serializes its own.
type Profiles struct {
	byID map[string]*Profile
	// shared counts, per unordered pair, the uses filed under both.
	shared map[PairKey]int
	// counted names the uses already folded into shared.
	//
	// A use reaches this twice: loading counts every use already in the
	// graph, and indexing one then reports it again. Without a record of
	// which have been counted, the first use of every run is counted twice,
	// and re-indexing a whole corpus doubles all of them -- while Uses,
	// being a set, stays right. A judge is then shown a pair filed together
	// more often than either topic was filed at all, which cannot happen,
	// on one of the few pieces of evidence it is given.
	counted map[string]bool
	// adjacent counts, per unordered pair, the times the two were
	// consecutive parts of one use.
	adjacent map[PairKey]int
}

// NewProfiles returns an empty collection.
func NewProfiles() *Profiles {
	return &Profiles{
		byID:     make(map[string]*Profile),
		shared:   make(map[PairKey]int),
		counted:  make(map[string]bool),
		adjacent: make(map[PairKey]int),
	}
}

// Get returns a topic's profile, or nil.
func (ps *Profiles) Get(id string) *Profile { return ps.byID[id] }

// Len reports how many topics have profiles.
func (ps *Profiles) Len() int { return len(ps.byID) }

// All returns the profiles in a stable order, by id.
func (ps *Profiles) All() []*Profile {
	out := make([]*Profile, 0, len(ps.byID))
	for _, p := range ps.byID {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// Shared reports how many uses are filed under both topics.
func (ps *Profiles) Shared(a, b string) int { return ps.shared[KeyOf(a, b)] }

// Adjacent reports how often the two topics were consecutive parts of a
// use.
func (ps *Profiles) Adjacent(a, b string) int { return ps.adjacent[KeyOf(a, b)] }

// Ensure returns the profile for a topic node, creating one for a topic not
// seen yet. The node is copied so that later reads do not touch a map
// another goroutine may be writing.
func (ps *Profiles) Ensure(n *graph.Node) *Profile {
	if p, ok := ps.byID[n.ID]; ok {
		return p
	}
	copied := *n
	copied.Properties = make(map[string]string, len(n.Properties))
	for k, v := range n.Properties {
		copied.Properties[k] = v
	}
	p := &Profile{Node: &copied, Uses: make(map[string]bool)}
	ps.byID[n.ID] = p
	return p
}

// CountUse records that one use was filed under all of these topics, for a
// loader reading the graph: every pair of distinct members gains one shared
// use, and the use is remembered as counted.
func (ps *Profiles) CountUse(use string, members []*Profile) {
	ps.counted[use] = true
	ps.countPairs(members)
}

// NoteUse records that one use was filed under all of these topics, unless
// that use was counted already -- the common case, since loading counts
// everything the graph holds and a use just written is part of that by the
// time it is reported. An empty use is always counted.
func (ps *Profiles) NoteUse(use string, members []*Profile) {
	if use != "" {
		if ps.counted[use] {
			return
		}
		ps.counted[use] = true
	}
	ps.countPairs(members)
}

func (ps *Profiles) countPairs(members []*Profile) {
	for i := range members {
		for j := i + 1; j < len(members); j++ {
			if members[i] != members[j] {
				ps.shared[KeyOf(members[i].ID(), members[j].ID())]++
			}
		}
	}
}

// AddAdjacent records that two topics were consecutive parts of one use:
// one meeting's segments, one document's sections.
func (ps *Profiles) AddAdjacent(a, b string) { ps.adjacent[KeyOf(a, b)]++ }

// WithVectors attaches stored embeddings to the profiles that have one: the
// label's, and those of the nodes that said something under it. Returns how
// many topics got a label vector.
func (ps *Profiles) WithVectors(lookup func(nodeID string) ([]float32, bool)) int {
	n := 0
	for _, p := range ps.byID {
		if v, ok := lookup(p.ID()); ok {
			p.Vector = v
			n++
		}
		if len(p.Said) == 0 {
			for _, id := range p.SaidIDs {
				if v, ok := lookup(id); ok {
					p.Said = append(p.Said, v)
				}
			}
		}
	}
	return n
}

// Pair builds a candidate for two topics, in canonical order, with the
// evidence the profiles hold about them.
func (ps *Profiles) Pair(a, b *Profile) *Pair {
	if a.ID() > b.ID() {
		a, b = b, a
	}
	p := &Pair{A: a, B: b, Cosine: math.NaN()}
	if a.Vector != nil && b.Vector != nil {
		p.Cosine = Cosine(a.Vector, b.Vector)
	}
	key := KeyOf(a.ID(), b.ID())
	p.Shared = ps.shared[key]
	p.Adjacent = ps.adjacent[key]
	return p
}

// Families groups the topics by parent.
func (ps *Profiles) Families() map[string][]*Profile {
	byParent := make(map[string][]*Profile)
	for _, p := range ps.All() {
		for _, parent := range p.Parents {
			byParent[parent] = append(byParent[parent], p)
		}
	}
	return byParent
}

// Siblings returns the other children of a topic's parents, for parents
// with at most maxFamily children.
func (ps *Profiles) Siblings(p *Profile, maxFamily int) []*Profile {
	families := ps.Families()
	var out []*Profile
	seen := map[string]bool{p.ID(): true}
	for _, parent := range p.Parents {
		family := families[parent]
		if len(family) > maxFamily {
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

// Neighbour is a topic near another, with how near.
type Neighbour struct {
	Profile *Profile
	Cosine  float64
	// Rank is the position in the nearest list, 1 being the nearest.
	Rank int
}

// Nearest returns the k topics among all whose labels embed closest to a's.
func (ps *Profiles) Nearest(a *Profile, all []*Profile, k int) []Neighbour {
	if a.Vector == nil {
		return nil
	}
	var out []Neighbour
	for _, b := range all {
		if b == a || b.Vector == nil {
			continue
		}
		out = append(out, Neighbour{Profile: b, Cosine: Cosine(a.Vector, b.Vector)})
	}
	return topK(out, k)
}

// NearestSaid returns the k topics among all under which something was said
// closest to what was said under a: the best cosine between any of the two
// topics' Said embeddings.
func (ps *Profiles) NearestSaid(a *Profile, all []*Profile, k int) []Neighbour {
	if len(a.Said) == 0 {
		return nil
	}
	var out []Neighbour
	for _, b := range all {
		if b == a || len(b.Said) == 0 {
			continue
		}
		best := -1.0
		for _, av := range a.Said {
			for _, bv := range b.Said {
				best = math.Max(best, Cosine(av, bv))
			}
		}
		out = append(out, Neighbour{Profile: b, Cosine: best})
	}
	return topK(out, k)
}

// topK orders neighbours nearest first, ties by id, keeps k and ranks them.
func topK(out []Neighbour, k int) []Neighbour {
	sort.Slice(out, func(i, j int) bool {
		if out[i].Cosine != out[j].Cosine {
			return out[i].Cosine > out[j].Cosine
		}
		return out[i].Profile.ID() < out[j].Profile.ID()
	})
	if len(out) > k {
		out = out[:k]
	}
	for i := range out {
		out[i].Rank = i + 1
	}
	return out
}

// Candidates proposes the pairs worth judging: the union of what every
// generator proposes across all the profiles, in key order.
func (ps *Profiles) Candidates(gens ...Generator) []*Pair {
	pairs := make(map[PairKey]*Pair)
	add := func(a, b *Profile, src Source, rank int) {
		key := KeyOf(a.ID(), b.ID())
		p, ok := pairs[key]
		if !ok {
			p = ps.Pair(a, b)
			pairs[key] = p
		}
		p.note(src, rank)
	}
	for _, g := range gens {
		g.Propose(ps, add)
	}
	out := make([]*Pair, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, p)
	}
	SortPairs(out)
	return out
}

// SortPairs orders pairs by their keys, so a run is reproducible.
func SortPairs(pairs []*Pair) {
	sort.Slice(pairs, func(i, j int) bool {
		ki, kj := pairs[i].Key(), pairs[j].Key()
		if ki.A != kj.A {
			return ki.A < kj.A
		}
		return ki.B < kj.B
	})
}

// Source says which signals proposed a pair, as a set of bits.
type Source uint8

const (
	// SourceNearest means one topic is among the other's nearest labels by
	// embedding.
	SourceNearest Source = 1 << iota
	// SourceSharedUse means at least one use is filed under both.
	SourceSharedUse
	// SourceAdjacent means the two were consecutive parts of one use.
	SourceAdjacent
	// SourceSharedParent means the two sit under the same parent in the tree.
	SourceSharedParent
	// SourceSaid means what was said under one topic embeds close to what
	// was said under the other, whatever the labels.
	SourceSaid
)

// sourceOrder is the order sources are named in, which is not bit order.
var sourceOrder = []struct {
	src  Source
	name string
}{
	{SourceNearest, "nearest"},
	{SourceSaid, "said"},
	{SourceSharedUse, "shared-use"},
	{SourceAdjacent, "adjacent"},
	{SourceSharedParent, "shared-parent"},
}

// String names the sources set, joined with "+"; "none" when empty.
func (s Source) String() string { return s.Label(nil) }

// Label names the sources set as String does, with names overriding the
// default name of any source it holds.
func (s Source) Label(names map[Source]string) string {
	var parts []string
	for _, o := range sourceOrder {
		if s&o.src == 0 {
			continue
		}
		name := o.name
		if n, ok := names[o.src]; ok {
			name = n
		}
		parts = append(parts, name)
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, "+")
}

// Pair is two topics proposed as possibly related, with the evidence that
// proposed them.
type Pair struct {
	A, B    *Profile
	Sources Source
	// Cosine is the similarity of the two labels' embeddings, or NaN when
	// either has none.
	Cosine float64
	// NearestRank is the better of the two ranks each holds in the other's
	// nearest list, 1 being the nearest; 0 when neither lists the other.
	NearestRank int
	// Shared is how many uses are filed under both topics, and Adjacent
	// how many times they were consecutive parts of one use.
	Shared   int
	Adjacent int
}

// Key identifies the pair regardless of order.
func (p *Pair) Key() PairKey { return KeyOf(p.A.ID(), p.B.ID()) }

// note records one more signal proposing the pair.
func (p *Pair) note(src Source, rank int) {
	p.Sources |= src
	if rank > 0 && (p.NearestRank == 0 || rank < p.NearestRank) {
		p.NearestRank = rank
	}
}

// AddFunc proposes the pair a, b, naming the signal that proposed it and,
// for a nearest-neighbour signal, the rank (1 being the nearest; 0 for a
// signal without one).
type AddFunc func(a, b *Profile, src Source, rank int)

// A Generator is one signal that proposes pairs of topics worth judging.
//
// Each signal is blind somewhere. Embeddings of labels compare a few words:
// measured on a real corpus, three labels of one conversation sat at cosine
// 0.60-0.64 from each other while each one's ten nearest labels sat at 0.70
// and above, so none of the three pairs was proposed that way. What was said
// under two of them embedded as each other's nearest; the third pair shared
// a meeting. Co-occurrence in turn cannot connect two uses that never shared
// a label; embeddings can. None knows what the tree already grouped.
type Generator interface {
	// Propose adds every pair the signal sees among all the profiles, for
	// a bulk pass.
	Propose(ps *Profiles, add AddFunc)
	// ProposeFor adds the pairs one use introduces: members are the
	// topics it was just filed under, and fresh those among them not yet
	// judged against anything, whose neighbourhood is new.
	ProposeFor(ps *Profiles, members, fresh []*Profile, add AddFunc)
}

// Defaults for the generators.
const (
	// DefaultNearestK is how many embedding neighbours each topic proposes
	// by its label.
	DefaultNearestK = 10
	// DefaultSaidK is how many neighbours each topic proposes by what was
	// said under it. Fewer than by label: a summary embeds richly enough
	// that its nearest few are already specific.
	DefaultSaidK = 5
	// MaxFamily caps how large a parent's family may be for its children
	// to propose each other.
	//
	// Measured on a real corpus the tree had a parent with 190 children,
	// and families over thirty accounted for 53,000 of the 56,000
	// shared-parent pairs -- nearly all of them unrelated, since a parent
	// that broad ("Opal platform") says almost nothing about its members.
	// Under thirty the grouping is specific enough to be worth a question.
	MaxFamily = 30
)

// DefaultGenerators are the five signals together: the nearestK nearest
// labels (DefaultNearestK when not positive), the DefaultSaidK nearest by
// what was said, shared uses, adjacency, and shared parents in families of
// at most MaxFamily.
func DefaultGenerators(nearestK int) []Generator {
	return []Generator{
		NearestLabels(nearestK),
		NearestSaid(DefaultSaidK),
		SharedUse(),
		Adjacency(),
		SharedParent(MaxFamily),
	}
}

// NearestLabels proposes each topic's k nearest labels by embedding
// (DefaultNearestK when k is not positive). Silent for topics without a
// label vector.
func NearestLabels(k int) Generator {
	if k <= 0 {
		k = DefaultNearestK
	}
	return nearestLabels{k}
}

type nearestLabels struct{ k int }

func (g nearestLabels) Propose(ps *Profiles, add AddFunc) {
	all := ps.All()
	for _, a := range all {
		for _, n := range ps.Nearest(a, all, g.k) {
			add(a, n.Profile, SourceNearest, n.Rank)
		}
	}
}

func (g nearestLabels) ProposeFor(ps *Profiles, _, fresh []*Profile, add AddFunc) {
	all := ps.All()
	for _, p := range fresh {
		for _, n := range ps.Nearest(p, all, g.k) {
			add(p, n.Profile, SourceNearest, n.Rank)
		}
	}
}

// NearestSaid proposes, for each topic, the k topics under which something
// was said closest to what was said under it (DefaultSaidK when k is not
// positive). Silent for topics without Said embeddings.
func NearestSaid(k int) Generator {
	if k <= 0 {
		k = DefaultSaidK
	}
	return nearestSaid{k}
}

type nearestSaid struct{ k int }

func (g nearestSaid) Propose(ps *Profiles, add AddFunc) {
	all := ps.All()
	for _, a := range all {
		for _, n := range ps.NearestSaid(a, all, g.k) {
			add(a, n.Profile, SourceSaid, 0)
		}
	}
}

func (g nearestSaid) ProposeFor(ps *Profiles, _, fresh []*Profile, add AddFunc) {
	all := ps.All()
	for _, p := range fresh {
		for _, n := range ps.NearestSaid(p, all, g.k) {
			add(p, n.Profile, SourceSaid, 0)
		}
	}
}

// SharedUse proposes every pair of topics one use is filed under: the same
// meeting, the same document.
func SharedUse() Generator { return sharedUse{} }

type sharedUse struct{}

func (sharedUse) Propose(ps *Profiles, add AddFunc) {
	for key := range ps.shared {
		a, b := ps.byID[key.A], ps.byID[key.B]
		if a != nil && b != nil {
			add(a, b, SourceSharedUse, 0)
		}
	}
}

func (sharedUse) ProposeFor(_ *Profiles, members, _ []*Profile, add AddFunc) {
	for i := range members {
		for j := i + 1; j < len(members); j++ {
			add(members[i], members[j], SourceSharedUse, 0)
		}
	}
}

// Adjacency proposes the pairs that were consecutive parts of one use. It
// proposes nothing for a single new use, whose parts SharedUse already
// pairs.
func Adjacency() Generator { return adjacency{} }

type adjacency struct{}

func (adjacency) Propose(ps *Profiles, add AddFunc) {
	for key := range ps.adjacent {
		a, b := ps.byID[key.A], ps.byID[key.B]
		if a != nil && b != nil {
			add(a, b, SourceAdjacent, 0)
		}
	}
}

func (adjacency) ProposeFor(*Profiles, []*Profile, []*Profile, AddFunc) {}

// SharedParent proposes the children of one parent to each other, for
// families of at most maxFamily (MaxFamily when not positive).
func SharedParent(maxFamily int) Generator {
	if maxFamily <= 0 {
		maxFamily = MaxFamily
	}
	return sharedParent{maxFamily}
}

type sharedParent struct{ max int }

func (g sharedParent) Propose(ps *Profiles, add AddFunc) {
	for _, family := range ps.Families() {
		if len(family) > g.max {
			continue
		}
		for i := range family {
			for j := i + 1; j < len(family); j++ {
				add(family[i], family[j], SourceSharedParent, 0)
			}
		}
	}
}

func (g sharedParent) ProposeFor(ps *Profiles, _, fresh []*Profile, add AddFunc) {
	for _, p := range fresh {
		for _, s := range ps.Siblings(p, g.max) {
			add(p, s, SourceSharedParent, 0)
		}
	}
}

// Cosine is the cosine similarity of two vectors, 0 when either is zero or
// the dimensions differ.
func Cosine(a, b []float32) float64 {
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

// LoadProfiles reads every topic something is filed under through a HasTopic
// edge: the nodes filed under it (each a use), what they said (their
// summary property, as the relater's WithSaidText reads it by default),
// which topics share a node, and where each topic sits in the tree.
//
// It suits a graph whose topics hang directly off the things that name
// them. A graph with more structure -- topics filed under a meeting and
// under each of its segments, say -- wants its own loader, passed to
// WithProfiles.
func LoadProfiles(ctx context.Context, store graph.Store) (*Profiles, error) {
	return loadProfiles(ctx, store, summaryOf)
}

func loadProfiles(ctx context.Context, store graph.Store, saidText func(*graph.Node) string) (*Profiles, error) {
	nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeTopic})
	if err != nil {
		return nil, fmt.Errorf("query topics: %w", err)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })

	ps := NewProfiles()
	byUse := make(map[string][]*Profile)
	for _, t := range nodes {
		edges, err := store.GetEdges(ctx, t.ID, graph.EdgeHasTopic)
		if err != nil {
			return nil, fmt.Errorf("topic edges of %q: %w", t.Name, err)
		}
		var uses []string
		for _, e := range edges {
			if e.TargetID == t.ID && e.SourceID != t.ID {
				uses = append(uses, e.SourceID)
			}
		}
		if len(uses) == 0 {
			continue
		}
		sort.Strings(uses)
		p := ps.Ensure(t)
		for _, use := range uses {
			if p.Uses[use] {
				continue
			}
			p.Uses[use] = true
			byUse[use] = append(byUse[use], p)
			if src, err := store.GetNode(ctx, use); err == nil && src != nil {
				if s := strings.TrimSpace(saidText(src)); s != "" {
					p.Summaries = append(p.Summaries, s)
					p.SaidIDs = append(p.SaidIDs, src.ID)
				}
			}
		}
		p.TrimSaid()
	}
	for use, members := range byUse {
		ps.CountUse(use, members)
	}
	for _, p := range ps.byID {
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
	return ps, nil
}

// summaryOf is what a node said, by default: its summary property.
func summaryOf(n *graph.Node) string { return n.Properties[graph.PropSummary] }
