package graph

import (
	"context"
	"fmt"
	"testing"
)

// memStore is a minimal in-memory Store implementation for testing the layered store.
type memStore struct {
	nodes map[string]*Node
	edges map[string]*Edge
}

func newMemStore() *memStore {
	return &memStore{
		nodes: make(map[string]*Node),
		edges: make(map[string]*Edge),
	}
}

func (m *memStore) AddNode(_ context.Context, node *Node) error {
	m.nodes[node.ID] = node
	return nil
}

func (m *memStore) UpdateNode(_ context.Context, node *Node) error {
	if _, ok := m.nodes[node.ID]; !ok {
		return fmt.Errorf("node %s not found", node.ID)
	}
	m.nodes[node.ID] = node
	return nil
}

func (m *memStore) DeleteNode(_ context.Context, id string) error {
	delete(m.nodes, id)
	// Delete connected edges.
	for eid, e := range m.edges {
		if e.SourceID == id || e.TargetID == id {
			delete(m.edges, eid)
		}
	}
	return nil
}

func (m *memStore) GetNode(_ context.Context, id string) (*Node, error) {
	n, ok := m.nodes[id]
	if !ok {
		return nil, fmt.Errorf("node %s not found", id)
	}
	return n, nil
}

func (m *memStore) QueryNodes(_ context.Context, filter NodeFilter) ([]*Node, error) {
	var result []*Node
	for _, n := range m.nodes {
		if filter.Type != "" && n.Type != filter.Type {
			continue
		}
		if filter.FilePath != "" && n.FilePath != filter.FilePath {
			continue
		}
		if filter.Package != "" && n.Package != filter.Package {
			continue
		}
		result = append(result, n)
	}
	return result, nil
}

func (m *memStore) AddEdge(_ context.Context, edge *Edge) error {
	m.edges[edge.ID] = edge
	return nil
}

func (m *memStore) DeleteEdge(_ context.Context, id string) error {
	delete(m.edges, id)
	return nil
}

func (m *memStore) GetEdges(_ context.Context, nodeID string, edgeType EdgeType) ([]*Edge, error) {
	var result []*Edge
	for _, e := range m.edges {
		if e.SourceID != nodeID && e.TargetID != nodeID {
			continue
		}
		if edgeType != "" && e.Type != edgeType {
			continue
		}
		result = append(result, e)
	}
	return result, nil
}

func (m *memStore) GetNeighbors(_ context.Context, nodeID string, edgeType EdgeType, direction Direction) ([]*Node, error) {
	seen := make(map[string]struct{})
	var result []*Node
	for _, e := range m.edges {
		if edgeType != "" && e.Type != edgeType {
			continue
		}
		var neighborID string
		if (direction == Outgoing || direction == Both) && e.SourceID == nodeID {
			neighborID = e.TargetID
		}
		if (direction == Incoming || direction == Both) && e.TargetID == nodeID {
			neighborID = e.SourceID
		}
		if neighborID == "" {
			continue
		}
		if _, ok := seen[neighborID]; ok {
			continue
		}
		seen[neighborID] = struct{}{}
		if n, ok := m.nodes[neighborID]; ok {
			result = append(result, n)
		}
	}
	return result, nil
}

func (m *memStore) DeleteByFile(_ context.Context, filePath string) error {
	for id, n := range m.nodes {
		if n.FilePath == filePath {
			delete(m.nodes, id)
			for eid, e := range m.edges {
				if e.SourceID == id || e.TargetID == id {
					delete(m.edges, eid)
				}
			}
		}
	}
	return nil
}

func (m *memStore) Stats(_ context.Context) (*GraphStats, error) {
	stats := &GraphStats{
		NodesByType: make(map[NodeType]int64),
		EdgesByType: make(map[EdgeType]int64),
	}
	for _, n := range m.nodes {
		stats.NodeCount++
		stats.NodesByType[n.Type]++
	}
	for _, e := range m.edges {
		stats.EdgeCount++
		stats.EdgesByType[e.Type]++
	}
	return stats, nil
}

func (m *memStore) Close() error { return nil }

func TestLayeredStoreWritesToLocal(t *testing.T) {
	ctx := context.Background()
	main := newMemStore()
	local := newMemStore()
	ls := NewLayeredStore(main, local)

	node := &Node{ID: "n1", Type: NodeFunction, Name: "foo", FilePath: "a.go"}
	if err := ls.AddNode(ctx, node); err != nil {
		t.Fatal(err)
	}

	// Node should be in local only.
	if _, err := local.GetNode(ctx, "n1"); err != nil {
		t.Error("node should be in local store")
	}
	if _, err := main.GetNode(ctx, "n1"); err == nil {
		t.Error("node should NOT be in main store")
	}
}

func TestLayeredStoreReadsFromBoth(t *testing.T) {
	ctx := context.Background()
	main := newMemStore()
	local := newMemStore()
	ls := NewLayeredStore(main, local)

	// Add a node to main.
	mainNode := &Node{ID: "m1", Type: NodeStruct, Name: "MainStruct", FilePath: "main.go"}
	if err := main.AddNode(ctx, mainNode); err != nil {
		t.Fatal(err)
	}
	// Add a node to local.
	localNode := &Node{ID: "l1", Type: NodeFunction, Name: "LocalFunc", FilePath: "local.go"}
	if err := local.AddNode(ctx, localNode); err != nil {
		t.Fatal(err)
	}

	// GetNode from main.
	got, err := ls.GetNode(ctx, "m1")
	if err != nil {
		t.Fatalf("GetNode m1: %v", err)
	}
	if got.Name != "MainStruct" {
		t.Errorf("Name = %q, want %q", got.Name, "MainStruct")
	}

	// GetNode from local.
	got, err = ls.GetNode(ctx, "l1")
	if err != nil {
		t.Fatalf("GetNode l1: %v", err)
	}
	if got.Name != "LocalFunc" {
		t.Errorf("Name = %q, want %q", got.Name, "LocalFunc")
	}
}

