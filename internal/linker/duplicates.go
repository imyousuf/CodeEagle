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

	for _, all := range groups {
		// Every node parsed from one file carries that file's hash, so a
		// markdown file's sections share it with the file. They are parts
		// of one file, not copies of each other: the file was being marked
		// a duplicate of its own section. A duplicate is another file, so
		// each file is represented once, by its own node.
		l.dropIntraFileDuplicates(ctx, all)
		nodes := oneNodePerFile(all)
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

// oneNodePerFile keeps one node per file path: the file's own node where it
// can be told apart from the parts parsed out of it, else the first.
func oneNodePerFile(nodes []*graph.Node) []*graph.Node {
	byPath := make(map[string]*graph.Node, len(nodes))
	var order []string
	for _, n := range nodes {
		cur, ok := byPath[n.FilePath]
		if !ok {
			byPath[n.FilePath] = n
			order = append(order, n.FilePath)
			continue
		}
		if !isFileOwnNode(cur) && isFileOwnNode(n) {
			byPath[n.FilePath] = n
		}
	}
	out := make([]*graph.Node, 0, len(order))
	for _, p := range order {
		out = append(out, byPath[p])
	}
	return out
}

// isFileOwnNode reports whether n stands for its whole file rather than a
// section, code block or other part of it.
func isFileOwnNode(n *graph.Node) bool {
	return n.Properties["kind"] == "" && (n.Name == n.FilePath || n.Type == graph.NodeFile || n.Type == graph.NodeTestFile)
}

// dropIntraFileDuplicates removes DuplicateOf edges an earlier version wrote
// between parts of one file. Only nodes sharing a file with another node of
// the group can have one, so only they are checked.
func (l *Linker) dropIntraFileDuplicates(ctx context.Context, nodes []*graph.Node) {
	perPath := make(map[string]int, len(nodes))
	for _, n := range nodes {
		perPath[n.FilePath]++
	}
	pathOf := make(map[string]string, len(nodes))
	for _, n := range nodes {
		pathOf[n.ID] = n.FilePath
	}
	for _, n := range nodes {
		if perPath[n.FilePath] < 2 {
			continue
		}
		edges, err := l.store.GetEdges(ctx, n.ID, graph.EdgeDuplicateOf)
		if err != nil {
			continue
		}
		for _, e := range edges {
			if e.SourceID == n.ID && pathOf[e.TargetID] == n.FilePath {
				_ = l.store.DeleteEdge(ctx, e.ID)
			}
		}
	}
}
