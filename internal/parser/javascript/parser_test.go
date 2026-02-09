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
