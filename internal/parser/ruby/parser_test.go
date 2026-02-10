package ruby

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

func TestLanguageAndExtensions(t *testing.T) {
	p := NewParser()
	if p.Language() != parser.LangRuby {
		t.Errorf("Language() = %q, want %q", p.Language(), parser.LangRuby)
	}
	exts := p.Extensions()
	if len(exts) != 2 || exts[0] != ".rb" || exts[1] != ".rake" {
		t.Errorf("Extensions() = %v, want [\".rb\", \".rake\"]", exts)
	}
}

func TestParseSample(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	fixturePath := filepath.Join(filepath.Dir(thisFile), "testdata", "sample.rb")

	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("could not read testdata/sample.rb: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile(fixturePath, content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	if result.Language != parser.LangRuby {
		t.Errorf("Language = %q, want %q", result.Language, parser.LangRuby)
	}

	// Count nodes by type.
	counts := make(map[graph.NodeType]int)
	for _, n := range result.Nodes {
		counts[n.Type]++
	}

	// 1 file node
	assertCount(t, counts, graph.NodeFile, 1)
	// 2 modules: MyApp, Services
	assertCount(t, counts, graph.NodeModule, 2)
	// 1 class: UserService
	assertCount(t, counts, graph.NodeClass, 1)
	// 2 requires: json, helpers/auth
	assertCount(t, counts, graph.NodeDependency, 2)
	// 1 constant: MY_CONST
	assertCount(t, counts, graph.NodeConstant, 1)

	// Methods: initialize, greet, process, validate_input + attr_reader(name) + attr_accessor(email) = 6
	assertCount(t, counts, graph.NodeMethod, 6)

	nodeByName := indexByName(result.Nodes)

	// Verify modules.
	if n, ok := nodeByName["MyApp"]; ok {
		if n.Type != graph.NodeModule {
			t.Errorf("MyApp should be Module, got %s", n.Type)
		}
	} else {
		t.Error("expected MyApp module node")
	}

	if n, ok := nodeByName["Services"]; ok {
		if n.Type != graph.NodeModule {
			t.Errorf("Services should be Module, got %s", n.Type)
		}
	} else {
		t.Error("expected Services module node")
	}

	// Verify class with inheritance.
	if n := findNodeByNameAndType(result.Nodes, "UserService", graph.NodeClass); n != nil {
		if n.Properties["bases"] != "BaseService" {
			t.Errorf("UserService bases = %q, want BaseService", n.Properties["bases"])
		}
		if !strings.Contains(n.Properties["includes"], "Loggable") {
			t.Errorf("UserService includes = %q, want to contain Loggable", n.Properties["includes"])
		}
	} else {
		t.Error("expected UserService class node")
	}

	// Verify constant.
	if _, ok := nodeByName["MY_CONST"]; !ok {
		t.Error("expected MY_CONST constant node")
	}

	// Verify requires.
	depNames := make(map[string]bool)
	for _, n := range result.Nodes {
		if n.Type == graph.NodeDependency {
			depNames[n.Name] = true
		}
	}
	if !depNames["json"] {
		t.Error("expected dependency node for 'json'")
	}
	if !depNames["helpers/auth"] {
		t.Error("expected dependency node for 'helpers/auth'")
	}

	// Verify methods.
	for _, name := range []string{"initialize", "greet", "process", "validate_input"} {
		if _, ok := nodeByName[name]; !ok {
			t.Errorf("expected method %s", name)
		}
	}

	// Verify private method visibility.
	if n, ok := nodeByName["validate_input"]; ok {
		if n.Properties["visibility"] != "private" {
			t.Errorf("validate_input visibility = %q, want private", n.Properties["visibility"])
		}
		if n.Exported {
			t.Error("validate_input should not be exported")
		}
	}

	// Verify attr_reader generated method.
	if n, ok := nodeByName["name"]; ok {
		if n.Properties["kind"] != "attr_reader" {
			t.Errorf("name accessor kind = %q, want attr_reader", n.Properties["kind"])
		}
	} else {
		t.Error("expected name accessor method node")
	}

	// Verify edges.
	edgeCounts := make(map[graph.EdgeType]int)
	for _, edge := range result.Edges {
		edgeCounts[edge.Type]++
	}

	// Import edges: 2 requires
	if edgeCounts[graph.EdgeImports] != 2 {
		t.Errorf("Imports edges = %d, want 2", edgeCounts[graph.EdgeImports])
	}

	// Implements edges: include Loggable
	if edgeCounts[graph.EdgeImplements] < 1 {
		t.Errorf("Implements edges = %d, want at least 1", edgeCounts[graph.EdgeImplements])
	}

	// Contains edges should be present.
	if edgeCounts[graph.EdgeContains] < 10 {
		t.Errorf("Contains edges = %d, want at least 10", edgeCounts[graph.EdgeContains])
	}

	// Calls edges: process -> validate_input, process -> greet
	if edgeCounts[graph.EdgeCalls] < 2 {
		t.Errorf("Calls edges = %d, want at least 2", edgeCounts[graph.EdgeCalls])
	}
}

