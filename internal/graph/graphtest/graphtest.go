// Package graphtest is a conformance suite for graph.Store implementations.
//
// It pins the single-namespace contract as the embedded Badger store behaves
// today: one write scope that is also the only read scope. Branch and scope
// behaviour is out of its reach and is tested beside the store that has it.
//
// The suite compares collections as sets, ignores the graph.PropGraphSource
// property a store may add on read, treats nil and empty maps as equal, and
// compares times with Equal, since a stored time loses its monotonic reading.
package graphtest

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// Run runs every conformance subtest. newStore must return an empty store
// for each subtest and arrange for it to be closed, for example with
// t.Cleanup.
func Run(t *testing.T, newStore func(t *testing.T) graph.Store) {
	t.Helper()
	tests := []struct {
		name string
		fn   func(t *testing.T, s graph.Store)
	}{
		{"AddNodeRoundTripsEveryField", testAddNodeRoundTripsEveryField},
		{"AddNodeOverwritesExistingID", testAddNodeOverwritesExistingID},
		{"GetNodeMissingReturnsError", testGetNodeMissingReturnsError},
		{"MutationsOfMissingIDsReturnErrors", testMutationsOfMissingIDsReturnErrors},
		{"UpdateNodeReplacesRecordAndIndexes", testUpdateNodeReplacesRecordAndIndexes},
		{"DeleteNodeCascadesEdges", testDeleteNodeCascadesEdges},
		{"NodesAreCopies", testNodesAreCopies},
		{"EdgesAreCopies", testEdgesAreCopies},
		{"AddEdgeAcceptsDanglingEndpoints", testAddEdgeAcceptsDanglingEndpoints},
		{"GetEdgesOutgoingThenIncoming", testGetEdgesOutgoingThenIncoming},
		{"GetNeighbors", testGetNeighbors},
		{"QueryNodesFilters", testQueryNodesFilters},
		{"QueryNodesNameMatchOncePerNode", testQueryNodesNameMatchOncePerNode},
		{"DeleteByFile", testDeleteByFile},
		{"Stats", testStats},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.fn(t, newStore(t))
		})
	}
}

// --- subtests ---

func testAddNodeRoundTripsEveryField(t *testing.T, s graph.Store) {
	ctx := context.Background()
	want := &graph.Node{
		ID:            "n1",
		Type:          graph.NodeFunction,
		Name:          "HandleAuth",
		QualifiedName: "auth.HandleAuth",
		FilePath:      "auth/handler.go",
		Line:          10,
		EndLine:       42,
		Package:       "auth",
		Language:      "go",
		Exported:      true,
		Signature:     "func HandleAuth(w http.ResponseWriter, r *http.Request)",
		DocComment:    "HandleAuth checks <tokens> & sessions — résumé.",
		Properties:    map[string]string{graph.PropArchRole: "controller", "k": ""},
		Metrics:       map[string]float64{"complexity": 3.5, "loc": 32},
		// time.Now carries a monotonic reading, which a store cannot keep.
		UpdatedAt: time.Now(),
	}
	mustAddNode(t, s, want)
	got, err := s.GetNode(ctx, "n1")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	assertNodeEqual(t, "GetNode", want, got)

	// A node with no properties, metrics or time reads back as such.
	bare := &graph.Node{ID: "n2", Type: graph.NodeFile, Name: "README.md"}
	mustAddNode(t, s, bare)
	got, err = s.GetNode(ctx, "n2")
	if err != nil {
		t.Fatalf("GetNode bare: %v", err)
	}
	assertNodeEqual(t, "GetNode bare", bare, got)
	if !got.UpdatedAt.IsZero() {
		t.Errorf("UpdatedAt = %v, want zero", got.UpdatedAt)
	}
}

func testAddNodeOverwritesExistingID(t *testing.T, s graph.Store) {
	ctx := context.Background()
	first := &graph.Node{
		ID: "n1", Type: graph.NodeFunction, Name: "Old", FilePath: "old.go",
		Package: "oldpkg", Language: "go", Exported: true, Line: 3,
		Properties: map[string]string{"stale": "yes"},
		Metrics:    map[string]float64{"complexity": 9},
	}
	second := &graph.Node{
		ID: "n1", Type: graph.NodeMethod, Name: "New", FilePath: "new.go",
		Package: "newpkg", Language: "python",
		Properties: map[string]string{"fresh": "yes"},
	}
	mustAddNode(t, s, first)
	mustAddNode(t, s, second)

	got, err := s.GetNode(ctx, "n1")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	// The whole record is replaced: nothing of the first survives.
	assertNodeEqual(t, "GetNode after overwrite", second, got)

	for _, f := range []graph.NodeFilter{
		{Package: "oldpkg"}, {Type: graph.NodeFunction}, {FilePath: "old.go"}, {Language: "go"},
		{Properties: map[string]string{"stale": ""}},
	} {
		assertQueryIDs(t, s, f)
	}
	for _, f := range []graph.NodeFilter{
		{Package: "newpkg"}, {Type: graph.NodeMethod}, {FilePath: "new.go"}, {Language: "python"},
	} {
		assertQueryIDs(t, s, f, "n1")
	}

	stats := mustStats(t, s)
	if stats.NodeCount != 1 {
		t.Errorf("NodeCount = %d after overwriting one ID, want 1", stats.NodeCount)
	}
	if stats.NodesByType[graph.NodeFunction] != 0 || stats.NodesByType[graph.NodeMethod] != 1 {
		t.Errorf("NodesByType = %v, want only Method: 1", stats.NodesByType)
	}
}

