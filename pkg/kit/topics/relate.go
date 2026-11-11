package topics

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/imyousuf/CodeEagle/pkg/kit/decide"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
)

// Property keys on a RelatedTo edge.
const (
	// PropRelatedProbability is the judge's probability that something
	// filed under either topic is worth showing to someone asking about the
	// other.
	PropRelatedProbability = "probability"
	// PropSameSubject is the judge's probability that the two labels are two
	// wordings of one subject.
	PropSameSubject = "same_subject"
	// PropRelatedCosine is the similarity of the two labels' embeddings,
	// when both had one.
	PropRelatedCosine = "cosine"
	// PropRelatedSources names the signals that proposed the pair.
	PropRelatedSources = "sources"
	// PropRelatedJudge is the model that answered.
	PropRelatedJudge = "judge"
)

// Option configures a Relater or a PairJudge. Options that concern only one
// of them are ignored by the other.
type Option func(*config)

type config struct {
	// The judge's.
	card      Card
	questions Questions
	batch     int

	// The relater's.
	generators  []Generator
	load        func(ctx context.Context, store graph.Store) (*Profiles, error)
	concurrency int
	vectors     func(nodeID string) ([]float32, bool)
	embed       func(ctx context.Context, text string) ([]float32, error)
	embedText   func(n *graph.Node) string
	saidText    func(n *graph.Node) string
	gate        float64
	sourceNames map[Source]string
	log         func(string, ...any)
}

func newConfig(opts []Option) config {
	cfg := config{
		card:        DefaultCard,
		questions:   DefaultQuestions,
		batch:       DefaultBatchSize,
		concurrency: 4,
		embedText:   defaultEmbedText,
		saidText:    summaryOf,
		gate:        DefaultRelatedGate,
		log:         func(string, ...any) {},
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.generators == nil {
		cfg.generators = DefaultGenerators(DefaultNearestK)
	}
	if cfg.load == nil {
		said := cfg.saidText
		cfg.load = func(ctx context.Context, store graph.Store) (*Profiles, error) {
			return loadProfiles(ctx, store, said)
		}
	}
	return cfg
}

// WithCard sets what the judge is shown of a batch; DefaultCard otherwise.
func WithCard(card Card) Option {
	return func(c *config) {
		if card != nil {
			c.card = card
		}
	}
}

// WithQuestions sets the two questions asked about each pair;
// DefaultQuestions otherwise.
func WithQuestions(q Questions) Option {
	return func(c *config) {
		if q.Related != nil && q.Same != nil {
			c.questions = q
		}
	}
}

// WithBatchSize sets how many pairs share one judge call; DefaultBatchSize
// otherwise.
func WithBatchSize(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.batch = n
		}
	}
}

// WithGenerators sets the signals that propose pairs; DefaultGenerators
// otherwise. Passing none proposes nothing.
func WithGenerators(gens ...Generator) Option {
	return func(c *config) { c.generators = append([]Generator{}, gens...) }
}

// WithProfiles sets how the topics and what they share are read from the
// graph; LoadProfiles otherwise, reading what was said with WithSaidText.
func WithProfiles(load func(ctx context.Context, store graph.Store) (*Profiles, error)) Option {
	return func(c *config) { c.load = load }
}

// WithConcurrency sets how many judge calls a bulk run keeps in flight;
// four otherwise.
func WithConcurrency(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.concurrency = n
		}
	}
}

// WithVectors reads a node's stored embedding, when there is a vector index.
// Without it, or an embedder, the nearest-neighbour signals are silent and
// pairs come from co-occurrence and the tree alone.
func WithVectors(lookup func(nodeID string) ([]float32, bool)) Option {
	return func(c *config) { c.vectors = lookup }
}

// WithEmbed places a node that has no stored vector yet -- a topic minted a
// moment ago -- among the ones that do, by embedding its WithEmbedText.
func WithEmbed(embed func(ctx context.Context, text string) ([]float32, error)) Option {
	return func(c *config) { c.embed = embed }
}

// WithEmbedText sets the text a node is embedded from when it has no stored
// vector. It must match how the stored vectors were made, or the two are
// not comparable. The default is the node's name, then its doc comment.
func WithEmbedText(text func(n *graph.Node) string) Option {
	return func(c *config) {
		if text != nil {
			c.embedText = text
		}
	}
}

// WithSaidText sets what a node said under a topic it is filed under; its
// graph.PropSummary property otherwise.
func WithSaidText(text func(n *graph.Node) string) Option {
	return func(c *config) {
		if text != nil {
			c.saidText = text
		}
	}
}

// WithGate sets the probability at or above which a verdict counts as
// related in Stats; DefaultRelatedGate otherwise. Every verdict is stored
// either way.
func WithGate(p float64) Option {
	return func(c *config) { c.gate = p }
}

// WithSourceNames renames sources in the edges' PropRelatedSources property,
// for a graph that already records them under other names.
func WithSourceNames(names map[Source]string) Option {
	return func(c *config) { c.sourceNames = names }
}

