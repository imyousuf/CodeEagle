package rust

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

const testSource = `use std::collections::HashMap;

/// A constant value.
pub const MAX_SIZE: usize = 100;

/// A trait for greeting.
pub trait Greeter {
    fn greet(&self) -> String;
    fn reset(&mut self);
}

/// Status codes.
pub enum Status {
    Ok,
    Error,
    Pending,
}

/// A greeting service.
pub struct GreetingService {
    pub prefix: String,
    count: u32,
}

impl GreetingService {
    /// Create a new service.
    pub fn new(prefix: String) -> Self {
        GreetingService { prefix, count: 0 }
    }

    pub fn get_count(&self) -> u32 {
        self.count
    }

    fn increment(&mut self) {
        self.count += 1;
    }
}

impl Greeter for GreetingService {
    fn greet(&self) -> String {
        format!("{} world", self.prefix)
    }

    fn reset(&mut self) {
        self.count = 0;
    }
}

/// A standalone helper function.
pub fn helper() -> String {
    String::from("help")
}

fn internal_helper() {
    helper();
}

type Callback = fn(u32) -> bool;
`

func TestParseFile(t *testing.T) {
	p := NewParser()

	result, err := p.ParseFile("src/greeting.rs", []byte(testSource))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	if result.FilePath != "src/greeting.rs" {
		t.Errorf("FilePath = %q, want %q", result.FilePath, "src/greeting.rs")
	}
	if result.Language != parser.LangRust {
		t.Errorf("Language = %q, want %q", result.Language, parser.LangRust)
	}

	// Count nodes by type
	counts := make(map[graph.NodeType]int)
	for _, n := range result.Nodes {
		counts[n.Type]++
	}

	// 1 file node
	assertCount(t, counts, graph.NodeFile, 1)
	// 1 use import
	assertCount(t, counts, graph.NodeDependency, 1)
	// 1 constant (MAX_SIZE)
	assertCount(t, counts, graph.NodeConstant, 1)
	// 1 trait: Greeter (mapped to NodeInterface)
	assertCount(t, counts, graph.NodeInterface, 1)
	// 1 enum: Status
	assertCount(t, counts, graph.NodeEnum, 1)
	// 1 struct: GreetingService
	assertCount(t, counts, graph.NodeStruct, 1)
	// 1 type alias: Callback
	assertCount(t, counts, graph.NodeType_, 1)
	// Functions: helper + internal_helper = 2
	assertCount(t, counts, graph.NodeFunction, 2)
	// Methods: GreetingService(new, get_count, increment) + Greeter impl(greet, reset) = 5
	assertCount(t, counts, graph.NodeMethod, 5)

	// Verify specific nodes
	nodeByName := indexByName(result.Nodes)

	// Trait with methods property
	if n, ok := nodeByName["Greeter"]; ok {
		if n.Type != graph.NodeInterface {
			t.Errorf("Greeter should be Interface, got %s", n.Type)
		}
		if n.Properties["methods"] == "" {
			t.Error("Greeter should list its methods in properties")
		}
		if !strings.Contains(n.Properties["methods"], "greet") {
			t.Errorf("Greeter methods should contain greet, got %q", n.Properties["methods"])
		}
		if n.DocComment == "" {
			t.Error("Greeter should have a doc comment")
		}
		if !n.Exported {
			t.Error("Greeter should be exported (pub)")
		}
	} else {
		t.Error("expected Greeter trait node")
	}

	// Enum with variants
	if n, ok := nodeByName["Status"]; ok {
		if n.Type != graph.NodeEnum {
			t.Errorf("Status should be Enum, got %s", n.Type)
		}
		if n.Properties["variants"] == "" {
			t.Error("Status enum should have variants in properties")
		}
		if !strings.Contains(n.Properties["variants"], "Ok") {
			t.Errorf("Status variants should contain Ok, got %q", n.Properties["variants"])
		}
	} else {
		t.Error("expected Status enum node")
	}

	// Struct with fields
	if n, ok := nodeByName["GreetingService"]; ok {
		if n.Type != graph.NodeStruct {
			t.Errorf("GreetingService should be Struct, got %s", n.Type)
		}
		if n.Properties["fields"] == "" {
			t.Error("GreetingService should have fields in properties")
		}
		if !strings.Contains(n.Properties["fields"], "prefix") {
			t.Errorf("GreetingService fields should contain prefix, got %q", n.Properties["fields"])
		}
		if n.DocComment == "" {
			t.Error("GreetingService should have a doc comment")
		}
	} else {
		t.Error("expected GreetingService struct node")
	}

	// Constant
	if n, ok := nodeByName["MAX_SIZE"]; ok {
		if n.Type != graph.NodeConstant {
			t.Errorf("MAX_SIZE should be Constant, got %s", n.Type)
		}
		if n.Properties["kind"] != "const" {
			t.Errorf("MAX_SIZE kind = %q, want const", n.Properties["kind"])
		}
		if !n.Exported {
			t.Error("MAX_SIZE should be exported (pub)")
		}
	} else {
		t.Error("expected MAX_SIZE constant node")
	}

	// Public function
	if n, ok := nodeByName["helper"]; ok {
		if n.Type != graph.NodeFunction {
			t.Errorf("helper should be Function, got %s", n.Type)
		}
		if !n.Exported {
			t.Error("helper should be exported (pub)")
		}
	} else {
		t.Error("expected helper function node")
	}

	// Private function
	if n, ok := nodeByName["internal_helper"]; ok {
		if n.Type != graph.NodeFunction {
			t.Errorf("internal_helper should be Function, got %s", n.Type)
		}
		if n.Exported {
			t.Error("internal_helper should not be exported")
		}
	} else {
		t.Error("expected internal_helper function node")
	}

	// Type alias
	if n, ok := nodeByName["Callback"]; ok {
		if n.Type != graph.NodeType_ {
			t.Errorf("Callback should be Type, got %s", n.Type)
		}
	} else {
		t.Error("expected Callback type alias node")
	}

	// Verify edges
	edgeCounts := make(map[graph.EdgeType]int)
	for _, edge := range result.Edges {
		edgeCounts[edge.Type]++
	}

	// Import edges: 1 use statement
	if edgeCounts[graph.EdgeImports] != 1 {
		t.Errorf("Imports edges = %d, want 1", edgeCounts[graph.EdgeImports])
	}

	// Implements edges: GreetingService -> Greeter
	if edgeCounts[graph.EdgeImplements] < 1 {
		t.Errorf("Implements edges = %d, want at least 1", edgeCounts[graph.EdgeImplements])
	}

	// Contains edges should be present
	if edgeCounts[graph.EdgeContains] < 10 {
		t.Errorf("Contains edges = %d, want at least 10", edgeCounts[graph.EdgeContains])
	}

	// Calls edges: internal_helper -> helper
	if edgeCounts[graph.EdgeCalls] < 1 {
		t.Errorf("Calls edges = %d, want at least 1", edgeCounts[graph.EdgeCalls])
	}

	// Verify the specific call edge
	foundHelperCall := false
	for _, edge := range result.Edges {
		if edge.Type == graph.EdgeCalls && edge.Properties != nil && edge.Properties["callee"] == "helper" {
			foundHelperCall = true
		}
	}
	if !foundHelperCall {
		t.Error("expected EdgeCalls: internal_helper -> helper")
	}
}

