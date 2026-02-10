package yaml

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

const testGHASource = `name: CI
on:
  push:
    branches: [main]
  pull_request:
    branches: [main]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Run tests
        run: make test
  build:
    needs: test
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Build
        run: make build
`

const testAnsibleSource = `---
- name: Deploy app
  hosts: webservers
  vars:
    app_port: 8080
    app_user: deploy
  roles:
    - common
    - role: nginx
  tasks:
    - name: Install deps
      apt:
        name: python3
        state: present
    - name: Copy files
      copy:
        src: /local/
        dest: /remote/
  handlers:
    - name: Restart nginx
      service:
        name: nginx
        state: restarted
`

const testGenericYAML = `project:
  name: myapp
version: "1.0"
database:
  host: localhost
  port: 5432
logging:
  level: info
`

func TestParseGitHubActionsWorkflow(t *testing.T) {
	p := NewParser()

	result, err := p.ParseFile(".github/workflows/ci.yml", []byte(testGHASource))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	if result.Language != parser.LangYAML {
		t.Errorf("Language = %q, want %q", result.Language, parser.LangYAML)
	}

	counts := make(map[graph.NodeType]int)
	for _, n := range result.Nodes {
		counts[n.Type]++
	}

	// 1 file
	assertCount(t, counts, graph.NodeFile, 1)
	// 1 workflow document
	assertCount(t, counts, graph.NodeDocument, 1)
	// 2 triggers: push, pull_request
	// Count gha_trigger variables specifically.
	triggerCount := 0
	for _, n := range result.Nodes {
		if n.Properties != nil && n.Properties["kind"] == "gha_trigger" {
			triggerCount++
		}
	}
	if triggerCount != 2 {
		t.Errorf("gha_trigger count = %d, want 2", triggerCount)
	}

	// 2 jobs: test, build
	jobCount := 0
	for _, n := range result.Nodes {
		if n.Properties != nil && n.Properties["kind"] == "gha_job" {
			jobCount++
		}
	}
	if jobCount != 2 {
		t.Errorf("gha_job count = %d, want 2", jobCount)
	}

	nodeByName := indexByName(result.Nodes)

	// Check workflow document.
	if n, ok := nodeByName["CI"]; ok {
		if n.Properties["kind"] != "workflow" {
			t.Errorf("CI kind = %q, want %q", n.Properties["kind"], "workflow")
		}
	} else {
		t.Error("expected CI workflow document node")
	}

	// Check test job.
	if n, ok := nodeByName["test"]; ok {
		if n.Properties["runs_on"] != "ubuntu-latest" {
			t.Errorf("test runs_on = %q, want %q", n.Properties["runs_on"], "ubuntu-latest")
		}
	} else {
		t.Error("expected test job node")
	}

	// Check actions (gha_action dependencies).
	actionCount := 0
	for _, n := range result.Nodes {
		if n.Properties != nil && n.Properties["kind"] == "gha_action" {
			actionCount++
		}
	}
	// 2 checkout actions (one per job)
	if actionCount < 2 {
		t.Errorf("gha_action count = %d, want at least 2", actionCount)
	}

	// Check steps.
	stepCount := 0
	for _, n := range result.Nodes {
		if n.Properties != nil && n.Properties["kind"] == "gha_step" {
			stepCount++
		}
	}
	// "Run tests" and "Build" are run steps
	if stepCount < 2 {
		t.Errorf("gha_step count = %d, want at least 2", stepCount)
	}

	// Check DependsOn edges (jobs -> actions).
	depEdges := 0
	for _, edge := range result.Edges {
		if edge.Type == graph.EdgeDependsOn {
			depEdges++
		}
	}
	if depEdges < 2 {
		t.Errorf("DependsOn edges = %d, want at least 2", depEdges)
	}

	// Verify file dialect property.
	fileNode := nodeByName[".github/workflows/ci.yml"]
	if fileNode == nil {
		t.Fatal("expected file node")
	}
	if fileNode.Properties["yaml_dialect"] != DialectGitHubActions {
		t.Errorf("yaml_dialect = %q, want %q", fileNode.Properties["yaml_dialect"], DialectGitHubActions)
	}
}

