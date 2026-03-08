package indexer

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

func openTestStore(t *testing.T) graph.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "testdb")
	store, err := embedded.NewStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestEnsureDateNodes(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	// Create a file node first.
	fileNode := &graph.Node{
		ID:       graph.NewNodeID(string(graph.NodeFile), "src/main.go", "main.go"),
		Type:     graph.NodeFile,
		Name:     "main.go",
		FilePath: "src/main.go",
	}
	if err := store.AddNode(ctx, fileNode); err != nil {
		t.Fatal(err)
	}

	ts := time.Date(2024, 3, 15, 10, 30, 0, 0, time.UTC)
	if err := EnsureDateNodes(ctx, store, ts, fileNode.ID); err != nil {
		t.Fatal(err)
	}

	// Verify Year node.
	yearID := graph.NewNodeID(string(graph.NodeYear), "", "2024")
	yearNodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeYear})
	if err != nil {
		t.Fatal(err)
	}
	if len(yearNodes) != 1 {
		t.Fatalf("expected 1 Year node, got %d", len(yearNodes))
	}
	if yearNodes[0].Name != "2024" || yearNodes[0].ID != yearID {
		t.Errorf("Year node: got name=%q id=%q, want name=%q id=%q", yearNodes[0].Name, yearNodes[0].ID, "2024", yearID)
	}

	// Verify Month node.
	monthNodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeMonth})
	if err != nil {
		t.Fatal(err)
	}
	if len(monthNodes) != 1 {
		t.Fatalf("expected 1 Month node, got %d", len(monthNodes))
	}
	if monthNodes[0].Name != "2024-03" {
		t.Errorf("Month node name = %q, want %q", monthNodes[0].Name, "2024-03")
	}

	// Verify Date node.
	dateNodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeDate})
	if err != nil {
		t.Fatal(err)
	}
	if len(dateNodes) != 1 {
		t.Fatalf("expected 1 Date node, got %d", len(dateNodes))
	}
	if dateNodes[0].Name != "2024-03-15" {
		t.Errorf("Date node name = %q, want %q", dateNodes[0].Name, "2024-03-15")
	}

	// Verify Contains edges (Year -> Month -> Date).
	yearEdges, err := store.GetEdges(ctx, yearID, graph.EdgeContains)
	if err != nil {
		t.Fatal(err)
	}
	if len(yearEdges) != 1 {
		t.Fatalf("expected 1 Contains edge from Year, got %d", len(yearEdges))
	}

	// Verify UpdatedOn edge (File -> Date).
	updatedOnEdges, err := store.GetEdges(ctx, fileNode.ID, graph.EdgeUpdatedOn)
	if err != nil {
		t.Fatal(err)
	}
	if len(updatedOnEdges) != 1 {
		t.Fatalf("expected 1 UpdatedOn edge, got %d", len(updatedOnEdges))
	}
	if updatedOnEdges[0].SourceID != fileNode.ID {
		t.Errorf("UpdatedOn edge source = %q, want %q", updatedOnEdges[0].SourceID, fileNode.ID)
	}
}

func TestEnsureDateNodesIdempotent(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	fileNode := &graph.Node{
		ID:       graph.NewNodeID(string(graph.NodeFile), "a.go", "a.go"),
		Type:     graph.NodeFile,
		Name:     "a.go",
		FilePath: "a.go",
	}
	if err := store.AddNode(ctx, fileNode); err != nil {
		t.Fatal(err)
	}

	ts := time.Date(2025, 1, 10, 0, 0, 0, 0, time.UTC)

	// Call twice.
	if err := EnsureDateNodes(ctx, store, ts, fileNode.ID); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDateNodes(ctx, store, ts, fileNode.ID); err != nil {
		t.Fatal(err)
	}

	// Should still have exactly 1 of each date node.
	for _, nodeType := range []graph.NodeType{graph.NodeYear, graph.NodeMonth, graph.NodeDate} {
		nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: nodeType})
		if err != nil {
			t.Fatal(err)
		}
		if len(nodes) != 1 {
			t.Errorf("expected 1 %s node after idempotent calls, got %d", nodeType, len(nodes))
		}
	}
}

func TestDeleteUpdatedOnEdges(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	fileNode := &graph.Node{
		ID:       graph.NewNodeID(string(graph.NodeFile), "b.go", "b.go"),
		Type:     graph.NodeFile,
		Name:     "b.go",
		FilePath: "b.go",
	}
	if err := store.AddNode(ctx, fileNode); err != nil {
		t.Fatal(err)
	}

	ts := time.Date(2024, 6, 20, 0, 0, 0, 0, time.UTC)
	if err := EnsureDateNodes(ctx, store, ts, fileNode.ID); err != nil {
		t.Fatal(err)
	}

	// Verify edge exists.
	edges, err := store.GetEdges(ctx, fileNode.ID, graph.EdgeUpdatedOn)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 UpdatedOn edge, got %d", len(edges))
	}

	// Delete UpdatedOn edges.
	if err := DeleteUpdatedOnEdges(ctx, store, fileNode.ID); err != nil {
		t.Fatal(err)
	}

	// Verify edge removed.
	edges, err = store.GetEdges(ctx, fileNode.ID, graph.EdgeUpdatedOn)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 UpdatedOn edges after delete, got %d", len(edges))
	}
}

func TestDifferentDatesShareYearNode(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	file1 := &graph.Node{
		ID:       graph.NewNodeID(string(graph.NodeFile), "a.go", "a.go"),
		Type:     graph.NodeFile,
		Name:     "a.go",
		FilePath: "a.go",
	}
	file2 := &graph.Node{
		ID:       graph.NewNodeID(string(graph.NodeFile), "b.go", "b.go"),
		Type:     graph.NodeFile,
		Name:     "b.go",
		FilePath: "b.go",
	}
	for _, n := range []*graph.Node{file1, file2} {
		if err := store.AddNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}

	// Two files in same year, different months.
	ts1 := time.Date(2024, 3, 10, 0, 0, 0, 0, time.UTC)
	ts2 := time.Date(2024, 7, 25, 0, 0, 0, 0, time.UTC)

	if err := EnsureDateNodes(ctx, store, ts1, file1.ID); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDateNodes(ctx, store, ts2, file2.ID); err != nil {
		t.Fatal(err)
	}

	// Should have 1 Year node, 2 Month nodes, 2 Date nodes.
	yearNodes, _ := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeYear})
	if len(yearNodes) != 1 {
		t.Errorf("expected 1 shared Year node, got %d", len(yearNodes))
	}

	monthNodes, _ := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeMonth})
	if len(monthNodes) != 2 {
		t.Errorf("expected 2 Month nodes, got %d", len(monthNodes))
	}

	dateNodes, _ := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeDate})
	if len(dateNodes) != 2 {
		t.Errorf("expected 2 Date nodes, got %d", len(dateNodes))
	}
}

func TestIsFileTypeNode(t *testing.T) {
	tests := []struct {
		nodeType graph.NodeType
		want     bool
	}{
		{graph.NodeFile, true},
		{graph.NodeTestFile, true},
		{graph.NodeDocument, true},
		{graph.NodeDirectory, true},
		{graph.NodeAIGuideline, true},
		{graph.NodeFunction, false},
		{graph.NodeStruct, false},
		{graph.NodeYear, false},
		{graph.NodePackage, false},
	}
	for _, tt := range tests {
		if got := isFileTypeNode(tt.nodeType); got != tt.want {
			t.Errorf("isFileTypeNode(%q) = %v, want %v", tt.nodeType, got, tt.want)
		}
	}
}