func TestLanguageAndExtensions(t *testing.T) {
	p := NewParser()
	if p.Language() != parser.LangRust {
		t.Errorf("Language() = %q, want %q", p.Language(), parser.LangRust)
	}
	exts := p.Extensions()
	if len(exts) != 1 || exts[0] != ".rs" {
		t.Errorf("Extensions() = %v, want [\".rs\"]", exts)
	}
}

func TestParseSampleFixture(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	fixturePath := filepath.Join(filepath.Dir(thisFile), "testdata", "sample.rs")

	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Skipf("testdata/sample.rs not found: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile(fixturePath, content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	nodeByName := indexByName(result.Nodes)

	// Check trait
	if _, ok := nodeByName["Validator"]; !ok {
		t.Error("expected Validator trait node")
	}

	// Check enum
	if _, ok := nodeByName["AppError"]; !ok {
		t.Error("expected AppError enum node")
	}

	// Check struct
	if n, ok := nodeByName["User"]; ok {
		if n.Type != graph.NodeStruct {
			t.Errorf("User should be Struct, got %s", n.Type)
		}
		if !strings.Contains(n.Properties["fields"], "name") {
			t.Errorf("User fields should contain name, got %q", n.Properties["fields"])
		}
	} else {
		t.Error("expected User struct node")
	}

	// Check methods
	for _, name := range []string{"display_name", "new", "is_adult"} {
		if _, ok := nodeByName[name]; !ok {
			t.Errorf("expected method %s", name)
		}
	}

	// Check functions
	for _, name := range []string{"greet", "format_greeting", "process_users"} {
		if _, ok := nodeByName[name]; !ok {
			t.Errorf("expected function %s", name)
		}
	}

	// Check Implements edges
	implementsCount := 0
	for _, edge := range result.Edges {
		if edge.Type == graph.EdgeImplements {
			implementsCount++
		}
	}
	// User implements Validator and fmt::Display
	if implementsCount < 2 {
		t.Errorf("expected at least 2 Implements edges, got %d", implementsCount)
	}

	// Check that mod helpers is extracted as a package
	if _, ok := nodeByName["helpers"]; !ok {
		t.Error("expected helpers module node")
	}

	// Check that const and static are extracted
	if _, ok := nodeByName["MAX_RETRIES"]; !ok {
		t.Error("expected MAX_RETRIES constant node")
	}
	if _, ok := nodeByName["APP_NAME"]; !ok {
		t.Error("expected APP_NAME static node")
	}

	// Check type alias
	if _, ok := nodeByName["Result"]; !ok {
		t.Error("expected Result type alias node")
	}

	// Check call edges
	callEdges := 0
	for _, edge := range result.Edges {
		if edge.Type == graph.EdgeCalls {
			callEdges++
		}
	}
	if callEdges < 1 {
		t.Errorf("expected at least 1 Calls edge, got %d", callEdges)
	}
}

func TestTestFileDetection(t *testing.T) {
	source := `use super::*;

#[test]
fn test_add() {
    assert_eq!(2 + 2, 4);
}

#[test]
fn test_subtract() {
    assert_eq!(4 - 2, 2);
}

fn helper_setup() -> u32 {
    42
}
`
	p := NewParser()
	result, err := p.ParseFile("tests/integration_test.rs", []byte(source))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	// Verify file node is NodeTestFile (because it's in tests/ directory).
	var testFileNodes []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeTestFile {
			testFileNodes = append(testFileNodes, n)
		}
	}
	if len(testFileNodes) != 1 {
		t.Errorf("TestFile count = %d, want 1", len(testFileNodes))
	}

	// Verify NodeFile is NOT present.
	for _, n := range result.Nodes {
		if n.Type == graph.NodeFile {
			t.Error("expected NodeTestFile, not NodeFile for test file")
		}
	}

	// Verify test functions are extracted.
	var testFuncs []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeTestFunction {
			testFuncs = append(testFuncs, n)
		}
	}

	if len(testFuncs) != 2 {
		t.Errorf("TestFunction count = %d, want 2", len(testFuncs))
		for _, tf := range testFuncs {
			t.Logf("  test func: %s", tf.Name)
		}
	}

	// Verify specific test functions are NodeTestFunction.
	nodeByName := indexByName(result.Nodes)
	for _, name := range []string{"test_add", "test_subtract"} {
		n, ok := nodeByName[name]
		if !ok {
			t.Errorf("expected function %s", name)
			continue
		}
		if n.Type != graph.NodeTestFunction {
			t.Errorf("%s should be TestFunction, got %s", name, n.Type)
		}
	}

	// Verify helper function is still NodeFunction.
	if n, ok := nodeByName["helper_setup"]; ok {
		if n.Type != graph.NodeFunction {
			t.Errorf("helper_setup should be Function (helper), got %s", n.Type)
		}
	} else {
		t.Error("expected helper_setup function")
	}
}