func testGetNodeMissingReturnsError(t *testing.T, s graph.Store) {
	ctx := context.Background()
	if _, err := s.GetNode(ctx, "missing"); err == nil {
		t.Error("GetNode on an empty store returned no error")
	}
	mustAddNode(t, s, &graph.Node{ID: "present", Type: graph.NodeFile, Name: "a"})
	if _, err := s.GetNode(ctx, "missing"); err == nil {
		t.Error("GetNode of a missing ID returned no error")
	}
}

func testMutationsOfMissingIDsReturnErrors(t *testing.T, s graph.Store) {
	ctx := context.Background()
	mustAddNode(t, s, &graph.Node{ID: "present", Type: graph.NodeFile, Name: "a"})
	mustAddEdge(t, s, &graph.Edge{ID: "e1", Type: graph.EdgeContains, SourceID: "present", TargetID: "present"})

	if err := s.UpdateNode(ctx, &graph.Node{ID: "missing", Type: graph.NodeFile, Name: "b"}); err == nil {
		t.Error("UpdateNode of a missing ID returned no error")
	}
	// A failed update does not create the node.
	if _, err := s.GetNode(ctx, "missing"); err == nil {
		t.Error("UpdateNode of a missing ID created it")
	}
	if err := s.DeleteNode(ctx, "missing"); err == nil {
		t.Error("DeleteNode of a missing ID returned no error")
	}
	if err := s.DeleteEdge(ctx, "missing"); err == nil {
		t.Error("DeleteEdge of a missing ID returned no error")
	}

	// Nothing that exists was touched.
	stats := mustStats(t, s)
	if stats.NodeCount != 1 || stats.EdgeCount != 1 {
		t.Errorf("Stats = %d nodes, %d edges after failed mutations, want 1 and 1", stats.NodeCount, stats.EdgeCount)
	}
}

func testUpdateNodeReplacesRecordAndIndexes(t *testing.T, s graph.Store) {
	ctx := context.Background()
	mustAddNode(t, s, &graph.Node{
		ID: "n1", Type: graph.NodeFunction, Name: "foo", FilePath: "a.go", Package: "pkg1",
		Language: "go", Properties: map[string]string{"gone": "soon"},
	})
	mustAddNode(t, s, &graph.Node{ID: "n2", Type: graph.NodeFunction, Name: "other", FilePath: "a.go", Package: "pkg1"})

	updated := &graph.Node{
		ID: "n1", Type: graph.NodeMethod, Name: "bar", FilePath: "b.go", Package: "pkg2",
		Language: "go", Properties: map[string]string{"kept": "yes"},
	}
	if err := s.UpdateNode(ctx, updated); err != nil {
		t.Fatalf("UpdateNode: %v", err)
	}
	got, err := s.GetNode(ctx, "n1")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	assertNodeEqual(t, "GetNode after update", updated, got)

	// The old indexed values no longer find it; the new ones do.
	assertQueryIDs(t, s, graph.NodeFilter{Package: "pkg1"}, "n2")
	assertQueryIDs(t, s, graph.NodeFilter{FilePath: "a.go"}, "n2")
	assertQueryIDs(t, s, graph.NodeFilter{Type: graph.NodeFunction}, "n2")
	assertQueryIDs(t, s, graph.NodeFilter{Package: "pkg2"}, "n1")
	assertQueryIDs(t, s, graph.NodeFilter{FilePath: "b.go"}, "n1")
	assertQueryIDs(t, s, graph.NodeFilter{Type: graph.NodeMethod}, "n1")
	assertQueryIDs(t, s, graph.NodeFilter{Properties: map[string]string{"gone": ""}})

	// DeleteByFile follows the updated path, not the old one.
	if err := s.DeleteByFile(ctx, "a.go"); err != nil {
		t.Fatalf("DeleteByFile a.go: %v", err)
	}
	assertQueryIDs(t, s, graph.NodeFilter{}, "n1")
	if err := s.DeleteByFile(ctx, "b.go"); err != nil {
		t.Fatalf("DeleteByFile b.go: %v", err)
	}
	assertQueryIDs(t, s, graph.NodeFilter{})
}

