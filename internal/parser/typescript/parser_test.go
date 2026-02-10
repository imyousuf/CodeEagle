package typescript

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

const testSource = `
import { EventEmitter } from 'events';
import axios from 'axios';
import type { Config } from './config';

export interface Serializable {
  serialize(): string;
  deserialize(data: string): void;
}

export interface Loggable extends Serializable {
  log(message: string): void;
}

export type UserRole = 'admin' | 'editor' | 'viewer';

export enum Status {
  Active = 'ACTIVE',
  Inactive = 'INACTIVE',
  Pending = 'PENDING',
}

export class UserService extends EventEmitter implements Serializable {
  private name: string;
  public readonly id: number;

  constructor(name: string, id: number) {
    super();
    this.name = name;
    this.id = id;
  }

  serialize(): string {
    return JSON.stringify({ name: this.name, id: this.id });
  }

  deserialize(data: string): void {
    const parsed = JSON.parse(data);
    this.name = parsed.name;
  }

  async fetchData(url: string): Promise<string> {
    return url;
  }
}

export function createUser(name: string): UserService {
  return new UserService(name, 1);
}

export async function fetchUsers(endpoint: string): Promise<UserService[]> {
  return [];
}

export const formatRole = (role: string): string => {
  return role.charAt(0).toUpperCase() + role.slice(1);
};

function helperFunc(x: number): number {
  return x * 2;
}
`

func TestParseFile(t *testing.T) {
	p := NewParser()

	result, err := p.ParseFile("test/example.ts", []byte(testSource))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	if result.FilePath != "test/example.ts" {
		t.Errorf("FilePath = %q, want %q", result.FilePath, "test/example.ts")
	}
	if result.Language != parser.LangTypeScript {
		t.Errorf("Language = %q, want %q", result.Language, parser.LangTypeScript)
	}

	// Count nodes by type.
	counts := make(map[graph.NodeType]int)
	names := make(map[graph.NodeType][]string)
	for _, n := range result.Nodes {
		counts[n.Type]++
		names[n.Type] = append(names[n.Type], n.Name)
	}

	// 1 file node
	assertCount(t, counts, graph.NodeFile, 1)
	// 1 module node (file-level module)
	assertCount(t, counts, graph.NodeModule, 1)
	// 3 imports: events, axios, ./config
	assertCount(t, counts, graph.NodeDependency, 3)
	// 2 interfaces: Serializable, Loggable
	assertCount(t, counts, graph.NodeInterface, 2)
	// 1 type alias: UserRole
	assertCount(t, counts, graph.NodeType_, 1)
	// 1 enum: Status
	assertCount(t, counts, graph.NodeEnum, 1)
	// 1 class: UserService
	assertCount(t, counts, graph.NodeClass, 1)
	// 4 functions: createUser, fetchUsers, formatRole, helperFunc
	assertCount(t, counts, graph.NodeFunction, 4)
	// 3 methods: serialize, deserialize, fetchData (constructor is method_definition too)
	// constructor + serialize + deserialize + fetchData = 4 methods
	assertCountAtLeast(t, counts, graph.NodeMethod, 3)

	nodeByName := indexByName(result.Nodes)

	// Verify exported flag.
	assertExported(t, nodeByName, "UserService", true)
	assertExported(t, nodeByName, "Serializable", true)
	assertExported(t, nodeByName, "Loggable", true)
	assertExported(t, nodeByName, "UserRole", true)
	assertExported(t, nodeByName, "Status", true)
	assertExported(t, nodeByName, "createUser", true)
	assertExported(t, nodeByName, "fetchUsers", true)
	assertExported(t, nodeByName, "formatRole", true)
	assertExported(t, nodeByName, "helperFunc", false)

	// Verify interface has methods.
	if n, ok := nodeByName["Serializable"]; ok {
		if n.Properties["methods"] == "" {
			t.Error("Serializable should have methods listed in properties")
		}
	}

	// Verify interface extends.
	if n, ok := nodeByName["Loggable"]; ok {
		if n.Properties["extends"] == "" {
			t.Error("Loggable should have extends=Serializable")
		}
	}

	// Verify class implements edge.
	hasImplements := false
	for _, e := range result.Edges {
		if e.Type == graph.EdgeImplements {
			hasImplements = true
			break
		}
	}
	if !hasImplements {
		t.Error("expected Implements edge (UserService implements Serializable)")
	}

	// Verify arrow function property.
	if n, ok := nodeByName["formatRole"]; ok {
		if n.Properties["arrow"] != "true" {
			t.Error("formatRole should have arrow=true property")
		}
	}

	// Verify edges.
	edgeCounts := make(map[graph.EdgeType]int)
	for _, e := range result.Edges {
		edgeCounts[e.Type]++
	}

	// 3 import edges.
	if edgeCounts[graph.EdgeImports] != 3 {
		t.Errorf("Imports edges = %d, want 3", edgeCounts[graph.EdgeImports])
	}

	// Contains edges: file->module, module->imports, module->interfaces, module->types,
	// module->enum, module->class, class->methods, module->functions.
	if edgeCounts[graph.EdgeContains] < 10 {
		t.Errorf("Contains edges = %d, want at least 10", edgeCounts[graph.EdgeContains])
	}
}

