package generic

import (
	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
	"github.com/imyousuf/CodeEagle/pkg/kit/topics"
)

// CreateTopicNodes creates NodeTopic nodes and EdgeHasTopic edges from an
// extraction result, linking topics to the given document node ID.
// Topics are normalized and deduplicated within the result.
func CreateTopicNodes(labels []string, docNodeID string) ([]*graph.Node, []*graph.Edge) {
	return topics.NewNodes(labels, docNodeID)
}

// NormalizeTopic normalizes a topic name for deduplication.
// Lowercase, trim whitespace, collapse multiple spaces.
func NormalizeTopic(topic string) string { return topics.Normalize(topic) }
