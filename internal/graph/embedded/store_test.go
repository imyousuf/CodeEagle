package embedded

import (
	"context"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestAddGetNode(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	node := &graph.Node{
		ID:            graph.NewNodeID("Function", "main.go", "main"),
		Type:          graph.NodeFunction,
		Name:          "main",
		QualifiedName: "main.main",
		FilePath:      "main.go",
		Line:          10,
		EndLine:       20,
		Package:       "main",
		Language:      "go",
		Exported:      false,
		Signature:     "func main()",
		DocComment:    "Entry point.",
		Properties:    map[string]string{"visibility": "private"},
		Metrics:       map[string]float64{"complexity": 3},
	}

	if err := s.AddNode(ctx, node); err != nil {
		t.Fatalf("AddNode: %v", err)
	}

	got, err := s.GetNode(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if got.Name != "main" {
		t.Errorf("Name = %q, want %q", got.Name, "main")
	}
	if got.QualifiedName != "main.main" {
		t.Errorf("QualifiedName = %q, want %q", got.QualifiedName, "main.main")
	}
	if got.Type != graph.NodeFunction {
		t.Errorf("Type = %q, want %q", got.Type, graph.NodeFunction)
	}
	if got.FilePath != "main.go" {
		t.Errorf("FilePath = %q, want %q", got.FilePath, "main.go")
	}
	if got.Package != "main" {
		t.Errorf("Package = %q, want %q", got.Package, "main")
	}
	if got.Language != "go" {
		t.Errorf("Language = %q, want %q", got.Language, "go")
	}
	if got.Exported {
		t.Error("Exported = true, want false")
	}
	if got.Signature != "func main()" {
		t.Errorf("Signature = %q, want %q", got.Signature, "func main()")
	}
	if got.Line != 10 || got.EndLine != 20 {
		t.Errorf("Line = %d, EndLine = %d, want 10, 20", got.Line, got.EndLine)
	}
	if v, ok := got.Properties["visibility"]; !ok || v != "private" {
		t.Errorf("Properties[visibility] = %q, want %q", v, "private")
	}
	if v, ok := got.Metrics["complexity"]; !ok || v != 3 {
		t.Errorf("Metrics[complexity] = %v, want 3", v)
	}
}

func TestUpdateNode(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	node := &graph.Node{
		ID:       "n1",
		Type:     graph.NodeFunction,
		Name:     "foo",
		FilePath: "a.go",
		Package:  "pkg1",
	}
	if err := s.AddNode(ctx, node); err != nil {
		t.Fatalf("AddNode: %v", err)
	}

	node.Name = "bar"
	node.Package = "pkg2"
	if err := s.UpdateNode(ctx, node); err != nil {
		t.Fatalf("UpdateNode: %v", err)
	}

	got, err := s.GetNode(ctx, "n1")
	if err != nil {
		t.Fatalf("GetNode after update: %v", err)
	}
	if got.Name != "bar" {
		t.Errorf("Name = %q, want %q", got.Name, "bar")
	}
	if got.Package != "pkg2" {
		t.Errorf("Package = %q, want %q", got.Package, "pkg2")
	}

	// Old package index should be gone.
	nodes, err := s.QueryNodes(ctx, graph.NodeFilter{Package: "pkg1"})
	if err != nil {
		t.Fatalf("QueryNodes pkg1: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("QueryNodes pkg1 returned %d, want 0", len(nodes))
	}

	// New package index should exist.
	nodes, err = s.QueryNodes(ctx, graph.NodeFilter{Package: "pkg2"})
	if err != nil {
		t.Fatalf("QueryNodes pkg2: %v", err)
	}
	if len(nodes) != 1 {
		t.Errorf("QueryNodes pkg2 returned %d, want 1", len(nodes))
	}
}

func TestDeleteNode(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	node := &graph.Node{
		ID:       "n1",
		Type:     graph.NodeFunction,
		Name:     "foo",
		FilePath: "a.go",
		Package:  "main",
	}
	if err := s.AddNode(ctx, node); err != nil {
		t.Fatalf("AddNode: %v", err)
	}

	if err := s.DeleteNode(ctx, "n1"); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}

	_, err := s.GetNode(ctx, "n1")
	if err == nil {
		t.Fatal("GetNode after delete: expected error, got nil")
	}
}

func TestDeleteNodeCascadesEdges(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	n1 := &graph.Node{ID: "n1", Type: graph.NodeFunction, Name: "foo", FilePath: "a.go"}
	n2 := &graph.Node{ID: "n2", Type: graph.NodeFunction, Name: "bar", FilePath: "b.go"}
	if err := s.AddNode(ctx, n1); err != nil {
		t.Fatal(err)
	}
	if err := s.AddNode(ctx, n2); err != nil {
		t.Fatal(err)
	}
	edge := &graph.Edge{ID: "e1", Type: graph.EdgeCalls, SourceID: "n1", TargetID: "n2"}
	if err := s.AddEdge(ctx, edge); err != nil {
		t.Fatal(err)
	}

	// Delete source node; edge should be removed.
	if err := s.DeleteNode(ctx, "n1"); err != nil {
		t.Fatal(err)
	}
	edges, err := s.GetEdges(ctx, "n2", graph.EdgeCalls)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 edges after cascading delete, got %d", len(edges))
	}
}

func TestQueryNodesByType(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	nodes := []*graph.Node{
		{ID: "n1", Type: graph.NodeFunction, Name: "foo", FilePath: "a.go"},
		{ID: "n2", Type: graph.NodeStruct, Name: "Bar", FilePath: "a.go"},
		{ID: "n3", Type: graph.NodeFunction, Name: "baz", FilePath: "b.go"},
	}
	for _, n := range nodes {
		if err := s.AddNode(ctx, n); err != nil {
			t.Fatalf("AddNode %s: %v", n.ID, err)
		}
	}

	results, err := s.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeFunction})
	if err != nil {
		t.Fatalf("QueryNodes: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 functions, got %d", len(results))
	}
}

