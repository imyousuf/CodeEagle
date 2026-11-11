package vectorstore

import (
	"slices"
	"strings"

	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
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

// EmbeddableTextVersion identifies what EmbeddableText produces. It is
// recorded in the index and compared on open: vectors computed from an
// earlier version of the text sit in a different place from ones computed
// from the current version, and an index holding both ranks them against
// each other as if they were comparable. Bump it whenever the text changes.
//
//	1: doc comment / signature, with package, file and qualified name
//	2: topics embed from their label and aliases
const EmbeddableTextVersion = 2

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
	// A relation's text is its brief, so an agent asking about a subject can
	// land on what is known about it directly.
	graph.NodeRelation,
}

// IsEmbeddable returns true if the node type should be considered for embedding.
func IsEmbeddable(nodeType graph.NodeType) bool {
	return slices.Contains(EmbeddableTypes, nodeType)
}