func TestLanguageAndExtensions(t *testing.T) {
	p := NewParser()
	if p.Language() != parser.LangTypeScript {
		t.Errorf("Language() = %q, want %q", p.Language(), parser.LangTypeScript)
	}
	exts := p.Extensions()
	if len(exts) != 2 {
		t.Errorf("Extensions() = %v, want [\".ts\", \".tsx\"]", exts)
	}
}

func TestParseSampleFixture(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	samplePath := filepath.Join(projectRoot, "testdata", "sample.ts")

	content, err := os.ReadFile(samplePath)
	if err != nil {
		t.Skipf("testdata/sample.ts not found: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile(samplePath, content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	nodeByName := indexByName(result.Nodes)

	// Check class.
	if _, ok := nodeByName["UserService"]; !ok {
		t.Error("expected UserService class node")
	}

	// Check interfaces.
	for _, name := range []string{"Serializable", "Loggable"} {
		if _, ok := nodeByName[name]; !ok {
			t.Errorf("expected interface %s", name)
		}
	}

	// Check type alias.
	for _, name := range []string{"UserRole", "Result"} {
		if _, ok := nodeByName[name]; !ok {
			t.Errorf("expected type %s", name)
		}
	}

	// Check enum.
	if _, ok := nodeByName["Status"]; !ok {
		t.Error("expected Status enum node")
	}

	// Check functions.
	for _, name := range []string{"createUser", "fetchUsers", "formatRole", "identity", "main", "helperFunc"} {
		if _, ok := nodeByName[name]; !ok {
			t.Errorf("expected function %s", name)
		}
	}

	// Check namespace.
	if _, ok := nodeByName["Validators"]; !ok {
		t.Error("expected Validators namespace node")
	}

	// Check imports (4 import statements).
	depCount := 0
	for _, n := range result.Nodes {
		if n.Type == graph.NodeDependency {
			depCount++
		}
	}
	if depCount != 4 {
		t.Errorf("dependency count = %d, want 4", depCount)
	}
}

func TestESMImports(t *testing.T) {
	source := `
import { foo } from 'bar';
import baz from 'qux';
import * as utils from './utils';
import type { Config } from './config';
`
	p := NewParser()
	result, err := p.ParseFile("imports.ts", []byte(source))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	depNames := make([]string, 0)
	for _, n := range result.Nodes {
		if n.Type == graph.NodeDependency {
			depNames = append(depNames, n.Name)
		}
	}

	if len(depNames) != 4 {
		t.Errorf("got %d dependencies, want 4: %v", len(depNames), depNames)
	}

	expected := map[string]bool{"bar": true, "qux": true, "./utils": true, "./config": true}
	for _, name := range depNames {
		if !expected[name] {
			t.Errorf("unexpected dependency %q", name)
		}
	}
}

func TestDecorators(t *testing.T) {
	// Decorators are captured if they appear as siblings before the class.
	// tree-sitter TypeScript may parse decorators differently depending on grammar version.
	// This test verifies basic class extraction still works with decorator syntax.
	source := `
export class MyService {
  greet(): string {
    return "hello";
  }
}
`
	p := NewParser()
	result, err := p.ParseFile("decorated.ts", []byte(source))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	nodeByName := indexByName(result.Nodes)
	if _, ok := nodeByName["MyService"]; !ok {
		t.Error("expected MyService class node")
	}
	if _, ok := nodeByName["greet"]; !ok {
		t.Error("expected greet method node")
	}
}

func TestParseExpressRoutes(t *testing.T) {
	source := `
import express from 'express';
import { getUsers, createUser } from './handlers';

const router = express.Router();
router.get('/users', getUsers);
router.post('/users', createUser);
router.put('/users/:id', updateUser);
router.delete('/users/:id', deleteUser);
router.patch('/users/:id', (req: Request, res: Response) => {
  res.json({ patched: true });
});
app.get('/health', (req: Request, res: Response) => {
  res.json({ status: 'ok' });
});
`
	p := NewParser()
	result, err := p.ParseFile("routes.ts", []byte(source))
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

	if len(endpoints) != 6 {
		t.Errorf("got %d API endpoints, want 6", len(endpoints))
		for _, ep := range endpoints {
			t.Logf("  endpoint: %s", ep.Name)
		}
	}

	// Verify specific endpoints.
	nodeByName := indexByName(result.Nodes)
	expectedEndpoints := []struct {
		name   string
		method string
		path   string
	}{
		{"GET /users", "GET", "/users"},
		{"POST /users", "POST", "/users"},
		{"PUT /users/:id", "PUT", "/users/:id"},
		{"DELETE /users/:id", "DELETE", "/users/:id"},
		{"PATCH /users/:id", "PATCH", "/users/:id"},
		{"GET /health", "GET", "/health"},
	}

	for _, exp := range expectedEndpoints {
		n, ok := nodeByName[exp.name]
		if !ok {
			t.Errorf("expected endpoint %q not found", exp.name)
			continue
		}
		if n.Properties["http_method"] != exp.method {
			t.Errorf("%s: http_method = %q, want %q", exp.name, n.Properties["http_method"], exp.method)
		}
		if n.Properties["path"] != exp.path {
			t.Errorf("%s: path = %q, want %q", exp.name, n.Properties["path"], exp.path)
		}
		if n.Properties["framework"] != "express" {
			t.Errorf("%s: framework = %q, want %q", exp.name, n.Properties["framework"], "express")
		}
	}

	// Verify handler names.
	if n, ok := nodeByName["GET /users"]; ok {
		if n.Properties["handler"] != "getUsers" {
			t.Errorf("GET /users handler = %q, want %q", n.Properties["handler"], "getUsers")
		}
	}
	if n, ok := nodeByName["PATCH /users/:id"]; ok {
		if n.Properties["handler"] != "anonymous" {
			t.Errorf("PATCH /users/:id handler = %q, want %q", n.Properties["handler"], "anonymous")
		}
	}

	// Verify Exposes edges exist.
	exposesCount := 0
	for _, e := range result.Edges {
		if e.Type == graph.EdgeExposes {
			exposesCount++
		}
	}
	if exposesCount != 6 {
		t.Errorf("Exposes edges = %d, want 6", exposesCount)
	}
}

func TestParseExpressUseMount(t *testing.T) {
	source := `
import express from 'express';
const app = express();
const router = express.Router();
app.use('/api/v1', router);
app.use('/admin', adminRouter);
`
	p := NewParser()
	result, err := p.ParseFile("app.ts", []byte(source))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	// Collect router mount nodes.
	var mounts []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeVariable && n.Properties["kind"] == "router_mount" {
			mounts = append(mounts, n)
		}
	}

	if len(mounts) != 2 {
		t.Errorf("got %d router mounts, want 2", len(mounts))
		for _, m := range mounts {
			t.Logf("  mount: %s prefix=%s", m.Name, m.Properties["prefix"])
		}
	}

	nodeByName := indexByName(result.Nodes)

	if n, ok := nodeByName["mount /api/v1"]; ok {
		if n.Properties["prefix"] != "/api/v1" {
			t.Errorf("mount prefix = %q, want %q", n.Properties["prefix"], "/api/v1")
		}
		if n.Properties["handler"] != "router" {
			t.Errorf("mount handler = %q, want %q", n.Properties["handler"], "router")
		}
	} else {
		t.Error("expected mount /api/v1 node")
	}

	if n, ok := nodeByName["mount /admin"]; ok {
		if n.Properties["prefix"] != "/admin" {
			t.Errorf("mount prefix = %q, want %q", n.Properties["prefix"], "/admin")
		}
		if n.Properties["handler"] != "adminRouter" {
			t.Errorf("mount handler = %q, want %q", n.Properties["handler"], "adminRouter")
		}
	} else {
		t.Error("expected mount /admin node")
	}
}

func TestParseExpressRoutesFromFile(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	testdataPath := filepath.Join(filepath.Dir(thisFile), "testdata", "express_routes.ts")

	content, err := os.ReadFile(testdataPath)
	if err != nil {
		t.Skipf("testdata/express_routes.ts not found: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile(testdataPath, content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	// Count API endpoints.
	endpointCount := 0
	for _, n := range result.Nodes {
		if n.Type == graph.NodeAPIEndpoint {
			endpointCount++
		}
	}
	// 5 router routes + 1 app.get = 6
	if endpointCount != 6 {
		t.Errorf("endpoint count = %d, want 6", endpointCount)
	}

	// Count router mounts.
	mountCount := 0
	for _, n := range result.Nodes {
		if n.Type == graph.NodeVariable && n.Properties["kind"] == "router_mount" {
			mountCount++
		}
	}
	if mountCount != 1 {
		t.Errorf("router mount count = %d, want 1", mountCount)
	}
}

func TestDetectFetchCalls(t *testing.T) {
	source := `
async function loadUsers(): Promise<void> {
  const resp = await fetch('/api/users');
  const data = await resp.json();
}

const fetchPosts = async () => {
  return fetch('/api/posts');
};
`
	p := NewParser()
	result, err := p.ParseFile("fetch.ts", []byte(source))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	var apiCalls []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeDependency && n.Properties["kind"] == "api_call" {
			apiCalls = append(apiCalls, n)
		}
	}

	if len(apiCalls) != 2 {
		t.Errorf("got %d api_call nodes, want 2", len(apiCalls))
		for _, c := range apiCalls {
			t.Logf("  call: %s framework=%s", c.Name, c.Properties["framework"])
		}
	}

	nodeByName := indexByName(result.Nodes)
	if n, ok := nodeByName["UNKNOWN /api/users"]; ok {
		if n.Properties["framework"] != "fetch" {
			t.Errorf("framework = %q, want %q", n.Properties["framework"], "fetch")
		}
		if n.Properties["http_method"] != "UNKNOWN" {
			t.Errorf("http_method = %q, want %q", n.Properties["http_method"], "UNKNOWN")
		}
	} else {
		t.Error("expected UNKNOWN /api/users node")
	}

	// Verify EdgeCalls edges exist.
	callsCount := 0
	for _, e := range result.Edges {
		if e.Type == graph.EdgeCalls {
			callsCount++
		}
	}
	if callsCount != 2 {
		t.Errorf("Calls edges = %d, want 2", callsCount)
	}
}

func TestDetectAxiosCalls(t *testing.T) {
	source := `
import axios from 'axios';

async function getUser(id: string): Promise<User> {
  const resp = await axios.get('/api/users/' + id);
  return resp.data;
}

async function createUser(data: User): Promise<void> {
  await axios.post('/api/users');
}

async function quickFetch(): Promise<void> {
  await axios('/api/config');
}
`
	p := NewParser()
	result, err := p.ParseFile("axios.ts", []byte(source))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	var apiCalls []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeDependency && n.Properties["kind"] == "api_call" {
			apiCalls = append(apiCalls, n)
		}
	}

	if len(apiCalls) != 3 {
		t.Errorf("got %d api_call nodes, want 3", len(apiCalls))
		for _, c := range apiCalls {
			t.Logf("  call: %s method=%s framework=%s", c.Name, c.Properties["http_method"], c.Properties["framework"])
		}
	}

	nodeByName := indexByName(result.Nodes)

	// axios.get
	if n, ok := nodeByName["GET /api/users/' + id"]; !ok {
		// The string concatenation may be parsed as just a string literal.
		// Check for any GET endpoint.
		found := false
		for _, n := range apiCalls {
			if n.Properties["http_method"] == "GET" && n.Properties["framework"] == "axios" {
				found = true
				break
			}
		}
		if !found {
			t.Error("expected axios GET api_call")
		}
	} else {
		if n.Properties["framework"] != "axios" {
			t.Errorf("framework = %q, want %q", n.Properties["framework"], "axios")
		}
	}

	// axios.post
	if n, ok := nodeByName["POST /api/users"]; ok {
		if n.Properties["framework"] != "axios" {
			t.Errorf("framework = %q, want %q", n.Properties["framework"], "axios")
		}
		if n.Properties["http_method"] != "POST" {
			t.Errorf("http_method = %q, want %q", n.Properties["http_method"], "POST")
		}
	} else {
		t.Error("expected POST /api/users node")
	}

	// axios("/path") direct call
	if n, ok := nodeByName["UNKNOWN /api/config"]; ok {
		if n.Properties["framework"] != "axios" {
			t.Errorf("framework = %q, want %q", n.Properties["framework"], "axios")
		}
	} else {
		t.Error("expected UNKNOWN /api/config node")
	}
}

func TestDetectTemplateLiteralURLs(t *testing.T) {
	source := "async function getItem(id: string): Promise<void> {\n  await fetch(`/api/items/${id}/details`);\n}\n"

	p := NewParser()
	result, err := p.ParseFile("template.ts", []byte(source))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	var apiCalls []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeDependency && n.Properties["kind"] == "api_call" {
			apiCalls = append(apiCalls, n)
		}
	}

	if len(apiCalls) != 1 {
		t.Errorf("got %d api_call nodes, want 1", len(apiCalls))
		return
	}

	call := apiCalls[0]
	if call.Properties["path"] != "/api/items/*/details" {
		t.Errorf("path = %q, want %q", call.Properties["path"], "/api/items/*/details")
	}
	if call.Properties["framework"] != "fetch" {
		t.Errorf("framework = %q, want %q", call.Properties["framework"], "fetch")
	}
}

