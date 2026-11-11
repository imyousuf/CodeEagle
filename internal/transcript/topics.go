package transcript

import (
	"context"

	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
	"github.com/imyousuf/CodeEagle/pkg/kit/topics"
)

// TopicRegistry resolves a subject named in one meeting to a durable topic in
// the graph, collapsing the rewordings each meeting chooses into one node, so
// HasTopic indexes something. The per-meeting wording is not lost: it stays
// on the TopicSegment, which is where the detail belongs.
type TopicRegistry = topics.Registry

// LoadTopicRegistry reads the topics already in the graph.
func LoadTopicRegistry(ctx context.Context, store graph.Store) (*TopicRegistry, error) {
	return topics.LoadRegistry(ctx, store)
}

// propTopicUses counts how many meetings have used a topic, so the vocabulary
// offered back to the model leads with the labels that have proven reusable.
const propTopicUses = topics.PropTopicUses

// CleanTopic normalizes a topic label for use as a shared subject.
func CleanTopic(s string) string { return topics.CleanTopic(s) }

// SameTopic reports whether two labels name the same subject.
func SameTopic(a, b string) bool { return topics.SameTopic(a, b) }
