package linker

import (
	"context"
	"sort"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// fileNodeTypes lists the node types that can be duplicates.
var fileNodeTypes = []graph.NodeType{
	graph.NodeFile,
	graph.NodeTestFile,
	graph.NodeDocument,
	graph.NodeAIGuideline,
}

// linkDuplicates finds groups of file nodes with identical content and creates
// EdgeDuplicateOf edges between them. Files are grouped by (content_hash, mime_type).
// Within each group, a star topology is used: all nodes point to the canonical
// node (the one with the lexicographically smallest FilePath).
func (l *Linker) linkDuplicates(ctx context.Context) (int, error) {
	// Collect all file-type nodes with content_hash.
	type groupKey struct {
		hash     string
		mimeType string
	}
	groups := make(map[groupKey][]*graph.Node)

	for _, nt := range fileNodeTypes {
		nodes, err := l.store.QueryNodes(ctx, graph.NodeFilter{Type: nt})
		if err != nil {
			continue
		}
		for _, n := range nodes {
			if n.Properties == nil {
				continue
			}
			h := n.Properties[graph.PropContentHash]
			if h == "" {
				continue
			}
			key := groupKey{hash: h, mimeType: n.Properties[graph.PropMimeType]}
			groups[key] = append(groups[key], n)
		}
	}

	linked := 0
	seen := make(map[string]bool)

	for _, nodes := range groups {
		if len(nodes) < 2 {
			continue
		}

		// Sort by FilePath so the canonical node is deterministic.
		sort.Slice(nodes, func(i, j int) bool {
			return nodes[i].FilePath < nodes[j].FilePath
		})

		canonical := nodes[0]
		for _, n := range nodes[1:] {
			edgeKey := n.ID + "→" + canonical.ID
			if seen[edgeKey] {
				continue
			}
			seen[edgeKey] = true

			edge := &graph.Edge{
				ID:       graph.NewNodeID(string(graph.EdgeDuplicateOf), n.ID, canonical.ID),
				Type:     graph.EdgeDuplicateOf,
				SourceID: n.ID,
				TargetID: canonical.ID,
				Properties: map[string]string{
					graph.PropContentHash: n.Properties[graph.PropContentHash],
					graph.PropMimeType:    n.Properties[graph.PropMimeType],
				},
			}
			if err := l.store.AddEdge(ctx, edge); err != nil {
				if l.verbose {
					l.log("  Warning: add DuplicateOf edge: %v", err)
				}
				continue
			}
			linked++
			if l.verbose {
				l.log("    Duplicate: %s -> %s", n.FilePath, canonical.FilePath)
			}
		}
	}

	return linked, nil
}