// WithLog reports progress and the failures that do not stop a run.
func WithLog(log func(string, ...any)) Option {
	return func(c *config) {
		if log != nil {
			c.log = log
		}
	}
}

// defaultEmbedText is a node's name, then its doc comment when it has one.
func defaultEmbedText(n *graph.Node) string {
	if n.DocComment == "" {
		return n.Name
	}
	return n.Name + "\n" + n.DocComment
}

// Stats reports what relating did.
type Stats struct {
	// Pairs is how many pairs were judged and written.
	Pairs int
	// Related is how many of them cleared the gate.
	Related int
	Judging Judging
}

// Add accumulates another round.
func (s *Stats) Add(o Stats) {
	s.Pairs += o.Pairs
	s.Related += o.Related
	s.Judging.Add(o.Judging)
}

// Relater proposes pairs of topics, has them judged, and records every
// verdict as a RelatedTo edge, related or not, so no pair is asked about
// twice.
//
// It works incrementally: each use reported with Relate proposes the pairs
// it introduces -- its own topics with each other, and for a topic new to
// the graph its neighbours by every generator -- and only pairs not yet
// judged are sent. A bulk pass with RelateAll is the repair tool for a
// corpus indexed before relating existed, not the mechanism.
//
// A Relater is safe for concurrent use; calls are serialized.
type Relater struct {
	store graph.Store
	judge *PairJudge
	cfg   config

	mu       sync.Mutex
	profiles *Profiles
	// judged holds every pair an edge exists for, related or not, and
	// judgedTopics every topic that has at least one.
	judged       map[PairKey]bool
	judgedTopics map[string]bool
	stats        Stats
}

// NewRelater builds a relater over judge. A nil judge is allowed: pairs are
// proposed, nothing is asked, and no edge is written.
func NewRelater(store graph.Store, judge decide.Judge, opts ...Option) *Relater {
	cfg := newConfig(opts)
	return &Relater{store: store, judge: &PairJudge{judge: judge, cfg: cfg}, cfg: cfg}
}

// Stats reports everything this relater has done.
func (r *Relater) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats
}

// load reads the topics and the pairs already judged, once.
func (r *Relater) load(ctx context.Context) error {
	if r.profiles != nil {
		return nil
	}
	profiles, err := r.cfg.load(ctx, r.store)
	if err != nil {
		return err
	}
	if r.cfg.vectors != nil {
		profiles.WithVectors(r.cfg.vectors)
	}
	judged := make(map[PairKey]bool)
	judgedTopics := make(map[string]bool)
	for _, p := range profiles.All() {
		edges, err := r.store.GetEdges(ctx, p.ID(), graph.EdgeRelatedTo)
		if err != nil {
			return fmt.Errorf("related edges of %q: %w", p.Node.Name, err)
		}
		for _, e := range edges {
			judged[KeyOf(e.SourceID, e.TargetID)] = true
			judgedTopics[e.SourceID] = true
			judgedTopics[e.TargetID] = true
		}
	}
	r.profiles, r.judged, r.judgedTopics = profiles, judged, judgedTopics
	return nil
}

// Relate judges the pairs one newly written use introduces.
//
// use identifies the thing written -- a meeting, a document -- topics are
// the nodes it was filed under, and said holds, by topic id, the node that
// says what this use said under each topic (WithSaidText reads it).
func (r *Relater) Relate(ctx context.Context, use string, topics []*graph.Node, said map[string]*graph.Node) (Stats, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.load(ctx); err != nil {
		return Stats{}, err
	}

	var members, fresh []*Profile
	for _, n := range topics {
		p := r.profiles.Ensure(n)
		// A use written again keeps one copy of what it said.
		if node := said[n.ID]; node != nil && !p.Uses[use] {
			if s := strings.TrimSpace(r.cfg.saidText(node)); s != "" {
				p.Summaries = append(p.Summaries, s)
				p.SaidIDs = append(p.SaidIDs, node.ID)
				if v := r.vectorFor(ctx, node); v != nil {
					p.Said = append(p.Said, v)
				}
				p.TrimSaid()
			}
		}
		p.Uses[use] = true
		r.refreshParents(ctx, p)
		if p.Vector == nil {
			p.Vector = r.vectorFor(ctx, p.Node)
		}
		members = append(members, p)
		if !r.judgedTopics[p.ID()] {
			// Already placed among its neighbours otherwise; only the
			// pairs this use adds are new.
			fresh = append(fresh, p)
		}
	}
	r.profiles.NoteUse(use, members)

	candidates := make(map[PairKey]*Pair)
	propose := func(a, b *Profile, src Source, rank int) {
		key := KeyOf(a.ID(), b.ID())
		if r.judged[key] {
			return
		}
		p, ok := candidates[key]
		if !ok {
			p = r.profiles.Pair(a, b)
			candidates[key] = p
		}
		p.note(src, rank)
	}
	for _, g := range r.cfg.generators {
		g.ProposeFor(r.profiles, members, fresh, propose)
	}
	pairs := make([]*Pair, 0, len(candidates))
	for _, p := range candidates {
		pairs = append(pairs, p)
	}
	SortPairs(pairs)
	return r.judgeAll(ctx, pairs)
}

