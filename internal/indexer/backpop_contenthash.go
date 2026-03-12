package indexer

import (
	"context"
	"os"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// contentHashNodeTypes lists the file-type nodes that should have content_hash.
// Directory nodes are excluded since they have no file content.
var contentHashNodeTypes = []graph.NodeType{
	graph.NodeFile,
	graph.NodeTestFile,
	graph.NodeDocument,
	graph.NodeAIGuideline,
}

// BackpopContentHash computes and stores content_hash and mime_type for
// existing file nodes that lack them. Returns the count of updated nodes.
func BackpopContentHash(ctx context.Context, store graph.Store, repoRoots []string, logFn func(format string, args ...any)) (int, error) {
	updated := 0

	for _, nodeType := range contentHashNodeTypes {
		nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: nodeType})
		if err != nil {
			return updated, err
		}

		for _, node := range nodes {
			if node.Properties != nil && node.Properties[graph.PropContentHash] != "" {
				continue // already has content hash
			}

			absPath := resolveAbsPath(node.FilePath, repoRoots)
			if absPath == "" {
				continue // file not found under any repo root
			}

			content, err := os.ReadFile(absPath)
			if err != nil {
				continue // file no longer readable
			}

			if node.Properties == nil {
				node.Properties = make(map[string]string)
			}
			node.Properties[graph.PropContentHash] = computeContentHash(content)
			if node.Properties[graph.PropMimeType] == "" {
				node.Properties[graph.PropMimeType] = detectMIMEType(node.FilePath)
			}

			if err := store.UpdateNode(ctx, node); err != nil {
				if logFn != nil {
					logFn("Warning: backpop content_hash for %s: %v", node.FilePath, err)
				}
				continue
			}

			updated++
		}
	}

	return updated, nil
}