func TestParseAnsiblePlaybook(t *testing.T) {
	p := NewParser()

	result, err := p.ParseFile("playbooks/deploy.yml", []byte(testAnsibleSource))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	counts := make(map[graph.NodeType]int)
	for _, n := range result.Nodes {
		counts[n.Type]++
	}

	// 1 file
	assertCount(t, counts, graph.NodeFile, 1)

	nodeByName := indexByName(result.Nodes)

	// Check play.
	if n, ok := nodeByName["Deploy app"]; ok {
		if n.Properties["kind"] != "ansible_play" {
			t.Errorf("Deploy app kind = %q, want %q", n.Properties["kind"], "ansible_play")
		}
		if n.Properties["hosts"] != "webservers" {
			t.Errorf("Deploy app hosts = %q, want %q", n.Properties["hosts"], "webservers")
		}
	} else {
		t.Error("expected 'Deploy app' play node")
	}

	// Check tasks.
	taskCount := 0
	for _, n := range result.Nodes {
		if n.Properties != nil && n.Properties["kind"] == "ansible_task" {
			taskCount++
		}
	}
	// 2 tasks in deploy play.
	if taskCount < 2 {
		t.Errorf("ansible_task count = %d, want at least 2", taskCount)
	}

	// Check handlers.
	handlerCount := 0
	for _, n := range result.Nodes {
		if n.Properties != nil && n.Properties["kind"] == "ansible_handler" {
			handlerCount++
		}
	}
	if handlerCount != 1 {
		t.Errorf("ansible_handler count = %d, want 1", handlerCount)
	}

	// Check roles.
	roleCount := 0
	for _, n := range result.Nodes {
		if n.Properties != nil && n.Properties["kind"] == "ansible_role" {
			roleCount++
		}
	}
	// 2 roles: common, nginx.
	if roleCount != 2 {
		t.Errorf("ansible_role count = %d, want 2", roleCount)
	}

	// Check vars.
	varCount := 0
	for _, n := range result.Nodes {
		if n.Properties != nil && n.Properties["kind"] == "ansible_var" {
			varCount++
		}
	}
	// 2 vars: app_port, app_user.
	if varCount != 2 {
		t.Errorf("ansible_var count = %d, want 2", varCount)
	}

	// Check DependsOn edges (play -> roles).
	depEdges := 0
	for _, edge := range result.Edges {
		if edge.Type == graph.EdgeDependsOn {
			depEdges++
		}
	}
	if depEdges < 2 {
		t.Errorf("DependsOn edges = %d, want at least 2", depEdges)
	}

	// Verify file dialect.
	fileNode := nodeByName["playbooks/deploy.yml"]
	if fileNode == nil {
		t.Fatal("expected file node")
	}
	if fileNode.Properties["yaml_dialect"] != DialectAnsible {
		t.Errorf("yaml_dialect = %q, want %q", fileNode.Properties["yaml_dialect"], DialectAnsible)
	}
}

func TestParseGenericYAML(t *testing.T) {
	p := NewParser()

	result, err := p.ParseFile("config/app.yaml", []byte(testGenericYAML))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	counts := make(map[graph.NodeType]int)
	for _, n := range result.Nodes {
		counts[n.Type]++
	}

	// 1 file + 4 top-level keys (project, version, database, logging).
	assertCount(t, counts, graph.NodeFile, 1)
	assertCount(t, counts, graph.NodeVariable, 4)

	nodeByName := indexByName(result.Nodes)

	for _, key := range []string{"project", "version", "database", "logging"} {
		if n, ok := nodeByName[key]; ok {
			if n.Properties["kind"] != "yaml_key" {
				t.Errorf("%s kind = %q, want %q", key, n.Properties["kind"], "yaml_key")
			}
		} else {
			t.Errorf("expected %q yaml_key node", key)
		}
	}

	// Verify file dialect.
	fileNode := nodeByName["config/app.yaml"]
	if fileNode == nil {
		t.Fatal("expected file node")
	}
	if fileNode.Properties["yaml_dialect"] != DialectGeneric {
		t.Errorf("yaml_dialect = %q, want %q", fileNode.Properties["yaml_dialect"], DialectGeneric)
	}
}

