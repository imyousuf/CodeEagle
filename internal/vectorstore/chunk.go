// Package vectorstore provides vector search over the knowledge graph using HNSW.
package vectorstore

import (
	"slices"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// ChunkConfig controls text chunking behavior.
type ChunkConfig struct {
	// ChunkSize is the target chars per chunk (default 1500).
	ChunkSize int
	// Overlap is the number of overlap chars between consecutive chunks (default 200).
	Overlap int
	// MinChunkSize is the minimum size for a trailing chunk; smaller ones extend the previous (default 100).
	MinChunkSize int
}

// DefaultChunkConfig returns the default chunking parameters.
func DefaultChunkConfig() ChunkConfig {
	return ChunkConfig{
		ChunkSize:    1500,
		Overlap:      200,
		MinChunkSize: 100,
	}
}

// Chunk splits text into overlapping chunks using semantic boundaries.
// If the text fits in a single chunk, it is returned as-is.
func Chunk(text string, cfg ChunkConfig) []string {
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = 1500
	}
	if cfg.Overlap < 0 {
		cfg.Overlap = 0
	}
	if cfg.MinChunkSize <= 0 {
		cfg.MinChunkSize = 100
	}

	if len(text) <= cfg.ChunkSize {
		return []string{text}
	}

	var chunks []string
	pos := 0
	textLen := len(text)

	for pos < textLen {
		end := pos + cfg.ChunkSize
		if end >= textLen {
			// Last chunk — take the rest.
			chunk := text[pos:]
			if len(chunks) > 0 && len(chunk) < cfg.MinChunkSize {
				// Too small — extend the previous chunk.
				chunks[len(chunks)-1] = chunks[len(chunks)-1] + chunk[findOverlapEnd(chunks[len(chunks)-1], chunk):]
			} else {
				chunks = append(chunks, chunk)
			}
			break
		}

		// Find a semantic boundary near the end of the chunk.
		boundary := findBoundary(text, pos, end)
		chunks = append(chunks, text[pos:boundary])

		// Slide forward, overlapping by cfg.Overlap chars.
		nextPos := boundary - cfg.Overlap
		if nextPos <= pos {
			nextPos = boundary // prevent infinite loop
		}
		pos = nextPos
	}

	return chunks
}

// findBoundary looks for the best semantic boundary in text[start:end].
// It prefers paragraph breaks (\n\n), then line breaks (\n), then sentence
// endings (". "), then spaces (" "). Falls back to end if no boundary found.
func findBoundary(text string, start, end int) int {
	// Search in the last 20% of the chunk for a good boundary.
	searchFrom := max(start, end-(end-start)/5)
	segment := text[searchFrom:end]

	// Try paragraph break.
	if idx := strings.LastIndex(segment, "\n\n"); idx >= 0 {
		return searchFrom + idx + 2 // after the double newline
	}
	// Try line break.
	if idx := strings.LastIndex(segment, "\n"); idx >= 0 {
		return searchFrom + idx + 1
	}
	// Try sentence end.
	if idx := strings.LastIndex(segment, ". "); idx >= 0 {
		return searchFrom + idx + 2
	}
	// Try space.
	if idx := strings.LastIndex(segment, " "); idx >= 0 {
		return searchFrom + idx + 1
	}
	// No good boundary found — just cut at end.
	return end
}

// findOverlapEnd finds how much of the trailing chunk already overlaps with prev.
// Returns the index in chunk where non-overlapping content starts.
func findOverlapEnd(_, _ string) int {
	// The trailing chunk is small, so just append all of it.
	return 0
}

// EmbeddableText returns the text to embed for a graph node.
// Returns empty string if the node has no embeddable content.
func EmbeddableText(n *graph.Node) string {
	if n.DocComment != "" {
		return n.Name + "\n" + n.DocComment
	}
	if n.Signature != "" {
		return n.Name + "\n" + n.Signature
	}
	return ""
}

// EmbeddableTypes returns the node types that should be embedded.
var EmbeddableTypes = []graph.NodeType{
	graph.NodeFunction,
	graph.NodeMethod,
	graph.NodeClass,
	graph.NodeStruct,
	graph.NodeInterface,
	graph.NodeDocument,
	graph.NodeAPIEndpoint,
	graph.NodeService,
	graph.NodeDBModel,
	graph.NodeAIGuideline,
}

// IsEmbeddable returns true if the node type should be considered for embedding.
func IsEmbeddable(nodeType graph.NodeType) bool {
	return slices.Contains(EmbeddableTypes, nodeType)
}