func TestImplTraitForStruct(t *testing.T) {
	source := `pub trait Drawable {
    fn draw(&self);
}

pub struct Circle {
    radius: f64,
}

impl Drawable for Circle {
    fn draw(&self) {
        println!("Drawing circle");
    }
}

impl Circle {
    pub fn new(radius: f64) -> Self {
        Circle { radius }
    }

    pub fn area(&self) -> f64 {
        std::f64::consts::PI * self.radius * self.radius
    }
}
`
	p := NewParser()
	result, err := p.ParseFile("src/shapes.rs", []byte(source))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	// Check Implements edge
	hasImplements := false
	for _, edge := range result.Edges {
		if edge.Type == graph.EdgeImplements {
			hasImplements = true
			if edge.Properties["implements"] != "Drawable" {
				t.Errorf("Implements edge property = %q, want Drawable", edge.Properties["implements"])
			}
		}
	}
	if !hasImplements {
		t.Error("expected Implements edge (Circle implements Drawable)")
	}

	// Check methods from inherent impl and trait impl
	nodeByName := indexByName(result.Nodes)
	for _, name := range []string{"new", "area", "draw"} {
		if _, ok := nodeByName[name]; !ok {
			t.Errorf("expected method %s", name)
		}
	}

	// Verify inherent impl methods have struct property
	if n, ok := nodeByName["new"]; ok {
		if n.Properties["struct"] != "Circle" {
			t.Errorf("new method struct = %q, want Circle", n.Properties["struct"])
		}
	}

	// Verify trait impl methods have both struct and trait properties
	if n, ok := nodeByName["draw"]; ok {
		if n.Properties["struct"] != "Circle" {
			t.Errorf("draw method struct = %q, want Circle", n.Properties["struct"])
		}
		if n.Properties["trait"] != "Drawable" {
			t.Errorf("draw method trait = %q, want Drawable", n.Properties["trait"])
		}
	}
}

