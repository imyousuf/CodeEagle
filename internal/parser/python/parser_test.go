package python

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

const testSource = `"""A test module for parsing."""

import os
import sys
from pathlib import Path
from typing import List, Optional

MAX_RETRIES = 3
DEFAULT_NAME = "test"
_private_var = 42

class Animal:
    """Base class for animals."""

    def __init__(self, name: str, age: int) -> None:
        self.name = name
        self.age = age

    def speak(self) -> str:
        """Return the sound."""
        return ""

    @property
    def info(self) -> str:
        """Formatted info."""
        return f"{self.name}"

    @staticmethod
    def kingdom() -> str:
        return "Animalia"

class Dog(Animal):
    """A dog."""

    def __init__(self, name: str, age: int, breed: str) -> None:
        super().__init__(name, age)
        self.breed = breed

    def speak(self) -> str:
        return "Woof!"

    def fetch(self, item: str) -> str:
        return f"{self.name} fetches {item}"

def create_animal(name: str, age: int) -> Animal:
    """Factory function."""
    return Animal(name, age)

def _helper(x):
    return x + 1
`

func TestParseFile(t *testing.T) {
	p := NewParser()

	result, err := p.ParseFile("testpkg/sample.py", []byte(testSource))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	if result.FilePath != "testpkg/sample.py" {
		t.Errorf("FilePath = %q, want %q", result.FilePath, "testpkg/sample.py")
	}
	if result.Language != parser.LangPython {
		t.Errorf("Language = %q, want %q", result.Language, parser.LangPython)
	}

	// Count nodes by type
	counts := make(map[graph.NodeType]int)
	for _, n := range result.Nodes {
		counts[n.Type]++
	}

	// 1 file node
	assertCount(t, counts, graph.NodeFile, 1)
	// 1 module node
	assertCount(t, counts, graph.NodeModule, 1)
	// 4 imports: os, sys, pathlib, typing
	assertCount(t, counts, graph.NodeDependency, 4)
	// 2 classes: Animal, Dog
	assertCount(t, counts, graph.NodeClass, 2)
	// 2 functions: create_animal, _helper
	assertCount(t, counts, graph.NodeFunction, 2)
	// 7 methods: Animal.__init__, speak, info, kingdom + Dog.__init__, speak, fetch
	assertCount(t, counts, graph.NodeMethod, 7)
	// 2 constants: MAX_RETRIES, DEFAULT_NAME (UPPER_CASE)
	assertCount(t, counts, graph.NodeConstant, 2)
	// 1 variable: _private_var
	assertCount(t, counts, graph.NodeVariable, 1)

	// Verify specific nodes
	nodeByName := indexByName(result.Nodes)

	// Module docstring
	if n, ok := nodeByName["sample"]; ok {
		if n.Type != graph.NodeModule {
			t.Errorf("sample should be Module, got %s", n.Type)
		}
		if n.DocComment == "" {
			t.Error("module should have a docstring")
		}
	} else {
		t.Error("expected module node 'sample'")
	}

	// Class with docstring
	if n, ok := nodeByName["Animal"]; ok {
		if n.Type != graph.NodeClass {
			t.Errorf("Animal should be Class, got %s", n.Type)
		}
		if n.DocComment == "" {
			t.Error("Animal class should have a docstring")
		}
		if n.Exported != true {
			t.Error("Animal should be exported")
		}
	} else {
		t.Error("expected Animal class node")
	}

	// Dog class with base class
	if n, ok := nodeByName["Dog"]; ok {
		if n.Properties["bases"] != "Animal" {
			t.Errorf("Dog bases = %q, want %q", n.Properties["bases"], "Animal")
		}
	} else {
		t.Error("expected Dog class node")
	}

	// Exported function
	if n, ok := nodeByName["create_animal"]; ok {
		if n.Type != graph.NodeFunction {
			t.Errorf("create_animal should be Function, got %s", n.Type)
		}
		if !n.Exported {
			t.Error("create_animal should be exported")
		}
		if n.DocComment == "" {
			t.Error("create_animal should have a docstring")
		}
	} else {
		t.Error("expected create_animal function node")
	}

	// Private function
	if n, ok := nodeByName["_helper"]; ok {
		if n.Exported {
			t.Error("_helper should not be exported")
		}
	} else {
		t.Error("expected _helper function node")
	}

	// Method with decorator
	if n, ok := nodeByName["info"]; ok {
		if n.Type != graph.NodeMethod {
			t.Errorf("info should be Method, got %s", n.Type)
		}
		if n.Properties["decorators"] != "property" {
			t.Errorf("info decorators = %q, want %q", n.Properties["decorators"], "property")
		}
		if n.Properties["class"] != "Animal" {
			t.Errorf("info class = %q, want %q", n.Properties["class"], "Animal")
		}
	} else {
		t.Error("expected info method node")
	}

	// Static method with decorator
	if n, ok := nodeByName["kingdom"]; ok {
		if n.Properties["decorators"] != "staticmethod" {
			t.Errorf("kingdom decorators = %q, want %q", n.Properties["decorators"], "staticmethod")
		}
	} else {
		t.Error("expected kingdom method node")
	}

	// Constants
	if n, ok := nodeByName["MAX_RETRIES"]; ok {
		if n.Type != graph.NodeConstant {
			t.Errorf("MAX_RETRIES should be Constant, got %s", n.Type)
		}
	} else {
		t.Error("expected MAX_RETRIES constant node")
	}

	// Private variable
	if n, ok := nodeByName["_private_var"]; ok {
		if n.Type != graph.NodeVariable {
			t.Errorf("_private_var should be Variable, got %s", n.Type)
		}
		if n.Exported {
			t.Error("_private_var should not be exported")
		}
	} else {
		t.Error("expected _private_var variable node")
	}

	// Verify edges
	edgeCounts := make(map[graph.EdgeType]int)
	for _, edge := range result.Edges {
		edgeCounts[edge.Type]++
	}

	// Contains edges: file->module, module->classes, module->functions, module->constants, module->variables
	// + class->methods
	// File->Module (1) + Module->Animal,Dog,create_animal,_helper,MAX_RETRIES,DEFAULT_NAME,_private_var (7)
	// + Animal->__init__,speak,info,kingdom (4) + Dog->__init__,speak,fetch (3) = 15
	if edgeCounts[graph.EdgeContains] < 15 {
		t.Errorf("Contains edges = %d, want at least 15", edgeCounts[graph.EdgeContains])
	}

	// 4 import edges: os, sys, pathlib, typing
	if edgeCounts[graph.EdgeImports] != 4 {
		t.Errorf("Imports edges = %d, want 4", edgeCounts[graph.EdgeImports])
	}
}

