package html

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

const testHTML = `<!DOCTYPE html>
<html>
<head>
    <meta name="description" content="Test page">
    <title>Test</title>
    <link rel="stylesheet" href="style.css">
    <link rel="icon" href="favicon.ico">
    <script src="vendor/lib.js"></script>
</head>
<body>
    <form action="/api/login" method="POST">
        <input type="text" name="user">
        <button type="submit">Login</button>
    </form>
    <form action="/api/search" method="GET">
        <input type="search" name="q">
    </form>
    <my-widget></my-widget>
    <app-header></app-header>
    <script src="app.js"></script>
</body>
</html>`

func TestParseHTMLFile(t *testing.T) {
	p := NewParser()
	result, err := p.ParseFile("index.html", []byte(testHTML))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	if result.FilePath != "index.html" {
		t.Errorf("FilePath = %q, want %q", result.FilePath, "index.html")
	}
	if result.Language != parser.LangHTML {
		t.Errorf("Language = %q, want %q", result.Language, parser.LangHTML)
	}

	nodesByType := countByType(result.Nodes)
	edgesByType := countEdgesByType(result.Edges)

	// 1 document node
	if nodesByType[graph.NodeDocument] != 1 {
		t.Errorf("Document nodes = %d, want 1", nodesByType[graph.NodeDocument])
	}

	// Dependencies: style.css, favicon.ico, vendor/lib.js, app.js, my-widget, app-header = 6
	if nodesByType[graph.NodeDependency] != 6 {
		t.Errorf("Dependency nodes = %d, want 6", nodesByType[graph.NodeDependency])
	}

	// API endpoints: POST /api/login, GET /api/search = 2
	if nodesByType[graph.NodeAPIEndpoint] != 2 {
		t.Errorf("APIEndpoint nodes = %d, want 2", nodesByType[graph.NodeAPIEndpoint])
	}

	// DependsOn edges: 4 file deps + 2 component deps = 6
	if edgesByType[graph.EdgeDependsOn] != 6 {
		t.Errorf("DependsOn edges = %d, want 6", edgesByType[graph.EdgeDependsOn])
	}

	// Consumes edges: 2 forms
	if edgesByType[graph.EdgeConsumes] != 2 {
		t.Errorf("Consumes edges = %d, want 2", edgesByType[graph.EdgeConsumes])
	}

	// Check specific nodes exist.
	nodeByName := indexByName(result.Nodes)

	assertNodeExists(t, nodeByName, "style.css", graph.NodeDependency)
	assertNodeExists(t, nodeByName, "vendor/lib.js", graph.NodeDependency)
	assertNodeExists(t, nodeByName, "app.js", graph.NodeDependency)
	assertNodeExists(t, nodeByName, "my-widget", graph.NodeDependency)
	assertNodeExists(t, nodeByName, "app-header", graph.NodeDependency)

	assertNodeExists(t, nodeByName, "POST /api/login", graph.NodeAPIEndpoint)
	assertNodeExists(t, nodeByName, "GET /api/search", graph.NodeAPIEndpoint)

	// Check meta was stored as property on document node.
	docNode := nodeByName["index.html"]
	if docNode == nil {
		t.Fatal("Document node not found")
	}
	if docNode.Properties["meta:description"] != "Test page" {
		t.Errorf("meta:description = %q, want %q", docNode.Properties["meta:description"], "Test page")
	}

	// Check dependency kinds.
	if n, ok := nodeByName["style.css"]; ok {
		if n.Properties["kind"] != "stylesheet" {
			t.Errorf("style.css kind = %q, want %q", n.Properties["kind"], "stylesheet")
		}
	}
	if n, ok := nodeByName["favicon.ico"]; ok {
		if n.Properties["kind"] != "icon" {
			t.Errorf("favicon.ico kind = %q, want %q", n.Properties["kind"], "icon")
		}
	}
	if n, ok := nodeByName["my-widget"]; ok {
		if n.Properties["kind"] != "component" {
			t.Errorf("my-widget kind = %q, want %q", n.Properties["kind"], "component")
		}
	}
}

const testJinja2 = `{% extends "base.html" %}
{% import "macros/forms.html" as forms %}

{% block title %}Dashboard{% endblock %}

{% block content %}
<div>
    {% include "partials/nav.html" %}
    <main>{{ content }}</main>
    {% include "partials/footer.html" %}
</div>
{% endblock %}
`

func TestParseJinja2Template(t *testing.T) {
	p := NewParser()
	result, err := p.ParseFile("templates/dashboard.jinja2", []byte(testJinja2))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	nodeByName := indexByName(result.Nodes)

	// Document node with template_type property.
	docNode := nodeByName["templates/dashboard.jinja2"]
	if docNode == nil {
		t.Fatal("Document node not found")
	}
	if docNode.Properties["template_type"] != "jinja2" {
		t.Errorf("template_type = %q, want %q", docNode.Properties["template_type"], "jinja2")
	}

	// Template dependencies: base.html (extends), macros/forms.html (import), partials/nav.html, partials/footer.html (includes)
	depNames := []string{"base.html", "macros/forms.html", "partials/nav.html", "partials/footer.html"}
	for _, name := range depNames {
		assertNodeExists(t, nodeByName, name, graph.NodeDependency)
	}

	// Check extends kind.
	if n, ok := nodeByName["base.html"]; ok {
		if n.Properties["kind"] != "extends" {
			t.Errorf("base.html kind = %q, want %q", n.Properties["kind"], "extends")
		}
	}

	// Check include kind.
	if n, ok := nodeByName["partials/nav.html"]; ok {
		if n.Properties["kind"] != "include" {
			t.Errorf("partials/nav.html kind = %q, want %q", n.Properties["kind"], "include")
		}
	}

	// Check import kind.
	if n, ok := nodeByName["macros/forms.html"]; ok {
		if n.Properties["kind"] != "import" {
			t.Errorf("macros/forms.html kind = %q, want %q", n.Properties["kind"], "import")
		}
	}

	// Verify DependsOn edges for all template deps.
	edgesByType := countEdgesByType(result.Edges)
	if edgesByType[graph.EdgeDependsOn] != 4 {
		t.Errorf("DependsOn edges = %d, want 4", edgesByType[graph.EdgeDependsOn])
	}
}

