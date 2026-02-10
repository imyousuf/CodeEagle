package shell

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

const testShellSource = `#!/bin/bash
# Sample script

export MY_VAR="hello"
ANOTHER_VAR=42
readonly CONFIG_PATH="/etc/app"

function greet() {
    echo "Hello, $1"
}

cleanup() {
    rm -rf /tmp/work
}

source ./helpers.sh
. ./utils.sh

declare -x EXPORTED_VAR="yes"
`

func TestParseShellFile(t *testing.T) {
	p := NewParser()

	result, err := p.ParseFile("scripts/test.sh", []byte(testShellSource))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	if result.Language != parser.LangShell {
		t.Errorf("Language = %q, want %q", result.Language, parser.LangShell)
	}

	counts := make(map[graph.NodeType]int)
	for _, n := range result.Nodes {
		counts[n.Type]++
	}

	// 1 file
	assertCount(t, counts, graph.NodeFile, 1)
	// 2 functions: greet, cleanup
	assertCount(t, counts, graph.NodeFunction, 2)
	// 5 variables: MY_VAR, ANOTHER_VAR, CONFIG_PATH, EXPORTED_VAR
	assertCount(t, counts, graph.NodeVariable, 4)
	// 2 source imports: helpers.sh, utils.sh
	assertCount(t, counts, graph.NodeDependency, 2)

	nodeByName := indexByName(result.Nodes)

	// Check functions.
	if n, ok := nodeByName["greet"]; ok {
		if n.Properties["kind"] != "function" {
			t.Errorf("greet kind = %q, want %q", n.Properties["kind"], "function")
		}
		if n.Line == 0 {
			t.Error("greet should have non-zero line number")
		}
	} else {
		t.Error("expected greet function node")
	}

	if _, ok := nodeByName["cleanup"]; !ok {
		t.Error("expected cleanup function node")
	}

	// Check exported variable.
	if n, ok := nodeByName["MY_VAR"]; ok {
		if n.Properties["exported"] != "true" {
			t.Error("MY_VAR should be exported")
		}
		if !n.Exported {
			t.Error("MY_VAR node.Exported should be true")
		}
	} else {
		t.Error("expected MY_VAR variable node")
	}

	// Check non-exported variable.
	if n, ok := nodeByName["ANOTHER_VAR"]; ok {
		if n.Properties["exported"] == "true" {
			t.Error("ANOTHER_VAR should not be exported")
		}
	} else {
		t.Error("expected ANOTHER_VAR variable node")
	}

	// Check declare -x variable.
	if n, ok := nodeByName["EXPORTED_VAR"]; ok {
		if n.Properties["exported"] != "true" {
			t.Error("EXPORTED_VAR should be exported via declare -x")
		}
	} else {
		t.Error("expected EXPORTED_VAR variable node")
	}

	// Check source imports.
	if n, ok := nodeByName["./helpers.sh"]; ok {
		if n.Properties["kind"] != "source" {
			t.Errorf("helpers.sh kind = %q, want %q", n.Properties["kind"], "source")
		}
	} else {
		t.Error("expected ./helpers.sh import node")
	}
	if _, ok := nodeByName["./utils.sh"]; !ok {
		t.Error("expected ./utils.sh import node")
	}

	// Verify edge counts.
	edgeCounts := make(map[graph.EdgeType]int)
	for _, edge := range result.Edges {
		edgeCounts[edge.Type]++
	}

	// Contains: 2 functions + 4 variables = 6
	if edgeCounts[graph.EdgeContains] < 6 {
		t.Errorf("Contains edges = %d, want at least 6", edgeCounts[graph.EdgeContains])
	}
	// Imports: 2 source commands
	if edgeCounts[graph.EdgeImports] != 2 {
		t.Errorf("Imports edges = %d, want 2", edgeCounts[graph.EdgeImports])
	}
}

func TestParseShebang(t *testing.T) {
	p := NewParser()

	result, err := p.ParseFile("test.sh", []byte("#!/usr/bin/env bash\necho hello\n"))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	nodeByName := indexByName(result.Nodes)
	if n, ok := nodeByName["test.sh"]; ok {
		if n.Properties["shebang"] != "#!/usr/bin/env bash" {
			t.Errorf("shebang = %q, want %q", n.Properties["shebang"], "#!/usr/bin/env bash")
		}
	} else {
		t.Error("expected file node")
	}
}

func TestParseNoShebang(t *testing.T) {
	p := NewParser()

	result, err := p.ParseFile("test.sh", []byte("# Just a comment\necho hello\n"))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	nodeByName := indexByName(result.Nodes)
	if n, ok := nodeByName["test.sh"]; ok {
		if n.Properties != nil && n.Properties["shebang"] != "" {
			t.Errorf("unexpected shebang: %q", n.Properties["shebang"])
		}
	}
}

func TestLanguageAndExtensions(t *testing.T) {
	p := NewParser()
	if p.Language() != parser.LangShell {
		t.Errorf("Language() = %q, want %q", p.Language(), parser.LangShell)
	}
	exts := p.Extensions()
	if len(exts) != 2 || exts[0] != ".sh" {
		t.Errorf("Extensions() = %v, want [\".sh\", \".bash\"]", exts)
	}
}

func TestParseShellFixture(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	samplePath := filepath.Join(projectRoot, "testdata", "sample.sh")

	content, err := os.ReadFile(samplePath)
	if err != nil {
		t.Skipf("testdata/sample.sh not found: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile("testdata/sample.sh", content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	nodeByName := indexByName(result.Nodes)

	// Check functions.
	for _, name := range []string{"greet", "cleanup", "start_server"} {
		if _, ok := nodeByName[name]; !ok {
			t.Errorf("expected function %q in fixture", name)
		}
	}

	// Check variables.
	for _, name := range []string{"APP_NAME", "APP_VERSION", "BASE_DIR"} {
		if _, ok := nodeByName[name]; !ok {
			t.Errorf("expected variable %q in fixture", name)
		}
	}

	// Check shebang.
	if n, ok := nodeByName["testdata/sample.sh"]; ok {
		if n.Properties == nil || n.Properties["shebang"] != "#!/bin/bash" {
			t.Errorf("fixture shebang = %q, want %q", n.Properties["shebang"], "#!/bin/bash")
		}
	}

	// Check source imports.
	if _, ok := nodeByName["./lib/helpers.sh"]; !ok {
		t.Error("expected ./lib/helpers.sh import node in fixture")
	}
	if _, ok := nodeByName["./lib/utils.sh"]; !ok {
		t.Error("expected ./lib/utils.sh import node in fixture")
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