func TestLanguageAndExtensions(t *testing.T) {
	p := NewParser()
	if p.Language() != parser.LangPython {
		t.Errorf("Language() = %q, want %q", p.Language(), parser.LangPython)
	}
	exts := p.Extensions()
	if len(exts) != 2 || exts[0] != ".py" {
		t.Errorf("Extensions() = %v, want [\".py\", \".pyi\"]", exts)
	}
}

func TestParseSampleFixture(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	samplePath := filepath.Join(projectRoot, "testdata", "sample.py")

	content, err := os.ReadFile(samplePath)
	if err != nil {
		t.Skipf("testdata/sample.py not found: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile(samplePath, content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	nodeByName := indexByName(result.Nodes)

	// Check classes
	if _, ok := nodeByName["Animal"]; !ok {
		t.Error("expected Animal class node")
	}
	if _, ok := nodeByName["Dog"]; !ok {
		t.Error("expected Dog class node")
	}

	// Check functions
	for _, name := range []string{"create_animal", "_validate_name", "process_animals"} {
		if _, ok := nodeByName[name]; !ok {
			t.Errorf("expected function %s", name)
		}
	}

	// Check methods
	for _, name := range []string{"speak", "info", "kingdom", "fetch"} {
		if _, ok := nodeByName[name]; !ok {
			t.Errorf("expected method %s", name)
		}
	}

	// Check constants
	for _, name := range []string{"MAX_RETRIES", "DEFAULT_TIMEOUT"} {
		if _, ok := nodeByName[name]; !ok {
			t.Errorf("expected constant %s", name)
		}
	}
}

const fastAPISource = `"""A FastAPI application."""

from fastapi import APIRouter, FastAPI

app = FastAPI()
router = APIRouter()


@router.get("/instances")
async def list_instances():
    """List all instances."""
    return []


@router.get("/instances/{instance_id}")
async def get_instance(instance_id: str):
    """Get a specific instance."""
    return {"id": instance_id}


@router.post("/instances")
async def create_instance(data):
    """Create a new instance."""
    return {"id": "new"}


@router.put("/instances/{instance_id}")
async def update_instance(instance_id: str, data):
    """Update an instance."""
    return {"id": instance_id}


@router.delete("/instances/{instance_id}")
async def delete_instance(instance_id: str):
    """Delete an instance."""
    return {"deleted": True}


@router.patch("/instances/{instance_id}/status")
async def patch_instance_status(instance_id: str):
    """Patch instance status."""
    return {"patched": True}


app.include_router(router, prefix="/api/v1")
`

func TestParseFastAPIEndpoints(t *testing.T) {
	p := NewParser()
	result, err := p.ParseFile("app/routes.py", []byte(fastAPISource))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	// Collect API endpoints
	var endpoints []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeAPIEndpoint {
			endpoints = append(endpoints, n)
		}
	}

	if len(endpoints) != 6 {
		t.Fatalf("expected 6 API endpoints, got %d", len(endpoints))
	}

	// Verify endpoint properties
	endpointsByPath := make(map[string]*graph.Node)
	for _, ep := range endpoints {
		endpointsByPath[ep.Properties["http_method"]+":"+ep.Properties["path"]] = ep
	}

	expectedEndpoints := []struct {
		method  string
		path    string
		handler string
	}{
		{"GET", "/instances", "list_instances"},
		{"GET", "/instances/{instance_id}", "get_instance"},
		{"POST", "/instances", "create_instance"},
		{"PUT", "/instances/{instance_id}", "update_instance"},
		{"DELETE", "/instances/{instance_id}", "delete_instance"},
		{"PATCH", "/instances/{instance_id}/status", "patch_instance_status"},
	}

	for _, exp := range expectedEndpoints {
		key := exp.method + ":" + exp.path
		ep, ok := endpointsByPath[key]
		if !ok {
			t.Errorf("missing endpoint %s", key)
			continue
		}
		if ep.Properties["framework"] != "fastapi" {
			t.Errorf("endpoint %s: framework = %q, want %q", key, ep.Properties["framework"], "fastapi")
		}
		if ep.Properties["handler"] != exp.handler {
			t.Errorf("endpoint %s: handler = %q, want %q", key, ep.Properties["handler"], exp.handler)
		}
	}

	// Verify EdgeExposes edges exist
	exposesCount := 0
	for _, edge := range result.Edges {
		if edge.Type == graph.EdgeExposes {
			exposesCount++
		}
	}
	if exposesCount != 6 {
		t.Errorf("expected 6 EdgeExposes edges, got %d", exposesCount)
	}
}

const flaskSource = `"""A Flask application."""

from flask import Flask, Blueprint

app = Flask(__name__)
bp = Blueprint("users", __name__)


@app.route("/health")
def health_check():
    """Health check endpoint."""
    return {"status": "ok"}


@bp.route("/users")
def list_users():
    """List users."""
    return []
`

func TestParseFlaskEndpoints(t *testing.T) {
	p := NewParser()
	result, err := p.ParseFile("app/flask_routes.py", []byte(flaskSource))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	var endpoints []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeAPIEndpoint {
			endpoints = append(endpoints, n)
		}
	}

	if len(endpoints) != 2 {
		t.Fatalf("expected 2 Flask endpoints, got %d", len(endpoints))
	}

	endpointsByPath := make(map[string]*graph.Node)
	for _, ep := range endpoints {
		endpointsByPath[ep.Properties["path"]] = ep
	}

	if ep, ok := endpointsByPath["/health"]; ok {
		if ep.Properties["framework"] != "flask" {
			t.Errorf("/health: framework = %q, want %q", ep.Properties["framework"], "flask")
		}
		if ep.Properties["handler"] != "health_check" {
			t.Errorf("/health: handler = %q, want %q", ep.Properties["handler"], "health_check")
		}
	} else {
		t.Error("missing /health endpoint")
	}

	if ep, ok := endpointsByPath["/users"]; ok {
		if ep.Properties["framework"] != "flask" {
			t.Errorf("/users: framework = %q, want %q", ep.Properties["framework"], "flask")
		}
	} else {
		t.Error("missing /users endpoint")
	}
}

