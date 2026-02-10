package java

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

const testSource = `package com.example.demo;

import java.util.List;
import java.util.Map;

/**
 * A simple interface for greeting.
 */
public interface Greeter {
    String greet(String name);
    void reset();
}

/**
 * Status codes.
 */
enum Status {
    OK,
    ERROR,
    PENDING
}

/**
 * A service that implements Greeter.
 */
public class GreetingService implements Greeter {

    private String prefix;
    private int count;

    /**
     * Create a new GreetingService.
     */
    public GreetingService(String prefix) {
        this.prefix = prefix;
        this.count = 0;
    }

    @Override
    public String greet(String name) {
        count++;
        return prefix + " " + name;
    }

    @Override
    public void reset() {
        count = 0;
    }

    /**
     * Get the greeting count.
     */
    public int getCount() {
        return count;
    }

    @Deprecated
    public String legacyGreet(String name) {
        return greet(name);
    }
}
`

func TestParseFile(t *testing.T) {
	p := NewParser()

	result, err := p.ParseFile("demo/GreetingService.java", []byte(testSource))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	if result.FilePath != "demo/GreetingService.java" {
		t.Errorf("FilePath = %q, want %q", result.FilePath, "demo/GreetingService.java")
	}
	if result.Language != parser.LangJava {
		t.Errorf("Language = %q, want %q", result.Language, parser.LangJava)
	}

	// Count nodes by type
	counts := make(map[graph.NodeType]int)
	for _, n := range result.Nodes {
		counts[n.Type]++
	}

	// 1 file node
	assertCount(t, counts, graph.NodeFile, 1)
	// 1 package node
	assertCount(t, counts, graph.NodePackage, 1)
	// 2 imports
	assertCount(t, counts, graph.NodeDependency, 2)
	// 1 interface: Greeter
	assertCount(t, counts, graph.NodeInterface, 1)
	// 1 enum: Status
	assertCount(t, counts, graph.NodeEnum, 1)
	// 1 class: GreetingService
	assertCount(t, counts, graph.NodeClass, 1)
	// Methods: Greeter(greet, reset) + GreetingService(constructor, greet, reset, getCount, legacyGreet) = 7
	assertCount(t, counts, graph.NodeMethod, 7)
	// Fields: prefix, count = 2
	assertCount(t, counts, graph.NodeVariable, 2)

	// Verify specific nodes
	nodeByName := indexByName(result.Nodes)

	// Package
	if n, ok := nodeByName["com.example.demo"]; ok {
		if n.Type != graph.NodePackage {
			t.Errorf("expected Package type, got %s", n.Type)
		}
	} else {
		t.Error("expected package node 'com.example.demo'")
	}

	// Interface with docstring
	if n, ok := nodeByName["Greeter"]; ok {
		if n.Type != graph.NodeInterface {
			t.Errorf("Greeter should be Interface, got %s", n.Type)
		}
		if n.DocComment == "" {
			t.Error("Greeter should have a Javadoc comment")
		}
		if n.Properties["methods"] == "" {
			t.Error("Greeter should list its methods in properties")
		}
	} else {
		t.Error("expected Greeter interface node")
	}

	// Enum
	if n, ok := nodeByName["Status"]; ok {
		if n.Type != graph.NodeEnum {
			t.Errorf("Status should be Enum, got %s", n.Type)
		}
		if n.Properties["constants"] == "" {
			t.Error("Status enum should have constants in properties")
		}
		if !strings.Contains(n.Properties["constants"], "OK") {
			t.Errorf("Status constants should contain OK, got %q", n.Properties["constants"])
		}
	} else {
		t.Error("expected Status enum node")
	}

	// Class with implements (use findNodeByNameAndType since constructor shares the name)
	if n := findNodeByNameAndType(result.Nodes, "GreetingService", graph.NodeClass); n != nil {
		if n.Properties["implements"] == "" {
			t.Error("GreetingService should list implemented interfaces")
		}
		if !strings.Contains(n.Properties["implements"], "Greeter") {
			t.Errorf("GreetingService implements = %q, want Greeter", n.Properties["implements"])
		}
		if n.DocComment == "" {
			t.Error("GreetingService should have a Javadoc comment")
		}
	} else {
		t.Error("expected GreetingService class node")
	}

	// Constructor
	if n := findNodeByNameAndType(result.Nodes, "GreetingService", graph.NodeMethod); n != nil {
		if n.Properties["constructor"] != "true" {
			t.Error("GreetingService constructor should have constructor=true property")
		}
	} else {
		t.Error("expected GreetingService constructor as a Method node")
	}

	// Method with annotation
	for _, n := range result.Nodes {
		if n.Name == "legacyGreet" && n.Type == graph.NodeMethod {
			if n.Properties["annotations"] == "" {
				t.Error("legacyGreet should have annotations")
			}
			if !strings.Contains(n.Properties["annotations"], "Deprecated") {
				t.Errorf("legacyGreet annotations = %q, want Deprecated", n.Properties["annotations"])
			}
		}
	}

	// Fields
	if n, ok := nodeByName["prefix"]; ok {
		if n.Type != graph.NodeVariable {
			t.Errorf("prefix should be Variable, got %s", n.Type)
		}
	} else {
		t.Error("expected prefix field node")
	}

	// Verify edges
	edgeCounts := make(map[graph.EdgeType]int)
	for _, edge := range result.Edges {
		edgeCounts[edge.Type]++
	}

	// Import edges
	if edgeCounts[graph.EdgeImports] != 2 {
		t.Errorf("Imports edges = %d, want 2", edgeCounts[graph.EdgeImports])
	}

	// Implements edges: GreetingService -> Greeter
	if edgeCounts[graph.EdgeImplements] < 1 {
		t.Errorf("Implements edges = %d, want at least 1", edgeCounts[graph.EdgeImplements])
	}

	// Contains edges should be present
	if edgeCounts[graph.EdgeContains] < 10 {
		t.Errorf("Contains edges = %d, want at least 10", edgeCounts[graph.EdgeContains])
	}
}

