// Package vectorstore provides vector search over the knowledge graph using HNSW.
package vectorstore

import (
	"slices"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// embeddableProperties are node property keys that enrich the embedded text
// when present (architectural role, design patterns, layer).
var embeddableProperties = []struct {
	key    string
	prefix string
}{
	{graph.PropArchRole, "Role"},
	{graph.PropDesignPattern, "Pattern"},
	{graph.PropLayerTag, "Layer"},
	// Meeting entities: who was there and when is often the whole of what
	// someone remembers about a meeting, so it has to be searchable text.
	{propParticipants, "Participants"},
	{graph.PropAssignee, "Assignee"},
	{graph.PropDueDate, "Due"},
	{propKeywords, "Keywords"},
}

// Property keys written by meeting indexing that are worth embedding.
const (
	propParticipants = "participants"
	propKeywords     = "keywords"
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
// It enriches the raw content (doc comment / signature) with contextual
// metadata — package name, file path, qualified name, and architectural
// properties — so the embedding model can connect semantic queries like
// "LLM provider" to functions whose names are generic (e.g. NewClient)
// but whose package context makes the relationship clear.
// Returns empty string if the node has no embeddable content.
//
// A topic is the exception to needing content: its label is the whole of
// what it is, and leaving labels out of the index meant the one node named
// exactly what someone was searching for could never be found by meaning.
// (Measured on the real corpus, the label alone scores 0.70 against a query
// for it; the segment discussing it, with its full summary, scores 0.66.)
func EmbeddableText(n *graph.Node) string {
	if n.DocComment == "" && n.Signature == "" && n.Type != graph.NodeTopic {
		return ""
	}

	var b strings.Builder
	b.WriteString(n.Name)

	if n.Package != "" {
		b.WriteString("\nPackage: ")
		b.WriteString(n.Package)
	}
	if n.FilePath != "" {
		b.WriteString("\nFile: ")
		b.WriteString(n.FilePath)
	}
	if n.QualifiedName != "" && n.QualifiedName != n.Name {
		b.WriteString("\nQualified: ")
		b.WriteString(n.QualifiedName)
	}

	// Architectural classifier properties.
	if n.Properties != nil {
		for _, prop := range embeddableProperties {
			if val := n.Properties[prop.key]; val != "" {
				b.WriteString("\n")
				b.WriteString(prop.prefix)
				b.WriteString(": ")
				b.WriteString(val)
			}
		}
		// The other wordings meetings used for a topic are the phrases a
		// searcher is most likely to reach for.
		if n.Type == graph.NodeTopic {
			if aliases := n.Properties[graph.PropAliases]; aliases != "" {
				b.WriteString("\nAlso worded as: ")
				b.WriteString(aliases)
			}
		}
	}

	if n.DocComment != "" {
		b.WriteString("\n")
		b.WriteString(n.DocComment)
	}
	if n.Signature != "" {
		b.WriteString("\n")
		b.WriteString(n.Signature)
	}

	return b.String()
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
	graph.NodeDirectory,
	graph.NodeTopic,
	graph.NodePerson,
	// Meeting entities carry their substance in DocComment, so they embed the
	// same way documents do. Including them is what lets a semantic query
	// reach what was *said* about something, not only what was written in code.
	graph.NodeMeeting,
	graph.NodeTopicSegment,
	graph.NodeDecision,
	graph.NodeActionItem,
}

// IsEmbeddable returns true if the node type should be considered for embedding.
func IsEmbeddable(nodeType graph.NodeType) bool {
	return slices.Contains(EmbeddableTypes, nodeType)
}
