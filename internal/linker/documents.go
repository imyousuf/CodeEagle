package linker

import (
	"context"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// linkDocuments scans NodeDocument DocComments for references to code entities
// (function names, file paths, package names) and creates EdgeDocuments edges.
func (l *Linker) linkDocuments(ctx context.Context) (int, error) {
	docs, err := l.store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeDocument})
	if err != nil {
		return 0, err
	}

	if len(docs) == 0 {
		return 0, nil
	}

	// Build a lookup of code entity names → node IDs.
	codeTypes := []graph.NodeType{
		graph.NodeFunction, graph.NodeMethod, graph.NodeClass, graph.NodeStruct,
		graph.NodeInterface, graph.NodePackage, graph.NodeFile,
	}

	nameToNodes := make(map[string][]string) // lowercase name → node IDs
	for _, nt := range codeTypes {
		nodes, err := l.store.QueryNodes(ctx, graph.NodeFilter{Type: nt})
		if err != nil {
			continue
		}
		for _, n := range nodes {
			lower := strings.ToLower(n.Name)
			if len(lower) < 3 {
				continue // skip very short names to avoid false positives
			}
			nameToNodes[lower] = append(nameToNodes[lower], n.ID)
		}
	}

	linked := 0
	seen := make(map[string]bool)

	for _, doc := range docs {
		text := strings.ToLower(doc.DocComment)
		if len(text) < 20 {
			continue
		}

		for name, nodeIDs := range nameToNodes {
			if !strings.Contains(text, name) {
				continue
			}
			for _, targetID := range nodeIDs {
				edgeKey := doc.ID + "→" + targetID
				if seen[edgeKey] {
					continue
				}
				seen[edgeKey] = true

				edgeID := graph.NewNodeID("edge", doc.ID, targetID+":Documents")
				edge := &graph.Edge{
					ID:       edgeID,
					Type:     graph.EdgeDocuments,
					SourceID: doc.ID,
					TargetID: targetID,
				}
				if err := l.store.AddEdge(ctx, edge); err != nil {
					if l.verbose {
						l.log("  Warning: add documents edge: %v", err)
					}
					continue
				}
				linked++
			}
		}
	}

	return linked, nil
}