func testDeleteNodeCascadesEdges(t *testing.T, s graph.Store) {
	ctx := context.Background()
	for _, id := range []string{"a", "b", "c"} {
		mustAddNode(t, s, &graph.Node{ID: id, Type: graph.NodeFunction, Name: id})
	}
	mustAddEdge(t, s, &graph.Edge{ID: "a-b", Type: graph.EdgeCalls, SourceID: "a", TargetID: "b"})
	mustAddEdge(t, s, &graph.Edge{ID: "c-a", Type: graph.EdgeImports, SourceID: "c", TargetID: "a"})
	mustAddEdge(t, s, &graph.Edge{ID: "a-a", Type: graph.EdgeCalls, SourceID: "a", TargetID: "a"})
	mustAddEdge(t, s, &graph.Edge{ID: "b-c", Type: graph.EdgeCalls, SourceID: "b", TargetID: "c"})

	if err := s.DeleteNode(ctx, "a"); err != nil {
		t.Fatalf("DeleteNode with a self-loop: %v", err)
	}
	if _, err := s.GetNode(ctx, "a"); err == nil {
		t.Error("GetNode of a deleted node returned no error")
	}
	assertEdgeIDs(t, s, "a", "")
	assertEdgeIDs(t, s, "b", "", "b-c")
	assertEdgeIDs(t, s, "c", "", "b-c")
	for _, id := range []string{"a-b", "c-a", "a-a"} {
		if err := s.DeleteEdge(ctx, id); err == nil {
			t.Errorf("DeleteEdge(%q) after its node was deleted returned no error", id)
		}
	}
	stats := mustStats(t, s)
	if stats.NodeCount != 2 || stats.EdgeCount != 1 {
		t.Errorf("Stats = %d nodes, %d edges, want 2 and 1", stats.NodeCount, stats.EdgeCount)
	}
}

func testNodesAreCopies(t *testing.T, s graph.Store) {
	ctx := context.Background()
	n := &graph.Node{
		ID: "n1", Type: graph.NodeFunction, Name: "Original", Package: "p",
		Properties: map[string]string{"k": "v"},
		Metrics:    map[string]float64{"m": 1},
	}
	mustAddNode(t, s, n)
	want := &graph.Node{
		ID: "n1", Type: graph.NodeFunction, Name: "Original", Package: "p",
		Properties: map[string]string{"k": "v"},
		Metrics:    map[string]float64{"m": 1},
	}

	// Mutating the caller's node after AddNode does not reach the store.
	n.Name = "Mutated"
	n.Properties["k"] = "changed"
	n.Properties["added"] = "x"
	n.Metrics["m"] = 99
	got, err := s.GetNode(ctx, "n1")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	assertNodeEqual(t, "after mutating the added node", want, got)

	// Nor does mutating a node the store returned.
	got.Name = "Mutated"
	got.Properties["k"] = "changed"
	got.Metrics["m"] = 99
	again, err := s.GetNode(ctx, "n1")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	assertNodeEqual(t, "after mutating a GetNode result", want, again)

	nodes, err := s.QueryNodes(ctx, graph.NodeFilter{Package: "p"})
	if err != nil || len(nodes) != 1 {
		t.Fatalf("QueryNodes = %d nodes, %v; want 1", len(nodes), err)
	}
	nodes[0].Name = "Mutated"
	nodes[0].Properties["k"] = "changed"
	again, err = s.GetNode(ctx, "n1")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	assertNodeEqual(t, "after mutating a QueryNodes result", want, again)
}

func testEdgesAreCopies(t *testing.T, s graph.Store) {
	e := &graph.Edge{ID: "e1", Type: graph.EdgeCalls, SourceID: "a", TargetID: "b", Properties: map[string]string{"k": "v"}}
	mustAddEdge(t, s, e)
	e.TargetID = "z"
	e.Properties["k"] = "changed"

	edges := mustGetEdges(t, s, "a", "")
	if len(edges) != 1 {
		t.Fatalf("GetEdges = %d edges, want 1", len(edges))
	}
	want := &graph.Edge{ID: "e1", Type: graph.EdgeCalls, SourceID: "a", TargetID: "b", Properties: map[string]string{"k": "v"}}
	assertEdgeEqual(t, "after mutating the added edge", want, edges[0])

	edges[0].Properties["k"] = "changed"
	edges[0].TargetID = "z"
	edges = mustGetEdges(t, s, "a", "")
	if len(edges) != 1 {
		t.Fatalf("GetEdges = %d edges, want 1", len(edges))
	}
	assertEdgeEqual(t, "after mutating a GetEdges result", want, edges[0])
}