func TestQueryNodesByFile(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	nodes := []*graph.Node{
		{ID: "n1", Type: graph.NodeFunction, Name: "foo", FilePath: "a.go"},
		{ID: "n2", Type: graph.NodeStruct, Name: "Bar", FilePath: "a.go"},
		{ID: "n3", Type: graph.NodeFunction, Name: "baz", FilePath: "b.go"},
	}
	for _, n := range nodes {
		if err := s.AddNode(ctx, n); err != nil {
			t.Fatalf("AddNode %s: %v", n.ID, err)
		}
	}

	results, err := s.QueryNodes(ctx, graph.NodeFilter{FilePath: "a.go"})
	if err != nil {
		t.Fatalf("QueryNodes: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 nodes in a.go, got %d", len(results))
	}
}

func TestQueryNodesByPackage(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	nodes := []*graph.Node{
		{ID: "n1", Type: graph.NodeFunction, Name: "foo", FilePath: "a.go", Package: "pkg1"},
		{ID: "n2", Type: graph.NodeFunction, Name: "bar", FilePath: "b.go", Package: "pkg2"},
		{ID: "n3", Type: graph.NodeFunction, Name: "baz", FilePath: "c.go", Package: "pkg1"},
	}
	for _, n := range nodes {
		if err := s.AddNode(ctx, n); err != nil {
			t.Fatalf("AddNode %s: %v", n.ID, err)
		}
	}

	results, err := s.QueryNodes(ctx, graph.NodeFilter{Package: "pkg1"})
	if err != nil {
		t.Fatalf("QueryNodes: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 nodes in pkg1, got %d", len(results))
	}
}

