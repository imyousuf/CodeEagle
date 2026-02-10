package manifest

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

func testdataDir() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("could not determine test file path")
	}
	return filepath.Join(filepath.Dir(thisFile), "testdata")
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testdataDir(), name))
	if err != nil {
		t.Fatalf("failed to read testdata/%s: %v", name, err)
	}
	return data
}

func TestParsePyprojectToml(t *testing.T) {
	p := NewParser()
	content := readTestdata(t, "pyproject.toml")
	result, err := p.ParseFile("services/email-receiver/pyproject.toml", content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	if result.Language != parser.LangManifest {
		t.Errorf("Language = %q, want %q", result.Language, parser.LangManifest)
	}

	counts := countByType(result.Nodes)
	assertCount(t, counts, graph.NodeFile, 1)
	assertCount(t, counts, graph.NodeService, 1)
	assertCount(t, counts, graph.NodeDependency, 5)

	byName := indexByName(result.Nodes)

	// Service node.
	svc, ok := byName["email-receiver"]
	if !ok {
		t.Fatal("expected service node 'email-receiver'")
	}
	if svc.Properties["ecosystem"] != "python" {
		t.Errorf("ecosystem = %q, want %q", svc.Properties["ecosystem"], "python")
	}
	if svc.Properties["version"] != "1.2.3" {
		t.Errorf("version = %q, want %q", svc.Properties["version"], "1.2.3")
	}

	// Dependency: fastapi.
	dep, ok := byName["fastapi"]
	if !ok {
		t.Fatal("expected dependency node 'fastapi'")
	}
	if dep.Properties["version"] != ">=0.100.0" {
		t.Errorf("fastapi version = %q, want %q", dep.Properties["version"], ">=0.100.0")
	}
	if dep.Properties["source"] != "pyproject.toml" {
		t.Errorf("source = %q, want %q", dep.Properties["source"], "pyproject.toml")
	}

	// Dependency: boto3 (no version).
	dep, ok = byName["boto3"]
	if !ok {
		t.Fatal("expected dependency node 'boto3'")
	}
	if dep.Properties["version"] != "" {
		t.Errorf("boto3 version = %q, want empty", dep.Properties["version"])
	}

	// Dependency: llm-framework (exact version).
	dep, ok = byName["llm-framework"]
	if !ok {
		t.Fatal("expected dependency node 'llm-framework'")
	}
	if dep.Properties["version"] != "==0.2.46" {
		t.Errorf("llm-framework version = %q, want %q", dep.Properties["version"], "==0.2.46")
	}

	// Edges: file->service (Contains) + service->dep (DependsOn) for each dep.
	containsCount := countEdgeType(result.Edges, graph.EdgeContains)
	if containsCount != 1 {
		t.Errorf("Contains edges = %d, want 1", containsCount)
	}
	dependsCount := countEdgeType(result.Edges, graph.EdgeDependsOn)
	if dependsCount != 5 {
		t.Errorf("DependsOn edges = %d, want 5", dependsCount)
	}
}

func TestParseRequirementsTxt(t *testing.T) {
	p := NewParser()
	content := readTestdata(t, "requirements.txt")
	result, err := p.ParseFile("services/api/requirements.txt", content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	counts := countByType(result.Nodes)
	assertCount(t, counts, graph.NodeFile, 1)
	assertCount(t, counts, graph.NodeService, 1)
	// 5 deps + 1 include + 1 celery[redis] = 7
	assertCount(t, counts, graph.NodeDependency, 7)

	byName := indexByName(result.Nodes)

	// Include directive.
	incl, ok := byName["shared/requirements.txt"]
	if !ok {
		t.Fatal("expected include node 'shared/requirements.txt'")
	}
	if incl.Properties["kind"] != "include" {
		t.Errorf("include kind = %q, want %q", incl.Properties["kind"], "include")
	}

	// Celery with extras.
	cel, ok := byName["celery[redis]"]
	if !ok {
		t.Fatal("expected dependency node 'celery[redis]'")
	}
	if cel.Properties["version"] != "==5.3.1" {
		t.Errorf("celery version = %q, want %q", cel.Properties["version"], "==5.3.1")
	}

	// Requests with compound version.
	req, ok := byName["requests"]
	if !ok {
		t.Fatal("expected dependency node 'requests'")
	}
	if req.Properties["version"] != ">=2.28.0,<3.0.0" {
		t.Errorf("requests version = %q, want %q", req.Properties["version"], ">=2.28.0,<3.0.0")
	}

	// Line numbers should be set.
	if cel.Line == 0 {
		t.Error("celery line should be set")
	}
}

func TestParsePackageJson(t *testing.T) {
	p := NewParser()
	content := readTestdata(t, "package.json")
	result, err := p.ParseFile("frontend/package.json", content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	counts := countByType(result.Nodes)
	assertCount(t, counts, graph.NodeFile, 1)
	assertCount(t, counts, graph.NodeService, 1)
	// 4 deps + 3 devDeps = 7
	assertCount(t, counts, graph.NodeDependency, 7)

	byName := indexByName(result.Nodes)

	svc, ok := byName["@opti/web-frontend"]
	if !ok {
		t.Fatal("expected service node '@opti/web-frontend'")
	}
	if svc.Properties["ecosystem"] != "nodejs" {
		t.Errorf("ecosystem = %q, want %q", svc.Properties["ecosystem"], "nodejs")
	}
	if svc.Properties["version"] != "2.5.0" {
		t.Errorf("version = %q, want %q", svc.Properties["version"], "2.5.0")
	}

	// Regular dependency.
	react, ok := byName["react"]
	if !ok {
		t.Fatal("expected dependency node 'react'")
	}
	if react.Properties["version"] != "^18.2.0" {
		t.Errorf("react version = %q, want %q", react.Properties["version"], "^18.2.0")
	}

	// Dev dependency.
	ts, ok := byName["typescript"]
	if !ok {
		t.Fatal("expected dependency node 'typescript'")
	}
	if ts.Properties["scope"] != "dev" {
		t.Errorf("typescript scope = %q, want %q", ts.Properties["scope"], "dev")
	}

	dependsCount := countEdgeType(result.Edges, graph.EdgeDependsOn)
	if dependsCount != 7 {
		t.Errorf("DependsOn edges = %d, want 7", dependsCount)
	}
}

func TestParseGoMod(t *testing.T) {
	p := NewParser()
	content := readTestdata(t, "go.mod")
	result, err := p.ParseFile("services/space-element/go.mod", content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	counts := countByType(result.Nodes)
	assertCount(t, counts, graph.NodeFile, 1)
	assertCount(t, counts, graph.NodeService, 1)
	// 4 direct + 2 indirect = 6
	assertCount(t, counts, graph.NodeDependency, 6)

	byName := indexByName(result.Nodes)

	svc, ok := byName["github.com/example/space-element"]
	if !ok {
		t.Fatal("expected service node 'github.com/example/space-element'")
	}
	if svc.Properties["ecosystem"] != "go" {
		t.Errorf("ecosystem = %q, want %q", svc.Properties["ecosystem"], "go")
	}

	// Direct dependency.
	gin, ok := byName["github.com/gin-gonic/gin"]
	if !ok {
		t.Fatal("expected dependency node 'github.com/gin-gonic/gin'")
	}
	if gin.Properties["version"] != "v1.9.1" {
		t.Errorf("gin version = %q, want %q", gin.Properties["version"], "v1.9.1")
	}
	if gin.Properties["scope"] == "indirect" {
		t.Error("gin should not be indirect")
	}

	// Indirect dependency.
	xxh, ok := byName["github.com/cespare/xxhash/v2"]
	if !ok {
		t.Fatal("expected dependency node 'github.com/cespare/xxhash/v2'")
	}
	if xxh.Properties["scope"] != "indirect" {
		t.Errorf("xxhash scope = %q, want %q", xxh.Properties["scope"], "indirect")
	}

	dependsCount := countEdgeType(result.Edges, graph.EdgeDependsOn)
	if dependsCount != 6 {
		t.Errorf("DependsOn edges = %d, want 6", dependsCount)
	}
}

func TestLanguageAndFilenames(t *testing.T) {
	p := NewParser()

	if p.Language() != parser.LangManifest {
		t.Errorf("Language() = %q, want %q", p.Language(), parser.LangManifest)
	}

	exts := p.Extensions()
	if len(exts) != 1 || exts[0] != ".toml" {
		t.Errorf("Extensions() = %v, want [\".toml\"]", exts)
	}

	filenames := p.Filenames()
	expected := map[string]bool{
		"pyproject.toml":   true,
		"requirements.txt": true,
		"setup.py":         true,
		"package.json":     true,
		"go.mod":           true,
	}
	if len(filenames) != len(expected) {
		t.Errorf("Filenames() has %d entries, want %d", len(filenames), len(expected))
	}
	for _, fn := range filenames {
		if !expected[fn] {
			t.Errorf("unexpected filename %q", fn)
		}
	}
}

func TestUnknownManifestFile(t *testing.T) {
	p := NewParser()
	result, err := p.ParseFile("some/unknown.toml", []byte("[tool]\nkey = 1"))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}
	if result.Language != parser.LangManifest {
		t.Errorf("Language = %q, want %q", result.Language, parser.LangManifest)
	}
	if len(result.Nodes) != 0 {
		t.Errorf("expected no nodes for unknown manifest, got %d", len(result.Nodes))
	}
}

// Helpers

func countByType(nodes []*graph.Node) map[graph.NodeType]int {
	m := make(map[graph.NodeType]int)
	for _, n := range nodes {
		m[n.Type]++
	}
	return m
}

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

func countEdgeType(edges []*graph.Edge, et graph.EdgeType) int {
	count := 0
	for _, e := range edges {
		if e.Type == et {
			count++
		}
	}
	return count
}
