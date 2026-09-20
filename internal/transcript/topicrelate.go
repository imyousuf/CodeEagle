package transcript

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/vectorstore"
	"github.com/imyousuf/CodeEagle/pkg/jev"
)

// Property keys on a RelatedTo edge.
const (
	// PropRelatedProbability is the judge's probability that a meeting filed
	// under either topic is worth showing to someone asking about the other.
	PropRelatedProbability = "probability"
	// PropSameSubject is the judge's probability that the two labels are two
	// wordings of one subject.
	PropSameSubject = "same_subject"
	// propRelatedCosine is the similarity of the two labels' embeddings, when
	// both had one.
	propRelatedCosine = "cosine"
	// propRelatedSources names the signals that proposed the pair.
	propRelatedSources = "sources"
	// propRelatedJudge is the model that answered.
	propRelatedJudge = "judge"
)

// maxSiblingFamily caps how large a parent's family may be for its children
// to propose each other as candidates.
//
// Measured on the real corpus the tree has a parent with 190 children, and
// families over thirty account for 53,000 of the 56,000 shared-parent pairs
// — nearly all of them unrelated, since a parent that broad ("Opal
// platform") says almost nothing about its members. Under thirty the
// grouping is specific enough that its members are worth a question.
const maxSiblingFamily = 30

// approxTokensPerPair is what one judged pair costs in input tokens; used
// only to say what a run will cost. Measured at 750-810 over a 91-pair
// sample weighted towards well-used topics, and at 540 over the whole
// corpus, where most topics carry one meeting's worth of summary.
const approxTokensPerPair = 650

// RelaterOptions configures a TopicRelater.
type RelaterOptions struct {
	// NearestK is how many nearest labels a topic new to the graph proposes;
	// DefaultNearestK when zero.
	NearestK int
	// Concurrency is how many requests a bulk run keeps in flight.
	Concurrency int
	// Vectors reads a topic's stored embedding, when the vector index is
	// open. Without it the nearest-label signal is silent and pairs come
	// from co-occurrence and the tree alone.
	Vectors func(nodeID string) ([]float32, bool)
	// Embed places a label that has no stored vector yet — a topic minted
	// during this run — among the ones that do.
	Embed func(ctx context.Context, text string) ([]float32, error)
	// Log reports progress and the failures that do not stop a run.
	Log func(string, ...any)
}

// RelateStats reports what relating did.
type RelateStats struct {
	// Pairs is how many pairs were judged and written.
	Pairs int
	// Related is how many of them cleared the default search gate.
	Related int
	Judging TopicJudging
}

// Add accumulates another round.
func (s *RelateStats) Add(o RelateStats) {
	s.Pairs += o.Pairs
	s.Related += o.Related
	s.Judging.Add(o.Judging)
}

// TopicRelater proposes pairs of topics, has them judged, and records the
// verdicts as edges.
//
// It works incrementally: each meeting written proposes the pairs it
// introduces — its own topics with each other, and for a topic new to the
// graph its nearest labels and the other children of its parent — and only
// pairs not yet judged are sent. A bulk pass over every candidate is the
// repair tool for a corpus indexed before this existed, not the mechanism.
type TopicRelater struct {
	store graph.Store
	judge *TopicJudge
	opts  RelaterOptions

	mu       sync.Mutex
	profiles *TopicProfiles
	// judged holds every pair an edge exists for, related or not, and
	// judgedTopics every topic that has at least one.
	judged       map[pairKey]bool
	judgedTopics map[string]bool
	stats        RelateStats
}

// NewTopicRelater builds a relater over a decision model.
func NewTopicRelater(store graph.Store, asker jev.Asker, opts RelaterOptions) *TopicRelater {
	if opts.NearestK <= 0 {
		opts.NearestK = DefaultNearestK
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	if opts.Log == nil {
		opts.Log = func(string, ...any) {}
	}
	return &TopicRelater{store: store, judge: NewTopicJudge(asker), opts: opts}
}

// Stats reports everything this relater has done.
func (r *TopicRelater) Stats() RelateStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats
}