func testAddEdgeAcceptsDanglingEndpoints(t *testing.T, s graph.Store) {
	ctx := context.Background()
	if err := s.AddEdge(ctx, &graph.Edge{ID: "e1", Type: graph.EdgeCalls, SourceID: "ghost-src", TargetID: "ghost-dst"}); err != nil {
		t.Fatalf("AddEdge between missing nodes: %v", err)
	}
	assertEdgeIDs(t, s, "ghost-src", "", "e1")
	assertEdgeIDs(t, s, "ghost-dst", "", "e1")
	// Neither endpoint exists, so there is no neighbour to return.
	if got := mustNeighbors(t, s, "ghost-src", "", graph.Both); len(got) != 0 {
		t.Errorf("GetNeighbors of a dangling edge = %v, want none", nodeIDs(got))
	}
	stats := mustStats(t, s)
	if stats.NodeCount != 0 || stats.EdgeCount != 1 {
		t.Errorf("Stats = %d nodes, %d edges, want 0 and 1", stats.NodeCount, stats.EdgeCount)
	}
	if err := s.DeleteEdge(ctx, "e1"); err != nil {
		t.Errorf("DeleteEdge of a dangling edge: %v", err)
	}
	assertEdgeIDs(t, s, "ghost-src", "")
}

func testGetEdgesOutgoingThenIncoming(t *testing.T, s graph.Store) {
	for _, id := range []string{"x", "a", "b", "c", "d"} {
		mustAddNode(t, s, &graph.Node{ID: id, Type: graph.NodeFunction, Name: id})
	}
	all := []*graph.Edge{
		{ID: "x-a", Type: graph.EdgeCalls, SourceID: "x", TargetID: "a", Properties: map[string]string{"line": "7"}},
		{ID: "x-b", Type: graph.EdgeImports, SourceID: "x", TargetID: "b"},
		{ID: "x-ghost", Type: graph.EdgeCalls, SourceID: "x", TargetID: "ghost"},
		{ID: "x-x", Type: graph.EdgeCalls, SourceID: "x", TargetID: "x"},
		{ID: "c-x", Type: graph.EdgeCalls, SourceID: "c", TargetID: "x"},
		{ID: "d-x", Type: graph.EdgeContains, SourceID: "d", TargetID: "x"},
		{ID: "a-b", Type: graph.EdgeCalls, SourceID: "a", TargetID: "b"},
	}
	for _, e := range all {
		mustAddEdge(t, s, e)
	}

	edges := mustGetEdges(t, s, "x", "")
	assertSameIDs(t, `GetEdges("x", "")`, edgeIDs(edges), "x-a", "x-b", "x-ghost", "x-x", "c-x", "d-x")
	assertOutgoingFirst(t, "x", edges)
	for _, e := range edges {
		if e.ID == "x-a" {
			assertEdgeEqual(t, "x-a", all[0], e)
		}
	}

	calls := mustGetEdges(t, s, "x", graph.EdgeCalls)
	assertSameIDs(t, `GetEdges("x", Calls)`, edgeIDs(calls), "x-a", "x-ghost", "x-x", "c-x")
	assertOutgoingFirst(t, "x", calls)
	assertEdgeIDs(t, s, "x", graph.EdgeContains, "d-x")
	assertEdgeIDs(t, s, "x", graph.EdgeTests)
	assertEdgeIDs(t, s, "nobody", "")
	assertEdgeIDs(t, s, "ghost", "", "x-ghost")
}

func testGetNeighbors(t *testing.T, s graph.Store) {
	for _, id := range []string{"x", "a", "b", "c", "d"} {
		mustAddNode(t, s, &graph.Node{ID: id, Type: graph.NodeFunction, Name: id})
	}
	for _, e := range []*graph.Edge{
		{ID: "x-a", Type: graph.EdgeCalls, SourceID: "x", TargetID: "a"},
		{ID: "x-b", Type: graph.EdgeImports, SourceID: "x", TargetID: "b"},
		{ID: "x-ghost", Type: graph.EdgeCalls, SourceID: "x", TargetID: "ghost"},
		{ID: "x-x", Type: graph.EdgeCalls, SourceID: "x", TargetID: "x"},
		{ID: "a-x", Type: graph.EdgeCalls, SourceID: "a", TargetID: "x"},
		{ID: "c-x", Type: graph.EdgeCalls, SourceID: "c", TargetID: "x"},
		{ID: "d-x", Type: graph.EdgeContains, SourceID: "d", TargetID: "x"},
		{ID: "ghost2-x", Type: graph.EdgeCalls, SourceID: "ghost2", TargetID: "x"},
	} {
		mustAddEdge(t, s, e)
	}

	tests := []struct {
		name      string
		edgeType  graph.EdgeType
		direction graph.Direction
		want      []string
	}{
		{"outgoing all types", "", graph.Outgoing, []string{"a", "b", "x"}},
		{"outgoing calls", graph.EdgeCalls, graph.Outgoing, []string{"a", "x"}},
		{"incoming all types", "", graph.Incoming, []string{"a", "c", "d", "x"}},
		{"incoming contains", graph.EdgeContains, graph.Incoming, []string{"d"}},
		{"both all types", "", graph.Both, []string{"a", "b", "c", "d", "x"}},
		{"both calls", graph.EdgeCalls, graph.Both, []string{"a", "c", "x"}},
		{"both of an unused type", graph.EdgeTests, graph.Both, nil},
	}
	for _, tc := range tests {
		got := mustNeighbors(t, s, "x", tc.edgeType, tc.direction)
		assertSameIDs(t, "GetNeighbors "+tc.name, nodeIDs(got), tc.want...)
	}
	if got := mustNeighbors(t, s, "nobody", "", graph.Both); len(got) != 0 {
		t.Errorf("GetNeighbors of an unknown node = %v, want none", nodeIDs(got))
	}
}