func TestParseIncludeRouter(t *testing.T) {
	p := NewParser()
	result, err := p.ParseFile("app/routes.py", []byte(fastAPISource))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	// Find router_mount variable
	var routerMount *graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeVariable && n.Properties != nil && n.Properties["kind"] == "router_mount" {
			routerMount = n
			break
		}
	}

	if routerMount == nil {
		t.Fatal("expected router_mount variable node")
	}
	if routerMount.Properties["prefix"] != "/api/v1" {
		t.Errorf("router_mount prefix = %q, want %q", routerMount.Properties["prefix"], "/api/v1")
	}
	if routerMount.Properties["router"] != "router" {
		t.Errorf("router_mount router = %q, want %q", routerMount.Properties["router"], "router")
	}
}

const httpClientSource = `"""Python code with HTTP client calls."""

import requests
import httpx


def fetch_instances():
    response = requests.get("/api/v1/instances")
    return response.json()


def create_instance(name):
    response = requests.post("/api/v1/instances", json={"name": name})
    return response.json()


def update_instance(instance_id, data):
    response = requests.put("/api/v1/instances/123", json=data)
    return response.json()


def delete_instance(instance_id):
    response = requests.delete("/api/v1/instances/456")
    return response.status_code
`

func TestDetectRequestsCalls(t *testing.T) {
	p := NewParser()
	result, err := p.ParseFile("services/client.py", []byte(httpClientSource))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	// Collect api_call dependencies
	var apiCalls []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeDependency && n.Properties != nil && n.Properties["kind"] == "api_call" {
			apiCalls = append(apiCalls, n)
		}
	}

	if len(apiCalls) < 4 {
		t.Fatalf("expected at least 4 api_call dependencies, got %d", len(apiCalls))
	}

	// Verify methods and paths are extracted
	foundMethods := make(map[string]bool)
	for _, call := range apiCalls {
		method := call.Properties["http_method"]
		foundMethods[method] = true
		if call.Properties["framework"] != "requests" {
			t.Errorf("api_call framework = %q, want %q", call.Properties["framework"], "requests")
		}
	}

	for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
		if !foundMethods[method] {
			t.Errorf("missing api_call with method %s", method)
		}
	}

	// Verify EdgeCalls edges exist
	callsCount := 0
	for _, edge := range result.Edges {
		if edge.Type == graph.EdgeCalls {
			callsCount++
		}
	}
	if callsCount < 4 {
		t.Errorf("expected at least 4 EdgeCalls edges, got %d", callsCount)
	}
}

