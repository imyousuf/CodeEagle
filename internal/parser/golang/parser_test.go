package golang

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

const testSource = `// Package testpkg provides test fixtures.
package testpkg

import (
	"fmt"
	"strings"
)

// MaxItems is the maximum number of items.
const MaxItems = 100

// DefaultPrefix is the default prefix.
const DefaultPrefix = "item"

// counter is an unexported variable.
var counter int

// Verbose controls logging.
var Verbose bool

// Processor defines the interface for processing items.
type Processor interface {
	// Process processes a single item.
	Process(item string) error
	// Reset resets the processor state.
	Reset()
}

// Item represents a single item.
type Item struct {
	ID   int
	Name string
	Tags []string
}

// Process processes the item (satisfies Processor).
func (it *Item) Process(item string) error {
	it.Name = strings.TrimSpace(item)
	return nil
}

// Reset resets the item (satisfies Processor).
func (it *Item) Reset() {
	it.Name = ""
	it.Tags = nil
}

// String returns a string representation.
func (it Item) String() string {
	return fmt.Sprintf("%d:%s", it.ID, it.Name)
}

// ItemID is a named type for item identifiers.
type ItemID int

// NewItem creates a new Item with the given name.
func NewItem(name string) *Item {
	counter++
	return &Item{ID: counter, Name: name}
}

// formatItem is an unexported helper.
func formatItem(it *Item) string {
	return fmt.Sprintf("[%d] %s", it.ID, it.Name)
}
`

func TestParseFile(t *testing.T) {
	p := NewParser()

	result, err := p.ParseFile("testpkg/example.go", []byte(testSource))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	if result.FilePath != "testpkg/example.go" {
		t.Errorf("FilePath = %q, want %q", result.FilePath, "testpkg/example.go")
	}
	if result.Language != parser.LangGo {
		t.Errorf("Language = %q, want %q", result.Language, parser.LangGo)
	}

	// Count nodes by type
	counts := make(map[graph.NodeType]int)
	names := make(map[graph.NodeType][]string)
	for _, n := range result.Nodes {
		counts[n.Type]++
		names[n.Type] = append(names[n.Type], n.Name)
	}

	// 1 file node
	assertCount(t, counts, graph.NodeFile, 1)
	// 1 package node
	assertCount(t, counts, graph.NodePackage, 1)
	// 2 imports: fmt, strings
	assertCount(t, counts, graph.NodeDependency, 2)
	// 2 functions: NewItem, formatItem
	assertCount(t, counts, graph.NodeFunction, 2)
	// 3 methods: Process, Reset, String
	assertCount(t, counts, graph.NodeMethod, 3)
	// 1 struct: Item
	assertCount(t, counts, graph.NodeStruct, 1)
	// 1 interface: Processor
	assertCount(t, counts, graph.NodeInterface, 1)
	// 1 type: ItemID
	assertCount(t, counts, graph.NodeType_, 1)
	// 2 constants: MaxItems, DefaultPrefix
	assertCount(t, counts, graph.NodeConstant, 2)
	// 2 variables: counter, Verbose
	assertCount(t, counts, graph.NodeVariable, 2)

	// Verify exported flag
	nodeByName := indexByName(result.Nodes)

	assertExported(t, nodeByName, "NewItem", true)
	assertExported(t, nodeByName, "formatItem", false)
	assertExported(t, nodeByName, "Item", true)
	assertExported(t, nodeByName, "Processor", true)
	assertExported(t, nodeByName, "ItemID", true)
	assertExported(t, nodeByName, "MaxItems", true)
	assertExported(t, nodeByName, "counter", false)
	assertExported(t, nodeByName, "Verbose", true)

	// Verify function signatures
	if n, ok := nodeByName["NewItem"]; ok {
		expected := "func NewItem(name string) *Item"
		if n.Signature != expected {
			t.Errorf("NewItem signature = %q, want %q", n.Signature, expected)
		}
	}

	// Verify method has receiver in properties
	if n, ok := nodeByName["Process"]; ok {
		if n.Type != graph.NodeMethod {
			t.Errorf("Process should be a Method, got %s", n.Type)
		}
		if n.Properties["receiver"] != "Item" {
			t.Errorf("Process receiver = %q, want %q", n.Properties["receiver"], "Item")
		}
	}

	// Verify doc comments
	if n, ok := nodeByName["Processor"]; ok {
		if n.DocComment == "" {
			t.Error("Processor interface should have a doc comment")
		}
	}

	// Verify edges
	edgeCounts := make(map[graph.EdgeType]int)
	for _, e := range result.Edges {
		edgeCounts[e.Type]++
	}

	// Contains edges: file->pkg, pkg->funcs, pkg->methods, pkg->struct, pkg->interface, pkg->type, pkg->consts, pkg->vars
	// = 1 + 2 + 3 + 1 + 1 + 1 + 2 + 2 = 13
	if edgeCounts[graph.EdgeContains] < 13 {
		t.Errorf("Contains edges = %d, want at least 13", edgeCounts[graph.EdgeContains])
	}

	// 2 import edges
	if edgeCounts[graph.EdgeImports] != 2 {
		t.Errorf("Imports edges = %d, want 2", edgeCounts[graph.EdgeImports])
	}

	// Implements edge: Item implements Processor (Process + Reset)
	if edgeCounts[graph.EdgeImplements] != 1 {
		t.Errorf("Implements edges = %d, want 1", edgeCounts[graph.EdgeImplements])
	}
}