// load reads the topics and the pairs already judged, once.
func (r *TopicRelater) load(ctx context.Context) error {
	if r.profiles != nil {
		return nil
	}
	profiles, err := LoadTopicProfiles(ctx, r.store)
	if err != nil {
		return err
	}
	if r.opts.Vectors != nil {
		profiles.WithVectors(r.opts.Vectors)
	}
	judged := make(map[pairKey]bool)
	judgedTopics := make(map[string]bool)
	for _, p := range profiles.All() {
		edges, err := r.store.GetEdges(ctx, p.ID(), graph.EdgeRelatedTo)
		if err != nil {
			return fmt.Errorf("related edges of %q: %w", p.Node.Name, err)
		}
		for _, e := range edges {
			judged[keyOf(e.SourceID, e.TargetID)] = true
			judgedTopics[e.SourceID] = true
			judgedTopics[e.TargetID] = true
		}
	}
	r.profiles, r.judged, r.judgedTopics = profiles, judged, judgedTopics
	return nil
}

// RelateMeeting judges the pairs one newly written meeting introduces.
//
// session is the meeting's identifier, topics the nodes it was filed under,
// and segments the meeting's segment under each topic, keyed by topic id.
func (r *TopicRelater) RelateMeeting(ctx context.Context, session string, topics []*graph.Node, segments map[string]*graph.Node) (RelateStats, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.load(ctx); err != nil {
		return RelateStats{}, err
	}

	var members []*TopicProfile
	for _, n := range topics {
		p := r.profiles.ensure(n)
		// A meeting written again keeps one copy of what it said.
		if seg := segments[n.ID]; seg != nil && !p.Meetings[session] {
			if s := strings.TrimSpace(seg.Properties[graph.PropSummary]); s != "" {
				p.Summaries = append(p.Summaries, s)
				p.SegmentIDs = append(p.SegmentIDs, seg.ID)
				if v := r.vectorFor(ctx, seg); v != nil {
					p.Said = append(p.Said, v)
				}
				p.trimSaid()
			}
		}
		p.Meetings[session] = true
		r.refreshParents(ctx, p)
		if p.Vector == nil {
			p.Vector = r.vectorFor(ctx, p.Node)
		}
		members = append(members, p)
	}
	r.profiles.noteMeeting(session, members)

	candidates := make(map[pairKey]*TopicPair)
	propose := func(a, b *TopicProfile, src CandidateSource, rank int) {
		key := keyOf(a.ID(), b.ID())
		if r.judged[key] {
			return
		}
		p, ok := candidates[key]
		if !ok {
			p = r.profiles.pair(a, b)
			candidates[key] = p
		}
		p.Sources |= src
		if rank > 0 && (p.NearestRank == 0 || rank < p.NearestRank) {
			p.NearestRank = rank
		}
	}
	for i := range members {
		for j := i + 1; j < len(members); j++ {
			propose(members[i], members[j], SourceSameMeeting, 0)
		}
	}
	all := r.profiles.All()
	for _, p := range members {
		if r.judgedTopics[p.ID()] {
			// Already placed among its neighbours; only the pairs this
			// meeting adds are new.
			continue
		}
		for _, n := range r.profiles.nearest(p, all, r.opts.NearestK) {
			propose(p, n.profile, SourceNearest, n.rank)
		}
		for _, n := range r.profiles.nearestSaid(p, all, saidK) {
			propose(p, n.profile, SourceSaid, 0)
		}
		for _, s := range r.profiles.siblings(p) {
			propose(p, s, SourceSharedParent, 0)
		}
	}
	pairs := make([]*TopicPair, 0, len(candidates))
	for _, p := range candidates {
		pairs = append(pairs, p)
	}
	sortPairs(pairs)
	return r.judgeAll(ctx, pairs)
}