func TestQueryNodesNamePattern(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	nodes := []*graph.Node{
		{ID: "n1", Type: graph.NodeFunction, Name: "HandleRequest", FilePath: "a.go"},
		{ID: "n2", Type: graph.NodeFunction, Name: "HandleResponse", FilePath: "a.go"},
		{ID: "n3", Type: graph.NodeFunction, Name: "ProcessData", FilePath: "a.go"},
	}
	for _, n := range nodes {
		if err := s.AddNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}

	results, err := s.QueryNodes(ctx, graph.NodeFilter{NamePattern: "Handle*"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 nodes matching Handle*, got %d", len(results))
	}
}

func TestQueryNodesExportedFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	exported := true
	nodes := []*graph.Node{
		{ID: "n1", Type: graph.NodeFunction, Name: "Foo", FilePath: "a.go", Exported: true},
		{ID: "n2", Type: graph.NodeFunction, Name: "bar", FilePath: "a.go", Exported: false},
	}
	for _, n := range nodes {
		if err := s.AddNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}

	results, err := s.QueryNodes(ctx, graph.NodeFilter{Exported: &exported})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 exported node, got %d", len(results))
	}
	if results[0].Name != "Foo" {
		t.Errorf("expected Foo, got %s", results[0].Name)
	}
}

func TestAddGetEdge(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	n1 := &graph.Node{ID: "n1", Type: graph.NodeFunction, Name: "foo", FilePath: "a.go"}
	n2 := &graph.Node{ID: "n2", Type: graph.NodeFunction, Name: "bar", FilePath: "b.go"}
	for _, n := range []*graph.Node{n1, n2} {
		if err := s.AddNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}

	edge := &graph.Edge{
		ID:         "e1",
		Type:       graph.EdgeCalls,
		SourceID:   "n1",
		TargetID:   "n2",
		Properties: map[string]string{"line": "15"},
	}
	if err := s.AddEdge(ctx, edge); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}

	// Get edges from source node perspective.
	edges, err := s.GetEdges(ctx, "n1", graph.EdgeCalls)
	if err != nil {
		t.Fatalf("GetEdges: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	if edges[0].SourceID != "n1" || edges[0].TargetID != "n2" {
		t.Errorf("edge source/target = %s/%s, want n1/n2", edges[0].SourceID, edges[0].TargetID)
	}
	if v, ok := edges[0].Properties["line"]; !ok || v != "15" {
		t.Errorf("Properties[line] = %q, want %q", v, "15")
	}

	// Get edges from target node perspective.
	edges, err = s.GetEdges(ctx, "n2", graph.EdgeCalls)
	if err != nil {
		t.Fatalf("GetEdges from target: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge from target, got %d", len(edges))
	}
}

func TestGetEdgesAllTypes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	n1 := &graph.Node{ID: "n1", Type: graph.NodePackage, Name: "pkg", FilePath: "pkg/"}
	n2 := &graph.Node{ID: "n2", Type: graph.NodeFunction, Name: "foo", FilePath: "pkg/a.go"}
	n3 := &graph.Node{ID: "n3", Type: graph.NodePackage, Name: "other", FilePath: "other/"}
	for _, n := range []*graph.Node{n1, n2, n3} {
		if err := s.AddNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	edges := []*graph.Edge{
		{ID: "e1", Type: graph.EdgeContains, SourceID: "n1", TargetID: "n2"},
		{ID: "e2", Type: graph.EdgeImports, SourceID: "n1", TargetID: "n3"},
	}
	for _, e := range edges {
		if err := s.AddEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	// Empty edgeType should return all edges connected to n1.
	results, err := s.GetEdges(ctx, "n1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 edges for n1 (all types), got %d", len(results))
	}
}

