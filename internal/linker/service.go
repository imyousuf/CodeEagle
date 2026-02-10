package linker

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// linkServices ensures each top-level directory group has a NodeService node
// and creates EdgeContains edges from services to their file nodes.
func (l *Linker) linkServices(ctx context.Context) (int, error) {
	// Query all nodes and group by top-level directory.
	allNodes, err := l.store.QueryNodes(ctx, graph.NodeFilter{})
	if err != nil {
		return 0, err
	}

	// Build service index: which top-level groups already have a NodeService?
	existingServices := make(map[string]*graph.Node)
	for _, n := range allNodes {
		if n.Type == graph.NodeService {
			group := topDir(n.FilePath)
			existingServices[group] = n
		}
	}

	// Group file nodes by top-level directory.
	fileGroups := make(map[string][]*graph.Node)
	for _, n := range allNodes {
		if n.Type != graph.NodeFile {
			continue
		}
		group := topDir(n.FilePath)
		if group == "" {
			continue
		}
		fileGroups[group] = append(fileGroups[group], n)
	}

	linked := 0
	for group, files := range fileGroups {
		svc, exists := existingServices[group]
		if !exists {
			// Create auto-detected service node.
			svcID := graph.NewNodeID(string(graph.NodeService), group, group)
			svc = &graph.Node{
				ID:   svcID,
				Type: graph.NodeService,
				Name: group,
				Properties: map[string]string{
					"kind": "auto_detected",
				},
			}
			if err := l.store.AddNode(ctx, svc); err != nil {
				if l.verbose {
					l.log("  Warning: add auto-detected service %s: %v", group, err)
				}
				continue
			}
		}

		// Create EdgeContains from service → each file.
		for _, fileNode := range files {
			edge := &graph.Edge{
				ID:       graph.NewNodeID(string(graph.EdgeContains), svc.ID, fileNode.ID),
				Type:     graph.EdgeContains,
				SourceID: svc.ID,
				TargetID: fileNode.ID,
			}
			if err := l.store.AddEdge(ctx, edge); err != nil {
				// Ignore duplicate edge errors.
				continue
			}
		}
		linked++
	}

	return linked, nil
}

// topDir extracts the top-level directory from a file path.
// For "hypatia/src/main.py" it returns "hypatia".
// For "main.py" (root level) it returns "(root)".
func topDir(filePath string) string {
	if filePath == "" {
		return ""
	}
	// Handle relative paths (which is the standard in CodeEagle).
	parts := strings.SplitN(filepath.ToSlash(filePath), "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return "(root)"
	}
	// If there's no separator, the file is at root level.
	if len(parts) == 1 {
		return "(root)"
	}
	return parts[0]
}