func TestParseRoutes(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	fixturePath := filepath.Join(filepath.Dir(thisFile), "testdata", "routes.rb")

	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("could not read testdata/routes.rb: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile(fixturePath, content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	// Collect API endpoint nodes.
	var endpoints []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeAPIEndpoint {
			endpoints = append(endpoints, n)
		}
	}

	if len(endpoints) != 5 {
		t.Fatalf("APIEndpoint count = %d, want 5", len(endpoints))
	}

	// Verify HTTP methods.
	foundMethods := make(map[string]bool)
	for _, ep := range endpoints {
		method := ep.Properties["http_method"]
		foundMethods[method] = true
	}

	for _, method := range []string{"GET", "POST", "PUT", "DELETE", "PATCH"} {
		if !foundMethods[method] {
			t.Errorf("missing API endpoint with HTTP method %s", method)
		}
	}

	// Verify paths contain /users.
	for _, ep := range endpoints {
		if !strings.Contains(ep.Properties["path"], "/users") {
			t.Errorf("endpoint path = %q, expected to contain /users", ep.Properties["path"])
		}
	}

	// Verify controller#action mapping.
	for _, ep := range endpoints {
		controller := ep.Properties["controller"]
		if controller == "" {
			t.Errorf("endpoint %s missing controller property", ep.Name)
		}
		if !strings.Contains(controller, "users#") {
			t.Errorf("endpoint %s controller = %q, expected users#<action>", ep.Name, controller)
		}
	}
}

func TestParseController(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	fixturePath := filepath.Join(filepath.Dir(thisFile), "testdata", "controller.rb")

	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("could not read testdata/controller.rb: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile(fixturePath, content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	counts := make(map[graph.NodeType]int)
	for _, n := range result.Nodes {
		counts[n.Type]++
	}

	// PostsController class.
	assertCount(t, counts, graph.NodeClass, 1)
	// User model (ActiveRecord).
	assertCount(t, counts, graph.NodeDBModel, 1)

	nodeByName := indexByName(result.Nodes)

	// Verify PostsController.
	if n := findNodeByNameAndType(result.Nodes, "PostsController", graph.NodeClass); n != nil {
		if n.Properties["bases"] != "ApplicationController" {
			t.Errorf("PostsController bases = %q, want ApplicationController", n.Properties["bases"])
		}
	} else {
		t.Error("expected PostsController class node")
	}

	// Verify ActiveRecord model.
	if n := findNodeByNameAndType(result.Nodes, "User", graph.NodeDBModel); n != nil {
		if n.Properties["bases"] != "ApplicationRecord" {
			t.Errorf("User bases = %q, want ApplicationRecord", n.Properties["bases"])
		}
	} else {
		t.Error("expected User DBModel node")
	}

	// Verify controller actions.
	for _, name := range []string{"index", "create", "show"} {
		if _, ok := nodeByName[name]; !ok {
			t.Errorf("expected controller method %s", name)
		}
	}

	// Verify private method.
	if n, ok := nodeByName["post_params"]; ok {
		if n.Properties["visibility"] != "private" {
			t.Errorf("post_params visibility = %q, want private", n.Properties["visibility"])
		}
	} else {
		t.Error("expected post_params method node")
	}

	// Verify API endpoint nodes from controller actions.
	var endpoints []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeAPIEndpoint {
			endpoints = append(endpoints, n)
		}
	}
	// Controller actions index, create, show should generate endpoints.
	if len(endpoints) < 3 {
		t.Errorf("APIEndpoint count = %d, want at least 3", len(endpoints))
	}

	// Verify EdgeExposes exists.
	exposesCount := 0
	for _, edge := range result.Edges {
		if edge.Type == graph.EdgeExposes {
			exposesCount++
		}
	}
	if exposesCount < 3 {
		t.Errorf("Exposes edges = %d, want at least 3", exposesCount)
	}
}

func TestTestFileDetection(t *testing.T) {
	source := `require 'rails_helper'

class UserServiceTest < Minitest::Test
  def test_greet
    svc = UserService.new("Alice")
    assert_equal "Hello, Alice", svc.greet
  end

  def test_process
    svc = UserService.new("Bob")
    svc.process
  end

  def helper_method
    "not a test"
  end
end
`
	p := NewParser()
	result, err := p.ParseFile("test/services/user_service_test.rb", []byte(source))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	// Verify file node is NodeTestFile.
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

	// Verify test methods are NodeTestFunction.
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

	// Verify specific test methods.
	nodeByName := indexByName(result.Nodes)
	for _, name := range []string{"test_greet", "test_process"} {
		n, ok := nodeByName[name]
		if !ok {
			t.Errorf("expected method %s", name)
			continue
		}
		if n.Type != graph.NodeTestFunction {
			t.Errorf("%s should be TestFunction, got %s", name, n.Type)
		}
	}

	// Verify helper method is still NodeMethod.
	if n, ok := nodeByName["helper_method"]; ok {
		if n.Type != graph.NodeMethod {
			t.Errorf("helper_method should be Method, got %s", n.Type)
		}
	} else {
		t.Error("expected helper_method node")
	}
}

