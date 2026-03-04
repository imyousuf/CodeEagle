package generic

import (
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

func TestClassify(t *testing.T) {
	excludeExts := []string{".lock", ".min.js", ".wasm"}

	tests := []struct {
		name     string
		filePath string
		want     FileClass
	}{
		{"text file", "docs/README.txt", FileClassText},
		{"markdown", "docs/spec.md", FileClassText},
		{"json", "config.json", FileClassText},
		{"yaml", "config.yaml", FileClassText},
		{"csv", "data.csv", FileClassText},
		{"log", "output.log", FileClassText},
		{"no extension", "README", FileClassText},
		{"png image", "photo.png", FileClassImage},
		{"jpg image", "photo.jpg", FileClassImage},
		{"jpeg image", "photo.jpeg", FileClassImage},
		{"gif image", "icon.gif", FileClassImage},
		{"webp image", "hero.webp", FileClassImage},
		{"bmp image", "scan.bmp", FileClassImage},
		{"tiff image", "raw.tiff", FileClassImage},
		{"excluded lock", "package-lock.lock", FileClassSkip},
		{"excluded min.js", "bundle.min.js", FileClassSkip},
		{"excluded wasm", "module.wasm", FileClassSkip},
		{"upper case PNG", "photo.PNG", FileClassImage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.filePath, excludeExts)
			if got != tt.want {
				t.Errorf("Classify(%q) = %d, want %d", tt.filePath, got, tt.want)
			}
		})
	}
}

