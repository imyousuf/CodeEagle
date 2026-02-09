package markdown

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

const testMarkdown = `---
title: Test Document
author: Test Author
date: 2024-01-15
---

# Main Title

This is an introduction.

## First Section

See [main.go](../cmd/main.go) for the entry point.

Also check [config.yaml](../configs/config.yaml).

### Sub Section

Details about the sub section.

` + "```go" + `
func main() {
    fmt.Println("hello")
}
` + "```" + `

## Second Section

Link to [external site](https://example.com) should be ignored.

` + "```python" + `
def hello():
    print("hello")
` + "```" + `

TODO: Add more documentation here

See the [README](../README.md) for more info.
`

func TestParseMarkdownFile(t *testing.T) {
	p := NewParser()
	result, err := p.ParseFile("docs/test.md", []byte(testMarkdown))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	if result.FilePath != "docs/test.md" {
		t.Errorf("FilePath = %q, want %q", result.FilePath, "docs/test.md")
	}
	if result.Language != parser.LangMarkdown {
		t.Errorf("Language = %q, want %q", result.Language, parser.LangMarkdown)
	}

	nodeByName := indexByName(result.Nodes)
	nodesByType := countByType(result.Nodes)
	edgesByType := countEdgesByType(result.Edges)

	// 1 main document node
	docNode := nodeByName["docs/test.md"]
	if docNode == nil {
		t.Fatal("Document node not found")
	}

	// Front matter properties.
	if docNode.Properties["frontmatter:title"] != "Test Document" {
		t.Errorf("frontmatter:title = %q, want %q", docNode.Properties["frontmatter:title"], "Test Document")
	}
	if docNode.Properties["frontmatter:author"] != "Test Author" {
		t.Errorf("frontmatter:author = %q, want %q", docNode.Properties["frontmatter:author"], "Test Author")
	}
	if docNode.Properties["frontmatter:date"] != "2024-01-15" {
		t.Errorf("frontmatter:date = %q, want %q", docNode.Properties["frontmatter:date"], "2024-01-15")
	}

	// Sections: Main Title, First Section, Sub Section, Second Section = 4 headings
	sections := findNodesByKind(result.Nodes, "section")
	if len(sections) != 4 {
		t.Errorf("Section nodes = %d, want 4; got sections: %v", len(sections), nodeNames(sections))
	}

	// Verify section hierarchy via level property.
	assertNodeExists(t, nodeByName, "Main Title", graph.NodeDocument)
	if n, ok := nodeByName["Main Title"]; ok {
		if n.Properties["level"] != "#" {
			t.Errorf("Main Title level = %q, want %q", n.Properties["level"], "#")
		}
	}
	assertNodeExists(t, nodeByName, "First Section", graph.NodeDocument)
	if n, ok := nodeByName["First Section"]; ok {
		if n.Properties["level"] != "##" {
			t.Errorf("First Section level = %q, want %q", n.Properties["level"], "##")
		}
	}
	assertNodeExists(t, nodeByName, "Sub Section", graph.NodeDocument)
	if n, ok := nodeByName["Sub Section"]; ok {
		if n.Properties["level"] != "###" {
			t.Errorf("Sub Section level = %q, want %q", n.Properties["level"], "###")
		}
	}

	// Cross-reference links (relative paths only, not https://).
	// ../cmd/main.go, ../configs/config.yaml, ../README.md = 3 links
	deps := findNodesByKind(result.Nodes, "cross-reference")
	if len(deps) != 3 {
		t.Errorf("Cross-reference nodes = %d, want 3; got: %v", len(deps), nodeNames(deps))
	}

	// Documents edges for cross-references.
	if edgesByType[graph.EdgeDocuments] != 3 {
		t.Errorf("Documents edges = %d, want 3", edgesByType[graph.EdgeDocuments])
	}

	// Code blocks: go, python = 2.
	codeBlocks := findNodesByKind(result.Nodes, "code-block")
	if len(codeBlocks) != 2 {
		t.Errorf("Code block nodes = %d, want 2", len(codeBlocks))
	}

	// Check code block languages.
	goBlockFound := false
	pyBlockFound := false
	for _, n := range codeBlocks {
		switch n.Properties["code_language"] {
		case "go":
			goBlockFound = true
		case "python":
			pyBlockFound = true
		}
	}
	if !goBlockFound {
		t.Error("expected Go code block")
	}
	if !pyBlockFound {
		t.Error("expected Python code block")
	}

	// TODO item.
	todoNodes := findNodesByKind(result.Nodes, "todo")
	if len(todoNodes) != 1 {
		t.Errorf("TODO nodes = %d, want 1", len(todoNodes))
	}
	if len(todoNodes) > 0 {
		if todoNodes[0].Properties["todo_type"] != "TODO" {
			t.Errorf("todo_type = %q, want %q", todoNodes[0].Properties["todo_type"], "TODO")
		}
	}

	// Contains edges: 4 sections + 2 code blocks + 1 todo = 7.
	if edgesByType[graph.EdgeContains] != 7 {
		t.Errorf("Contains edges = %d, want 7", edgesByType[graph.EdgeContains])
	}

	// Total Document nodes: 1 main + 4 sections + 2 code blocks + 1 todo = 8.
	if nodesByType[graph.NodeDocument] != 8 {
		t.Errorf("Document nodes = %d, want 8", nodesByType[graph.NodeDocument])
	}
}