func testQueryNodesFilters(t *testing.T, s graph.Store) {
	for _, n := range []*graph.Node{
		{
			ID: "n1", Type: graph.NodeFunction, Name: "HandleAuth", FilePath: "auth/handler.go",
			Package: "auth", Language: "go", Exported: true,
			Properties: map[string]string{graph.PropArchRole: "controller", graph.PropDesignPattern: "factory,singleton"},
		},
		{
			ID: "n2", Type: graph.NodeFunction, Name: "handleLogin", FilePath: "auth/handler.go",
			Package: "auth", Language: "go",
			Properties: map[string]string{graph.PropArchRole: "service"},
		},
		{ID: "n3", Type: graph.NodeMethod, Name: "Serve", FilePath: "server/server.go", Package: "server", Language: "Go", Exported: true},
		{
			ID: "n4", Type: graph.NodeClass, Name: "AuthService", FilePath: "auth/service.py",
			Package: "auth", Language: "python", Exported: true,
			Properties: map[string]string{graph.PropDesignPattern: ""},
		},
		{ID: "n5", Type: graph.NodeDocument, Name: "README.md", FilePath: "README.md", Properties: map[string]string{graph.PropLayerTag: "docs"}},
	} {
		mustAddNode(t, s, n)
	}
	yes, no := true, false

	tests := []struct {
		name   string
		filter graph.NodeFilter
		want   []string
	}{
		{"empty filter returns every node", graph.NodeFilter{}, []string{"n1", "n2", "n3", "n4", "n5"}},
		{"type", graph.NodeFilter{Type: graph.NodeFunction}, []string{"n1", "n2"}},
		{"type is case-sensitive", graph.NodeFilter{Type: "function"}, nil},
		{"type with no nodes", graph.NodeFilter{Type: graph.NodeEnum}, nil},
		{"file path", graph.NodeFilter{FilePath: "auth/handler.go"}, []string{"n1", "n2"}},
		{"file path is exact, not a prefix", graph.NodeFilter{FilePath: "auth"}, nil},
		{"file path is case-sensitive", graph.NodeFilter{FilePath: "Auth/handler.go"}, nil},
		{"package", graph.NodeFilter{Package: "auth"}, []string{"n1", "n2", "n4"}},
		{"package is case-sensitive", graph.NodeFilter{Package: "Auth"}, nil},
		{"language", graph.NodeFilter{Language: "go"}, []string{"n1", "n2"}},
		{"language is case-sensitive", graph.NodeFilter{Language: "Go"}, []string{"n3"}},
		{"name glob", graph.NodeFilter{NamePattern: "Handle*"}, []string{"n1"}},
		{"name glob in the middle", graph.NodeFilter{NamePattern: "*Auth*"}, []string{"n1", "n4"}},
		{"name glob with a class", graph.NodeFilter{NamePattern: "[hH]andle*"}, []string{"n1", "n2"}},
		{"name glob matches the whole name", graph.NodeFilter{NamePattern: "Serv"}, nil},
		{"name glob with a dot", graph.NodeFilter{NamePattern: "README.md"}, []string{"n5"}},
		{"malformed name glob matches nothing", graph.NodeFilter{NamePattern: "["}, nil},
		{"exported", graph.NodeFilter{Exported: &yes}, []string{"n1", "n3", "n4"}},
		{"unexported", graph.NodeFilter{Exported: &no}, []string{"n2", "n5"}},
		{"property equal", graph.NodeFilter{Properties: map[string]string{graph.PropArchRole: "controller"}}, []string{"n1"}},
		{"property equal to a list", graph.NodeFilter{Properties: map[string]string{graph.PropDesignPattern: "factory,singleton"}}, []string{"n1"}},
		{"property list member", graph.NodeFilter{Properties: map[string]string{graph.PropDesignPattern: "singleton"}}, []string{"n1"}},
		{"property substring", graph.NodeFilter{Properties: map[string]string{graph.PropDesignPattern: "ory,sin"}}, []string{"n1"}},
		{"property value is case-sensitive", graph.NodeFilter{Properties: map[string]string{graph.PropArchRole: "Controller"}}, nil},
		{"empty property value matches any node with the key", graph.NodeFilter{Properties: map[string]string{graph.PropDesignPattern: ""}}, []string{"n1", "n4"}},
		{"properties are ANDed", graph.NodeFilter{Properties: map[string]string{graph.PropArchRole: "controller", graph.PropDesignPattern: "factory"}}, []string{"n1"}},
		{"properties ANDed with no match", graph.NodeFilter{Properties: map[string]string{graph.PropArchRole: "service", graph.PropDesignPattern: "factory"}}, nil},
		{"absent property key", graph.NodeFilter{Properties: map[string]string{"absent": ""}}, nil},
		{"empty properties map filters nothing", graph.NodeFilter{Properties: map[string]string{}}, []string{"n1", "n2", "n3", "n4", "n5"}},
		{"type, package and exported", graph.NodeFilter{Type: graph.NodeFunction, Package: "auth", Exported: &no}, []string{"n2"}},
		{"file path and type", graph.NodeFilter{FilePath: "auth/handler.go", Type: graph.NodeFunction}, []string{"n1", "n2"}},
		{"file path and a type it lacks", graph.NodeFilter{FilePath: "auth/handler.go", Type: graph.NodeClass}, nil},
		{"package and language", graph.NodeFilter{Package: "auth", Language: "python"}, []string{"n4"}},
		{"type and name glob", graph.NodeFilter{Type: graph.NodeFunction, NamePattern: "handle*"}, []string{"n2"}},
		{"file path and property", graph.NodeFilter{FilePath: "auth/handler.go", Properties: map[string]string{graph.PropArchRole: "service"}}, []string{"n2"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertQueryIDs(t, s, tc.filter, tc.want...)
		})
	}

	// A name predicate composes with the other filters.
	byPrefix := graph.NodeFilter{
		Package:   "auth",
		NameMatch: func(name string) bool { return strings.HasPrefix(name, "Auth") || strings.HasPrefix(name, "handle") },
	}
	assertQueryIDs(t, s, byPrefix, "n2", "n4")
}