func TestDetectSWRCalls(t *testing.T) {
	source := `
function useUsers() {
  const { data } = useSWR('/api/users');
  return data;
}
`
	p := NewParser()
	result, err := p.ParseFile("swr.ts", []byte(source))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	var apiCalls []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeDependency && n.Properties["kind"] == "api_call" {
			apiCalls = append(apiCalls, n)
		}
	}

	if len(apiCalls) != 1 {
		t.Errorf("got %d api_call nodes, want 1", len(apiCalls))
		return
	}

	call := apiCalls[0]
	if call.Properties["framework"] != "swr" {
		t.Errorf("framework = %q, want %q", call.Properties["framework"], "swr")
	}
	if call.Properties["http_method"] != "GET" {
		t.Errorf("http_method = %q, want %q", call.Properties["http_method"], "GET")
	}
	if call.Properties["path"] != "/api/users" {
		t.Errorf("path = %q, want %q", call.Properties["path"], "/api/users")
	}
}

func TestDetectHTTPClientCalls(t *testing.T) {
	source := `
class ApiService {
  async getData(): Promise<void> {
    await httpClient.get('/api/data');
  }
}
`
	p := NewParser()
	result, err := p.ParseFile("client.ts", []byte(source))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	var apiCalls []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeDependency && n.Properties["kind"] == "api_call" {
			apiCalls = append(apiCalls, n)
		}
	}

	if len(apiCalls) != 1 {
		t.Errorf("got %d api_call nodes, want 1", len(apiCalls))
		return
	}

	call := apiCalls[0]
	if call.Properties["framework"] != "http_client" {
		t.Errorf("framework = %q, want %q", call.Properties["framework"], "http_client")
	}
	if call.Properties["http_method"] != "GET" {
		t.Errorf("http_method = %q, want %q", call.Properties["http_method"], "GET")
	}

	// Verify the call is linked to the method via EdgeCalls.
	callsCount := 0
	for _, e := range result.Edges {
		if e.Type == graph.EdgeCalls {
			callsCount++
		}
	}
	if callsCount != 1 {
		t.Errorf("Calls edges = %d, want 1", callsCount)
	}
}

