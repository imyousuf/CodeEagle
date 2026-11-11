package transcript

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
	"github.com/imyousuf/CodeEagle/pkg/kit/topics"
)

// Every meeting names its subject in its own words, so a corpus of six
// hundred meetings carries two thousand topic labels of which most are used
// exactly once. pkg/kit/topics keeps every label as it is and puts the
// commonality on judged edges between them; what is meeting-specific is
// here: which topics count (those a meeting was filed under), what a topic's
// uses are (the meetings), what was said under it (its segments' summaries),
// which topics were adjacent (consecutive segments), and a verified decision
// quote.

// The kit's relating types, under the names this package has always used.
type (
	// TopicProfile is what is known about one topic when relating it to
	// others; its Uses are the session identifiers of the meetings filed
	// under it directly.
	TopicProfile = topics.Profile
	// TopicProfiles holds every topic meetings were filed under.
	TopicProfiles = topics.Profiles
	// TopicPair is two topics proposed as possibly related; Shared counts
	// the meetings filed under both.
	TopicPair = topics.Pair
	// CandidateSource says which signal proposed a pair.
	CandidateSource = topics.Source

	pairKey = topics.PairKey
)

// The signals, by the names this package uses for them.
const (
	SourceNearest      = topics.SourceNearest
	SourceSameMeeting  = topics.SourceSharedUse
	SourceAdjacent     = topics.SourceAdjacent
	SourceSharedParent = topics.SourceSharedParent
	SourceSaid         = topics.SourceSaid
)

// meetingSourceNames are the sources as RelatedTo edges record them: a
// shared use is a shared meeting.
var meetingSourceNames = map[topics.Source]string{topics.SourceSharedUse: "same-meeting"}

// DefaultNearestK is how many embedding neighbours each topic proposes by
// its label.
const DefaultNearestK = topics.DefaultNearestK

func keyOf(a, b string) pairKey { return topics.KeyOf(a, b) }

func cosine(a, b []float32) float64 { return topics.Cosine(a, b) }

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
	tp := topics.NewProfiles()

	profile := func(id string) (*TopicProfile, error) {
		if p := tp.Get(id); p != nil {
			return p, nil
		}
		n, err := store.GetNode(ctx, id)
		if err != nil || n == nil || n.Type != graph.NodeTopic {
			return nil, err
		}
		return tp.Ensure(n), nil
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
		var members []*TopicProfile
		for _, e := range edges {
			if e.SourceID != m.ID {
				continue
			}
			p, err := profile(e.TargetID)
			if err != nil || p == nil {
				continue
			}
			if !p.Uses[m.QualifiedName] {
				p.Uses[m.QualifiedName] = true
				members = append(members, p)
			}
		}
		tp.CountUse(m.QualifiedName, members)
	}

	// What was said under each topic, and which topics followed which.
	segments, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeTopicSegment})
	if err != nil {
		return nil, fmt.Errorf("query segments: %w", err)
	}
	type placed struct {
		start   float64
		topicID string
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
				p.SaidIDs = append(p.SaidIDs, s.ID)
			}
			start, _ := strconv.ParseFloat(s.Properties[graph.PropStartTime], 64)
			order[key] = append(order[key], placed{start: start, topicID: p.ID()})
		}
	}
	for _, segs := range order {
		sort.Slice(segs, func(i, j int) bool { return segs[i].start < segs[j].start })
		for i := 1; i < len(segs); i++ {
			if segs[i-1].topicID != segs[i].topicID {
				tp.AddAdjacent(segs[i-1].topicID, segs[i].topicID)
			}
		}
	}
	for _, p := range tp.All() {
		p.TrimSaid()
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
			if p := tp.Get(e.TargetID); p != nil && p.Quote == "" {
				p.Quote = quote
			}
		}
	}

	// Where each topic sits in the tree.
	for _, p := range tp.All() {
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