func TestDialectDetection(t *testing.T) {
	p := NewParser()

	tests := []struct {
		name     string
		filePath string
		content  string
		want     string
	}{
		{
			name:     "GHA by path",
			filePath: ".github/workflows/ci.yml",
			content:  "name: test\non: push\njobs: {}",
			want:     DialectGitHubActions,
		},
		{
			name:     "GHA by content",
			filePath: "workflow.yml",
			content:  "name: test\non:\n  push:\njobs:\n  test:\n    runs-on: ubuntu-latest",
			want:     DialectGitHubActions,
		},
		{
			name:     "Ansible by hosts",
			filePath: "playbook.yml",
			content:  "---\n- name: test\n  hosts: all\n  tasks: []",
			want:     DialectAnsible,
		},
		{
			name:     "Ansible tasks by module",
			filePath: "tasks/main.yml",
			content:  "---\n- name: Install\n  apt:\n    name: vim",
			want:     DialectAnsible,
		},
		{
			name:     "Generic",
			filePath: "config.yml",
			content:  "key: value\nother: 42",
			want:     DialectGeneric,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := p.ParseFile(tt.filePath, []byte(tt.content))
			if err != nil {
				t.Fatalf("ParseFile returned error: %v", err)
			}

			// Find the file node and check dialect.
			for _, n := range result.Nodes {
				if n.Type == graph.NodeFile {
					if n.Properties["yaml_dialect"] != tt.want {
						t.Errorf("dialect = %q, want %q", n.Properties["yaml_dialect"], tt.want)
					}
					return
				}
			}
			t.Error("no file node found")
		})
	}
}

func TestLanguageAndExtensions(t *testing.T) {
	p := NewParser()
	if p.Language() != parser.LangYAML {
		t.Errorf("Language() = %q, want %q", p.Language(), parser.LangYAML)
	}
	exts := p.Extensions()
	if len(exts) != 2 || exts[0] != ".yml" {
		t.Errorf("Extensions() = %v, want [\".yml\", \".yaml\"]", exts)
	}
}

func TestParseGHAFixture(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	fixturePath := filepath.Join(projectRoot, "testdata", "sample_gha.yml")

	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Skipf("testdata/sample_gha.yml not found: %v", err)
	}

	p := NewParser()
	// Use GHA path to trigger path-based detection.
	result, err := p.ParseFile(".github/workflows/sample_gha.yml", content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	nodeByName := indexByName(result.Nodes)

	// Check workflow.
	if _, ok := nodeByName["CI Pipeline"]; !ok {
		t.Error("expected 'CI Pipeline' workflow document")
	}

	// Check jobs.
	for _, name := range []string{"test", "build"} {
		if _, ok := nodeByName[name]; !ok {
			t.Errorf("expected job %q", name)
		}
	}

	// Check actions.
	if _, ok := nodeByName["actions/checkout@v4"]; !ok {
		t.Error("expected actions/checkout@v4 dependency")
	}
}

func TestParseAnsibleFixture(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	fixturePath := filepath.Join(projectRoot, "testdata", "sample_ansible.yml")

	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Skipf("testdata/sample_ansible.yml not found: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile("playbooks/sample_ansible.yml", content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	nodeByName := indexByName(result.Nodes)

	// Check plays.
	if _, ok := nodeByName["Deploy web application"]; !ok {
		t.Error("expected 'Deploy web application' play")
	}
	if _, ok := nodeByName["Configure database"]; !ok {
		t.Error("expected 'Configure database' play")
	}

	// Check roles.
	for _, name := range []string{"common", "nginx"} {
		if _, ok := nodeByName[name]; !ok {
			t.Errorf("expected role %q", name)
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