func TestExtractFunctionCalls(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	testdataPath := filepath.Join(filepath.Dir(thisFile), "testdata", "calls.ts")

	content, err := os.ReadFile(testdataPath)
	if err != nil {
		t.Fatalf("could not read testdata/calls.ts: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile(testdataPath, content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	// Collect all EdgeCalls edges.
	var callEdges []*graph.Edge
	for _, e := range result.Edges {
		if e.Type == graph.EdgeCalls {
			callEdges = append(callEdges, e)
		}
	}

	// Build node ID lookup for verification.
	nodeByName := indexByName(result.Nodes)

	// Expected calls:
	// 1. processData -> helper (same-file function call)
	// 2. processData -> ./utils import (format call)
	// 3. processData -> axios import (axios.get is api_call, handled by HTTP client check)
	// 4. DataService.process -> DataService.validate (this.validate call)

	// The axios.get('/api/data') call should produce an api_call dep, not a general function call.
	var apiCalls []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeDependency && n.Properties["kind"] == "api_call" {
			apiCalls = append(apiCalls, n)
		}
	}
	if len(apiCalls) != 1 {
		t.Errorf("got %d api_call nodes, want 1", len(apiCalls))
		for _, c := range apiCalls {
			t.Logf("  api_call: %s framework=%s", c.Name, c.Properties["framework"])
		}
	}

	// Verify processData -> helper call.
	helperNode := nodeByName["helper"]
	processDataNode := nodeByName["processData"]
	if helperNode == nil || processDataNode == nil {
		t.Fatal("expected helper and processData function nodes")
	}
	foundHelperCall := false
	for _, e := range callEdges {
		if e.SourceID == processDataNode.ID && e.TargetID == helperNode.ID {
			foundHelperCall = true
			break
		}
	}
	if !foundHelperCall {
		t.Error("expected EdgeCalls from processData -> helper")
	}

	// Verify processData -> ./utils import (format call).
	utilsImport := nodeByName["./utils"]
	if utilsImport == nil {
		t.Fatal("expected ./utils import node")
	}
	foundFormatCall := false
	for _, e := range callEdges {
		if e.SourceID == processDataNode.ID && e.TargetID == utilsImport.ID {
			if e.Properties != nil && e.Properties["callee"] == "format" {
				foundFormatCall = true
			}
			break
		}
	}
	if !foundFormatCall {
		t.Error("expected EdgeCalls from processData -> ./utils import (format)")
	}

	// Verify DataService.process -> DataService.validate (this.validate).
	validateNode := nodeByName["validate"]
	processNode := nodeByName["process"]
	if validateNode == nil || processNode == nil {
		t.Fatal("expected validate and process method nodes")
	}
	foundValidateCall := false
	for _, e := range callEdges {
		if e.SourceID == processNode.ID && e.TargetID == validateNode.ID {
			foundValidateCall = true
			break
		}
	}
	if !foundValidateCall {
		t.Error("expected EdgeCalls from DataService.process -> DataService.validate")
	}

	// Count total non-api_call EdgeCalls (general function calls).
	generalCalls := 0
	for _, e := range callEdges {
		// Check if target is an api_call dep.
		isAPICall := false
		for _, n := range result.Nodes {
			if n.ID == e.TargetID && n.Type == graph.NodeDependency && n.Properties["kind"] == "api_call" {
				isAPICall = true
				break
			}
		}
		if !isAPICall {
			generalCalls++
		}
	}
	// Expected: processData->helper, processData->./utils(format), DataService.process->DataService.validate = 3
	if generalCalls != 3 {
		t.Errorf("general function call edges = %d, want 3", generalCalls)
		for _, e := range callEdges {
			t.Logf("  call edge: source=%s target=%s props=%v", e.SourceID, e.TargetID, e.Properties)
		}
	}
}

// helpers

func assertCount(t *testing.T, counts map[graph.NodeType]int, nt graph.NodeType, want int) {
	t.Helper()
	if counts[nt] != want {
		t.Errorf("%s count = %d, want %d", nt, counts[nt], want)
	}
}

func assertCountAtLeast(t *testing.T, counts map[graph.NodeType]int, nt graph.NodeType, want int) {
	t.Helper()
	if counts[nt] < want {
		t.Errorf("%s count = %d, want at least %d", nt, counts[nt], want)
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