func TestLanguageAndExtensions(t *testing.T) {
	p := NewParser()
	if p.Language() != parser.LangJava {
		t.Errorf("Language() = %q, want %q", p.Language(), parser.LangJava)
	}
	exts := p.Extensions()
	if len(exts) != 1 || exts[0] != ".java" {
		t.Errorf("Extensions() = %v, want [\".java\"]", exts)
	}
}

func TestParseSampleFixture(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	samplePath := filepath.Join(projectRoot, "testdata", "Sample.java")

	content, err := os.ReadFile(samplePath)
	if err != nil {
		t.Skipf("testdata/Sample.java not found: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile(samplePath, content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	nodeByName := indexByName(result.Nodes)

	// Check interface
	if _, ok := nodeByName["Repository"]; !ok {
		t.Error("expected Repository interface node")
	}

	// Check enum
	if _, ok := nodeByName["AccountStatus"]; !ok {
		t.Error("expected AccountStatus enum node")
	}

	// Check class (use findNodeByNameAndType since constructor shares the name)
	if n := findNodeByNameAndType(result.Nodes, "User", graph.NodeClass); n != nil {
		if n.Properties["implements"] == "" {
			t.Error("User should implement interfaces")
		}
	} else {
		t.Error("expected User class node")
	}

	// Check methods
	for _, name := range []string{"getDisplayName", "isActive", "findById"} {
		if _, ok := nodeByName[name]; !ok {
			t.Errorf("expected method %s", name)
		}
	}

	// Check Implements edges
	hasImplements := false
	for _, edge := range result.Edges {
		if edge.Type == graph.EdgeImplements {
			hasImplements = true
			break
		}
	}
	if !hasImplements {
		t.Error("expected Implements edge (User implements Repository)")
	}
}

func TestExtractHTTPClientCalls(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	fixturePath := filepath.Join(filepath.Dir(thisFile), "testdata", "http_client.java")

	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("could not read testdata/http_client.java: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile(fixturePath, content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	// Collect api_call dependency nodes
	var apiCalls []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeDependency && n.Properties != nil && n.Properties["kind"] == "api_call" {
			apiCalls = append(apiCalls, n)
		}
	}

	if len(apiCalls) < 3 {
		t.Fatalf("expected at least 3 api_call dependencies, got %d", len(apiCalls))
	}

	// Verify HTTP methods are extracted
	foundMethods := make(map[string]bool)
	for _, call := range apiCalls {
		method := call.Properties["http_method"]
		foundMethods[method] = true
		if call.Properties["framework"] != "spring-resttemplate" {
			t.Errorf("api_call framework = %q, want %q", call.Properties["framework"], "spring-resttemplate")
		}
	}

	for _, method := range []string{"GET", "POST", "DELETE"} {
		if !foundMethods[method] {
			t.Errorf("missing api_call with HTTP method %s", method)
		}
	}

	// Verify paths contain /api/users
	for _, call := range apiCalls {
		if !strings.Contains(call.Properties["path"], "/api/users") {
			t.Errorf("api_call path = %q, expected to contain /api/users", call.Properties["path"])
		}
	}

	// Verify EdgeCalls edges from methods to api_call deps
	callsCount := 0
	for _, edge := range result.Edges {
		if edge.Type == graph.EdgeCalls {
			callsCount++
		}
	}
	if callsCount < 3 {
		t.Errorf("expected at least 3 EdgeCalls edges, got %d", callsCount)
	}
}

func TestExtractFunctionCalls(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	fixturePath := filepath.Join(filepath.Dir(thisFile), "testdata", "calls.java")

	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("could not read testdata/calls.java: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile(fixturePath, content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	// Build a set of EdgeCalls edges with their properties
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

	// Build node ID lookup
	nodeIDMap := make(map[string]*graph.Node)
	for _, n := range result.Nodes {
		nodeIDMap[n.ID] = n
	}

	// Verify: processUser calls validate (same-class)
	processUserID := graph.NewNodeID(string(graph.NodeMethod), fixturePath, "UserService.processUser")
	validateID := graph.NewNodeID(string(graph.NodeMethod), fixturePath, "UserService.validate")
	handleRequestID := graph.NewNodeID(string(graph.NodeMethod), fixturePath, "UserService.handleRequest")

	foundProcessUserValidate := false
	foundProcessUserStringUtils := false
	foundProcessUserUser := false
	foundHandleRequestValidate := false
	foundHandleRequestProcessUser := false

	for _, c := range calls {
		if c.sourceID == processUserID && c.targetID == validateID && c.callee == "validate" {
			foundProcessUserValidate = true
		}
		if c.sourceID == processUserID && c.callee == "capitalize" {
			foundProcessUserStringUtils = true
		}
		if c.sourceID == processUserID && c.callee == "create" {
			foundProcessUserUser = true
		}
		if c.sourceID == handleRequestID && c.targetID == validateID && c.callee == "validate" {
			foundHandleRequestValidate = true
		}
		if c.sourceID == handleRequestID && c.targetID == processUserID && c.callee == "processUser" {
			foundHandleRequestProcessUser = true
		}
	}

	if !foundProcessUserValidate {
		t.Error("expected EdgeCalls: processUser -> validate (same-class)")
	}
	if !foundProcessUserStringUtils {
		t.Error("expected EdgeCalls: processUser -> StringUtils import (capitalize)")
	}
	if !foundProcessUserUser {
		t.Error("expected EdgeCalls: processUser -> User import (create)")
	}
	if !foundHandleRequestValidate {
		t.Error("expected EdgeCalls: handleRequest -> validate (this-qualified)")
	}
	if !foundHandleRequestProcessUser {
		t.Error("expected EdgeCalls: handleRequest -> processUser (same-class)")
	}

	// Verify builtins are NOT generating calls (e.g., getName)
	for _, c := range calls {
		if c.callee == "getName" {
			t.Error("builtin getName should not generate an EdgeCalls edge")
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
