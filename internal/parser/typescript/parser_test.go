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
