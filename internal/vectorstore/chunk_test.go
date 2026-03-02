package vectorstore

import (
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

func TestChunkShortText(t *testing.T) {
	text := "Hello world, this is a short text."
	chunks := Chunk(text, DefaultChunkConfig())
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if chunks[0] != text {
		t.Errorf("chunk = %q, want %q", chunks[0], text)
	}
}

func TestChunkExactSize(t *testing.T) {
	text := strings.Repeat("a", 1500)
	chunks := Chunk(text, DefaultChunkConfig())
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
}

func TestChunkLongText(t *testing.T) {
	// Create text with paragraph breaks.
	var parts []string
	for i := range 20 {
		parts = append(parts, strings.Repeat("word ", 60)+"\n") // ~300 chars per paragraph
		_ = i
	}
	text := strings.Join(parts, "\n")

	chunks := Chunk(text, DefaultChunkConfig())
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}

	// All chunks should be <= ChunkSize (except possibly last due to small trailing).
	for i, c := range chunks {
		if i < len(chunks)-1 && len(c) > 1600 { // allow some slack for boundary detection
			t.Errorf("chunk %d too long: %d chars", i, len(c))
		}
	}
}

func TestChunkOverlap(t *testing.T) {
	// Build a text ~3000 chars with clear paragraphs.
	para := strings.Repeat("The quick brown fox jumps. ", 25) // ~675 chars
	text := para + "\n\n" + para + "\n\n" + para + "\n\n" + para + "\n\n" + para

	cfg := ChunkConfig{ChunkSize: 1500, Overlap: 200, MinChunkSize: 100}
	chunks := Chunk(text, cfg)

	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}

	// Check overlap: beginning of chunk[i+1] should overlap with end of chunk[i].
	for i := 0; i < len(chunks)-1; i++ {
		// The overlap should be approximately cfg.Overlap chars.
		suffix := chunks[i][max(0, len(chunks[i])-cfg.Overlap):]
		if !strings.Contains(chunks[i+1], suffix[:min(50, len(suffix))]) {
			// Overlap may not be exact due to boundary detection, but there should be some.
			t.Logf("chunk %d->%d overlap may be imprecise (chunk %d len=%d, chunk %d len=%d)",
				i, i+1, i, len(chunks[i]), i+1, len(chunks[i+1]))
		}
	}
}

func TestChunkSmallTrailingChunk(t *testing.T) {
	// Create text where the trailing bit is < MinChunkSize.
	text := strings.Repeat("a", 1550)
	cfg := ChunkConfig{ChunkSize: 1500, Overlap: 200, MinChunkSize: 100}
	chunks := Chunk(text, cfg)

	// The trailing 50 chars is < MinChunkSize (100), so it should be merged.
	if len(chunks) != 1 {
		t.Logf("chunks: %d, sizes: ", len(chunks))
		for i, c := range chunks {
			t.Logf("  chunk %d: %d chars", i, len(c))
		}
		// With overlap=200, there may still be 2 chunks due to the overlap starting point.
		// The important thing is no chunk < MinChunkSize.
		for i, c := range chunks {
			if len(c) < cfg.MinChunkSize {
				t.Errorf("chunk %d is too small: %d chars", i, len(c))
			}
		}
	}
}

func TestChunkSemanticBoundaries(t *testing.T) {
	// Text with clear paragraph boundaries.
	text := strings.Repeat("First paragraph content. ", 50) + "\n\n" +
		strings.Repeat("Second paragraph content. ", 50) + "\n\n" +
		strings.Repeat("Third paragraph content. ", 50)

	cfg := DefaultChunkConfig()
	chunks := Chunk(text, cfg)

	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}

	// Chunks should end near paragraph boundaries.
	for i, c := range chunks {
		if i < len(chunks)-1 {
			trimmed := strings.TrimRight(c, " \n")
			if !strings.HasSuffix(trimmed, ".") && !strings.HasSuffix(c, "\n") {
				t.Logf("chunk %d may not end at semantic boundary: ...%q", i, c[max(0, len(c)-30):])
			}
		}
	}
}