// Pending counts the candidate pairs not yet judged, and what judging them
// would cost, without judging any.
func (r *TopicRelater) Pending(ctx context.Context) (pairs, approxTokens int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.load(ctx); err != nil {
		return 0, 0, err
	}
	n := len(r.unjudged())
	return n, n * approxTokensPerPair, nil
}

// RelateAll judges every candidate pair not yet judged, at most limit of
// them when limit is positive.
func (r *TopicRelater) RelateAll(ctx context.Context, limit int) (RelateStats, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.load(ctx); err != nil {
		return RelateStats{}, err
	}
	pairs := r.unjudged()
	if limit > 0 && len(pairs) > limit {
		pairs = pairs[:limit]
	}
	return r.judgeAll(ctx, pairs)
}

// judgeAll judges pairs in batches, several in flight at once, and records
// every verdict that comes back — even when a batch fails, the others are
// still answers. The lock is held throughout, so nothing else touches the
// profiles, and the writes are serialized through the results channel.
func (r *TopicRelater) judgeAll(ctx context.Context, pairs []*TopicPair) (RelateStats, error) {
	if len(pairs) == 0 {
		return RelateStats{}, nil
	}
	type batch struct {
		verdicts []*TopicVerdict
		judging  TopicJudging
		err      error
	}
	jobs := make(chan []*TopicPair)
	results := make(chan batch)
	var wg sync.WaitGroup
	for range r.opts.Concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pairs := range jobs {
				verdicts, judging, err := r.judge.Judge(ctx, pairs)
				select {
				case results <- batch{verdicts, judging, err}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for start := 0; start < len(pairs); start += maxPairsPerRequest {
			end := min(start+maxPairsPerRequest, len(pairs))
			select {
			case jobs <- pairs[start:end]:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	var stats RelateStats
	var firstErr error
	done := 0
	for b := range results {
		if b.err != nil && firstErr == nil {
			firstErr = b.err
		}
		st, err := r.record(ctx, b.verdicts, b.judging)
		stats.Add(st)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		done += len(b.verdicts)
		// Progress is worth a line on a long run, not on a meeting's few.
		if len(pairs) >= 500 && done%500 < len(b.verdicts) {
			r.opts.Log("  related %d of %d pairs (%d requests, %d tokens)", done, len(pairs), stats.Judging.Requests, stats.Judging.InputTokens)
		}
	}
	if firstErr != nil {
		return stats, firstErr
	}
	return stats, ctx.Err()
}

// unjudged returns the candidate pairs no edge exists for.
func (r *TopicRelater) unjudged() []*TopicPair {
	var out []*TopicPair
	for _, p := range r.profiles.Candidates(r.opts.NearestK) {
		if !r.judged[p.Key()] {
			out = append(out, p)
		}
	}
	return out
}

// record writes verdicts as edges and marks the pairs judged.
func (r *TopicRelater) record(ctx context.Context, verdicts []*TopicVerdict, judging TopicJudging) (RelateStats, error) {
	stats := RelateStats{Judging: judging}
	for _, v := range verdicts {
		if err := r.writeEdge(ctx, v, judging.Model); err != nil {
			r.stats.Add(stats)
			return stats, err
		}
		r.judged[v.Pair.Key()] = true
		r.judgedTopics[v.Pair.A.ID()] = true
		r.judgedTopics[v.Pair.B.ID()] = true
		stats.Pairs++
		if v.Related >= BreadthDefault.minProbability() {
			stats.Related++
		}
	}
	r.stats.Add(stats)
	return stats, nil
}

// writeEdge records one verdict. The edge id is deterministic in the pair,
// so judging a pair again replaces the earlier answer.
func (r *TopicRelater) writeEdge(ctx context.Context, v *TopicVerdict, model string) error {
	a, b := v.Pair.A.ID(), v.Pair.B.ID()
	if a > b {
		a, b = b, a
	}
	props := map[string]string{
		PropRelatedProbability: strconv.FormatFloat(v.Related, 'f', 2, 64),
		PropSameSubject:        strconv.FormatFloat(v.Same, 'f', 2, 64),
		propRelatedSources:     v.Pair.Sources.String(),
	}
	if model != "" {
		props[propRelatedJudge] = model
	}
	if !math.IsNaN(v.Pair.Cosine) {
		props[propRelatedCosine] = strconv.FormatFloat(v.Pair.Cosine, 'f', 2, 64)
	}
	edge := &graph.Edge{
		ID:         graph.NewNodeID("edge", a, b+":"+string(graph.EdgeRelatedTo)),
		Type:       graph.EdgeRelatedTo,
		SourceID:   a,
		TargetID:   b,
		Properties: props,
	}
	if err := r.store.AddEdge(ctx, edge); err != nil {
		return fmt.Errorf("add RelatedTo edge: %w", err)
	}
	return nil
}

// refreshParents rereads where a topic sits in the tree, which the writer
// may have just changed.
func (r *TopicRelater) refreshParents(ctx context.Context, p *TopicProfile) {
	parents, err := r.store.GetNeighbors(ctx, p.ID(), graph.EdgeContains, graph.Incoming)
	if err != nil {
		return
	}
	p.Parents = p.Parents[:0]
	for _, parent := range parents {
		if parent.Type == graph.NodeTopic {
			p.Parents = append(p.Parents, parent.ID)
		}
	}
	sort.Strings(p.Parents)
}

// vectorFor returns a node's embedding: the stored one when the index has
// it, otherwise one computed now so a label or segment written a moment ago
// can still be placed among the others; nil when neither is possible.
func (r *TopicRelater) vectorFor(ctx context.Context, n *graph.Node) []float32 {
	if r.opts.Vectors != nil {
		if v, ok := r.opts.Vectors(n.ID); ok {
			return v
		}
	}
	if r.opts.Embed == nil {
		return nil
	}
	text := vectorstore.EmbeddableText(n)
	if text == "" {
		return nil
	}
	v, err := r.opts.Embed(ctx, text)
	if err != nil {
		r.opts.Log("warning: could not embed %s %q: %v", n.Type, n.Name, err)
		return nil
	}
	return v
}

// RelatedTopic is a neighbour of a topic in the adjacency graph.
type RelatedTopic struct {
	Topic *graph.Node
	// Probability is the judge's probability that the two are related.
	Probability float64
	// Same is the judge's probability that the labels name one subject.
	Same float64
}

// RelatedTopics returns the topics judged related to one at or above a
// probability, most probable first. Every judged pair is stored, so the
// threshold is not optional: at zero it lists the pairs found unrelated too.
//
// An absent edge is not evidence of unrelatedness. Measured against
// hand-labelled pairs, the default gate admits only related pairs but
// roughly two related pairs in five fall below it, and a pair no signal
// proposed was never judged at all.
func RelatedTopics(ctx context.Context, store graph.Store, topicID string, minProbability float64) ([]RelatedTopic, error) {
	edges, err := store.GetEdges(ctx, topicID, graph.EdgeRelatedTo)
	if err != nil {
		return nil, err
	}
	var out []RelatedTopic
	for _, e := range edges {
		p, err := strconv.ParseFloat(e.Properties[PropRelatedProbability], 64)
		if err != nil || p < minProbability {
			continue
		}
		other := e.TargetID
		if other == topicID {
			other = e.SourceID
		}
		n, err := store.GetNode(ctx, other)
		if err != nil || n == nil {
			continue
		}
		same, _ := strconv.ParseFloat(e.Properties[PropSameSubject], 64)
		out = append(out, RelatedTopic{Topic: n, Probability: p, Same: same})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Probability != out[j].Probability {
			return out[i].Probability > out[j].Probability
		}
		return out[i].Topic.Name < out[j].Topic.Name
	})
	return out, nil
}