const testVue = `<template>
  <div class="app">
    <h1>{{ title }}</h1>
    <my-button @click="handleClick">Click me</my-button>
  </div>
</template>

<script lang="ts">
import { defineComponent } from 'vue';

export default defineComponent({
  name: 'App',
  data() {
    return { title: 'Hello Vue' };
  }
});
</script>

<style scoped lang="scss">
.app {
  color: blue;
}
</style>
`

func TestParseVueSFC(t *testing.T) {
	p := NewParser()
	result, err := p.ParseFile("components/App.vue", []byte(testVue))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	nodeByName := indexByName(result.Nodes)

	// Document node with vue template type.
	docNode := nodeByName["components/App.vue"]
	if docNode == nil {
		t.Fatal("Document node not found")
	}
	if docNode.Properties["template_type"] != "vue" {
		t.Errorf("template_type = %q, want %q", docNode.Properties["template_type"], "vue")
	}

	// Vue sections: template, script, style.
	assertNodeExists(t, nodeByName, "template", graph.NodeDocument)
	assertNodeExists(t, nodeByName, "script", graph.NodeDocument)
	assertNodeExists(t, nodeByName, "style", graph.NodeDocument)

	// Check script lang.
	if n, ok := nodeByName["script"]; ok {
		if n.Properties["lang"] != "ts" {
			t.Errorf("script lang = %q, want %q", n.Properties["lang"], "ts")
		}
	}

	// Check style lang.
	if n, ok := nodeByName["style"]; ok {
		if n.Properties["lang"] != "scss" {
			t.Errorf("style lang = %q, want %q", n.Properties["lang"], "scss")
		}
	}

	// Contains edges for vue sections.
	edgesByType := countEdgesByType(result.Edges)
	if edgesByType[graph.EdgeContains] < 3 {
		t.Errorf("Contains edges = %d, want at least 3", edgesByType[graph.EdgeContains])
	}
}

func TestParseHTMLFixture(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	samplePath := filepath.Join(projectRoot, "testdata", "sample.html")

	content, err := os.ReadFile(samplePath)
	if err != nil {
		t.Skipf("testdata/sample.html not found: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile(samplePath, content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	nodesByType := countByType(result.Nodes)

	// Should have at least: document + script deps + link deps + form endpoints + custom elements
	if nodesByType[graph.NodeDocument] < 1 {
		t.Errorf("Document nodes = %d, want at least 1", nodesByType[graph.NodeDocument])
	}
	if nodesByType[graph.NodeDependency] < 3 {
		t.Errorf("Dependency nodes = %d, want at least 3", nodesByType[graph.NodeDependency])
	}
	if nodesByType[graph.NodeAPIEndpoint] < 2 {
		t.Errorf("APIEndpoint nodes = %d, want at least 2", nodesByType[graph.NodeAPIEndpoint])
	}
}

func TestParseJinja2Fixture(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	samplePath := filepath.Join(projectRoot, "testdata", "sample.jinja2")

	content, err := os.ReadFile(samplePath)
	if err != nil {
		t.Skipf("testdata/sample.jinja2 not found: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile(samplePath, content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	nodeByName := indexByName(result.Nodes)

	// Should find extends and include directives.
	assertNodeExists(t, nodeByName, "base.html", graph.NodeDependency)
	assertNodeExists(t, nodeByName, "partials/sidebar.html", graph.NodeDependency)
	assertNodeExists(t, nodeByName, "partials/footer.html", graph.NodeDependency)
}

func TestLanguageAndExtensions(t *testing.T) {
	p := NewParser()
	if p.Language() != parser.LangHTML {
		t.Errorf("Language() = %q, want %q", p.Language(), parser.LangHTML)
	}
	exts := p.Extensions()
	if len(exts) == 0 {
		t.Error("Extensions() returned empty slice")
	}
	// Should include .html at minimum.
	found := false
	for _, ext := range exts {
		if ext == ".html" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Extensions() = %v, missing .html", exts)
	}
}

// Helpers.

func countByType(nodes []*graph.Node) map[graph.NodeType]int {
	m := make(map[graph.NodeType]int)
	for _, n := range nodes {
		m[n.Type]++
	}
	return m
}

func countEdgesByType(edges []*graph.Edge) map[graph.EdgeType]int {
	m := make(map[graph.EdgeType]int)
	for _, e := range edges {
		m[e.Type]++
	}
	return m
}

func indexByName(nodes []*graph.Node) map[string]*graph.Node {
	m := make(map[string]*graph.Node, len(nodes))
	for _, n := range nodes {
		m[n.Name] = n
	}
	return m
}

func assertNodeExists(t *testing.T, nodes map[string]*graph.Node, name string, expectedType graph.NodeType) {
	t.Helper()
	n, ok := nodes[name]
	if !ok {
		t.Errorf("expected node %q not found", name)
		return
	}
	if n.Type != expectedType {
		t.Errorf("node %q type = %q, want %q", name, n.Type, expectedType)
	}
}
