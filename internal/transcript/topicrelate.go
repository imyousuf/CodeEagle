package transcript

import (
	"context"

	"github.com/imyousuf/CodeEagle/internal/vectorstore"
	"github.com/imyousuf/CodeEagle/pkg/jev"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
	"github.com/imyousuf/CodeEagle/pkg/kit/topics"
)

// Property keys on a RelatedTo edge.
const (
	// PropRelatedProbability is the judge's probability that a meeting filed
	// under either topic is worth showing to someone asking about the other.
	PropRelatedProbability = topics.PropRelatedProbability
	// PropSameSubject is the judge's probability that the two labels are two
	// wordings of one subject.
	PropSameSubject = topics.PropSameSubject
	// propRelatedSources names the signals that proposed the pair.
	propRelatedSources = topics.PropRelatedSources
	// propRelatedJudge is the model that answered.
	propRelatedJudge = topics.PropRelatedJudge
)

// maxSiblingFamily caps how large a parent's family may be for its children
// to propose each other as candidates.
const maxSiblingFamily = topics.MaxFamily

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
type RelateStats = topics.Stats

// TopicRelater proposes pairs of topics, has them judged, and records the
// verdicts as edges: pkg/kit/topics' relater, reading meetings.
//
// It works incrementally: each meeting written proposes the pairs it
// introduces — its own topics with each other, and for a topic new to the
// graph its nearest labels and the other children of its parent — and only
// pairs not yet judged are sent. A bulk pass over every candidate is the
// repair tool for a corpus indexed before this existed, not the mechanism.
type TopicRelater struct {
	*topics.Relater
	opts RelaterOptions
}

// NewTopicRelater builds a relater over a decision model. A nil asker gives
// a relater that proposes pairs but asks nothing and writes no edge.
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
	kitOpts := append(meetingJudgeOptions(),
		topics.WithProfiles(LoadTopicProfiles),
		topics.WithGenerators(topics.DefaultGenerators(opts.NearestK)...),
		topics.WithConcurrency(opts.Concurrency),
		topics.WithVectors(opts.Vectors),
		topics.WithEmbed(opts.Embed),
		topics.WithEmbedText(vectorstore.EmbeddableText),
		topics.WithGate(BreadthDefault.MinProbability()),
		topics.WithSourceNames(meetingSourceNames),
		topics.WithLog(opts.Log),
	)
	return &TopicRelater{Relater: topics.NewRelater(store, topicDecider(asker), kitOpts...), opts: opts}
}

// RelateMeeting judges the pairs one newly written meeting introduces.
//
// session is the meeting's identifier, filedUnder the topic nodes it was
// filed under, and segments the meeting's segment under each topic, keyed by
// topic id.
func (r *TopicRelater) RelateMeeting(ctx context.Context, session string, filedUnder []*graph.Node, segments map[string]*graph.Node) (RelateStats, error) {
	return r.Relate(ctx, session, filedUnder, segments)
}

// Pending counts the candidate pairs not yet judged, and what judging them
// would cost, without judging any.
func (r *TopicRelater) Pending(ctx context.Context) (pairs, approxTokens int, err error) {
	n, err := r.Relater.Pending(ctx)
	if err != nil {
		return 0, 0, err
	}
	return n, n * approxTokensPerPair, nil
}

// RelatedTopic is a neighbour of a topic in the adjacency graph.
type RelatedTopic = topics.Related

// RelatedTopics returns the topics judged related to one at or above a
// probability, most probable first. Every judged pair is stored, so the
// threshold is not optional: at zero it lists the pairs found unrelated too.
func RelatedTopics(ctx context.Context, store graph.Store, topicID string, minProbability float64) ([]RelatedTopic, error) {
	return topics.RelatedTopics(ctx, store, topicID, minProbability)
}