func TestLayeredStoreLocalOverridesMain(t *testing.T) {
	ctx := context.Background()
	main := newMemStore()
	local := newMemStore()
	ls := NewLayeredStore(main, local)

	// Same ID in both stores with different names.
	if err := main.AddNode(ctx, &Node{ID: "dup", Type: NodeFunction, Name: "MainVersion", FilePath: "a.go"}); err != nil {
		t.Fatal(err)
	}
	if err := local.AddNode(ctx, &Node{ID: "dup", Type: NodeFunction, Name: "LocalVersion", FilePath: "a.go"}); err != nil {
		t.Fatal(err)
	}

	// GetNode should return local version.
	got, err := ls.GetNode(ctx, "dup")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "LocalVersion" {
		t.Errorf("GetNode Name = %q, want LocalVersion (local override)", got.Name)
	}

	// QueryNodes should also prefer local.
	results, err := ls.QueryNodes(ctx, NodeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 merged result (deduped), got %d", len(results))
	}
	if results[0].Name != "LocalVersion" {
		t.Errorf("QueryNodes Name = %q, want LocalVersion", results[0].Name)
	}
}

func TestLayeredStoreQueryNodesMerge(t *testing.T) {
	ctx := context.Background()
	main := newMemStore()
	local := newMemStore()
	ls := NewLayeredStore(main, local)

	if err := main.AddNode(ctx, &Node{ID: "m1", Type: NodeFunction, Name: "fromMain", FilePath: "a.go"}); err != nil {
		t.Fatal(err)
	}
	if err := local.AddNode(ctx, &Node{ID: "l1", Type: NodeFunction, Name: "fromLocal", FilePath: "b.go"}); err != nil {
		t.Fatal(err)
	}

	results, err := ls.QueryNodes(ctx, NodeFilter{Type: NodeFunction})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 merged results, got %d", len(results))
	}
}

func TestLayeredStoreEdgeMerge(t *testing.T) {
	ctx := context.Background()
	main := newMemStore()
	local := newMemStore()
	ls := NewLayeredStore(main, local)

	// Add nodes to both.
	for _, s := range []Store{main, local} {
		if err := s.AddNode(ctx, &Node{ID: "n1", Type: NodeFunction, Name: "a", FilePath: "a.go"}); err != nil {
			t.Fatal(err)
		}
		if err := s.AddNode(ctx, &Node{ID: "n2", Type: NodeFunction, Name: "b", FilePath: "b.go"}); err != nil {
			t.Fatal(err)
		}
	}

	// Add edge to main.
	if err := main.AddEdge(ctx, &Edge{ID: "e1", Type: EdgeCalls, SourceID: "n1", TargetID: "n2"}); err != nil {
		t.Fatal(err)
	}
	// Add different edge to local.
	if err := local.AddEdge(ctx, &Edge{ID: "e2", Type: EdgeImports, SourceID: "n1", TargetID: "n2"}); err != nil {
		t.Fatal(err)
	}

	edges, err := ls.GetEdges(ctx, "n1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 2 {
		t.Errorf("expected 2 merged edges, got %d", len(edges))
	}
}

func TestLayeredStoreStatsSummed(t *testing.T) {
	ctx := context.Background()
	main := newMemStore()
	local := newMemStore()
	ls := NewLayeredStore(main, local)

	if err := main.AddNode(ctx, &Node{ID: "m1", Type: NodeFunction, Name: "foo", FilePath: "a.go"}); err != nil {
		t.Fatal(err)
	}
	if err := local.AddNode(ctx, &Node{ID: "l1", Type: NodeStruct, Name: "Bar", FilePath: "b.go"}); err != nil {
		t.Fatal(err)
	}

	stats, err := ls.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.NodeCount != 2 {
		t.Errorf("NodeCount = %d, want 2", stats.NodeCount)
	}
	if stats.NodesByType[NodeFunction] != 1 {
		t.Errorf("NodesByType[Function] = %d, want 1", stats.NodesByType[NodeFunction])
	}
	if stats.NodesByType[NodeStruct] != 1 {
		t.Errorf("NodesByType[Struct] = %d, want 1", stats.NodesByType[NodeStruct])
	}
}

func TestLayeredStoreDeleteWritesToLocal(t *testing.T) {
	ctx := context.Background()
	main := newMemStore()
	local := newMemStore()
	ls := NewLayeredStore(main, local)

	// Add node to local, then delete via layered.
	if err := local.AddNode(ctx, &Node{ID: "n1", Type: NodeFunction, Name: "foo", FilePath: "a.go"}); err != nil {
		t.Fatal(err)
	}

	if err := ls.DeleteNode(ctx, "n1"); err != nil {
		t.Fatal(err)
	}

	if _, err := local.GetNode(ctx, "n1"); err == nil {
		t.Error("node should have been deleted from local")
	}
}
