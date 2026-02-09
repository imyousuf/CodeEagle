package embedded

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

func TestExportImportRoundTrip(t *testing.T) {
	ctx := context.Background()

	// Create a store with some data.
	src := newTestStore(t)
	nodes := []*graph.Node{
		{ID: "n1", Type: graph.NodeFunction, Name: "foo", FilePath: "a.go", Package: "main", Language: "go", Exported: true, Signature: "func foo()"},
		{ID: "n2", Type: graph.NodeStruct, Name: "Bar", FilePath: "a.go", Package: "main", Language: "go"},
		{ID: "n3", Type: graph.NodeFunction, Name: "baz", FilePath: "b.go", Package: "pkg", Language: "go", Properties: map[string]string{"role": "handler"}, Metrics: map[string]float64{"complexity": 5}},
	}
	for _, n := range nodes {
		if err := src.AddNode(ctx, n); err != nil {
			t.Fatalf("AddNode %s: %v", n.ID, err)
		}
	}
	edges := []*graph.Edge{
		{ID: "e1", Type: graph.EdgeCalls, SourceID: "n1", TargetID: "n3"},
		{ID: "e2", Type: graph.EdgeContains, SourceID: "n2", TargetID: "n1", Properties: map[string]string{"line": "10"}},
	}
	for _, e := range edges {
		if err := src.AddEdge(ctx, e); err != nil {
			t.Fatalf("AddEdge %s: %v", e.ID, err)
		}
	}

	// Export to buffer.
	var buf bytes.Buffer
	if err := src.Export(ctx, &buf); err != nil {
		t.Fatalf("Export: %v", err)
	}

	if buf.Len() == 0 {
		t.Fatal("Export produced empty output")
	}

	// Import into a fresh store.
	dst := newTestStore(t)
	if err := dst.Import(ctx, &buf); err != nil {
		t.Fatalf("Import: %v", err)
	}

	// Verify all nodes.
	for _, want := range nodes {
		got, err := dst.GetNode(ctx, want.ID)
		if err != nil {
			t.Errorf("GetNode %s after import: %v", want.ID, err)
			continue
		}
		if got.Name != want.Name {
			t.Errorf("node %s Name = %q, want %q", want.ID, got.Name, want.Name)
		}
		if got.Type != want.Type {
			t.Errorf("node %s Type = %q, want %q", want.ID, got.Type, want.Type)
		}
		if got.FilePath != want.FilePath {
			t.Errorf("node %s FilePath = %q, want %q", want.ID, got.FilePath, want.FilePath)
		}
		if got.Package != want.Package {
			t.Errorf("node %s Package = %q, want %q", want.ID, got.Package, want.Package)
		}
		if got.Exported != want.Exported {
			t.Errorf("node %s Exported = %v, want %v", want.ID, got.Exported, want.Exported)
		}
	}

	// Verify node with properties and metrics.
	n3, err := dst.GetNode(ctx, "n3")
	if err != nil {
		t.Fatalf("GetNode n3: %v", err)
	}
	if n3.Properties["role"] != "handler" {
		t.Errorf("n3 Properties[role] = %q, want %q", n3.Properties["role"], "handler")
	}
	if n3.Metrics["complexity"] != 5 {
		t.Errorf("n3 Metrics[complexity] = %v, want 5", n3.Metrics["complexity"])
	}

	// Verify edges.
	e1Edges, err := dst.GetEdges(ctx, "n1", graph.EdgeCalls)
	if err != nil {
		t.Fatalf("GetEdges n1 Calls: %v", err)
	}
	if len(e1Edges) != 1 {
		t.Errorf("expected 1 Calls edge for n1, got %d", len(e1Edges))
	}

	e2Edges, err := dst.GetEdges(ctx, "n2", graph.EdgeContains)
	if err != nil {
		t.Fatalf("GetEdges n2 Contains: %v", err)
	}
	if len(e2Edges) != 1 {
		t.Errorf("expected 1 Contains edge for n2, got %d", len(e2Edges))
	} else if e2Edges[0].Properties["line"] != "10" {
		t.Errorf("edge e2 Properties[line] = %q, want %q", e2Edges[0].Properties["line"], "10")
	}

	// Verify stats match.
	srcStats, _ := src.Stats(ctx)
	dstStats, _ := dst.Stats(ctx)
	if srcStats.NodeCount != dstStats.NodeCount {
		t.Errorf("NodeCount: src=%d, dst=%d", srcStats.NodeCount, dstStats.NodeCount)
	}
	if srcStats.EdgeCount != dstStats.EdgeCount {
		t.Errorf("EdgeCount: src=%d, dst=%d", srcStats.EdgeCount, dstStats.EdgeCount)
	}
}

