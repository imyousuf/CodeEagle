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
