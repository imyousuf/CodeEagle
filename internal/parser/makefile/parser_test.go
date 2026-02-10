package makefile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

const testMakefile = `.PHONY: build test clean all

# Binary name
BINARY_NAME=myapp
# Build directory
BUILD_DIR=bin

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test

# Build flags
LDFLAGS=-ldflags "-s -w"

# Default target
all: build

## build: Build the binary
build:
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/main

## test: Run tests
test: build
	$(GOTEST) -v ./...

## clean: Clean build artifacts
clean:
	rm -rf $(BUILD_DIR)

install: build
	cp $(BUILD_DIR)/$(BINARY_NAME) /usr/local/bin/

include common.mk
-include local.mk
`

func TestParseMakefile(t *testing.T) {
	p := NewParser()

	result, err := p.ParseFile("Makefile", []byte(testMakefile))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	if result.Language != parser.LangMakefile {
		t.Errorf("Language = %q, want %q", result.Language, parser.LangMakefile)
	}

	// Count nodes by type.
	counts := make(map[graph.NodeType]int)
	for _, n := range result.Nodes {
		counts[n.Type]++
	}

	// 1 file
	assertCount(t, counts, graph.NodeFile, 1)
	// 5 targets: all, build, test, clean, install
	assertCount(t, counts, graph.NodeFunction, 5)
	// 6 variables: BINARY_NAME, BUILD_DIR, GOCMD, GOBUILD, GOTEST, LDFLAGS
	assertCount(t, counts, graph.NodeVariable, 6)
	// 2 includes: common.mk, local.mk
	assertCount(t, counts, graph.NodeDependency, 2)

	nodeByName := indexByName(result.Nodes)

	// Check build target has doc comment.
	if n, ok := nodeByName["build"]; ok {
		if n.Properties["kind"] != "target" {
			t.Errorf("build kind = %q, want %q", n.Properties["kind"], "target")
		}
		if n.DocComment == "" {
			t.Error("build target should have doc comment")
		}
		if n.Properties["phony"] != "true" {
			t.Error("build should be phony")
		}
	} else {
		t.Error("expected build target node")
	}

	// Check test target depends on build.
	if n, ok := nodeByName["test"]; ok {
		if n.Properties["prerequisites"] != "build" {
			t.Errorf("test prerequisites = %q, want %q", n.Properties["prerequisites"], "build")
		}
		if n.DocComment == "" {
			t.Error("test target should have doc comment")
		}
	} else {
		t.Error("expected test target node")
	}

	// Check all target.
	if n, ok := nodeByName["all"]; ok {
		if n.Properties["prerequisites"] != "build" {
			t.Errorf("all prerequisites = %q, want %q", n.Properties["prerequisites"], "build")
		}
	} else {
		t.Error("expected all target node")
	}

	// Check install target (not phony).
	if n, ok := nodeByName["install"]; ok {
		if n.Properties["phony"] == "true" {
			t.Error("install should not be phony")
		}
	} else {
		t.Error("expected install target node")
	}

	// Check variable properties.
	if n, ok := nodeByName["BINARY_NAME"]; ok {
		if n.Properties["kind"] != "makefile_var" {
			t.Errorf("BINARY_NAME kind = %q, want %q", n.Properties["kind"], "makefile_var")
		}
		if n.Properties["assignment_op"] != "=" {
			t.Errorf("BINARY_NAME assignment_op = %q, want %q", n.Properties["assignment_op"], "=")
		}
		if n.Properties["value"] != "myapp" {
			t.Errorf("BINARY_NAME value = %q, want %q", n.Properties["value"], "myapp")
		}
	} else {
		t.Error("expected BINARY_NAME variable node")
	}

	// Check include nodes.
	if n, ok := nodeByName["common.mk"]; ok {
		if n.Properties["kind"] != "include" {
			t.Errorf("common.mk kind = %q, want %q", n.Properties["kind"], "include")
		}
	} else {
		t.Error("expected common.mk include node")
	}

	// Verify DependsOn edges exist.
	depEdges := 0
	for _, edge := range result.Edges {
		if edge.Type == graph.EdgeDependsOn {
			depEdges++
		}
	}
	// test->build, all->build, install->build = 3
	if depEdges < 3 {
		t.Errorf("DependsOn edges = %d, want at least 3", depEdges)
	}

	// Verify Contains edges.
	containsEdges := 0
	for _, edge := range result.Edges {
		if edge.Type == graph.EdgeContains {
			containsEdges++
		}
	}
	// 5 targets + 6 variables = 11
	if containsEdges < 11 {
		t.Errorf("Contains edges = %d, want at least 11", containsEdges)
	}

	// Verify Imports edges.
	importEdges := 0
	for _, edge := range result.Edges {
		if edge.Type == graph.EdgeImports {
			importEdges++
		}
	}
	if importEdges != 2 {
		t.Errorf("Imports edges = %d, want 2", importEdges)
	}
}

func TestParseNoTargets(t *testing.T) {
	p := NewParser()
	src := `# Just variables
FOO = bar
BAZ := qux
OPT ?= default
`
	result, err := p.ParseFile("vars.mk", []byte(src))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	counts := make(map[graph.NodeType]int)
	for _, n := range result.Nodes {
		counts[n.Type]++
	}

	assertCount(t, counts, graph.NodeFile, 1)
	assertCount(t, counts, graph.NodeFunction, 0)
	assertCount(t, counts, graph.NodeVariable, 3)

	nodeByName := indexByName(result.Nodes)
	if n, ok := nodeByName["OPT"]; ok {
		if n.Properties["assignment_op"] != "?=" {
			t.Errorf("OPT assignment_op = %q, want %q", n.Properties["assignment_op"], "?=")
		}
	} else {
		t.Error("expected OPT variable node")
	}
}

func TestLanguageAndExtensions(t *testing.T) {
	p := NewParser()
	if p.Language() != parser.LangMakefile {
		t.Errorf("Language() = %q, want %q", p.Language(), parser.LangMakefile)
	}
	exts := p.Extensions()
	if len(exts) != 1 || exts[0] != ".mk" {
		t.Errorf("Extensions() = %v, want [\".mk\"]", exts)
	}
}

func TestFilenames(t *testing.T) {
	p := NewParser()
	names := p.Filenames()
	expected := map[string]bool{"Makefile": true, "makefile": true, "GNUmakefile": true}
	if len(names) != len(expected) {
		t.Errorf("Filenames() has %d entries, want %d", len(names), len(expected))
	}
	for _, n := range names {
		if !expected[n] {
			t.Errorf("unexpected filename %q", n)
		}
	}
}

func TestParseMakefileFixture(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	fixturePath := filepath.Join(projectRoot, "Makefile")

	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Skipf("project Makefile not found: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile("Makefile", content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	nodeByName := indexByName(result.Nodes)

	// The project Makefile has these targets.
	for _, name := range []string{"build", "test", "clean", "lint", "fmt"} {
		if _, ok := nodeByName[name]; !ok {
			t.Errorf("expected target %q in project Makefile", name)
		}
	}

	// Should have BINARY_NAME variable.
	if _, ok := nodeByName["BINARY_NAME"]; !ok {
		t.Error("expected BINARY_NAME variable in project Makefile")
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