func TestClassifyContent(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
		want    FileClass
	}{
		{"empty file", []byte{}, FileClassText},
		{"text content", []byte("hello world"), FileClassText},
		{"binary with null", []byte("hello\x00world"), FileClassSkip},
		{"utf8 text", []byte("こんにちは"), FileClassText},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyContent(tt.content)
			if got != tt.want {
				t.Errorf("ClassifyContent() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestGenericParserText(t *testing.T) {
	p := NewGenericParser([]string{".lock"}, nil, nil, 0)
	content := []byte("This is a changelog entry about authentication.")

	result, err := p.ParseFile("docs/CHANGELOG.txt", content)
	if err != nil {
		t.Fatalf("ParseFile() error: %v", err)
	}

	// Should have document node + directory nodes.
	if len(result.Nodes) == 0 {
		t.Fatal("expected at least 1 node")
	}

	// First node should be the document.
	docNode := result.Nodes[0]
	if docNode.Type != graph.NodeDocument {
		t.Errorf("expected NodeDocument, got %s", docNode.Type)
	}
	if docNode.Name != "CHANGELOG.txt" {
		t.Errorf("expected name CHANGELOG.txt, got %s", docNode.Name)
	}
	if docNode.QualifiedName != "docs/CHANGELOG.txt" {
		t.Errorf("expected qualified name docs/CHANGELOG.txt, got %s", docNode.QualifiedName)
	}
	if docNode.Package != "docs" {
		t.Errorf("expected package docs, got %s", docNode.Package)
	}
	if docNode.Properties["kind"] != "text" {
		t.Errorf("expected kind text, got %s", docNode.Properties["kind"])
	}
	if !strings.Contains(docNode.Properties["content_hash"], "sha256:") {
		t.Error("expected content_hash with sha256 prefix")
	}
	if docNode.DocComment != string(content) {
		t.Errorf("expected DocComment to be raw text, got %s", docNode.DocComment)
	}
}

func TestGenericParserImage(t *testing.T) {
	p := NewGenericParser(nil, nil, nil, 0)
	content := []byte("fake image data")

	result, err := p.ParseFile("photos/vacation/beach.jpg", content)
	if err != nil {
		t.Fatalf("ParseFile() error: %v", err)
	}

	if len(result.Nodes) == 0 {
		t.Fatal("expected at least 1 node")
	}

	docNode := result.Nodes[0]
	if docNode.Type != graph.NodeDocument {
		t.Errorf("expected NodeDocument, got %s", docNode.Type)
	}
	if docNode.Properties["kind"] != "image" {
		t.Errorf("expected kind image, got %s", docNode.Properties["kind"])
	}
	if !strings.Contains(docNode.DocComment, "Image file:") {
		t.Errorf("expected image metadata DocComment, got %s", docNode.DocComment)
	}
}

func TestGenericParserSkipped(t *testing.T) {
	p := NewGenericParser([]string{".lock"}, nil, nil, 0)
	content := []byte("lock file content")

	result, err := p.ParseFile("yarn.lock", content)
	if err != nil {
		t.Fatalf("ParseFile() error: %v", err)
	}

	if len(result.Nodes) != 0 {
		t.Errorf("expected 0 nodes for skipped file, got %d", len(result.Nodes))
	}
}

func TestGenericParserBinary(t *testing.T) {
	p := NewGenericParser(nil, nil, nil, 0)
	content := []byte("binary\x00content\x00with\x00nulls")

	result, err := p.ParseFile("data.bin", content)
	if err != nil {
		t.Fatalf("ParseFile() error: %v", err)
	}

	if len(result.Nodes) != 0 {
		t.Errorf("expected 0 nodes for binary file, got %d", len(result.Nodes))
	}
}

func TestEnsureDirectoryHierarchy(t *testing.T) {
	seen := make(map[string]bool)

	nodes, edges := EnsureDirectoryHierarchy("docs/design/auth-flow.png", seen)

	// Should create 2 directory nodes: "docs" and "docs/design".
	if len(nodes) != 2 {
		t.Fatalf("expected 2 directory nodes, got %d", len(nodes))
	}

	// Check outer directory.
	if nodes[0].Name != "docs" {
		t.Errorf("expected first dir name 'docs', got %s", nodes[0].Name)
	}
	if nodes[0].QualifiedName != "docs" {
		t.Errorf("expected first dir qualified name 'docs', got %s", nodes[0].QualifiedName)
	}
	if nodes[0].Type != graph.NodeDirectory {
		t.Errorf("expected NodeDirectory, got %s", nodes[0].Type)
	}

	// Check inner directory.
	if nodes[1].Name != "design" {
		t.Errorf("expected second dir name 'design', got %s", nodes[1].Name)
	}
	if nodes[1].QualifiedName != "docs/design" {
		t.Errorf("expected second dir qualified name 'docs/design', got %s", nodes[1].QualifiedName)
	}

	// Should have edges: docs→docs/design, docs/design→auth-flow.png.
	if len(edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(edges))
	}

	// Calling again with same seen map should produce no new nodes.
	nodes2, _ := EnsureDirectoryHierarchy("docs/design/other.png", seen)
	if len(nodes2) != 0 {
		t.Errorf("expected 0 new nodes (already seen), got %d", len(nodes2))
	}
}

func TestEnsureDirectoryHierarchyRootFile(t *testing.T) {
	seen := make(map[string]bool)

	nodes, edges := EnsureDirectoryHierarchy("README.txt", seen)

	// Root-level file — no directories to create.
	if len(nodes) != 0 {
		t.Errorf("expected 0 directory nodes for root file, got %d", len(nodes))
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 edges for root file, got %d", len(edges))
	}
}

func TestNormalizeTopic(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Authentication", "authentication"},
		{"  database  schema  ", "database schema"},
		{"LLM Client", "llm client"},
		{"", ""},
	}

	for _, tt := range tests {
		got := NormalizeTopic(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeTopic(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestExtractTextSVG(t *testing.T) {
	svg := `<svg><text>Hello</text><rect/><text>World</text></svg>`
	result := ExtractText("diagram.svg", []byte(svg))
	if !strings.Contains(result, "Hello") || !strings.Contains(result, "World") {
		t.Errorf("expected stripped SVG text, got %s", result)
	}
	if strings.Contains(result, "<svg>") {
		t.Error("SVG tags should be stripped")
	}
}

func TestExtractTextCSV(t *testing.T) {
	csv := "name,age,city\nAlice,30,NYC\nBob,25,LA\n"
	result := ExtractText("data.csv", []byte(csv))
	if !strings.Contains(result, "CSV headers:") {
		t.Errorf("expected CSV headers prefix, got %s", result)
	}
	if !strings.Contains(result, "name,age,city") {
		t.Error("expected header row in output")
	}
}

func TestCreateTopicNodes(t *testing.T) {
	topics := []string{"Authentication", "authentication", "Database Schema", "  database  schema  ", ""}
	docNodeID := "test-doc-node-id"

	nodes, edges := CreateTopicNodes(topics, docNodeID)

	// Should deduplicate: "authentication" and "database schema" (2 unique topics).
	if len(nodes) != 2 {
		t.Fatalf("expected 2 topic nodes, got %d", len(nodes))
	}
	if len(edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(edges))
	}

	if nodes[0].Type != graph.NodeTopic {
		t.Errorf("expected NodeTopic, got %s", nodes[0].Type)
	}
	if nodes[0].Name != "authentication" {
		t.Errorf("expected 'authentication', got %s", nodes[0].Name)
	}
	if nodes[1].Name != "database schema" {
		t.Errorf("expected 'database schema', got %s", nodes[1].Name)
	}
	if edges[0].Type != graph.EdgeHasTopic {
		t.Errorf("expected EdgeHasTopic, got %s", edges[0].Type)
	}
	if edges[0].SourceID != docNodeID {
		t.Errorf("expected source %s, got %s", docNodeID, edges[0].SourceID)
	}
}

func TestExtractTextPlain(t *testing.T) {
	text := "Just a plain text file."
	result := ExtractText("notes.txt", []byte(text))
	if result != text {
		t.Errorf("expected plain passthrough, got %s", result)
	}
}