func TestGetNeighborsOutgoing(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	n1 := &graph.Node{ID: "n1", Type: graph.NodePackage, Name: "pkg", FilePath: "pkg/"}
	n2 := &graph.Node{ID: "n2", Type: graph.NodeFunction, Name: "foo", FilePath: "pkg/a.go"}
	n3 := &graph.Node{ID: "n3", Type: graph.NodeFunction, Name: "bar", FilePath: "pkg/b.go"}
	for _, n := range []*graph.Node{n1, n2, n3} {
		if err := s.AddNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []*graph.Edge{
		{ID: "e1", Type: graph.EdgeContains, SourceID: "n1", TargetID: "n2"},
		{ID: "e2", Type: graph.EdgeContains, SourceID: "n1", TargetID: "n3"},
	} {
		if err := s.AddEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	neighbors, err := s.GetNeighbors(ctx, "n1", graph.EdgeContains, graph.Outgoing)
	if err != nil {
		t.Fatalf("GetNeighbors: %v", err)
	}
	if len(neighbors) != 2 {
		t.Errorf("expected 2 outgoing neighbors, got %d", len(neighbors))
	}
}

func TestGetNeighborsIncoming(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	n1 := &graph.Node{ID: "n1", Type: graph.NodePackage, Name: "pkg", FilePath: "pkg/"}
	n2 := &graph.Node{ID: "n2", Type: graph.NodeFunction, Name: "foo", FilePath: "pkg/a.go"}
	for _, n := range []*graph.Node{n1, n2} {
		if err := s.AddNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddEdge(ctx, &graph.Edge{ID: "e1", Type: graph.EdgeContains, SourceID: "n1", TargetID: "n2"}); err != nil {
		t.Fatal(err)
	}

	neighbors, err := s.GetNeighbors(ctx, "n2", graph.EdgeContains, graph.Incoming)
	if err != nil {
		t.Fatalf("GetNeighbors: %v", err)
	}
	if len(neighbors) != 1 {
		t.Fatalf("expected 1 incoming neighbor, got %d", len(neighbors))
	}
	if neighbors[0].ID != "n1" {
		t.Errorf("incoming neighbor ID = %q, want %q", neighbors[0].ID, "n1")
	}
}

func TestGetNeighborsBoth(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	n1 := &graph.Node{ID: "n1", Type: graph.NodeFunction, Name: "a", FilePath: "a.go"}
	n2 := &graph.Node{ID: "n2", Type: graph.NodeFunction, Name: "b", FilePath: "b.go"}
	n3 := &graph.Node{ID: "n3", Type: graph.NodeFunction, Name: "c", FilePath: "c.go"}
	for _, n := range []*graph.Node{n1, n2, n3} {
		if err := s.AddNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	// n1 -> n2 (n1 calls n2)
	if err := s.AddEdge(ctx, &graph.Edge{ID: "e1", Type: graph.EdgeCalls, SourceID: "n1", TargetID: "n2"}); err != nil {
		t.Fatal(err)
	}
	// n3 -> n2 (n3 calls n2)
	if err := s.AddEdge(ctx, &graph.Edge{ID: "e2", Type: graph.EdgeCalls, SourceID: "n3", TargetID: "n2"}); err != nil {
		t.Fatal(err)
	}
	// n2 -> n1 (n2 calls n1)
	if err := s.AddEdge(ctx, &graph.Edge{ID: "e3", Type: graph.EdgeCalls, SourceID: "n2", TargetID: "n1"}); err != nil {
		t.Fatal(err)
	}

	neighbors, err := s.GetNeighbors(ctx, "n2", graph.EdgeCalls, graph.Both)
	if err != nil {
		t.Fatal(err)
	}
	// n2's neighbors via Calls in both directions: n1 (incoming via e1, outgoing via e3), n3 (incoming via e2)
	if len(neighbors) != 2 {
		t.Errorf("expected 2 neighbors (both directions), got %d", len(neighbors))
	}
}

func TestDeleteByFile(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Create nodes in two files.
	nodes := []*graph.Node{
		{ID: "n1", Type: graph.NodeFunction, Name: "foo", FilePath: "target.go", Package: "main"},
		{ID: "n2", Type: graph.NodeStruct, Name: "Bar", FilePath: "target.go", Package: "main"},
		{ID: "n3", Type: graph.NodeFunction, Name: "baz", FilePath: "other.go", Package: "main"},
	}
	for _, n := range nodes {
		if err := s.AddNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	// Cross-file edge: n3 calls n1.
	if err := s.AddEdge(ctx, &graph.Edge{ID: "e1", Type: graph.EdgeCalls, SourceID: "n3", TargetID: "n1"}); err != nil {
		t.Fatal(err)
	}
	// Intra-file edge: n1 -> n2 (contains).
	if err := s.AddEdge(ctx, &graph.Edge{ID: "e2", Type: graph.EdgeContains, SourceID: "n1", TargetID: "n2"}); err != nil {
		t.Fatal(err)
	}

	// Delete everything in target.go.
	if err := s.DeleteByFile(ctx, "target.go"); err != nil {
		t.Fatalf("DeleteByFile: %v", err)
	}

	// n1, n2 should be gone.
	for _, id := range []string{"n1", "n2"} {
		if _, err := s.GetNode(ctx, id); err == nil {
			t.Errorf("node %s should have been deleted", id)
		}
	}
	// n3 should still exist.
	if _, err := s.GetNode(ctx, "n3"); err != nil {
		t.Errorf("node n3 should still exist: %v", err)
	}
	// Both edges should be gone (e1 because target n1 was deleted, e2 because both endpoints were deleted).
	edges, err := s.GetEdges(ctx, "n3", graph.EdgeCalls)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 edges for n3 after DeleteByFile, got %d", len(edges))
	}
}

func TestStats(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	nodes := []*graph.Node{
		{ID: "n1", Type: graph.NodeFunction, Name: "foo", FilePath: "a.go"},
		{ID: "n2", Type: graph.NodeFunction, Name: "bar", FilePath: "b.go"},
		{ID: "n3", Type: graph.NodeStruct, Name: "Baz", FilePath: "a.go"},
	}
	for _, n := range nodes {
		if err := s.AddNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	edges := []*graph.Edge{
		{ID: "e1", Type: graph.EdgeCalls, SourceID: "n1", TargetID: "n2"},
		{ID: "e2", Type: graph.EdgeContains, SourceID: "n3", TargetID: "n1"},
	}
	for _, e := range edges {
		if err := s.AddEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	stats, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.NodeCount != 3 {
		t.Errorf("NodeCount = %d, want 3", stats.NodeCount)
	}
	if stats.EdgeCount != 2 {
		t.Errorf("EdgeCount = %d, want 2", stats.EdgeCount)
	}
	if stats.NodesByType[graph.NodeFunction] != 2 {
		t.Errorf("NodesByType[Function] = %d, want 2", stats.NodesByType[graph.NodeFunction])
	}
	if stats.NodesByType[graph.NodeStruct] != 1 {
		t.Errorf("NodesByType[Struct] = %d, want 1", stats.NodesByType[graph.NodeStruct])
	}
	if stats.EdgesByType[graph.EdgeCalls] != 1 {
		t.Errorf("EdgesByType[Calls] = %d, want 1", stats.EdgesByType[graph.EdgeCalls])
	}
	if stats.EdgesByType[graph.EdgeContains] != 1 {
		t.Errorf("EdgesByType[Contains] = %d, want 1", stats.EdgesByType[graph.EdgeContains])
	}
}

func TestDeleteEdge(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	n1 := &graph.Node{ID: "n1", Type: graph.NodeFunction, Name: "foo", FilePath: "a.go"}
	n2 := &graph.Node{ID: "n2", Type: graph.NodeFunction, Name: "bar", FilePath: "b.go"}
	for _, n := range []*graph.Node{n1, n2} {
		if err := s.AddNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	edge := &graph.Edge{ID: "e1", Type: graph.EdgeCalls, SourceID: "n1", TargetID: "n2"}
	if err := s.AddEdge(ctx, edge); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteEdge(ctx, "e1"); err != nil {
		t.Fatalf("DeleteEdge: %v", err)
	}

	edges, err := s.GetEdges(ctx, "n1", graph.EdgeCalls)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 edges after delete, got %d", len(edges))
	}
}

func TestNewNodeIDDeterministic(t *testing.T) {
	id1 := graph.NewNodeID("Function", "main.go", "main")
	id2 := graph.NewNodeID("Function", "main.go", "main")
	if id1 != id2 {
		t.Errorf("NewNodeID not deterministic: %q != %q", id1, id2)
	}

	id3 := graph.NewNodeID("Function", "main.go", "other")
	if id1 == id3 {
		t.Error("NewNodeID collision for different names")
	}
}