func TestNonTestFileHasNoTestNodes(t *testing.T) {
	source := `pub fn process() -> bool {
    true
}

fn helper() {}
`
	p := NewParser()
	result, err := p.ParseFile("src/lib.rs", []byte(source))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	for _, n := range result.Nodes {
		if n.Type == graph.NodeTestFile {
			t.Error("non-test file should not have NodeTestFile")
		}
	}
}

func TestRustTestFilePathPatterns(t *testing.T) {
	tests := []struct {
		filePath string
		isTest   bool
	}{
		{"tests/integration_test.rs", true},
		{"tests/common/mod.rs", true},
		{"src/lib.rs", false},
		{"src/main.rs", false},
		{"src/module/handler.rs", false},
	}
	for _, tc := range tests {
		got := isTestFilePath(tc.filePath)
		if got != tc.isTest {
			t.Errorf("isTestFilePath(%q) = %v, want %v", tc.filePath, got, tc.isTest)
		}
	}
}

func TestModDeclaration(t *testing.T) {
	source := `mod internal {
    pub fn inside() -> bool {
        true
    }
}

mod external;
`
	p := NewParser()
	result, err := p.ParseFile("src/lib.rs", []byte(source))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	nodeByName := indexByName(result.Nodes)

	// Check inline mod
	if n, ok := nodeByName["internal"]; ok {
		if n.Type != graph.NodePackage {
			t.Errorf("internal should be Package, got %s", n.Type)
		}
	} else {
		t.Error("expected internal module node")
	}

	// Check external mod declaration
	if n, ok := nodeByName["external"]; ok {
		if n.Type != graph.NodePackage {
			t.Errorf("external should be Package, got %s", n.Type)
		}
	} else {
		t.Error("expected external module node")
	}

	// Check function inside inline mod
	if _, ok := nodeByName["inside"]; !ok {
		t.Error("expected inside function from inline mod")
	}
}

func TestUseImports(t *testing.T) {
	source := `use std::io;
use std::collections::HashMap;
use crate::models::User;
`
	p := NewParser()
	result, err := p.ParseFile("src/lib.rs", []byte(source))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	var deps []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeDependency {
			deps = append(deps, n)
		}
	}

	if len(deps) != 3 {
		t.Errorf("Dependency count = %d, want 3", len(deps))
	}

	// Verify import edges
	importEdges := 0
	for _, edge := range result.Edges {
		if edge.Type == graph.EdgeImports {
			importEdges++
		}
	}
	if importEdges != 3 {
		t.Errorf("Import edges = %d, want 3", importEdges)
	}
}

// Helpers

func assertCount(t *testing.T, counts map[graph.NodeType]int, nt graph.NodeType, want int) {
	t.Helper()
	if counts[nt] != want {
		t.Errorf("%s count = %d, want %d", nt, counts[nt], want)
	}
}

func indexByName(nodes []*graph.Node) map[string]*graph.Node {
	m := make(map[string]*graph.Node, len(nodes))
	for _, n := range nodes {
		m[n.Name] = n
	}
	return m
}
