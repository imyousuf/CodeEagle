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

func TestParseGinRoutes(t *testing.T) {
	content := []byte(`package main

import "github.com/gin-gonic/gin"

func healthCheck(c *gin.Context) {}
func createUser(c *gin.Context)  {}
func updateUser(c *gin.Context)  {}
func deleteUser(c *gin.Context)  {}

func SetupRoutes(r *gin.Engine) {
	r.GET("/health", healthCheck)
	r.POST("/users", createUser)
	r.PUT("/users/:id", updateUser)
	r.DELETE("/users/:id", deleteUser)
}
`)

	p := NewParser()
	result, err := p.ParseFile("routes.go", content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	endpoints := filterNodesByType(result.Nodes, graph.NodeAPIEndpoint)
	if len(endpoints) != 4 {
		t.Fatalf("expected 4 API endpoints, got %d", len(endpoints))
	}

	wantRoutes := map[string]string{
		"GET /health":       "gin",
		"POST /users":       "gin",
		"PUT /users/:id":    "gin",
		"DELETE /users/:id": "gin",
	}

	for _, ep := range endpoints {
		fw, ok := wantRoutes[ep.Name]
		if !ok {
			t.Errorf("unexpected endpoint %q", ep.Name)
			continue
		}
		if ep.Properties["framework"] != fw {
			t.Errorf("endpoint %q framework = %q, want %q", ep.Name, ep.Properties["framework"], fw)
		}
		delete(wantRoutes, ep.Name)
	}
	for name := range wantRoutes {
		t.Errorf("missing endpoint %q", name)
	}

	// Verify Exposes edges exist.
	exposesCount := 0
	for _, e := range result.Edges {
		if e.Type == graph.EdgeExposes {
			exposesCount++
		}
	}
	if exposesCount != 4 {
		t.Errorf("Exposes edge count = %d, want 4", exposesCount)
	}
}

func TestParseNetHTTPRoutes(t *testing.T) {
	content := []byte(`package main

import "net/http"

func dataHandler(w http.ResponseWriter, r *http.Request) {}
func statusHandler(w http.ResponseWriter, r *http.Request) {}

func SetupHTTP() {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/data", dataHandler)
	http.HandleFunc("/status", statusHandler)
}
`)

	p := NewParser()
	result, err := p.ParseFile("httpsetup.go", content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	endpoints := filterNodesByType(result.Nodes, graph.NodeAPIEndpoint)
	if len(endpoints) != 2 {
		t.Fatalf("expected 2 API endpoints, got %d", len(endpoints))
	}

	wantPaths := map[string]bool{
		"ANY /api/data": true,
		"ANY /status":   true,
	}

	for _, ep := range endpoints {
		if !wantPaths[ep.Name] {
			t.Errorf("unexpected endpoint %q", ep.Name)
		}
		if ep.Properties["framework"] != "net/http" {
			t.Errorf("endpoint %q framework = %q, want %q", ep.Name, ep.Properties["framework"], "net/http")
		}
		delete(wantPaths, ep.Name)
	}
	for name := range wantPaths {
		t.Errorf("missing endpoint %q", name)
	}
}

func TestParseGinRouterGroups(t *testing.T) {
	content := []byte(`package main

import "github.com/gin-gonic/gin"

func listUsers(c *gin.Context)  {}
func createUser(c *gin.Context) {}
func listItems(c *gin.Context)  {}
func createItem(c *gin.Context) {}

func SetupGrouped(router *gin.Engine) {
	api := router.Group("/api/v1")
	api.GET("/users", listUsers)
	api.POST("/users", createUser)

	items := api.Group("/items")
	items.GET("/", listItems)
	items.POST("/", createItem)
}
`)

	p := NewParser()
	result, err := p.ParseFile("grouped.go", content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	endpoints := filterNodesByType(result.Nodes, graph.NodeAPIEndpoint)
	if len(endpoints) != 4 {
		t.Fatalf("expected 4 API endpoints, got %d", len(endpoints))
	}

	wantRoutes := map[string]string{
		"GET /api/v1/users":   "gin",
		"POST /api/v1/users":  "gin",
		"GET /api/v1/items/":  "gin",
		"POST /api/v1/items/": "gin",
	}

	for _, ep := range endpoints {
		fw, ok := wantRoutes[ep.Name]
		if !ok {
			t.Errorf("unexpected endpoint %q", ep.Name)
			continue
		}
		if ep.Properties["framework"] != fw {
			t.Errorf("endpoint %q framework = %q, want %q", ep.Name, ep.Properties["framework"], fw)
		}
		if ep.Properties["path"] == "" {
			t.Errorf("endpoint %q missing path property", ep.Name)
		}
		delete(wantRoutes, ep.Name)
	}
	for name := range wantRoutes {
		t.Errorf("missing endpoint %q", name)
	}
}

func TestParseGorillaRoutes(t *testing.T) {
	content := []byte(`package main

import (
	"net/http"
	"github.com/gorilla/mux"
)

func getUsers(w http.ResponseWriter, r *http.Request)  {}
func createUser(w http.ResponseWriter, r *http.Request) {}

func SetupMux() {
	r := mux.NewRouter()
	r.HandleFunc("/api/users", getUsers).Methods("GET")
	r.HandleFunc("/api/users", createUser).Methods("POST")
}
`)

	p := NewParser()
	result, err := p.ParseFile("gorilla.go", content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	endpoints := filterNodesByType(result.Nodes, graph.NodeAPIEndpoint)
	if len(endpoints) != 2 {
		t.Fatalf("expected 2 API endpoints, got %d", len(endpoints))
	}

	wantRoutes := map[string]string{
		"GET /api/users":  "gorilla/mux",
		"POST /api/users": "gorilla/mux",
	}

	for _, ep := range endpoints {
		fw, ok := wantRoutes[ep.Name]
		if !ok {
			t.Errorf("unexpected endpoint %q", ep.Name)
			continue
		}
		if ep.Properties["framework"] != fw {
			t.Errorf("endpoint %q framework = %q, want %q", ep.Name, ep.Properties["framework"], fw)
		}
		delete(wantRoutes, ep.Name)
	}
	for name := range wantRoutes {
		t.Errorf("missing endpoint %q", name)
	}
}

func TestParseRouteProperties(t *testing.T) {
	content := []byte(`package main

import "github.com/gin-gonic/gin"

func getUser(c *gin.Context) {}

func Setup(r *gin.Engine) {
	r.GET("/users/:id", getUser)
}
`)

	p := NewParser()
	result, err := p.ParseFile("props.go", content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	endpoints := filterNodesByType(result.Nodes, graph.NodeAPIEndpoint)
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 API endpoint, got %d", len(endpoints))
	}

	ep := endpoints[0]
	if ep.Properties["http_method"] != "GET" {
		t.Errorf("http_method = %q, want GET", ep.Properties["http_method"])
	}
	if ep.Properties["path"] != "/users/:id" {
		t.Errorf("path = %q, want /users/:id", ep.Properties["path"])
	}
	if ep.Properties["framework"] != "gin" {
		t.Errorf("framework = %q, want gin", ep.Properties["framework"])
	}
	if ep.Properties["handler"] != "getUser" {
		t.Errorf("handler = %q, want getUser", ep.Properties["handler"])
	}
	if ep.Line == 0 {
		t.Error("endpoint line should be > 0")
	}
}

func TestExtractHTTPClientCalls(t *testing.T) {
	content, err := os.ReadFile("testdata/http_client.go")
	if err != nil {
		t.Fatalf("reading testdata: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile("testdata/http_client.go", content)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	// Collect api_call dependency nodes.
	var apiCalls []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeDependency && n.Properties["kind"] == "api_call" {
			apiCalls = append(apiCalls, n)
		}
	}

	if len(apiCalls) < 6 {
		t.Errorf("expected at least 6 api_call deps, got %d", len(apiCalls))
		for _, n := range apiCalls {
			t.Logf("  %s: method=%s path=%s framework=%s", n.Name, n.Properties["http_method"], n.Properties["path"], n.Properties["framework"])
		}
	}

	// Verify specific calls.
	found := map[string]bool{}
	for _, n := range apiCalls {
		key := n.Properties["http_method"] + ":" + n.Properties["path"]
		found[key] = true
		if n.Properties["framework"] != "net/http" {
			t.Errorf("api_call %q framework = %q, want net/http", n.Name, n.Properties["framework"])
		}
	}

	wantCalls := []string{
		"GET:/api/users/*",
		"POST:/api/users",
		"HEAD:/health",
		"POST:/api/login",
		"PUT:/api/users/123",
		"GET:/api/items",
		"POST:/api/items",
	}
	for _, want := range wantCalls {
		if !found[want] {
			t.Errorf("missing api_call: %s", want)
		}
	}

	// Verify EdgeCalls exist.
	callEdges := 0
	for _, e := range result.Edges {
		if e.Type == graph.EdgeCalls {
			callEdges++
		}
	}
	if callEdges < 6 {
		t.Errorf("EdgeCalls count = %d, want at least 6", callEdges)
	}
}

func TestExtractFunctionCalls(t *testing.T) {
	content, err := os.ReadFile("testdata/calls.go")
	if err != nil {
		t.Fatalf("reading testdata: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile("testdata/calls.go", content)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	// Collect EdgeCalls edges.
	type callEdge struct {
		sourceID string
		targetID string
		callee   string
	}
	var calls []callEdge
	for _, e := range result.Edges {
		if e.Type == graph.EdgeCalls {
			calls = append(calls, callEdge{
				sourceID: e.SourceID,
				targetID: e.TargetID,
				callee:   e.Properties["callee"],
			})
		}
	}

	// Build ID maps for verification.
	funcID := func(name string) string {
		return graph.NewNodeID(string(graph.NodeFunction), "testdata/calls.go", name)
	}
	methodID := func(recv, name string) string {
		return graph.NewNodeID(string(graph.NodeMethod), "testdata/calls.go", recv+"."+name)
	}
	depID := func(path string) string {
		return graph.NewNodeID(string(graph.NodeDependency), "testdata/calls.go", path)
	}

	// Check same-file calls
	hasEdge := func(src, tgt, callee string) bool {
		for _, c := range calls {
			if c.sourceID == src && c.targetID == tgt {
				if callee == "" || c.callee == callee {
					return true
				}
			}
		}
		return false
	}

	// processData → helper (same-file)
	if !hasEdge(funcID("processData"), funcID("helper"), "") {
		t.Error("missing call edge: processData → helper")
	}

	// processData → formatOutput (same-file)
	if !hasEdge(funcID("processData"), funcID("formatOutput"), "") {
		t.Error("missing call edge: processData → formatOutput")
	}

	// processData → json import (json.Marshal)
	if !hasEdge(funcID("processData"), depID("encoding/json"), "Marshal") {
		t.Error("missing call edge: processData → encoding/json (Marshal)")
	}

	// processData → fmt import (fmt.Println)
	if !hasEdge(funcID("processData"), depID("fmt"), "Println") {
		t.Error("missing call edge: processData → fmt (Println)")
	}

	// processData → os import (os.Exit)
	if !hasEdge(funcID("processData"), depID("os"), "Exit") {
		t.Error("missing call edge: processData → os (Exit)")
	}

	// Processor.Process → formatOutput (cross-type same-file)
	if !hasEdge(methodID("Processor", "Process"), funcID("formatOutput"), "") {
		t.Error("missing call edge: Processor.Process → formatOutput")
	}

	// Verify builtins are NOT present (len is used in validate)
	for _, c := range calls {
		if c.callee == "len" || c.callee == "make" || c.callee == "append" {
			t.Errorf("builtin %q should not create EdgeCalls", c.callee)
		}
	}
}

func TestImportNodesHaveKindImport(t *testing.T) {
	p := NewParser()
	result, err := p.ParseFile("testpkg/example.go", []byte(testSource))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	for _, n := range result.Nodes {
		if n.Type == graph.NodeDependency {
			if n.Properties == nil || n.Properties["kind"] != "import" {
				t.Errorf("import dep %q missing kind=import property", n.Name)
			}
		}
	}
}

func TestTestFileDetection(t *testing.T) {
	content := []byte(`package mypkg

import "testing"

func TestAdd(t *testing.T) {
	if add(1, 2) != 3 {
		t.Error("bad")
	}
}

func BenchmarkAdd(b *testing.B) {
	for i := 0; i < b.N; i++ {
		add(1, 2)
	}
}

func ExampleAdd() {
	add(1, 2)
}

func FuzzAdd(f *testing.F) {
	f.Fuzz(func(t *testing.T, a, b int) {
		add(a, b)
	})
}

func add(a, b int) int {
	return a + b
}

type helper struct{}

func (h *helper) setup() {}
`)

	p := NewParser()
	result, err := p.ParseFile("mypkg/math_test.go", content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	// Count nodes by type
	counts := make(map[graph.NodeType]int)
	nodeByName := indexByName(result.Nodes)

	for _, n := range result.Nodes {
		counts[n.Type]++
	}

	// File node should be TestFile, not File
	assertCount(t, counts, graph.NodeTestFile, 1)
	assertCount(t, counts, graph.NodeFile, 0)

	// Test/Benchmark/Example/Fuzz functions should be TestFunction
	assertCount(t, counts, graph.NodeTestFunction, 4)

	// "add" is a regular function even in a test file
	assertCount(t, counts, graph.NodeFunction, 1)
	if n, ok := nodeByName["add"]; ok {
		if n.Type != graph.NodeFunction {
			t.Errorf("add should be NodeFunction, got %s", n.Type)
		}
	} else {
		t.Error("expected 'add' function node")
	}

	// Methods in test files remain NodeMethod
	assertCount(t, counts, graph.NodeMethod, 1)
	if n, ok := nodeByName["setup"]; ok {
		if n.Type != graph.NodeMethod {
			t.Errorf("setup should be NodeMethod, got %s", n.Type)
		}
	} else {
		t.Error("expected 'setup' method node")
	}

	// Verify test functions have correct type
	for _, name := range []string{"TestAdd", "BenchmarkAdd", "ExampleAdd", "FuzzAdd"} {
		if n, ok := nodeByName[name]; ok {
			if n.Type != graph.NodeTestFunction {
				t.Errorf("%s should be NodeTestFunction, got %s", name, n.Type)
			}
		} else {
			t.Errorf("expected %s test function node", name)
		}
	}
}

func TestNonTestFileDoesNotProduceTestNodes(t *testing.T) {
	// Regular file with a function that happens to start with "Test" but is not a test file.
	content := []byte(`package mypkg

func TestHelper() string {
	return "not actually a test"
}
`)

	p := NewParser()
	result, err := p.ParseFile("mypkg/helper.go", content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	counts := make(map[graph.NodeType]int)
	for _, n := range result.Nodes {
		counts[n.Type]++
	}

	// Should be a regular File and Function since it's not a _test.go file.
	assertCount(t, counts, graph.NodeFile, 1)
	assertCount(t, counts, graph.NodeTestFile, 0)
	assertCount(t, counts, graph.NodeFunction, 1)
	assertCount(t, counts, graph.NodeTestFunction, 0)
}

func TestStructFieldTypeResolution(t *testing.T) {
	content, err := os.ReadFile("testdata/field_calls.go")
	if err != nil {
		t.Fatalf("reading testdata: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile("testdata/field_calls.go", content)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	// Collect EdgeCalls edges.
	type callEdge struct {
		sourceID string
		targetID string
		callee   string
	}
	var calls []callEdge
	for _, e := range result.Edges {
		if e.Type == graph.EdgeCalls {
			calls = append(calls, callEdge{
				sourceID: e.SourceID,
				targetID: e.TargetID,
				callee:   e.Properties["callee"],
			})
		}
	}

	methodID := func(recv, name string) string {
		return graph.NewNodeID(string(graph.NodeMethod), "testdata/field_calls.go", recv+"."+name)
	}
	depID := func(path string) string {
		return graph.NewNodeID(string(graph.NodeDependency), "testdata/field_calls.go", path)
	}

	hasEdge := func(src, tgt, callee string) bool {
		for _, c := range calls {
			if c.sourceID == src && c.targetID == tgt {
				if callee == "" || c.callee == callee {
					return true
				}
			}
		}
		return false
	}

	graphDepID := depID("github.com/imyousuf/CodeEagle/internal/graph")
	configDepID := depID("github.com/imyousuf/CodeEagle/internal/config")

	// --- Cross-package field calls with qualified callee names ---

	// l.store.QueryNodes() -> graph dep with callee "Store.QueryNodes"
	if !hasEdge(methodID("Linker", "Run"), graphDepID, "Store.QueryNodes") {
		t.Error("missing call edge: Linker.Run -> graph dep (Store.QueryNodes) via l.store")
	}

	// l.settings.Validate() -> config dep with callee "Settings.Validate"
	if !hasEdge(methodID("Linker", "Run"), configDepID, "Settings.Validate") {
		t.Error("missing call edge: Linker.Run -> config dep (Settings.Validate) via l.settings")
	}

	// l.store.AddNode() in helper() -> graph dep with callee "Store.AddNode"
	if !hasEdge(methodID("Linker", "helper"), graphDepID, "Store.AddNode") {
		t.Error("missing call edge: Linker.helper -> graph dep (Store.AddNode) via l.store")
	}

	// --- Local (same-package) field type resolution ---

	// l.logger.Info() should resolve directly to Logger.Info method node
	if !hasEdge(methodID("Linker", "Run"), methodID("Logger", "Info"), "Logger.Info") {
		t.Error("missing call edge: Linker.Run -> Logger.Info via l.logger (local type)")
	}

	// l.logger.Error() in helper() should resolve directly to Logger.Error method node
	if !hasEdge(methodID("Linker", "helper"), methodID("Logger", "Error"), "Logger.Error") {
		t.Error("missing call edge: Linker.helper -> Logger.Error via l.logger (local type)")
	}

	// --- Deep chain resolution (a.b.c.Method) ---

	// l.inner.logger.Info() should resolve through Inner.logger -> Logger.Info
	if !hasEdge(methodID("Linker", "DeepChain"), methodID("Logger", "Info"), "Logger.Info") {
		t.Error("missing call edge: Linker.DeepChain -> Logger.Info via l.inner.logger (deep chain)")
	}
}

func TestExtractTypeName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"graph.Store", "Store"},
		{"*graph.Store", "Store"},
		{"[]config.Item", "Item"},
		{"*[]config.Item", "Item"},
		{"string", "string"},
		{"Store", "Store"},
		{"*Store", "Store"},
		{"**graph.Store", "Store"},
	}

	for _, tt := range tests {
		got := extractTypeName(tt.input)
		if got != tt.want {
			t.Errorf("extractTypeName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestExtractPackagePrefix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"graph.Store", "graph"},
		{"*graph.Store", "graph"},
		{"[]config.Item", "config"},
		{"*[]config.Item", "config"},
		{"string", ""},
		{"int", ""},
		{"*string", ""},
		{"**graph.Store", "graph"},
		{"[][]graph.Store", "graph"},
	}

	for _, tt := range tests {
		got := extractPackagePrefix(tt.input)
		if got != tt.want {
			t.Errorf("extractPackagePrefix(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func filterNodesByType(nodes []*graph.Node, nt graph.NodeType) []*graph.Node {
	var result []*graph.Node
	for _, n := range nodes {
		if n.Type == nt {
			result = append(result, n)
		}
	}
	return result
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
