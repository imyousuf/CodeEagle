package csharp

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

const testSource = `using System;
using System.Collections.Generic;

namespace MyApp.Services
{
    /// <summary>
    /// Interface for greeting.
    /// </summary>
    public interface IGreeter
    {
        string Greet(string name);
        void Reset();
    }

    /// <summary>
    /// Status codes.
    /// </summary>
    public enum Status
    {
        OK,
        Error,
        Pending
    }

    /// <summary>
    /// A service that implements IGreeter.
    /// </summary>
    public class GreetingService : IGreeter
    {
        private string _prefix;
        private int _count;

        /// <summary>
        /// Create a new GreetingService.
        /// </summary>
        public GreetingService(string prefix)
        {
            _prefix = prefix;
            _count = 0;
        }

        public string Greet(string name)
        {
            _count++;
            return _prefix + " " + name;
        }

        public void Reset()
        {
            _count = 0;
        }

        /// <summary>
        /// Get the greeting count.
        /// </summary>
        public int GetCount()
        {
            return _count;
        }

        [Obsolete]
        public string LegacyGreet(string name)
        {
            return Greet(name);
        }
    }
}
`

func TestParseFile(t *testing.T) {
	p := NewParser()

	result, err := p.ParseFile("demo/GreetingService.cs", []byte(testSource))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	if result.FilePath != "demo/GreetingService.cs" {
		t.Errorf("FilePath = %q, want %q", result.FilePath, "demo/GreetingService.cs")
	}
	if result.Language != parser.LangCSharp {
		t.Errorf("Language = %q, want %q", result.Language, parser.LangCSharp)
	}

	// Count nodes by type
	counts := make(map[graph.NodeType]int)
	for _, n := range result.Nodes {
		counts[n.Type]++
	}

	// 1 file node
	assertCount(t, counts, graph.NodeFile, 1)
	// 1 package (namespace) node
	assertCount(t, counts, graph.NodePackage, 1)
	// 2 imports (using directives)
	assertCount(t, counts, graph.NodeDependency, 2)
	// 1 interface: IGreeter
	assertCount(t, counts, graph.NodeInterface, 1)
	// 1 enum: Status
	assertCount(t, counts, graph.NodeEnum, 1)
	// 1 class: GreetingService
	assertCount(t, counts, graph.NodeClass, 1)

	// Verify specific nodes
	nodeByName := indexByName(result.Nodes)

	// Namespace
	if n, ok := nodeByName["MyApp.Services"]; ok {
		if n.Type != graph.NodePackage {
			t.Errorf("expected Package type, got %s", n.Type)
		}
	} else {
		t.Error("expected namespace node 'MyApp.Services'")
	}

	// Interface with docstring
	if n, ok := nodeByName["IGreeter"]; ok {
		if n.Type != graph.NodeInterface {
			t.Errorf("IGreeter should be Interface, got %s", n.Type)
		}
		if n.DocComment == "" {
			t.Error("IGreeter should have a doc comment")
		}
		if n.Properties["methods"] == "" {
			t.Error("IGreeter should list its methods in properties")
		}
	} else {
		t.Error("expected IGreeter interface node")
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

	// Class with implements
	if n := findNodeByNameAndType(result.Nodes, "GreetingService", graph.NodeClass); n != nil {
		if n.Properties["implements"] == "" {
			t.Error("GreetingService should list implemented interfaces")
		}
		if !strings.Contains(n.Properties["implements"], "IGreeter") {
			t.Errorf("GreetingService implements = %q, want IGreeter", n.Properties["implements"])
		}
		if n.DocComment == "" {
			t.Error("GreetingService should have a doc comment")
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
		if n.Name == "LegacyGreet" && n.Type == graph.NodeMethod {
			if n.Properties["annotations"] == "" {
				t.Error("LegacyGreet should have annotations")
			}
			if !strings.Contains(n.Properties["annotations"], "Obsolete") {
				t.Errorf("LegacyGreet annotations = %q, want Obsolete", n.Properties["annotations"])
			}
		}
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

	// Implements edges: GreetingService -> IGreeter
	if edgeCounts[graph.EdgeImplements] < 1 {
		t.Errorf("Implements edges = %d, want at least 1", edgeCounts[graph.EdgeImplements])
	}

	// Contains edges should be present
	if edgeCounts[graph.EdgeContains] < 5 {
		t.Errorf("Contains edges = %d, want at least 5", edgeCounts[graph.EdgeContains])
	}
}

func TestLanguageAndExtensions(t *testing.T) {
	p := NewParser()
	if p.Language() != parser.LangCSharp {
		t.Errorf("Language() = %q, want %q", p.Language(), parser.LangCSharp)
	}
	exts := p.Extensions()
	if len(exts) != 1 || exts[0] != ".cs" {
		t.Errorf("Extensions() = %v, want [\".cs\"]", exts)
	}
}

func TestParseSampleFixture(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	samplePath := filepath.Join(filepath.Dir(thisFile), "testdata", "sample.cs")

	content, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatalf("could not read testdata/sample.cs: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile(samplePath, content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	nodeByName := indexByName(result.Nodes)

	// Check interface
	if n, ok := nodeByName["IRepository<T>"]; ok {
		if n.Type != graph.NodeInterface {
			t.Errorf("IRepository should be Interface, got %s", n.Type)
		}
	} else {
		// Tree-sitter might parse generic name differently
		found := false
		for _, n := range result.Nodes {
			if n.Type == graph.NodeInterface && strings.HasPrefix(n.Name, "IRepository") {
				found = true
				break
			}
		}
		if !found {
			t.Error("expected IRepository interface node")
		}
	}

	// Check enum
	if _, ok := nodeByName["AccountStatus"]; !ok {
		t.Error("expected AccountStatus enum node")
	}

	// Check struct
	if _, ok := nodeByName["Point"]; !ok {
		t.Error("expected Point struct node")
	}

	// Check class
	if n := findNodeByNameAndType(result.Nodes, "User", graph.NodeClass); n == nil {
		t.Error("expected User class node")
	}

	// Check UserRepository implements IRepository
	if n := findNodeByNameAndType(result.Nodes, "UserRepository", graph.NodeClass); n != nil {
		if n.Properties["implements"] == "" {
			t.Error("UserRepository should implement interfaces")
		}
	} else {
		t.Error("expected UserRepository class node")
	}

	// Check methods
	for _, name := range []string{"GetDisplayName", "Validate", "FindById", "Save"} {
		if _, ok := nodeByName[name]; !ok {
			t.Errorf("expected method %s", name)
		}
	}

	// Check constant
	found := false
	for _, n := range result.Nodes {
		if n.Name == "MaxUsers" && n.Type == graph.NodeConstant {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected MaxUsers constant node")
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
		t.Error("expected Implements edge")
	}
}

func TestParseControllerFixture(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	controllerPath := filepath.Join(filepath.Dir(thisFile), "testdata", "controller.cs")

	content, err := os.ReadFile(controllerPath)
	if err != nil {
		t.Fatalf("could not read testdata/controller.cs: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile(controllerPath, content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	// Verify class is detected
	classNode := findNodeByNameAndType(result.Nodes, "UsersController", graph.NodeClass)
	if classNode == nil {
		t.Fatal("expected UsersController class node")
	}
	if !strings.Contains(classNode.Properties["annotations"], "ApiController") {
		t.Errorf("UsersController should have ApiController annotation, got %q", classNode.Properties["annotations"])
	}

	// Verify API endpoint nodes are extracted
	var endpoints []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeAPIEndpoint {
			endpoints = append(endpoints, n)
		}
	}

	if len(endpoints) < 5 {
		t.Fatalf("expected at least 5 API endpoints, got %d", len(endpoints))
	}

	// Verify HTTP methods
	foundMethods := make(map[string]bool)
	for _, ep := range endpoints {
		foundMethods[ep.Properties["http_method"]] = true
	}

	for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
		if !foundMethods[method] {
			t.Errorf("missing API endpoint with HTTP method %s", method)
		}
	}

	// Verify route paths contain api/users
	for _, ep := range endpoints {
		if !strings.Contains(ep.Properties["path"], "api/users") {
			t.Errorf("endpoint path = %q, expected to contain api/users", ep.Properties["path"])
		}
	}

	// Verify EdgeExposes edges
	exposesCount := 0
	for _, edge := range result.Edges {
		if edge.Type == graph.EdgeExposes {
			exposesCount++
		}
	}
	if exposesCount < 5 {
		t.Errorf("expected at least 5 EdgeExposes edges, got %d", exposesCount)
	}
}

func TestTestFileDetection(t *testing.T) {
	source := `using System;
using Xunit;

namespace MyApp.Tests
{
    public class UserServiceTests
    {
        [Fact]
        public void TestCreateUser()
        {
            var svc = new UserService();
            Assert.NotNull(svc.Create("test"));
        }

        [Theory]
        [InlineData("Alice")]
        [InlineData("Bob")]
        public void TestCreateUserWithName(string name)
        {
            var svc = new UserService();
            Assert.NotNull(svc.Create(name));
        }

        [Fact]
        public void TestDeleteUser()
        {
            var svc = new UserService();
            svc.Delete(1);
        }

        // This is a helper, not a test method.
        private UserService CreateService()
        {
            return new UserService();
        }
    }
}
`
	p := NewParser()
	result, err := p.ParseFile("src/Tests/UserServiceTests.cs", []byte(source))
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

	// Verify test functions are extracted.
	var testFuncs []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeTestFunction {
			testFuncs = append(testFuncs, n)
		}
	}

	if len(testFuncs) != 3 {
		t.Errorf("TestFunction count = %d, want 3", len(testFuncs))
		for _, tf := range testFuncs {
			t.Logf("  test func: %s (annotations=%s)", tf.Name, tf.Properties["annotations"])
		}
	}

	// Verify specific test methods are NodeTestFunction.
	nodeByName := indexByName(result.Nodes)
	for _, name := range []string{"TestCreateUser", "TestCreateUserWithName", "TestDeleteUser"} {
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
	if n, ok := nodeByName["CreateService"]; ok {
		if n.Type != graph.NodeMethod {
			t.Errorf("CreateService should be Method (helper), got %s", n.Type)
		}
	} else {
		t.Error("expected CreateService helper method")
	}
}

func TestNonTestFileHasNoTestNodes(t *testing.T) {
	source := `using System;
using Xunit;

namespace MyApp.Services
{
    public class UserService
    {
        [Fact]
        public void TestMethod() {}
    }
}
`
	p := NewParser()
	result, err := p.ParseFile("src/Services/UserService.cs", []byte(source))
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

func TestCSharpTestFilenamePatterns(t *testing.T) {
	tests := []struct {
		filename string
		isTest   bool
	}{
		{"UserServiceTest.cs", true},
		{"UserServiceTests.cs", true},
		{"TestUserService.cs", true},
		{"UserService.cs", false},
		{"TestData.txt", false},
		{"MyTest.cs", true},
	}
	for _, tc := range tests {
		got := isTestFilename(tc.filename)
		if got != tc.isTest {
			t.Errorf("isTestFilename(%q) = %v, want %v", tc.filename, got, tc.isTest)
		}
	}
}

func TestFileScopedNamespace(t *testing.T) {
	source := `using System;

namespace MyApp.Services;

public class SimpleService
{
    public void DoWork()
    {
    }
}
`
	p := NewParser()
	result, err := p.ParseFile("demo/SimpleService.cs", []byte(source))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	nodeByName := indexByName(result.Nodes)

	// Check namespace
	if n, ok := nodeByName["MyApp.Services"]; ok {
		if n.Type != graph.NodePackage {
			t.Errorf("expected Package type, got %s", n.Type)
		}
	} else {
		t.Error("expected namespace node 'MyApp.Services'")
	}

	// Check class is found
	if n := findNodeByNameAndType(result.Nodes, "SimpleService", graph.NodeClass); n == nil {
		t.Error("expected SimpleService class node")
	}

	// Check method
	if _, ok := nodeByName["DoWork"]; !ok {
		t.Error("expected DoWork method node")
	}
}

func TestStructWithInterfaces(t *testing.T) {
	source := `using System;

namespace MyApp.Models
{
    public struct Coordinate : IEquatable<Coordinate>
    {
        public double Lat { get; set; }
        public double Lng { get; set; }

        public bool Equals(Coordinate other)
        {
            return Lat == other.Lat && Lng == other.Lng;
        }
    }
}
`
	p := NewParser()
	result, err := p.ParseFile("demo/Coordinate.cs", []byte(source))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	// Check struct
	var structNode *graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeStruct && n.Name == "Coordinate" {
			structNode = n
			break
		}
	}
	if structNode == nil {
		t.Fatal("expected Coordinate struct node")
	}

	// Check implements edge
	hasImplements := false
	for _, edge := range result.Edges {
		if edge.Type == graph.EdgeImplements {
			hasImplements = true
			break
		}
	}
	if !hasImplements {
		t.Error("expected Implements edge for struct : IEquatable")
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