const httpxSource = `"""Httpx client calls."""

import httpx


async def async_fetch(instance_id):
    async with httpx.AsyncClient() as client:
        response = await client.get("/api/v1/agents")
        return response.json()
`

func TestDetectHttpxCalls(t *testing.T) {
	p := NewParser()
	result, err := p.ParseFile("services/async_client.py", []byte(httpxSource))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	var apiCalls []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeDependency && n.Properties != nil && n.Properties["kind"] == "api_call" {
			apiCalls = append(apiCalls, n)
		}
	}

	if len(apiCalls) < 1 {
		t.Fatalf("expected at least 1 api_call dependency, got %d", len(apiCalls))
	}

	found := false
	for _, call := range apiCalls {
		if call.Properties["http_method"] == "GET" && call.Properties["path"] == "/api/v1/agents" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected api_call for GET /api/v1/agents")
	}
}

func TestParseFastAPIFixture(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	fixturePath := filepath.Join(filepath.Dir(thisFile), "testdata", "fastapi_app.py")

	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Skipf("testdata/fastapi_app.py not found: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile(fixturePath, content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	var endpoints []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeAPIEndpoint {
			endpoints = append(endpoints, n)
		}
	}

	if len(endpoints) != 6 {
		t.Errorf("expected 6 API endpoints from fixture, got %d", len(endpoints))
	}

	// Check router mount
	var routerMount *graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeVariable && n.Properties != nil && n.Properties["kind"] == "router_mount" {
			routerMount = n
			break
		}
	}
	if routerMount == nil {
		t.Error("expected router_mount variable node from fixture")
	}
}