func TestParseFileSyntaxError(t *testing.T) {
	p := NewParser()
	badSource := []byte(`package bad
func broken( {
`)
	_, err := p.ParseFile("bad.go", badSource)
	if err == nil {
		t.Error("expected error for syntax-error source, got nil")
	}
}

func TestLanguageAndExtensions(t *testing.T) {
	p := NewParser()
	if p.Language() != parser.LangGo {
		t.Errorf("Language() = %q, want %q", p.Language(), parser.LangGo)
	}
	exts := p.Extensions()
	if len(exts) != 1 || exts[0] != ".go" {
		t.Errorf("Extensions() = %v, want [\".go\"]", exts)
	}
}

func TestParseSampleFixture(t *testing.T) {
	// Find the testdata directory relative to this test file.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	samplePath := filepath.Join(projectRoot, "testdata", "sample.go")

	content, err := os.ReadFile(samplePath)
	if err != nil {
		t.Skipf("testdata/sample.go not found: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile(samplePath, content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	nodeByName := indexByName(result.Nodes)

	// Check struct
	if _, ok := nodeByName["User"]; !ok {
		t.Error("expected User struct node")
	}

	// Check interface
	if _, ok := nodeByName["Greeter"]; !ok {
		t.Error("expected Greeter interface node")
	}

	// Check methods
	for _, name := range []string{"Greet", "String", "IsAdult"} {
		if _, ok := nodeByName[name]; !ok {
			t.Errorf("expected method %s", name)
		}
	}

	// Check functions
	for _, name := range []string{"NewUser", "formatName"} {
		if _, ok := nodeByName[name]; !ok {
			t.Errorf("expected function %s", name)
		}
	}

	// Check constants
	for _, name := range []string{"MaxRetries", "DefaultName"} {
		if _, ok := nodeByName[name]; !ok {
			t.Errorf("expected constant %s", name)
		}
	}

	// Check type alias
	if _, ok := nodeByName["UserID"]; !ok {
		t.Error("expected UserID type node")
	}

	// User implements Greeter (has Greet method)
	hasImplements := false
	for _, e := range result.Edges {
		if e.Type == graph.EdgeImplements {
			hasImplements = true
			break
		}
	}
	if !hasImplements {
		t.Error("expected Implements edge (User implements Greeter)")
	}
}

// helpers

func assertCount(t *testing.T, counts map[graph.NodeType]int, nt graph.NodeType, want int) {
	t.Helper()
	if counts[nt] != want {
		t.Errorf("%s count = %d, want %d", nt, counts[nt], want)
	}
}

func assertExported(t *testing.T, nodes map[string]*graph.Node, name string, want bool) {
	t.Helper()
	n, ok := nodes[name]
	if !ok {
		t.Errorf("node %q not found", name)
		return
	}
	if n.Exported != want {
		t.Errorf("%s.Exported = %v, want %v", name, n.Exported, want)
	}
}

func indexByName(nodes []*graph.Node) map[string]*graph.Node {
	m := make(map[string]*graph.Node, len(nodes))
	for _, n := range nodes {
		m[n.Name] = n
	}
	return m
}
