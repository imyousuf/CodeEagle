package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

func newTestGraphStore(t *testing.T) graph.Store {
	t.Helper()
	store, err := embedded.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func addTestNodes(t *testing.T, store graph.Store, nodes ...*graph.Node) {
	t.Helper()
	ctx := context.Background()
	for _, n := range nodes {
		if err := store.AddNode(ctx, n); err != nil {
			t.Fatalf("add node %s: %v", n.Name, err)
		}
	}
}

func addTestEdges(t *testing.T, store graph.Store, edges ...*graph.Edge) {
	t.Helper()
	ctx := context.Background()
	for _, e := range edges {
		if err := store.AddEdge(ctx, e); err != nil {
			t.Fatalf("add edge %s: %v", e.ID, err)
		}
	}
}

// --- isTestFileByPath tests ---

func TestIsTestFileByPath(t *testing.T) {
	tests := []struct {
		path     string
		language string
		want     bool
	}{
		{"handler_test.go", "go", true},
		{"handler.go", "go", false},
		{"test_handler.py", "python", true},
		{"handler_test.py", "python", true},
		{"handler.py", "python", false},
		{"handler.test.ts", "typescript", true},
		{"handler.spec.ts", "typescript", true},
		{"handler.ts", "typescript", false},
		{"handler.test.js", "javascript", true},
		{"handler.spec.js", "javascript", true},
		{"handler.js", "javascript", false},
		{"HandlerTest.java", "java", true},
		{"HandlerTests.java", "java", true},
		{"TestHandler.java", "java", true},
		{"HandlerIT.java", "java", true},
		{"Handler.java", "java", false},
		{"unknown.rb", "ruby", false},
		{"handler.test.tsx", "typescript", true},
		{"handler.spec.jsx", "javascript", true},
	}
	for _, tt := range tests {
		got := isTestFileByPath(tt.path, tt.language)
		if got != tt.want {
			t.Errorf("isTestFileByPath(%q, %q) = %v, want %v", tt.path, tt.language, got, tt.want)
		}
	}
}

func TestIsTestFuncByName(t *testing.T) {
	tests := []struct {
		name     string
		language string
		filePath string
		want     bool
	}{
		{"TestFoo", "go", "foo_test.go", true},
		{"BenchmarkFoo", "go", "foo_test.go", true},
		{"ExampleFoo", "go", "foo_test.go", true},
		{"FuzzFoo", "go", "foo_test.go", true},
		{"foo", "go", "foo.go", false},
		{"test_process", "python", "test_handler.py", true},
		{"test_process", "python", "handler.py", false},
		{"describe", "typescript", "handler.test.ts", true},
		{"describe", "typescript", "handler.ts", false},
		{"testProcess", "java", "HandlerTest.java", true},
		{"process", "java", "Handler.java", false},
	}
	for _, tt := range tests {
		got := isTestFuncByName(tt.name, tt.language, tt.filePath)
		if got != tt.want {
			t.Errorf("isTestFuncByName(%q, %q, %q) = %v, want %v", tt.name, tt.language, tt.filePath, got, tt.want)
		}
	}
}

func TestInferLangFromPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"foo.go", "go"},
		{"bar.py", "python"},
		{"baz.ts", "typescript"},
		{"baz.tsx", "typescript"},
		{"qux.js", "javascript"},
		{"qux.jsx", "javascript"},
		{"Foo.java", "java"},
		{"unknown.rb", ""},
	}
	for _, tt := range tests {
		got := inferLangFromPath(tt.path)
		if got != tt.want {
			t.Errorf("inferLangFromPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// --- shouldSkipForUnused tests ---

func TestShouldSkipForUnused(t *testing.T) {
	tests := []struct {
		name            string
		node            *graph.Node
		includeExported bool
		want            bool
	}{
		{
			name:            "test function type",
			node:            &graph.Node{Name: "TestFoo", Type: graph.NodeTestFunction, Language: "go"},
			includeExported: false,
			want:            true,
		},
		{
			name:            "init function",
			node:            &graph.Node{Name: "init", Type: graph.NodeFunction, Language: "go"},
			includeExported: false,
			want:            true,
		},
		{
			name:            "main function",
			node:            &graph.Node{Name: "main", Type: graph.NodeFunction, Language: "go"},
			includeExported: false,
			want:            true,
		},
		{
			name:            "exported function excluded by default",
			node:            &graph.Node{Name: "HandleRequest", Type: graph.NodeFunction, Exported: true, Language: "go"},
			includeExported: false,
			want:            true,
		},
		{
			name:            "exported function included when flag set",
			node:            &graph.Node{Name: "HandleRequest", Type: graph.NodeFunction, Exported: true, Language: "go"},
			includeExported: true,
			want:            false,
		},
		{
			name:            "unexported function not skipped",
			node:            &graph.Node{Name: "processData", Type: graph.NodeFunction, Language: "go", FilePath: "handler.go"},
			includeExported: false,
			want:            false,
		},
		{
			name:            "go test func by name",
			node:            &graph.Node{Name: "TestBar", Type: graph.NodeFunction, Language: "go", FilePath: "bar_test.go"},
			includeExported: false,
			want:            true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldSkipForUnused(tt.node, tt.includeExported)
			if got != tt.want {
				t.Errorf("shouldSkipForUnused(%s) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

// --- computePackageCoverage tests ---

func TestComputePackageCoverage(t *testing.T) {
	entries := []coverageEntry{
		{Package: "pkg/a", Covered: true, Language: "go"},
		{Package: "pkg/a", Covered: false, Language: "go"},
		{Package: "pkg/a", Covered: true, Language: "go"},
		{Package: "pkg/b", Covered: false, Language: "go"},
		{Package: "", Covered: true, Language: "python"},
	}

	result := computePackageCoverage(entries)

	if len(result) != 3 {
		t.Fatalf("expected 3 packages, got %d", len(result))
	}

	// Results are sorted by package name.
	// (unknown), pkg/a, pkg/b
	if result[0].Package != "(unknown)" || result[0].Total != 1 || result[0].Covered != 1 {
		t.Errorf("(unknown) package: got total=%d covered=%d, want total=1 covered=1", result[0].Total, result[0].Covered)
	}
	if result[1].Package != "pkg/a" || result[1].Total != 3 || result[1].Covered != 2 {
		t.Errorf("pkg/a: got total=%d covered=%d, want total=3 covered=2", result[1].Total, result[1].Covered)
	}
	if result[1].Percent < 66.6 || result[1].Percent > 66.7 {
		t.Errorf("pkg/a percent: got %.1f, want ~66.7", result[1].Percent)
	}
	if result[2].Package != "pkg/b" || result[2].Total != 1 || result[2].Covered != 0 {
		t.Errorf("pkg/b: got total=%d covered=%d, want total=1 covered=0", result[2].Total, result[2].Covered)
	}
}

// --- Integration tests using a real embedded store ---

func TestUnusedCmd_NoUnused(t *testing.T) {
	store := newTestGraphStore(t)
	ctx := context.Background()

	// Create two functions: callerFn calls calledFn.
	calledFn := &graph.Node{
		ID:       graph.NewNodeID("Function", "handler.go", "processData"),
		Type:     graph.NodeFunction,
		Name:     "processData",
		FilePath: "handler.go",
		Line:     10,
		Package:  "pkg/handler",
		Language: "go",
	}
	callerFn := &graph.Node{
		ID:       graph.NewNodeID("Function", "main.go", "runApp"),
		Type:     graph.NodeFunction,
		Name:     "runApp",
		FilePath: "main.go",
		Line:     5,
		Package:  "pkg/handler",
		Language: "go",
	}
	addTestNodes(t, store, calledFn, callerFn)

	// Add a Calls edge: callerFn -> calledFn.
	edge := &graph.Edge{
		ID:       graph.NewNodeID("Calls", callerFn.ID, calledFn.ID),
		Type:     graph.EdgeCalls,
		SourceID: callerFn.ID,
		TargetID: calledFn.ID,
	}
	addTestEdges(t, store, edge)

	// Both are unexported but calledFn has an incoming call, so only callerFn is "unused".
	// Verify using direct edge check.
	edges, err := store.GetEdges(ctx, calledFn.ID, graph.EdgeCalls)
	if err != nil {
		t.Fatalf("GetEdges: %v", err)
	}
	hasIncoming := false
	for _, e := range edges {
		if e.TargetID == calledFn.ID {
			hasIncoming = true
		}
	}
	if !hasIncoming {
		t.Fatal("expected calledFn to have incoming Calls edge")
	}
}

func TestUnusedCmd_FindsUnused(t *testing.T) {
	store := newTestGraphStore(t)

	// Create an unexported function with no callers.
	orphanFn := &graph.Node{
		ID:       graph.NewNodeID("Function", "util.go", "helperFunc"),
		Type:     graph.NodeFunction,
		Name:     "helperFunc",
		FilePath: "util.go",
		Line:     20,
		Package:  "pkg/util",
		Language: "go",
	}
	addTestNodes(t, store, orphanFn)

	// Verify no incoming calls.
	ctx := context.Background()
	edges, err := store.GetEdges(ctx, orphanFn.ID, graph.EdgeCalls)
	if err != nil {
		t.Fatalf("GetEdges: %v", err)
	}
	for _, e := range edges {
		if e.TargetID == orphanFn.ID {
			t.Fatal("expected no incoming Calls edge")
		}
	}
}

func TestUnusedCmd_SkipsExported(t *testing.T) {
	node := &graph.Node{
		Name:     "HandleRequest",
		Type:     graph.NodeFunction,
		Exported: true,
		Language: "go",
		FilePath: "handler.go",
	}
	if !shouldSkipForUnused(node, false) {
		t.Error("expected exported function to be skipped")
	}
	if shouldSkipForUnused(node, true) {
		t.Error("expected exported function to NOT be skipped with --include-exported")
	}
}

func TestCoverage_FileLevelJSON(t *testing.T) {
	store := newTestGraphStore(t)

	// Create source files.
	fileA := &graph.Node{
		ID:       graph.NewNodeID("File", "handler.go", "handler.go"),
		Type:     graph.NodeFile,
		Name:     "handler.go",
		FilePath: "handler.go",
		Package:  "pkg/handler",
		Language: "go",
	}
	fileB := &graph.Node{
		ID:       graph.NewNodeID("File", "util.go", "util.go"),
		Type:     graph.NodeFile,
		Name:     "util.go",
		FilePath: "util.go",
		Package:  "pkg/util",
		Language: "go",
	}
	// Create a test file that tests handler.go.
	testFile := &graph.Node{
		ID:       graph.NewNodeID("TestFile", "handler_test.go", "handler_test.go"),
		Type:     graph.NodeTestFile,
		Name:     "handler_test.go",
		FilePath: "handler_test.go",
		Package:  "pkg/handler",
		Language: "go",
	}
	addTestNodes(t, store, fileA, fileB, testFile)

	// Add EdgeTests: testFile -> fileA.
	edge := &graph.Edge{
		ID:       graph.NewNodeID("Tests", testFile.ID, fileA.ID),
		Type:     graph.EdgeTests,
		SourceID: testFile.ID,
		TargetID: fileA.ID,
		Properties: map[string]string{
			"kind": "file_coverage",
		},
	}
	addTestEdges(t, store, edge)

	// Run file coverage check directly.
	ctx := context.Background()
	cmd := &cobra.Command{}
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)

	err := runFileCoverage(ctx, cmd, store, "", "", true)
	if err != nil {
		t.Fatalf("runFileCoverage: %v", err)
	}

	var result struct {
		Files    []coverageEntry   `json:"files"`
		Packages []packageCoverage `json:"packages"`
	}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(result.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(result.Files))
	}

	// handler.go should be covered, util.go should not.
	coveredMap := make(map[string]bool)
	for _, f := range result.Files {
		coveredMap[f.FilePath] = f.Covered
	}

	if !coveredMap["handler.go"] {
		t.Error("expected handler.go to be covered")
	}
	if coveredMap["util.go"] {
		t.Error("expected util.go to NOT be covered")
	}

	// Check package stats.
	if len(result.Packages) != 2 {
		t.Fatalf("expected 2 packages, got %d", len(result.Packages))
	}
}

func TestCoverage_FunctionLevelJSON(t *testing.T) {
	store := newTestGraphStore(t)

	// Create source functions.
	funcA := &graph.Node{
		ID:       graph.NewNodeID("Function", "handler.go", "handleRequest"),
		Type:     graph.NodeFunction,
		Name:     "handleRequest",
		FilePath: "handler.go",
		Line:     10,
		Package:  "pkg/handler",
		Language: "go",
	}
	funcB := &graph.Node{
		ID:       graph.NewNodeID("Function", "handler.go", "processData"),
		Type:     graph.NodeFunction,
		Name:     "processData",
		FilePath: "handler.go",
		Line:     30,
		Package:  "pkg/handler",
		Language: "go",
	}
	// Create a test function that tests funcA.
	testFunc := &graph.Node{
		ID:       graph.NewNodeID("TestFunction", "handler_test.go", "TestHandleRequest"),
		Type:     graph.NodeTestFunction,
		Name:     "TestHandleRequest",
		FilePath: "handler_test.go",
		Line:     5,
		Package:  "pkg/handler",
		Language: "go",
	}
	addTestNodes(t, store, funcA, funcB, testFunc)

	// Add EdgeTests: testFunc -> funcA.
	edge := &graph.Edge{
		ID:       graph.NewNodeID("Tests", testFunc.ID, funcA.ID),
		Type:     graph.EdgeTests,
		SourceID: testFunc.ID,
		TargetID: funcA.ID,
		Properties: map[string]string{
			"kind": "function_coverage",
		},
	}
	addTestEdges(t, store, edge)

	ctx := context.Background()
	cmd := &cobra.Command{}
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)

	err := runFunctionCoverage(ctx, cmd, store, "", "", true)
	if err != nil {
		t.Fatalf("runFunctionCoverage: %v", err)
	}

	var result struct {
		Functions []coverageEntry   `json:"functions"`
		Packages  []packageCoverage `json:"packages"`
	}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(result.Functions) != 2 {
		t.Fatalf("expected 2 functions, got %d", len(result.Functions))
	}

	coveredMap := make(map[string]bool)
	for _, f := range result.Functions {
		coveredMap[f.Name] = f.Covered
	}

	if !coveredMap["handleRequest"] {
		t.Error("expected handleRequest to be covered")
	}
	if coveredMap["processData"] {
		t.Error("expected processData to NOT be covered")
	}

	// Package stats: 1 package, 2 total, 1 covered.
	if len(result.Packages) != 1 {
		t.Fatalf("expected 1 package, got %d", len(result.Packages))
	}
	if result.Packages[0].Total != 2 || result.Packages[0].Covered != 1 {
		t.Errorf("expected total=2 covered=1, got total=%d covered=%d",
			result.Packages[0].Total, result.Packages[0].Covered)
	}
}

func TestCoverage_ExcludesTestFiles(t *testing.T) {
	store := newTestGraphStore(t)

	// Source file + test file (detected by name).
	srcFile := &graph.Node{
		ID:       graph.NewNodeID("File", "handler.go", "handler.go"),
		Type:     graph.NodeFile,
		Name:     "handler.go",
		FilePath: "handler.go",
		Package:  "pkg/handler",
		Language: "go",
	}
	// This is a File node (not TestFile) but with test file naming.
	testFileNode := &graph.Node{
		ID:       graph.NewNodeID("File", "handler_test.go", "handler_test.go"),
		Type:     graph.NodeFile,
		Name:     "handler_test.go",
		FilePath: "handler_test.go",
		Package:  "pkg/handler",
		Language: "go",
	}
	addTestNodes(t, store, srcFile, testFileNode)

	ctx := context.Background()
	cmd := &cobra.Command{}
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)

	err := runFileCoverage(ctx, cmd, store, "", "", true)
	if err != nil {
		t.Fatalf("runFileCoverage: %v", err)
	}

	var result struct {
		Files    []coverageEntry   `json:"files"`
		Packages []packageCoverage `json:"packages"`
	}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Only handler.go should appear, not handler_test.go.
	if len(result.Files) != 1 {
		t.Fatalf("expected 1 file (test file excluded), got %d", len(result.Files))
	}
	if result.Files[0].FilePath != "handler.go" {
		t.Errorf("expected handler.go, got %s", result.Files[0].FilePath)
	}
}

func TestCoverage_TextOutput(t *testing.T) {
	store := newTestGraphStore(t)

	fileA := &graph.Node{
		ID:       graph.NewNodeID("File", "handler.go", "handler.go"),
		Type:     graph.NodeFile,
		Name:     "handler.go",
		FilePath: "handler.go",
		Package:  "pkg/handler",
		Language: "go",
	}
	addTestNodes(t, store, fileA)

	ctx := context.Background()
	cmd := &cobra.Command{}
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)

	err := runFileCoverage(ctx, cmd, store, "", "", false)
	if err != nil {
		t.Fatalf("runFileCoverage: %v", err)
	}

	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("Uncovered files")) {
		t.Errorf("expected 'Uncovered files' in output, got: %s", output)
	}
	if !bytes.Contains([]byte(output), []byte("handler.go")) {
		t.Errorf("expected 'handler.go' in output, got: %s", output)
	}
	if !bytes.Contains([]byte(output), []byte("Coverage by package")) {
		t.Errorf("expected 'Coverage by package' in output, got: %s", output)
	}
}

func TestCoverage_AllCoveredText(t *testing.T) {
	store := newTestGraphStore(t)

	fileA := &graph.Node{
		ID:       graph.NewNodeID("File", "handler.go", "handler.go"),
		Type:     graph.NodeFile,
		Name:     "handler.go",
		FilePath: "handler.go",
		Package:  "pkg/handler",
		Language: "go",
	}
	testFile := &graph.Node{
		ID:       graph.NewNodeID("TestFile", "handler_test.go", "handler_test.go"),
		Type:     graph.NodeTestFile,
		Name:     "handler_test.go",
		FilePath: "handler_test.go",
		Package:  "pkg/handler",
		Language: "go",
	}
	addTestNodes(t, store, fileA, testFile)

	edge := &graph.Edge{
		ID:       graph.NewNodeID("Tests", testFile.ID, fileA.ID),
		Type:     graph.EdgeTests,
		SourceID: testFile.ID,
		TargetID: fileA.ID,
	}
	addTestEdges(t, store, edge)

	ctx := context.Background()
	cmd := &cobra.Command{}
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)

	err := runFileCoverage(ctx, cmd, store, "", "", false)
	if err != nil {
		t.Fatalf("runFileCoverage: %v", err)
	}

	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("All source files have test coverage")) {
		t.Errorf("expected 'All source files have test coverage' in output, got: %s", output)
	}
}