func testQueryNodesNameMatchOncePerNode(t *testing.T, s graph.Store) {
	names := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	for i, name := range names {
		mustAddNode(t, s, &graph.Node{ID: fmt.Sprintf("n%d", i), Type: graph.NodeFunction, Name: name})
	}
	seen := make(map[string]int)
	got, err := s.QueryNodes(context.Background(), graph.NodeFilter{
		NameMatch: func(name string) bool {
			seen[name]++
			return strings.Contains(name, "l")
		},
	})
	if err != nil {
		t.Fatalf("QueryNodes: %v", err)
	}
	assertSameIDs(t, "QueryNodes with NameMatch", nodeIDs(got), "n0", "n3", "n4")
	want := make(map[string]int)
	for _, name := range names {
		want[name] = 1
	}
	if !maps.Equal(seen, want) {
		t.Errorf("NameMatch calls by name = %v, want each node's name once: %v", seen, want)
	}
}

func testDeleteByFile(t *testing.T, s graph.Store) {
	ctx := context.Background()
	for _, n := range []*graph.Node{
		{ID: "f1a", Type: graph.NodeFunction, Name: "A", FilePath: "f1.go"},
		{ID: "f1b", Type: graph.NodeFunction, Name: "B", FilePath: "f1.go"},
		{ID: "f2", Type: graph.NodeFunction, Name: "C", FilePath: "f2.go"},
		{ID: "nofile", Type: graph.NodeTopic, Name: "topic"},
	} {
		mustAddNode(t, s, n)
	}
	for _, e := range []*graph.Edge{
		{ID: "f1a-f1b", Type: graph.EdgeCalls, SourceID: "f1a", TargetID: "f1b"},
		{ID: "f1a-f2", Type: graph.EdgeCalls, SourceID: "f1a", TargetID: "f2"},
		{ID: "f2-f1b", Type: graph.EdgeCalls, SourceID: "f2", TargetID: "f1b"},
		{ID: "f2-nofile", Type: graph.EdgeHasTopic, SourceID: "f2", TargetID: "nofile"},
		{ID: "nofile-ghost", Type: graph.EdgeRelatedTo, SourceID: "nofile", TargetID: "ghost"},
	} {
		mustAddEdge(t, s, e)
	}

	// An unknown path, and the empty path, delete nothing.
	for _, path := range []string{"unknown.go", "", "f1"} {
		if err := s.DeleteByFile(ctx, path); err != nil {
			t.Fatalf("DeleteByFile(%q): %v", path, err)
		}
	}
	assertQueryIDs(t, s, graph.NodeFilter{}, "f1a", "f1b", "f2", "nofile")

	if err := s.DeleteByFile(ctx, "f1.go"); err != nil {
		t.Fatalf("DeleteByFile: %v", err)
	}
	assertQueryIDs(t, s, graph.NodeFilter{}, "f2", "nofile")
	assertEdgeIDs(t, s, "f2", "", "f2-nofile")
	assertEdgeIDs(t, s, "nofile", "", "f2-nofile", "nofile-ghost")
	assertEdgeIDs(t, s, "f1a", "")
	assertEdgeIDs(t, s, "f1b", "")
	stats := mustStats(t, s)
	if stats.NodeCount != 2 || stats.EdgeCount != 2 {
		t.Errorf("Stats = %d nodes, %d edges, want 2 and 2", stats.NodeCount, stats.EdgeCount)
	}
}

