package vectorstore

import (
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// TestEmbeddableTextIsPinned fixes the embedded text of node types that were
// embedded before markdown sections gained text. Changing it silently would
// leave every stored vector describing text that is no longer produced; a real
// change must bump EmbeddableTextVersion so indexes rebuild.
func TestEmbeddableTextIsPinned(t *testing.T) {
	tests := []struct {
		name string
		node *graph.Node
		want string
	}{
		{
			name: "function",
			node: &graph.Node{
				Type: graph.NodeFunction, Name: "NewClient", Package: "llm",
				FilePath: "pkg/llm/client.go", QualifiedName: "llm.NewClient",
				Properties: map[string]string{graph.PropArchRole: "factory"},
				DocComment: "NewClient builds a client.",
				Signature:  "func NewClient(cfg Config) (Client, error)",
			},
			want: "NewClient\nPackage: llm\nFile: pkg/llm/client.go\nQualified: llm.NewClient\n" +
				"Role: factory\nNewClient builds a client.\nfunc NewClient(cfg Config) (Client, error)",
		},
		{
			name: "topic segment",
			node: &graph.Node{
				Type: graph.NodeTopicSegment, Name: "Retry budget", FilePath: "sessions/a/session.json",
				Properties: map[string]string{"participants": "Ana, Bo", "keywords": "retry, budget"},
				DocComment: "Agreed to raise the cap.",
			},
			want: "Retry budget\nFile: sessions/a/session.json\nParticipants: Ana, Bo\n" +
				"Keywords: retry, budget\nAgreed to raise the cap.",
		},
		{
			name: "topic with no content still embeds its label",
			node: &graph.Node{Type: graph.NodeTopic, Name: "retry policy"},
			want: "retry policy",
		},
		{
			name: "no content",
			node: &graph.Node{Type: graph.NodeDocument, Name: "Overview"},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EmbeddableText(tt.node); got != tt.want {
				t.Errorf("EmbeddableText =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}