func TestImportClearsExistingData(t *testing.T) {
	ctx := context.Background()

	dst := newTestStore(t)

	// Add existing data.
	if err := dst.AddNode(ctx, &graph.Node{ID: "old1", Type: graph.NodeFunction, Name: "old", FilePath: "old.go"}); err != nil {
		t.Fatal(err)
	}

	// Create export data with different content.
	src := newTestStore(t)
	if err := src.AddNode(ctx, &graph.Node{ID: "new1", Type: graph.NodeStruct, Name: "new", FilePath: "new.go"}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := src.Export(ctx, &buf); err != nil {
		t.Fatal(err)
	}

	// Import should clear old data.
	if err := dst.Import(ctx, &buf); err != nil {
		t.Fatal(err)
	}

	// Old node should be gone.
	if _, err := dst.GetNode(ctx, "old1"); err == nil {
		t.Error("old node should have been cleared by import")
	}

	// New node should be present.
	got, err := dst.GetNode(ctx, "new1")
	if err != nil {
		t.Fatalf("new node not found after import: %v", err)
	}
	if got.Name != "new" {
		t.Errorf("Name = %q, want %q", got.Name, "new")
	}
}

func TestExportEmptyStore(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	var buf bytes.Buffer
	if err := s.Export(ctx, &buf); err != nil {
		t.Fatalf("Export empty store: %v", err)
	}

	// Should produce empty output (no records).
	if buf.Len() != 0 {
		t.Errorf("expected empty export, got %d bytes", buf.Len())
	}
}

func TestExportBranch(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir()

	// Write to two branches.
	mainStore, err := NewBranchStore(dbPath, "main", []string{"main"})
	if err != nil {
		t.Fatal(err)
	}
	if err := mainStore.AddNode(ctx, &graph.Node{ID: "m1", Type: graph.NodeFunction, Name: "mainFn", FilePath: "a.go"}); err != nil {
		t.Fatal(err)
	}
	if err := mainStore.AddNode(ctx, &graph.Node{ID: "m2", Type: graph.NodeFunction, Name: "mainFn2", FilePath: "b.go"}); err != nil {
		t.Fatal(err)
	}
	mainStore.Close()

	featureStore, err := NewBranchStore(dbPath, "feature", []string{"feature"})
	if err != nil {
		t.Fatal(err)
	}
	if err := featureStore.AddNode(ctx, &graph.Node{ID: "f1", Type: graph.NodeFunction, Name: "featFn", FilePath: "c.go"}); err != nil {
		t.Fatal(err)
	}
	featureStore.Close()

	// Export only main branch.
	store, err := NewBranchStore(dbPath, "main", []string{"main", "feature"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var buf bytes.Buffer
	if err := store.ExportBranch(ctx, &buf, "main"); err != nil {
		t.Fatal(err)
	}

	// Verify branch field is set.
	branch, err := ReadExportBranch(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatal(err)
	}
	if branch != "main" {
		t.Errorf("export branch = %q, want %q", branch, "main")
	}

	// Import into a fresh store with "main" in readBranches.
	dstPath := t.TempDir()
	dstStore, err := NewBranchStore(dstPath, "main", []string{"main"})
	if err != nil {
		t.Fatal(err)
	}
	defer dstStore.Close()

	if err := dstStore.Import(ctx, &buf); err != nil {
		t.Fatal(err)
	}

	// Verify only main nodes imported.
	stats, _ := dstStore.Stats(ctx)
	if stats.NodeCount != 2 {
		t.Errorf("expected 2 nodes (main only), got %d", stats.NodeCount)
	}
}

func TestImportIntoBranch(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir()

	// Create export data from a "main" store.
	srcStore, err := NewBranchStore(t.TempDir(), "main", []string{"main"})
	if err != nil {
		t.Fatal(err)
	}
	if err := srcStore.AddNode(ctx, &graph.Node{ID: "m1", Type: graph.NodeFunction, Name: "mainFn", FilePath: "a.go"}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := srcStore.Export(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	srcStore.Close()

	// Write feature data to dest store.
	destStore, err := NewBranchStore(dbPath, "feature", []string{"feature", "main"})
	if err != nil {
		t.Fatal(err)
	}
	if err := destStore.AddNode(ctx, &graph.Node{ID: "f1", Type: graph.NodeFunction, Name: "featFn", FilePath: "b.go"}); err != nil {
		t.Fatal(err)
	}

	// Import into "main" branch — should preserve "feature" data.
	sourceBranch, err := destStore.ImportIntoBranch(ctx, &buf, "main")
	if err != nil {
		t.Fatal(err)
	}
	if sourceBranch != "main" {
		t.Errorf("sourceBranch = %q, want %q", sourceBranch, "main")
	}
	destStore.Close()

	// Verify both branches exist.
	verifyStore, err := NewBranchStore(dbPath, "feature", []string{"feature", "main"})
	if err != nil {
		t.Fatal(err)
	}
	defer verifyStore.Close()

	// Feature node should still exist.
	if _, err := verifyStore.GetNode(ctx, "f1"); err != nil {
		t.Errorf("feature node should still exist: %v", err)
	}
	// Main node should exist.
	if _, err := verifyStore.GetNode(ctx, "m1"); err != nil {
		t.Errorf("imported main node should exist: %v", err)
	}
}

func TestImportLegacyFormat(t *testing.T) {
	ctx := context.Background()

	// Create legacy export (no Branch field).
	legacy := `{"kind":"node","data":{"id":"n1","type":"Function","name":"foo","qualified_name":"","file_path":"a.go","line":0,"end_line":0,"package":"","language":"","exported":false}}
{"kind":"edge","data":{"id":"e1","type":"Calls","source_id":"n1","target_id":"n1"}}
`

	store := newTestStore(t)
	if err := store.Import(ctx, strings.NewReader(legacy)); err != nil {
		t.Fatal(err)
	}

	// Should import into the default write branch.
	got, err := store.GetNode(ctx, "n1")
	if err != nil {
		t.Fatalf("node not found after legacy import: %v", err)
	}
	if got.Name != "foo" {
		t.Errorf("Name = %q, want %q", got.Name, "foo")
	}
}

func TestReadExportBranch(t *testing.T) {
	// Branch-aware export.
	data := `{"kind":"node","branch":"main","data":{"id":"n1","type":"Function","name":"foo","qualified_name":"","file_path":"a.go","line":0,"end_line":0,"package":"","language":"","exported":false}}
`
	branch, err := ReadExportBranch(strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if branch != "main" {
		t.Errorf("branch = %q, want %q", branch, "main")
	}

	// Legacy export (no branch).
	legacyData := `{"kind":"node","data":{"id":"n1","type":"Function","name":"foo","qualified_name":"","file_path":"a.go","line":0,"end_line":0,"package":"","language":"","exported":false}}
`
	branch, err = ReadExportBranch(strings.NewReader(legacyData))
	if err != nil {
		t.Fatal(err)
	}
	if branch != "" {
		t.Errorf("legacy branch = %q, want empty", branch)
	}

	// Empty file.
	branch, err = ReadExportBranch(strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if branch != "" {
		t.Errorf("empty file branch = %q, want empty", branch)
	}
}