func TestParseMarkdownNoFrontMatter(t *testing.T) {
	md := `# Simple Doc

Some content with a [link](./file.go).
`
	p := NewParser()
	result, err := p.ParseFile("simple.md", []byte(md))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	nodeByName := indexByName(result.Nodes)
	docNode := nodeByName["simple.md"]
	if docNode == nil {
		t.Fatal("Document node not found")
	}
	// No front matter properties should be set.
	if len(docNode.Properties) != 0 {
		t.Errorf("expected no properties, got %v", docNode.Properties)
	}

	// 1 heading.
	sections := findNodesByKind(result.Nodes, "section")
	if len(sections) != 1 {
		t.Errorf("Section nodes = %d, want 1", len(sections))
	}

	// 1 relative link.
	deps := findNodesByKind(result.Nodes, "cross-reference")
	if len(deps) != 1 {
		t.Errorf("Cross-reference nodes = %d, want 1", len(deps))
	}
}

func TestParseMarkdownFixture(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	samplePath := filepath.Join(projectRoot, "testdata", "sample.md")

	content, err := os.ReadFile(samplePath)
	if err != nil {
		t.Skipf("testdata/sample.md not found: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile(samplePath, content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	nodesByType := countByType(result.Nodes)
	edgesByType := countEdgesByType(result.Edges)

	// Should have at least 1 document, some sections, some links.
	if nodesByType[graph.NodeDocument] < 5 {
		t.Errorf("Document nodes = %d, want at least 5", nodesByType[graph.NodeDocument])
	}

	// Should have Documents edges for relative links.
	if edgesByType[graph.EdgeDocuments] < 3 {
		t.Errorf("Documents edges = %d, want at least 3", edgesByType[graph.EdgeDocuments])
	}

	// Should have Contains edges for sections.
	if edgesByType[graph.EdgeContains] < 4 {
		t.Errorf("Contains edges = %d, want at least 4", edgesByType[graph.EdgeContains])
	}

	// Check front matter was extracted.
	nodeByName := indexByName(result.Nodes)
	docNode := nodeByName[samplePath]
	if docNode == nil {
		t.Fatal("Document node not found")
	}
	if docNode.Properties["frontmatter:title"] != "Architecture Overview" {
		t.Errorf("frontmatter:title = %q, want %q", docNode.Properties["frontmatter:title"], "Architecture Overview")
	}

	// Check TODO item was found.
	todoNodes := findNodesByKind(result.Nodes, "todo")
	if len(todoNodes) < 1 {
		t.Error("expected at least 1 TODO node")
	}
}

func TestLanguageAndExtensions(t *testing.T) {
	p := NewParser()
	if p.Language() != parser.LangMarkdown {
		t.Errorf("Language() = %q, want %q", p.Language(), parser.LangMarkdown)
	}
	exts := p.Extensions()
	if len(exts) == 0 {
		t.Error("Extensions() returned empty slice")
	}
	found := false
	for _, ext := range exts {
		if ext == ".md" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Extensions() = %v, missing .md", exts)
	}
}

// Helpers.

func countByType(nodes []*graph.Node) map[graph.NodeType]int {
	m := make(map[graph.NodeType]int)
	for _, n := range nodes {
		m[n.Type]++
	}
	return m
}

func countEdgesByType(edges []*graph.Edge) map[graph.EdgeType]int {
	m := make(map[graph.EdgeType]int)
	for _, e := range edges {
		m[e.Type]++
	}
	return m
}

func indexByName(nodes []*graph.Node) map[string]*graph.Node {
	m := make(map[string]*graph.Node, len(nodes))
	for _, n := range nodes {
		m[n.Name] = n
	}
	return m
}

func findNodesByKind(nodes []*graph.Node, kind string) []*graph.Node {
	var result []*graph.Node
	for _, n := range nodes {
		if n.Properties != nil && n.Properties["kind"] == kind {
			result = append(result, n)
		}
	}
	return result
}

func nodeNames(nodes []*graph.Node) []string {
	names := make([]string, len(nodes))
	for i, n := range nodes {
		names[i] = n.Name
	}
	return names
}

func assertNodeExists(t *testing.T, nodes map[string]*graph.Node, name string, expectedType graph.NodeType) {
	t.Helper()
	n, ok := nodes[name]
	if !ok {
		t.Errorf("expected node %q not found", name)
		return
	}
	if n.Type != expectedType {
		t.Errorf("node %q type = %q, want %q", name, n.Type, expectedType)
	}
}
