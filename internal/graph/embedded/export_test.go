package embedded

import (
	"bytes"
	"context"
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
