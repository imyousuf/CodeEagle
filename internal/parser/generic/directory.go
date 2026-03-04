package generic

import (
	"path/filepath"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// EnsureDirectoryHierarchy creates NodeDirectory nodes and EdgeContains edges
// for all ancestor directories of the given file path. It skips directories
// that already exist in the seen set to avoid duplicates within a parse session.
// Returns all created nodes and edges.
func EnsureDirectoryHierarchy(filePath string, seen map[string]bool) ([]*graph.Node, []*graph.Edge) {
	var nodes []*graph.Node
	var edges []*graph.Edge

	dir := filepath.Dir(filePath)
	if dir == "." || dir == "" {
		return nodes, edges
	}

	// Collect ancestor dirs from innermost to outermost.
	var dirs []string
	current := dir
	for current != "." && current != "" {
		dirs = append(dirs, current)
		current = filepath.Dir(current)
	}

	// Process from outermost to innermost (reverse order).
	for i := len(dirs) - 1; i >= 0; i-- {
		d := dirs[i]
		if seen[d] {
			continue
		}
		seen[d] = true

		dirName := filepath.Base(d)
		parentDir := filepath.Dir(d)
		pkg := ""
		if parentDir != "." && parentDir != "" {
			pkg = parentDir
		}

		nodeID := graph.NewNodeID(string(graph.NodeDirectory), d, dirName)
		node := &graph.Node{
			ID:            nodeID,
			Type:          graph.NodeDirectory,
			Name:          dirName,
			QualifiedName: d,
			FilePath:      d,
			Package:       pkg,
		}
		nodes = append(nodes, node)

		// Create Contains edge from parent directory.
		if parentDir != "." && parentDir != "" {
			parentName := filepath.Base(parentDir)
			parentID := graph.NewNodeID(string(graph.NodeDirectory), parentDir, parentName)
			edgeID := graph.NewNodeID("edge", parentID, nodeID)
			edges = append(edges, &graph.Edge{
				ID:       edgeID,
				Type:     graph.EdgeContains,
				SourceID: parentID,
				TargetID: nodeID,
			})
		}
	}

	// Create Contains edge from innermost directory to the file.
	if len(dirs) > 0 {
		innerDir := dirs[0]
		innerName := filepath.Base(innerDir)
		innerID := graph.NewNodeID(string(graph.NodeDirectory), innerDir, innerName)
		fileName := filepath.Base(filePath)
		fileNodeID := graph.NewNodeID(string(graph.NodeDocument), filePath, fileName)
		edgeID := graph.NewNodeID("edge", innerID, fileNodeID)
		edges = append(edges, &graph.Edge{
			ID:       edgeID,
			Type:     graph.EdgeContains,
			SourceID: innerID,
			TargetID: fileNodeID,
		})
	}

	return nodes, edges
}

// NormalizeTopic normalizes a topic name for deduplication.
// Lowercase, trim whitespace, collapse multiple spaces.
func NormalizeTopic(topic string) string {
	topic = strings.ToLower(strings.TrimSpace(topic))
	// Collapse multiple spaces.
	parts := strings.Fields(topic)
	return strings.Join(parts, " ")
}