func TestParseHTTPClientFixture(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	fixturePath := filepath.Join(filepath.Dir(thisFile), "testdata", "http_clients.py")

	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Skipf("testdata/http_clients.py not found: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile(fixturePath, content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	var apiCalls []*graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeDependency && n.Properties != nil && n.Properties["kind"] == "api_call" {
			apiCalls = append(apiCalls, n)
		}
	}

	if len(apiCalls) < 3 {
		t.Errorf("expected at least 3 api_call dependencies from fixture, got %d", len(apiCalls))
	}
}

func TestExtractFunctionCalls(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	fixturePath := filepath.Join(filepath.Dir(thisFile), "testdata", "calls.py")

	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("testdata/calls.py not found: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile(fixturePath, content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	// Build edge lookup: sourceID+targetID → edge
	type edgeKey struct {
		sourceID string
		targetID string
		edgeType graph.EdgeType
	}
	edgeMap := make(map[edgeKey]*graph.Edge)
	for _, edge := range result.Edges {
		edgeMap[edgeKey{edge.SourceID, edge.TargetID, edge.Type}] = edge
	}

	// Build node lookup by name and type
	nodeByNameType := make(map[string]*graph.Node)
	for _, n := range result.Nodes {
		key := string(n.Type) + ":" + n.Name
		nodeByNameType[key] = n
	}

	// Helper to get a node ID
	getNodeID := func(nodeType, name string) string {
		if n, ok := nodeByNameType[nodeType+":"+name]; ok {
			return n.ID
		}
		t.Fatalf("missing node %s:%s", nodeType, name)
		return ""
	}

	processDataID := getNodeID("Function", "process_data")
	helperID := getNodeID("Function", "helper")
	osDepID := getNodeID("Dependency", "os")
	jsonDepID := getNodeID("Dependency", "json")
	datetimeDepID := getNodeID("Dependency", "datetime")
	processMethodID := getNodeID("Method", "process")
	validateMethodID := getNodeID("Method", "validate")

	// 1. process_data → helper (same-file call)
	if _, ok := edgeMap[edgeKey{processDataID, helperID, graph.EdgeCalls}]; !ok {
		t.Error("missing EdgeCalls: process_data → helper (same-file call)")
	}

	// 2. process_data → os import dep (import-qualified: os.path.join)
	if _, ok := edgeMap[edgeKey{processDataID, osDepID, graph.EdgeCalls}]; !ok {
		t.Error("missing EdgeCalls: process_data → os (import-qualified call)")
	}

	// 3. process_data → json import dep (import-qualified: json.dumps)
	if e, ok := edgeMap[edgeKey{processDataID, jsonDepID, graph.EdgeCalls}]; !ok {
		t.Error("missing EdgeCalls: process_data → json (import-qualified call)")
	} else if e.Properties["callee"] != "dumps" {
		t.Errorf("process_data → json callee = %q, want %q", e.Properties["callee"], "dumps")
	}

	// 4. process_data → datetime import dep (import-qualified: datetime.now)
	if _, ok := edgeMap[edgeKey{processDataID, datetimeDepID, graph.EdgeCalls}]; !ok {
		t.Error("missing EdgeCalls: process_data → datetime (import-qualified call)")
	}

	// 5. DataProcessor.process → DataProcessor.validate (self call)
	if e, ok := edgeMap[edgeKey{processMethodID, validateMethodID, graph.EdgeCalls}]; !ok {
		t.Error("missing EdgeCalls: DataProcessor.process → DataProcessor.validate (self call)")
	} else if e.Properties["callee"] != "validate" {
		t.Errorf("process → validate callee = %q, want %q", e.Properties["callee"], "validate")
	}

	// 6. DataProcessor.process → json import dep (import-qualified: json.loads)
	if e, ok := edgeMap[edgeKey{processMethodID, jsonDepID, graph.EdgeCalls}]; !ok {
		t.Error("missing EdgeCalls: DataProcessor.process → json (import-qualified call)")
	} else if e.Properties["callee"] != "loads" {
		t.Errorf("process → json callee = %q, want %q", e.Properties["callee"], "loads")
	}

	// 7. Verify builtins like len() do NOT generate EdgeCalls
	// len() is called in validate() - make sure no EdgeCalls points to a "len" node
	for _, edge := range result.Edges {
		if edge.Type == graph.EdgeCalls {
			for _, n := range result.Nodes {
				if n.ID == edge.TargetID && n.Name == "len" {
					t.Error("builtin len() should NOT generate EdgeCalls")
				}
			}
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
