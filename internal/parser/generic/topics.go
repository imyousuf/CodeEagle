package generic

import (
	"github.com/imyousuf/CodeEagle/internal/graph"
)

// CreateTopicNodes creates NodeTopic nodes and EdgeHasTopic edges from an
// extraction result, linking topics to the given document node ID.
// Topics are normalized and deduplicated within the result.
func CreateTopicNodes(topics []string, docNodeID string) ([]*graph.Node, []*graph.Edge) {
	var nodes []*graph.Node
	var edges []*graph.Edge

	seen := make(map[string]bool)
	for _, raw := range topics {
		normalized := NormalizeTopic(raw)
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

		edgeID := graph.NewNodeID("edge", docNodeID, topicID+":HasTopic")
		edges = append(edges, &graph.Edge{
			ID:       edgeID,
			Type:     graph.EdgeHasTopic,
			SourceID: docNodeID,
			TargetID: topicID,
		})
	}

	return nodes, edges
}