// Pending counts the candidate pairs not yet judged, without judging any.
func (r *Relater) Pending(ctx context.Context) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.load(ctx); err != nil {
		return 0, err
	}
	return len(r.unjudged()), nil
}

// RelateAll judges every candidate pair not yet judged, at most limit of
// them when limit is positive.
func (r *Relater) RelateAll(ctx context.Context, limit int) (Stats, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.load(ctx); err != nil {
		return Stats{}, err
	}
	pairs := r.unjudged()
	if limit > 0 && len(pairs) > limit {
		pairs = pairs[:limit]
	}
	return r.judgeAll(ctx, pairs)
}

// judgeAll judges pairs in batches, several in flight at once, and records
// every verdict that comes back -- even when a batch fails, the others are
// still answers. The lock is held throughout, so nothing else touches the
// profiles, and the writes are serialized through the results channel.
func (r *Relater) judgeAll(ctx context.Context, pairs []*Pair) (Stats, error) {
	if len(pairs) == 0 {
		return Stats{}, nil
	}
	type batch struct {
		verdicts []*Verdict
		judging  Judging
		err      error
	}
	jobs := make(chan []*Pair)
	results := make(chan batch)
	var wg sync.WaitGroup
	for range r.cfg.concurrency {
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
		for start := 0; start < len(pairs); start += r.cfg.batch {
			end := min(start+r.cfg.batch, len(pairs))
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

	var stats Stats
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
		// Progress is worth a line on a long run, not on one use's few.
		if len(pairs) >= 500 && done%500 < len(b.verdicts) {
			r.cfg.log("  related %d of %d pairs (%d requests, %d tokens)", done, len(pairs), stats.Judging.Requests, stats.Judging.InputTokens)
		}
	}
	if firstErr != nil {
		return stats, firstErr
	}
	return stats, ctx.Err()
}

// unjudged returns the candidate pairs no edge exists for.
func (r *Relater) unjudged() []*Pair {
	var out []*Pair
	for _, p := range r.profiles.Candidates(r.cfg.generators...) {
		if !r.judged[p.Key()] {
			out = append(out, p)
		}
	}
	return out
}

// record writes verdicts as edges and marks the pairs judged.
func (r *Relater) record(ctx context.Context, verdicts []*Verdict, judging Judging) (Stats, error) {
	stats := Stats{Judging: judging}
	for _, v := range verdicts {
		if err := r.writeEdge(ctx, v, judging.Model); err != nil {
			r.stats.Add(stats)
			return stats, err
		}
		r.judged[v.Pair.Key()] = true
		r.judgedTopics[v.Pair.A.ID()] = true
		r.judgedTopics[v.Pair.B.ID()] = true
		stats.Pairs++
		if v.Related >= r.cfg.gate {
			stats.Related++
		}
	}
	r.stats.Add(stats)
	return stats, nil
}

// writeEdge records one verdict. The edge id is deterministic in the pair,
// so judging a pair again replaces the earlier answer.
func (r *Relater) writeEdge(ctx context.Context, v *Verdict, model string) error {
	a, b := v.Pair.A.ID(), v.Pair.B.ID()
	if a > b {
		a, b = b, a
	}
	props := map[string]string{
		PropRelatedProbability: strconv.FormatFloat(v.Related, 'f', 2, 64),
		PropSameSubject:        strconv.FormatFloat(v.Same, 'f', 2, 64),
		PropRelatedSources:     v.Pair.Sources.Label(r.cfg.sourceNames),
	}
	if model != "" {
		props[PropRelatedJudge] = model
	}
	if !math.IsNaN(v.Pair.Cosine) {
		props[PropRelatedCosine] = strconv.FormatFloat(v.Pair.Cosine, 'f', 2, 64)
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
func (r *Relater) refreshParents(ctx context.Context, p *Profile) {
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
// it, otherwise one computed now so a label or passage written a moment ago
// can still be placed among the others; nil when neither is possible.
func (r *Relater) vectorFor(ctx context.Context, n *graph.Node) []float32 {
	if r.cfg.vectors != nil {
		if v, ok := r.cfg.vectors(n.ID); ok {
			return v
		}
	}
	if r.cfg.embed == nil {
		return nil
	}
	text := r.cfg.embedText(n)
	if text == "" {
		return nil
	}
	v, err := r.cfg.embed(ctx, text)
	if err != nil {
		r.cfg.log("warning: could not embed %s %q: %v", n.Type, n.Name, err)
		return nil
	}
	return v
}

// Related is a neighbour of a topic in the adjacency graph.
type Related struct {
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
func RelatedTopics(ctx context.Context, store graph.Store, topicID string, minProbability float64) ([]Related, error) {
	edges, err := store.GetEdges(ctx, topicID, graph.EdgeRelatedTo)
	if err != nil {
		return nil, err
	}
	var out []Related
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
		out = append(out, Related{Topic: n, Probability: p, Same: same})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Probability != out[j].Probability {
			return out[i].Probability > out[j].Probability
		}
		return out[i].Topic.Name < out[j].Topic.Name
	})
	return out, nil
}
