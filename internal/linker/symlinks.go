package linker

import (
	"context"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// linkSymlinks resolves symlink_target properties on file nodes and creates
// EdgeSymLink edges pointing from the symlink node to the target node.
func (l *Linker) linkSymlinks(ctx context.Context) (int, error) {
	linked := 0
	seen := make(map[string]bool)

	for _, nt := range fileNodeTypes {
		nodes, err := l.store.QueryNodes(ctx, graph.NodeFilter{Type: nt})
		if err != nil {
			continue
		}

		for _, n := range nodes {
			if n.Properties == nil {
				continue
			}
			target := n.Properties[graph.PropSymlinkTarget]
			if target == "" {
				continue
			}

			// Find the target node by searching all file-type node types.
			var targetNode *graph.Node
			for _, tt := range fileNodeTypes {
				targets, err := l.store.QueryNodes(ctx, graph.NodeFilter{
					Type:     tt,
					FilePath: target,
				})
				if err != nil || len(targets) == 0 {
					continue
				}
				targetNode = targets[0]
				break
			}

			if targetNode == nil {
				if l.verbose {
					l.log("    SymLink target not found: %s -> %s", n.FilePath, target)
				}
				continue
			}

			edgeKey := n.ID + "→" + targetNode.ID
			if seen[edgeKey] {
				continue
			}
			seen[edgeKey] = true

			edge := &graph.Edge{
				ID:       graph.NewNodeID(string(graph.EdgeSymLink), n.ID, targetNode.ID),
				Type:     graph.EdgeSymLink,
				SourceID: n.ID,
				TargetID: targetNode.ID,
			}
			if err := l.store.AddEdge(ctx, edge); err != nil {
				if l.verbose {
					l.log("  Warning: add SymLink edge: %v", err)
				}
				continue
			}
			linked++
			if l.verbose {
				l.log("    SymLink: %s -> %s", n.FilePath, targetNode.FilePath)
			}
		}
	}

	return linked, nil
}
