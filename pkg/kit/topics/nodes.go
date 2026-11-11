package topics

import (
	"strings"

	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
)

// Normalize reduces an extracted topic label to a key for deduplication:
// lowercase, trimmed, single-spaced.
func Normalize(topic string) string {
	topic = strings.ToLower(strings.TrimSpace(topic))
	return strings.Join(strings.Fields(topic), " ")
}

// NewNodes builds a Topic node for each distinct label and a HasTopic edge
// to it from the node fromID, for a caller that writes them itself. Labels
// are normalized and deduplicated; the node IDs depend on the normalized
// label alone, so every node that names a topic points at the same one.
//
// Unlike Registry.Resolve this merges nothing but exact rewordings of case
// and spacing, and reads nothing from the graph.
func NewNodes(labels []string, fromID string) ([]*graph.Node, []*graph.Edge) {
	var nodes []*graph.Node
	var edges []*graph.Edge

	seen := make(map[string]bool)
	for _, raw := range labels {
		normalized := Normalize(raw)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true

		topicID := graph.NewNodeID(string(graph.NodeTopic), "", normalized)
		nodes = append(nodes, &graph.Node{
			ID:            topicID,
			Type:          graph.NodeTopic,
			Name:          normalized,
			QualifiedName: normalized,
		})
		edges = append(edges, hasTopicEdge(fromID, topicID))
	}

	return nodes, edges
}

// hasTopicEdge files the node from under the topic to. The ID depends only
// on the two ends, so filing a node under a topic again replaces the edge.
func hasTopicEdge(from, to string) *graph.Edge {
	return &graph.Edge{
		ID:       graph.NewNodeID("edge", from, to+":"+string(graph.EdgeHasTopic)),
		Type:     graph.EdgeHasTopic,
		SourceID: from,
		TargetID: to,
	}
}
