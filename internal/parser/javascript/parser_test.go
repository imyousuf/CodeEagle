package javascript

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

const testSource = `
import { readFile } from 'fs/promises';
import path from 'path';

const express = require('express');
const lodash = require('lodash');

export class HttpClient {
  constructor(baseURL) {
    this.baseURL = baseURL;
  }

  async get(endpoint) {
    return fetch(this.baseURL + endpoint);
  }

  post(endpoint, data) {
    return fetch(this.baseURL + endpoint, { method: 'POST', body: JSON.stringify(data) });
  }
}

export function createClient(baseURL) {
  return new HttpClient(baseURL);
}

export async function fetchData(url) {
  return fetch(url);
}

export const formatURL = (base, path) => {
  return base + '/' + path;
};

export default function main() {
  console.log('Hello');
}

function helperFunc(x) {
  return x * 2;
}

const internalHelper = (s) => s.trim();
`

func TestParseFile(t *testing.T) {
	p := NewParser()

	result, err := p.ParseFile("test/example.js", []byte(testSource))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	if result.FilePath != "test/example.js" {
		t.Errorf("FilePath = %q, want %q", result.FilePath, "test/example.js")
	}
	if result.Language != parser.LangJavaScript {
		t.Errorf("Language = %q, want %q", result.Language, parser.LangJavaScript)
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
	// 1 module node
	assertCount(t, counts, graph.NodeModule, 1)
	// 2 ESM imports + 2 CommonJS requires = 4 dependencies
	assertCount(t, counts, graph.NodeDependency, 4)
	// 1 class: HttpClient
	assertCount(t, counts, graph.NodeClass, 1)
	// 5 functions: createClient, fetchData, formatURL, main, helperFunc + internalHelper = 6
	// formatURL is an exported arrow fn, internalHelper is unexported arrow fn
	assertCountAtLeast(t, counts, graph.NodeFunction, 5)
	// 3 methods: constructor, get, post
	assertCountAtLeast(t, counts, graph.NodeMethod, 3)

	nodeByName := indexByName(result.Nodes)

	// Verify exported flags.
	assertExported(t, nodeByName, "HttpClient", true)
	assertExported(t, nodeByName, "createClient", true)
	assertExported(t, nodeByName, "fetchData", true)
	assertExported(t, nodeByName, "formatURL", true)
	assertExported(t, nodeByName, "main", true)
	assertExported(t, nodeByName, "helperFunc", false)

	// Verify arrow function property.
	if n, ok := nodeByName["formatURL"]; ok {
		if n.Properties["arrow"] != "true" {
			t.Error("formatURL should have arrow=true property")
		}
	}

	// Verify async property.
	if n, ok := nodeByName["fetchData"]; ok {
		if n.Properties["async"] != "true" {
			t.Error("fetchData should have async=true property")
		}
	}

	// Verify CommonJS require dependency.
	foundExpress := false
	for _, n := range result.Nodes {
		if n.Type == graph.NodeDependency && n.Name == "express" {
			foundExpress = true
			if n.Properties["system"] != "commonjs" {
				t.Error("express dependency should have system=commonjs")
			}
		}
	}
	if !foundExpress {
		t.Error("expected express CommonJS dependency")
	}

	// Verify edges.
	edgeCounts := make(map[graph.EdgeType]int)
	for _, e := range result.Edges {
		edgeCounts[e.Type]++
	}

	// 4 import edges (2 ESM + 2 CommonJS).
	if edgeCounts[graph.EdgeImports] != 4 {
		t.Errorf("Imports edges = %d, want 4", edgeCounts[graph.EdgeImports])
	}

	// Contains edges: file->module, module->class, class->methods, module->functions.
	if edgeCounts[graph.EdgeContains] < 8 {
		t.Errorf("Contains edges = %d, want at least 8", edgeCounts[graph.EdgeContains])
	}
}

func TestLanguageAndExtensions(t *testing.T) {
	p := NewParser()
	if p.Language() != parser.LangJavaScript {
		t.Errorf("Language() = %q, want %q", p.Language(), parser.LangJavaScript)
	}
	exts := p.Extensions()
	if len(exts) != 4 {
		t.Errorf("Extensions() = %v, want [\".js\", \".jsx\", \".mjs\", \".cjs\"]", exts)
	}
}

func TestParseSampleFixture(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	samplePath := filepath.Join(projectRoot, "testdata", "sample.js")

	content, err := os.ReadFile(samplePath)
	if err != nil {
		t.Skipf("testdata/sample.js not found: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile(samplePath, content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	nodeByName := indexByName(result.Nodes)

	// Check class.
	if _, ok := nodeByName["HttpClient"]; !ok {
		t.Error("expected HttpClient class node")
	}

	// Check functions.
	for _, name := range []string{"createClient", "fetchData", "main", "helperFunc"} {
		if _, ok := nodeByName[name]; !ok {
			t.Errorf("expected function %s", name)
		}
	}

	// Check arrow function.
	if _, ok := nodeByName["formatURL"]; !ok {
		t.Error("expected formatURL arrow function")
	}

	// Check dependencies: 3 ESM + 3 CommonJS = 6 total.
	depCount := 0
	for _, n := range result.Nodes {
		if n.Type == graph.NodeDependency {
			depCount++
		}
	}
	if depCount != 6 {
		t.Errorf("dependency count = %d, want 6", depCount)
	}
}

func TestCommonJSRequire(t *testing.T) {
	source := `
const fs = require('fs');
const { join } = require('path');
`
	p := NewParser()
	result, err := p.ParseFile("cjs.js", []byte(source))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	depNames := make([]string, 0)
	for _, n := range result.Nodes {
		if n.Type == graph.NodeDependency {
			depNames = append(depNames, n.Name)
		}
	}

	if len(depNames) != 2 {
		t.Errorf("got %d dependencies, want 2: %v", len(depNames), depNames)
	}

	expected := map[string]bool{"fs": true, "path": true}
	for _, name := range depNames {
		if !expected[name] {
			t.Errorf("unexpected dependency %q", name)
		}
	}
}

func TestESMImports(t *testing.T) {
	source := `
import { foo } from 'bar';
import baz from 'qux';
import * as utils from './utils';
`
	p := NewParser()
	result, err := p.ParseFile("esm.js", []byte(source))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	depNames := make([]string, 0)
	for _, n := range result.Nodes {
		if n.Type == graph.NodeDependency {
			depNames = append(depNames, n.Name)
		}
	}

	if len(depNames) != 3 {
		t.Errorf("got %d dependencies, want 3: %v", len(depNames), depNames)
	}
}

func TestBothModuleSystems(t *testing.T) {
	source := `
import { readFile } from 'fs/promises';
const express = require('express');

export function handler(req, res) {
  res.send('ok');
}
`
	p := NewParser()
	result, err := p.ParseFile("mixed.js", []byte(source))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	depCount := 0
	for _, n := range result.Nodes {
		if n.Type == graph.NodeDependency {
			depCount++
		}
	}
	if depCount != 2 {
		t.Errorf("dependency count = %d, want 2", depCount)
	}

	nodeByName := indexByName(result.Nodes)
	if _, ok := nodeByName["handler"]; !ok {
		t.Error("expected handler function")
	}
}

func TestParseExpressJSRoutes(t *testing.T) {
	source := `
const express = require('express');
const { getUsers, createUser } = require('./handlers');

const router = express.Router();
router.get('/users', getUsers);
router.post('/users', createUser);
router.put('/users/:id', updateUser);
router.delete('/users/:id', deleteUser);
router.patch('/users/:id', function(req, res) {
  res.json({ patched: true });
});
app.get('/health', function(req, res) {
  res.json({ status: 'ok' });
});
`
	p := NewParser()
	result, err := p.ParseFile("routes.js", []byte(source))
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

func TestParseExpressJSUseMount(t *testing.T) {
	source := `
const express = require('express');
const app = express();
const router = express.Router();
app.use('/api/v1', router);
app.use('/admin', adminRouter);
`
	p := NewParser()
	result, err := p.ParseFile("app.js", []byte(source))
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

func TestParseExpressJSRoutesFromFile(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	testdataPath := filepath.Join(filepath.Dir(thisFile), "testdata", "express_routes.js")

	content, err := os.ReadFile(testdataPath)
	if err != nil {
		t.Skipf("testdata/express_routes.js not found: %v", err)
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

func TestDetectJSFetchCalls(t *testing.T) {
	source := `
async function loadUsers() {
  const resp = await fetch('/api/users');
  const data = await resp.json();
}

const fetchPosts = async () => {
  return fetch('/api/posts');
};
`
	p := NewParser()
	result, err := p.ParseFile("fetch.js", []byte(source))
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

func TestDetectJSAxiosCalls(t *testing.T) {
	source := `
const axios = require('axios');

async function getUser(id) {
  const resp = await axios.get('/api/users/' + id);
  return resp.data;
}

async function createUser(data) {
  await axios.post('/api/users');
}

async function quickFetch() {
  await axios('/api/config');
}
`
	p := NewParser()
	result, err := p.ParseFile("axios.js", []byte(source))
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

func TestDetectJSTemplateLiteralURLs(t *testing.T) {
	source := "async function getItem(id) {\n  await fetch(`/api/items/${id}/details`);\n}\n"

	p := NewParser()
	result, err := p.ParseFile("template.js", []byte(source))
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

func TestDetectJSHTTPClientCalls(t *testing.T) {
	source := `
class ApiService {
  async getData() {
    await httpClient.get('/api/data');
  }
}
`
	p := NewParser()
	result, err := p.ParseFile("client.js", []byte(source))
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

	// Verify the call is linked via EdgeCalls.
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
	testdataPath := filepath.Join(filepath.Dir(thisFile), "testdata", "calls.js")

	content, err := os.ReadFile(testdataPath)
	if err != nil {
		t.Fatalf("could not read testdata/calls.js: %v", err)
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
	// 1. processItems -> helper (same-file function call)
	// 2. processItems -> ./validators import (validate call)
	// 3. processItems -> lodash import (lodash.sortBy call)
	// 4. ItemProcessor.process -> ItemProcessor.transform (this.transform call)
	// 5. ItemProcessor.process -> lodash import (lodash.cloneDeep call)

	// Verify processItems -> helper call.
	helperNode := nodeByName["helper"]
	processItemsNode := nodeByName["processItems"]
	if helperNode == nil || processItemsNode == nil {
		t.Fatal("expected helper and processItems function nodes")
	}
	foundHelperCall := false
	for _, e := range callEdges {
		if e.SourceID == processItemsNode.ID && e.TargetID == helperNode.ID {
			foundHelperCall = true
			break
		}
	}
	if !foundHelperCall {
		t.Error("expected EdgeCalls from processItems -> helper")
	}

	// Verify processItems -> ./validators import (validate call).
	validatorsImport := nodeByName["./validators"]
	if validatorsImport == nil {
		t.Fatal("expected ./validators import node")
	}
	foundValidateCall := false
	for _, e := range callEdges {
		if e.SourceID == processItemsNode.ID && e.TargetID == validatorsImport.ID {
			if e.Properties != nil && e.Properties["callee"] == "validate" {
				foundValidateCall = true
			}
			break
		}
	}
	if !foundValidateCall {
		t.Error("expected EdgeCalls from processItems -> ./validators import (validate)")
	}

	// Verify processItems -> lodash import (lodash.sortBy).
	lodashImport := nodeByName["lodash"]
	if lodashImport == nil {
		t.Fatal("expected lodash import node")
	}
	foundSortByCall := false
	for _, e := range callEdges {
		if e.SourceID == processItemsNode.ID && e.TargetID == lodashImport.ID {
			if e.Properties != nil && e.Properties["callee"] == "sortBy" {
				foundSortByCall = true
			}
		}
	}
	if !foundSortByCall {
		t.Error("expected EdgeCalls from processItems -> lodash import (sortBy)")
	}

	// Verify ItemProcessor.process -> ItemProcessor.transform (this.transform).
	transformNode := nodeByName["transform"]
	processNode := nodeByName["process"]
	if transformNode == nil || processNode == nil {
		t.Fatal("expected transform and process method nodes")
	}
	foundTransformCall := false
	for _, e := range callEdges {
		if e.SourceID == processNode.ID && e.TargetID == transformNode.ID {
			foundTransformCall = true
			break
		}
	}
	if !foundTransformCall {
		t.Error("expected EdgeCalls from ItemProcessor.process -> ItemProcessor.transform")
	}

	// Verify ItemProcessor.process -> lodash import (lodash.cloneDeep).
	foundCloneDeepCall := false
	for _, e := range callEdges {
		if e.SourceID == processNode.ID && e.TargetID == lodashImport.ID {
			if e.Properties != nil && e.Properties["callee"] == "cloneDeep" {
				foundCloneDeepCall = true
			}
		}
	}
	if !foundCloneDeepCall {
		t.Error("expected EdgeCalls from ItemProcessor.process -> lodash import (cloneDeep)")
	}

	// Total EdgeCalls: 5 general function calls, no api_call deps expected.
	var apiCalls []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeDependency && n.Properties["kind"] == "api_call" {
			apiCalls = append(apiCalls, n)
		}
	}
	if len(apiCalls) != 0 {
		t.Errorf("got %d api_call nodes, want 0", len(apiCalls))
	}

	if len(callEdges) != 5 {
		t.Errorf("total EdgeCalls = %d, want 5", len(callEdges))
		for _, e := range callEdges {
			t.Logf("  call edge: source=%s target=%s props=%v", e.SourceID, e.TargetID, e.Properties)
		}
	}
}

func TestTestFileDetection(t *testing.T) {
	source := `
const { UserService } = require('./user-service');

describe('UserService', () => {
  it('should create a user', () => {
    const svc = new UserService();
    expect(svc.create('test')).toBeTruthy();
  });

  test('handles invalid input', () => {
    const svc = new UserService();
    expect(() => svc.create('')).toThrow();
  });
});
`
	p := NewParser()

	// Parse as a test file (.test.js extension).
	result, err := p.ParseFile("src/user-service.test.js", []byte(source))
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

	// Verify NodeFile is NOT present (replaced by NodeTestFile).
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
			t.Logf("  test func: %s (test_type=%s)", tf.Name, tf.Properties["test_type"])
		}
	}

	// Verify specific test functions.
	nodeByName := indexByName(result.Nodes)
	if n, ok := nodeByName["UserService"]; ok {
		if n.Type != graph.NodeTestFunction {
			t.Errorf("UserService should be TestFunction (describe), got %s", n.Type)
		}
		if n.Properties["test_type"] != "describe" {
			t.Errorf("test_type = %q, want %q", n.Properties["test_type"], "describe")
		}
	} else {
		t.Error("expected 'UserService' describe block as TestFunction")
	}

	if n, ok := nodeByName["should create a user"]; ok {
		if n.Type != graph.NodeTestFunction {
			t.Errorf("'should create a user' should be TestFunction (it), got %s", n.Type)
		}
		if n.Properties["test_type"] != "it" {
			t.Errorf("test_type = %q, want %q", n.Properties["test_type"], "it")
		}
	} else {
		t.Error("expected 'should create a user' it block as TestFunction")
	}

	if n, ok := nodeByName["handles invalid input"]; ok {
		if n.Type != graph.NodeTestFunction {
			t.Errorf("'handles invalid input' should be TestFunction (test), got %s", n.Type)
		}
		if n.Properties["test_type"] != "test" {
			t.Errorf("test_type = %q, want %q", n.Properties["test_type"], "test")
		}
	} else {
		t.Error("expected 'handles invalid input' test block as TestFunction")
	}

	// Verify Contains edges exist for test functions.
	testContainsCount := 0
	for _, e := range result.Edges {
		if e.Type == graph.EdgeContains {
			for _, tf := range testFuncs {
				if e.TargetID == tf.ID {
					testContainsCount++
				}
			}
		}
	}
	if testContainsCount != 3 {
		t.Errorf("Contains edges for test functions = %d, want 3", testContainsCount)
	}
}

func TestNonTestFileHasNoTestNodes(t *testing.T) {
	source := `
describe('example', () => {
  it('works', () => {});
});
`
	p := NewParser()
	result, err := p.ParseFile("src/utils.js", []byte(source))
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

func TestSpecFileDetection(t *testing.T) {
	source := `
test('adds numbers', () => {
  expect(1 + 1).toBe(2);
});
`
	p := NewParser()
	result, err := p.ParseFile("src/math.spec.js", []byte(source))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	var hasTestFile bool
	for _, n := range result.Nodes {
		if n.Type == graph.NodeTestFile {
			hasTestFile = true
		}
	}
	if !hasTestFile {
		t.Error("spec.js file should be detected as NodeTestFile")
	}

	var testFuncs []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeTestFunction {
			testFuncs = append(testFuncs, n)
		}
	}
	if len(testFuncs) != 1 {
		t.Errorf("TestFunction count = %d, want 1", len(testFuncs))
	}
}

func TestJSXTestFileDetection(t *testing.T) {
	tests := []struct {
		filename string
		isTest   bool
	}{
		{"component.test.jsx", true},
		{"component.spec.jsx", true},
		{"component.jsx", false},
		{"helper.test.js", true},
		{"helper.spec.js", true},
		{"helper.js", false},
	}
	for _, tc := range tests {
		got := isTestFilename(tc.filename)
		if got != tc.isTest {
			t.Errorf("isTestFilename(%q) = %v, want %v", tc.filename, got, tc.isTest)
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