func testStats(t *testing.T, s graph.Store) {
	ctx := context.Background()
	stats := mustStats(t, s)
	if stats.NodeCount != 0 || stats.EdgeCount != 0 || len(stats.NodesByType) != 0 || len(stats.EdgesByType) != 0 {
		t.Errorf("Stats of an empty store = %+v, want all zero", stats)
	}

	for _, n := range []*graph.Node{
		{ID: "f1", Type: graph.NodeFunction, Name: "f1"},
		{ID: "f2", Type: graph.NodeFunction, Name: "f2"},
		{ID: "s1", Type: graph.NodeStruct, Name: "s1"},
		{ID: "d1", Type: graph.NodeDocument, Name: "d1"},
	} {
		mustAddNode(t, s, n)
	}
	for _, e := range []*graph.Edge{
		{ID: "e1", Type: graph.EdgeCalls, SourceID: "f1", TargetID: "f2"},
		{ID: "e2", Type: graph.EdgeCalls, SourceID: "f2", TargetID: "ghost"},
		{ID: "e3", Type: graph.EdgeDocuments, SourceID: "d1", TargetID: "s1"},
		{ID: "e4", Type: graph.EdgeContains, SourceID: "ghost1", TargetID: "ghost2"},
	} {
		mustAddEdge(t, s, e)
	}
	stats = mustStats(t, s)
	wantNodes := map[graph.NodeType]int64{graph.NodeFunction: 2, graph.NodeStruct: 1, graph.NodeDocument: 1}
	wantEdges := map[graph.EdgeType]int64{graph.EdgeCalls: 2, graph.EdgeDocuments: 1, graph.EdgeContains: 1}
	if stats.NodeCount != 4 || stats.EdgeCount != 4 {
		t.Errorf("Stats = %d nodes, %d edges, want 4 and 4 (dangling edges count)", stats.NodeCount, stats.EdgeCount)
	}
	if !maps.Equal(stats.NodesByType, wantNodes) {
		t.Errorf("NodesByType = %v, want %v", stats.NodesByType, wantNodes)
	}
	if !maps.Equal(stats.EdgesByType, wantEdges) {
		t.Errorf("EdgesByType = %v, want %v", stats.EdgesByType, wantEdges)
	}

	if err := s.DeleteEdge(ctx, "e4"); err != nil {
		t.Fatalf("DeleteEdge: %v", err)
	}
	if err := s.DeleteNode(ctx, "s1"); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	stats = mustStats(t, s)
	wantNodes = map[graph.NodeType]int64{graph.NodeFunction: 2, graph.NodeDocument: 1}
	wantEdges = map[graph.EdgeType]int64{graph.EdgeCalls: 2}
	if stats.NodeCount != 3 || stats.EdgeCount != 2 {
		t.Errorf("Stats after deletes = %d nodes, %d edges, want 3 and 2", stats.NodeCount, stats.EdgeCount)
	}
	if !maps.Equal(stats.NodesByType, wantNodes) {
		t.Errorf("NodesByType after deletes = %v, want %v", stats.NodesByType, wantNodes)
	}
	if !maps.Equal(stats.EdgesByType, wantEdges) {
		t.Errorf("EdgesByType after deletes = %v, want %v", stats.EdgesByType, wantEdges)
	}
}

// --- helpers ---

func mustAddNode(t *testing.T, s graph.Store, n *graph.Node) {
	t.Helper()
	if err := s.AddNode(context.Background(), n); err != nil {
		t.Fatalf("AddNode(%s): %v", n.ID, err)
	}
}

func mustAddEdge(t *testing.T, s graph.Store, e *graph.Edge) {
	t.Helper()
	if err := s.AddEdge(context.Background(), e); err != nil {
		t.Fatalf("AddEdge(%s): %v", e.ID, err)
	}
}

