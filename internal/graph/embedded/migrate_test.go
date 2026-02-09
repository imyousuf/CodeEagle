package embedded

import (
	"context"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

func TestMigrateAbsToRelPaths(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	repoRoot := "/home/user/project"

	// Add nodes with absolute paths.
	file1 := &graph.Node{
		ID:       graph.NewNodeID("File", repoRoot+"/src/main.go", repoRoot+"/src/main.go"),
		Type:     graph.NodeFile,
		Name:     repoRoot + "/src/main.go",
		FilePath: repoRoot + "/src/main.go",
		Language: "go",
	}
	fn1 := &graph.Node{
		ID:       graph.NewNodeID("Function", repoRoot+"/src/main.go", "main"),
		Type:     graph.NodeFunction,
		Name:     "main",
		FilePath: repoRoot + "/src/main.go",
		Language: "go",
		Package:  "main",
	}
	file2 := &graph.Node{
		ID:       graph.NewNodeID("File", repoRoot+"/src/util.go", repoRoot+"/src/util.go"),
		Type:     graph.NodeFile,
		Name:     repoRoot + "/src/util.go",
		FilePath: repoRoot + "/src/util.go",
		Language: "go",
	}
	fn2 := &graph.Node{
		ID:       graph.NewNodeID("Function", repoRoot+"/src/util.go", "helper"),
		Type:     graph.NodeFunction,
		Name:     "helper",
		FilePath: repoRoot + "/src/util.go",
		Language: "go",
		Package:  "util",
	}

	for _, n := range []*graph.Node{file1, fn1, file2, fn2} {
		if err := store.AddNode(ctx, n); err != nil {
			t.Fatalf("add node: %v", err)
		}
	}

	// Add an edge between fn1 and fn2.
	edge := &graph.Edge{
		ID:       fn1.ID + "-Calls-" + fn2.ID,
		Type:     graph.EdgeCalls,
		SourceID: fn1.ID,
		TargetID: fn2.ID,
	}
	if err := store.AddEdge(ctx, edge); err != nil {
		t.Fatalf("add edge: %v", err)
	}

	// Verify initial state.
	stats, _ := store.Stats(ctx)
	if stats.NodeCount != 4 {
		t.Fatalf("expected 4 nodes before migration, got %d", stats.NodeCount)
	}
	if stats.EdgeCount != 1 {
		t.Fatalf("expected 1 edge before migration, got %d", stats.EdgeCount)
	}

	// Run dry-run first.
	dryResult, err := store.MigrateAbsToRelPaths(ctx, []string{repoRoot}, true)
	if err != nil {
		t.Fatalf("dry run failed: %v", err)
	}
	if dryResult.NodesMigrated != 4 {
		t.Errorf("dry run: expected 4 migrated nodes, got %d", dryResult.NodesMigrated)
	}
	if dryResult.EdgesRemapped != 1 {
		t.Errorf("dry run: expected 1 remapped edge, got %d", dryResult.EdgesRemapped)
	}

	// Verify DB unchanged after dry run.
	stats, _ = store.Stats(ctx)
	if stats.NodeCount != 4 {
		t.Fatalf("expected 4 nodes after dry run, got %d", stats.NodeCount)
	}

	// Run actual migration.
	result, err := store.MigrateAbsToRelPaths(ctx, []string{repoRoot}, false)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	if result.NodesMigrated != 4 {
		t.Errorf("expected 4 migrated nodes, got %d", result.NodesMigrated)
	}
	if result.EdgesRemapped != 1 {
		t.Errorf("expected 1 remapped edge, got %d", result.EdgesRemapped)
	}

	// Verify migrated nodes have relative paths.
	expectedFileID := graph.NewNodeID("File", "src/main.go", "src/main.go")
	fileNode, err := store.GetNode(ctx, expectedFileID)
	if err != nil {
		t.Fatalf("get migrated file node: %v", err)
	}
	if fileNode.FilePath != "src/main.go" {
		t.Errorf("expected relative path src/main.go, got %s", fileNode.FilePath)
	}
	if fileNode.Name != "src/main.go" {
		t.Errorf("expected Name src/main.go for File node, got %s", fileNode.Name)
	}

	expectedFnID := graph.NewNodeID("Function", "src/main.go", "main")
	fnNode, err := store.GetNode(ctx, expectedFnID)
	if err != nil {
		t.Fatalf("get migrated function node: %v", err)
	}
	if fnNode.FilePath != "src/main.go" {
		t.Errorf("expected relative path src/main.go, got %s", fnNode.FilePath)
	}
	if fnNode.Name != "main" {
		t.Errorf("expected Name main, got %s", fnNode.Name)
	}

	// Verify edge was remapped.
	expectedFn2ID := graph.NewNodeID("Function", "src/util.go", "helper")
	edges, err := store.GetEdges(ctx, expectedFnID, graph.EdgeCalls)
	if err != nil {
		t.Fatalf("get edges: %v", err)
	}
	if len(edges) == 0 {
		t.Fatal("expected at least 1 edge after migration")
	}
	found := false
	for _, e := range edges {
		if e.SourceID == expectedFnID && e.TargetID == expectedFn2ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected edge from %s to %s after remapping", expectedFnID, expectedFn2ID)
	}

	// Verify node count preserved.
	stats, _ = store.Stats(ctx)
	if stats.NodeCount != 4 {
		t.Errorf("expected 4 nodes after migration, got %d", stats.NodeCount)
	}
	if stats.EdgeCount != 1 {
		t.Errorf("expected 1 edge after migration, got %d", stats.EdgeCount)
	}

	// Verify file index works with relative path.
	nodes, err := store.QueryNodes(ctx, graph.NodeFilter{FilePath: "src/main.go"})
	if err != nil {
		t.Fatalf("query by file: %v", err)
	}
	if len(nodes) != 2 { // File node + Function node
		t.Errorf("expected 2 nodes for src/main.go, got %d", len(nodes))
	}
}

func TestMigrateAlreadyRelative(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Add a node that already has a relative path.
	node := &graph.Node{
		ID:       graph.NewNodeID("Function", "src/main.go", "main"),
		Type:     graph.NodeFunction,
		Name:     "main",
		FilePath: "src/main.go",
		Language: "go",
	}
	if err := store.AddNode(ctx, node); err != nil {
		t.Fatalf("add node: %v", err)
	}

	result, err := store.MigrateAbsToRelPaths(ctx, []string{"/some/root"}, false)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	if result.NodesMigrated != 0 {
		t.Errorf("expected 0 migrated nodes for already-relative paths, got %d", result.NodesMigrated)
	}
}