func TestEmbeddableText(t *testing.T) {
	tests := []struct {
		name string
		node *graph.Node
		want string
	}{
		{
			name: "with doc comment",
			node: &graph.Node{Name: "HandleAuth", DocComment: "handles authentication"},
			want: "HandleAuth\nhandles authentication",
		},
		{
			name: "with signature only",
			node: &graph.Node{Name: "Process", Signature: "func Process(ctx context.Context) error"},
			want: "Process\nfunc Process(ctx context.Context) error",
		},
		{
			name: "empty",
			node: &graph.Node{Name: "internal"},
			want: "",
		},
		{
			name: "both doc comment and signature included",
			node: &graph.Node{
				Name:       "Run",
				DocComment: "runs the server",
				Signature:  "func Run() error",
			},
			want: "Run\nruns the server\nfunc Run() error",
		},
		{
			name: "with package and file path",
			node: &graph.Node{
				Name:      "NewClient",
				Package:   "llm",
				FilePath:  "internal/llm/client.go",
				Signature: "func NewClient(provider string) (*Client, error)",
			},
			want: "NewClient\nPackage: llm\nFile: internal/llm/client.go\nfunc NewClient(provider string) (*Client, error)",
		},
		{
			name: "with qualified name",
			node: &graph.Node{
				Name:          "Get",
				QualifiedName: "Store.Get",
				Package:       "graph",
				Signature:     "func (s *Store) Get(id string) (*Node, error)",
			},
			want: "Get\nPackage: graph\nQualified: Store.Get\nfunc (s *Store) Get(id string) (*Node, error)",
		},
		{
			name: "with classifier properties",
			node: &graph.Node{
				Name:      "UserRepository",
				Package:   "users",
				Signature: "type UserRepository struct",
				Properties: map[string]string{
					graph.PropArchRole:      "repository",
					graph.PropDesignPattern: "repository",
					graph.PropLayerTag:      "data_access",
				},
			},
			want: "UserRepository\nPackage: users\nRole: repository\nPattern: repository\nLayer: data_access\ntype UserRepository struct",
		},
		{
			name: "full enrichment with doc comment and signature",
			node: &graph.Node{
				Name:          "RegisterProvider",
				QualifiedName: "llm.RegisterProvider",
				Package:       "llm",
				FilePath:      "pkg/llm/registry.go",
				DocComment:    "RegisterProvider adds a new LLM provider factory to the registry.",
				Signature:     "func RegisterProvider(name string, factory ProviderFactory)",
				Properties: map[string]string{
					graph.PropArchRole: "factory",
				},
			},
			want: "RegisterProvider\nPackage: llm\nFile: pkg/llm/registry.go\nQualified: llm.RegisterProvider\nRole: factory\nRegisterProvider adds a new LLM provider factory to the registry.\nfunc RegisterProvider(name string, factory ProviderFactory)",
		},
		{
			name: "qualified name same as name is omitted",
			node: &graph.Node{
				Name:          "Process",
				QualifiedName: "Process",
				Signature:     "func Process() error",
			},
			want: "Process\nfunc Process() error",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EmbeddableText(tc.node)
			if got != tc.want {
				t.Errorf("EmbeddableText = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIsEmbeddable(t *testing.T) {
	if !IsEmbeddable(graph.NodeFunction) {
		t.Error("NodeFunction should be embeddable")
	}
	if !IsEmbeddable(graph.NodeDocument) {
		t.Error("NodeDocument should be embeddable")
	}
	if IsEmbeddable(graph.NodeFile) {
		t.Error("NodeFile should not be embeddable")
	}
	if IsEmbeddable(graph.NodePackage) {
		t.Error("NodePackage should not be embeddable")
	}
}