func TestRSpecDetection(t *testing.T) {
	source := `require 'rails_helper'

RSpec.describe UserService do
  describe '#greet' do
    it 'returns greeting' do
      svc = UserService.new("Alice")
      expect(svc.greet).to eq("Hello, Alice")
    end

    it 'handles empty name' do
      svc = UserService.new("")
      expect(svc.greet).to eq("Hello, ")
    end
  end
end
`
	p := NewParser()
	result, err := p.ParseFile("spec/services/user_service_spec.rb", []byte(source))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	// Should have test file node.
	var testFileNodes []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeTestFile {
			testFileNodes = append(testFileNodes, n)
		}
	}
	if len(testFileNodes) != 1 {
		t.Errorf("TestFile count = %d, want 1", len(testFileNodes))
	}

	// Should have test function nodes for `it` blocks.
	var testFuncs []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeTestFunction {
			testFuncs = append(testFuncs, n)
		}
	}

	if len(testFuncs) != 2 {
		t.Errorf("TestFunction count = %d, want 2 (one per `it` block)", len(testFuncs))
		for _, tf := range testFuncs {
			t.Logf("  test func: %s", tf.Name)
		}
	}

	// Verify names come from `it` block descriptions.
	names := make(map[string]bool)
	for _, tf := range testFuncs {
		names[tf.Name] = true
	}
	if !names["returns greeting"] {
		t.Error("expected TestFunction 'returns greeting'")
	}
	if !names["handles empty name"] {
		t.Error("expected TestFunction 'handles empty name'")
	}
}

func TestRubyTestFilenamePatterns(t *testing.T) {
	tests := []struct {
		filename string
		isTest   bool
	}{
		{"user_service_spec.rb", true},
		{"user_service_test.rb", true},
		{"test_user_service.rb", true},
		{"user_service.rb", false},
		{"test_helper.rb", true},
		{"spec_helper.rb", false},
		{"config.rb", false},
	}
	for _, tc := range tests {
		got := isTestFilename(tc.filename)
		if got != tc.isTest {
			t.Errorf("isTestFilename(%q) = %v, want %v", tc.filename, got, tc.isTest)
		}
	}
}

func TestFunctionCallExtraction(t *testing.T) {
	source := `module MyApp
  class UserService
    def process
      validate_input
      greet
    end

    def greet
      "hello"
    end

    def validate_input
      true
    end
  end
end
`

	p := NewParser()
	result, err := p.ParseFile("app/services/user_service.rb", []byte(source))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	// Verify EdgeCalls edges.
	type callEdge struct {
		sourceID string
		targetID string
		callee   string
	}
	var calls []callEdge
	for _, edge := range result.Edges {
		if edge.Type == graph.EdgeCalls {
			callee := ""
			if edge.Properties != nil {
				callee = edge.Properties["callee"]
			}
			calls = append(calls, callEdge{
				sourceID: edge.SourceID,
				targetID: edge.TargetID,
				callee:   callee,
			})
		}
	}

	processID := graph.NewNodeID(string(graph.NodeMethod), "app/services/user_service.rb", "UserService#process")
	validateID := graph.NewNodeID(string(graph.NodeMethod), "app/services/user_service.rb", "UserService#validate_input")
	greetID := graph.NewNodeID(string(graph.NodeMethod), "app/services/user_service.rb", "UserService#greet")

	foundProcessValidate := false
	foundProcessGreet := false
	for _, c := range calls {
		if c.sourceID == processID && c.targetID == validateID && c.callee == "validate_input" {
			foundProcessValidate = true
		}
		if c.sourceID == processID && c.targetID == greetID && c.callee == "greet" {
			foundProcessGreet = true
		}
	}

	if !foundProcessValidate {
		t.Error("expected EdgeCalls: process -> validate_input (same-class)")
	}
	if !foundProcessGreet {
		t.Error("expected EdgeCalls: process -> greet (same-class)")
	}
}

func TestNonTestFileHasNoTestNodes(t *testing.T) {
	source := `class UserService
  def test_method
    "not a test"
  end
end
`
	p := NewParser()
	result, err := p.ParseFile("app/services/user_service.rb", []byte(source))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	for _, n := range result.Nodes {
		if n.Type == graph.NodeTestFile {
			t.Error("non-test file should not have NodeTestFile")
		}
		if n.Type == graph.NodeTestFunction {
			t.Error("non-test file should not have NodeTestFunction")
		}
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

func findNodeByNameAndType(nodes []*graph.Node, name string, nodeType graph.NodeType) *graph.Node {
	for _, n := range nodes {
		if n.Name == name && n.Type == nodeType {
			return n
		}
	}
	return nil
}