func mustGetEdges(t *testing.T, s graph.Store, nodeID string, edgeType graph.EdgeType) []*graph.Edge {
	t.Helper()
	edges, err := s.GetEdges(context.Background(), nodeID, edgeType)
	if err != nil {
		t.Fatalf("GetEdges(%q, %q): %v", nodeID, edgeType, err)
	}
	return edges
}

func mustNeighbors(t *testing.T, s graph.Store, nodeID string, edgeType graph.EdgeType, dir graph.Direction) []*graph.Node {
	t.Helper()
	nodes, err := s.GetNeighbors(context.Background(), nodeID, edgeType, dir)
	if err != nil {
		t.Fatalf("GetNeighbors(%q, %q, %d): %v", nodeID, edgeType, dir, err)
	}
	return nodes
}

func mustStats(t *testing.T, s graph.Store) *graph.GraphStats {
	t.Helper()
	stats, err := s.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	return stats
}

// assertQueryIDs checks that QueryNodes returns exactly the given IDs, each
// once, in any order, and no error.
func assertQueryIDs(t *testing.T, s graph.Store, f graph.NodeFilter, want ...string) {
	t.Helper()
	nodes, err := s.QueryNodes(context.Background(), f)
	if err != nil {
		t.Fatalf("QueryNodes(%+v): %v", f, err)
	}
	assertSameIDs(t, fmt.Sprintf("QueryNodes(%+v)", f), nodeIDs(nodes), want...)
}

// assertEdgeIDs checks that GetEdges returns exactly the given edge IDs.
func assertEdgeIDs(t *testing.T, s graph.Store, nodeID string, edgeType graph.EdgeType, want ...string) {
	t.Helper()
	assertSameIDs(t, fmt.Sprintf("GetEdges(%q, %q)", nodeID, edgeType), edgeIDs(mustGetEdges(t, s, nodeID, edgeType)), want...)
}

// assertSameIDs compares two ID lists as sets, and fails on duplicates.
func assertSameIDs(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	g := slices.Clone(got)
	w := slices.Clone(want)
	slices.Sort(g)
	slices.Sort(w)
	if !slices.Equal(g, w) {
		t.Errorf("%s = %v, want %v", what, g, w)
	}
	if c := slices.Compact(slices.Clone(g)); len(c) != len(g) {
		t.Errorf("%s returned duplicates: %v", what, g)
	}
}

// assertOutgoingFirst checks that every edge leaving nodeID comes before
// every edge only arriving at it.
func assertOutgoingFirst(t *testing.T, nodeID string, edges []*graph.Edge) {
	t.Helper()
	incoming := false
	for _, e := range edges {
		switch {
		case e.SourceID == nodeID && incoming:
			t.Errorf("outgoing edge %s follows an incoming one in %v", e.ID, edgeIDs(edges))
		case e.SourceID != nodeID:
			incoming = true
		}
	}
}

func nodeIDs(nodes []*graph.Node) []string {
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	return ids
}

func edgeIDs(edges []*graph.Edge) []string {
	ids := make([]string, 0, len(edges))
	for _, e := range edges {
		ids = append(ids, e.ID)
	}
	return ids
}

// normalizeProps drops the read-time source tag and maps an empty map to nil.
func normalizeProps(p map[string]string) map[string]string {
	out := make(map[string]string, len(p))
	for k, v := range p {
		if k != graph.PropGraphSource {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func assertNodeEqual(t *testing.T, what string, want, got *graph.Node) {
	t.Helper()
	if got == nil {
		t.Errorf("%s: got nil node, want %+v", what, want)
		return
	}
	if !want.UpdatedAt.Equal(got.UpdatedAt) {
		t.Errorf("%s: UpdatedAt = %v, want %v", what, got.UpdatedAt, want.UpdatedAt)
	}
	w, g := *want, *got
	w.UpdatedAt, g.UpdatedAt = time.Time{}, time.Time{}
	w.Properties, g.Properties = normalizeProps(w.Properties), normalizeProps(g.Properties)
	if len(w.Metrics) == 0 {
		w.Metrics = nil
	}
	if len(g.Metrics) == 0 {
		g.Metrics = nil
	}
	if !reflect.DeepEqual(w, g) {
		t.Errorf("%s:\n got  %+v\n want %+v", what, g, w)
	}
}

func assertEdgeEqual(t *testing.T, what string, want, got *graph.Edge) {
	t.Helper()
	w, g := *want, *got
	w.Properties, g.Properties = normalizeProps(w.Properties), normalizeProps(g.Properties)
	if !reflect.DeepEqual(w, g) {
		t.Errorf("%s:\n got  %+v\n want %+v", what, g, w)
	}
}
