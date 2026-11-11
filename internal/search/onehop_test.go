package search_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/search"
)

// oneHopFixture surrounds a service with edges of every kind OneHopEdges
// shows, and some it does not, spread over scopes.
//
//   - Contains: six outgoing edges, four in the current scope and two in
//     the default one. The third points at a node that does not exist.
//   - Exposes: one outgoing.
//   - Calls: a self-loop in the current scope, and one incoming in the
//     default scope.
//   - Tests: one incoming, from the relation scope.
//   - Implements: one outgoing, to an interface whose ID is in two scopes
//     under different names.
//   - Imports: none.
//   - Documents and DependsOn: present, but not among the types shown.
func oneHopFixture(t *testing.T) *embedded.BranchStore {
	t.Helper()
	nodes := []scopedNode{
		{scopeCurrent, &graph.Node{ID: "svc", Type: graph.NodeService, Name: "billing"}},
		{scopeCurrent, &graph.Node{ID: "f1", Type: graph.NodeFile, Name: "a.go"}},
		{scopeCurrent, &graph.Node{ID: "f2", Type: graph.NodeFile, Name: "b.go"}},
		{scopeCurrent, &graph.Node{ID: "f4", Type: graph.NodeFile, Name: "d.go"}},
		{scopeDefault, &graph.Node{ID: "f5", Type: graph.NodeFile, Name: "e.go"}},
		{scopeDefault, &graph.Node{ID: "f6", Type: graph.NodeFile, Name: "f.go"}},
		{scopeCurrent, &graph.Node{ID: "ep", Type: graph.NodeAPIEndpoint, Name: "POST /charge"}},
		{scopeDefault, &graph.Node{ID: "caller", Type: graph.NodeFunction, Name: "Checkout"}},
		{embedded.RelationScope, &graph.Node{ID: "tst", Type: graph.NodeTestFunction, Name: "TestBilling"}},
		{scopeCurrent, &graph.Node{ID: "iface", Type: graph.NodeInterface, Name: "Biller"}},
		{scopeDefault, &graph.Node{ID: "iface", Type: graph.NodeInterface, Name: "OldBiller"}},
		{scopeDefault, &graph.Node{ID: "doc", Type: graph.NodeDocument, Name: "Billing spec"}},
		{scopeDefault, &graph.Node{ID: "dep", Type: graph.NodeDependency, Name: "stripe"}},
	}
	edges := []scopedEdge{
		{scopeCurrent, &graph.Edge{ID: "c1", Type: graph.EdgeContains, SourceID: "svc", TargetID: "f1"}},
		{scopeCurrent, &graph.Edge{ID: "c2", Type: graph.EdgeContains, SourceID: "svc", TargetID: "f2"}},
		{scopeCurrent, &graph.Edge{ID: "c3", Type: graph.EdgeContains, SourceID: "svc", TargetID: "f3-missing"}},
		{scopeCurrent, &graph.Edge{ID: "c4", Type: graph.EdgeContains, SourceID: "svc", TargetID: "f4"}},
		{scopeDefault, &graph.Edge{ID: "c5", Type: graph.EdgeContains, SourceID: "svc", TargetID: "f5"}},
		{scopeDefault, &graph.Edge{ID: "c6", Type: graph.EdgeContains, SourceID: "svc", TargetID: "f6"}},
		{scopeCurrent, &graph.Edge{ID: "x1", Type: graph.EdgeExposes, SourceID: "svc", TargetID: "ep"}},
		{scopeCurrent, &graph.Edge{ID: "k1", Type: graph.EdgeCalls, SourceID: "svc", TargetID: "svc"}},
		{scopeDefault, &graph.Edge{ID: "k2", Type: graph.EdgeCalls, SourceID: "caller", TargetID: "svc"}},
		{embedded.RelationScope, &graph.Edge{ID: "t1", Type: graph.EdgeTests, SourceID: "tst", TargetID: "svc"}},
		{scopeCurrent, &graph.Edge{ID: "i1", Type: graph.EdgeImplements, SourceID: "svc", TargetID: "iface"}},
		{scopeDefault, &graph.Edge{ID: "d1", Type: graph.EdgeDocuments, SourceID: "doc", TargetID: "svc"}},
		{scopeDefault, &graph.Edge{ID: "d2", Type: graph.EdgeDependsOn, SourceID: "svc", TargetID: "dep"}},
	}
	store, _ := openScopedStore(t, nodes, edges)
	return store
}

// TestCharacterizeOneHopEdges pins what is shown and in what order: by edge
// type (Contains, Exposes, Calls, Tests, Implements, Imports), then as the
// store returns edges. At most five edges of a type are taken before peers
// are looked up, so a missing peer costs a slot: c3 is dropped and c6 never
// reached. A self-loop is one edge, reported as incoming. A peer is read by
// ID, so the current scope's name wins.
func TestCharacterizeOneHopEdges(t *testing.T) {
	store := oneHopFixture(t)
	ctx := context.Background()

	var got []string
	for _, e := range search.OneHopEdges(ctx, store, "svc") {
		got = append(got, fmt.Sprintf("%s %s %s %s", e.Direction, e.EdgeType, e.NodeName, e.NodeType))
	}
	diffLines(t, "OneHopEdges", got, []string{
		"out Contains a.go File",
		"out Contains b.go File",
		"out Contains d.go File",
		"out Contains e.go File",
		"out Exposes POST /charge APIEndpoint",
		"in Calls billing Service",
		"in Calls Checkout Function",
		"in Tests TestBilling TestFunction",
		"out Implements Biller Interface",
	})

	// From the other end, the same edges read as incoming.
	got = nil
	for _, e := range search.OneHopEdges(ctx, store, "iface") {
		got = append(got, fmt.Sprintf("%s %s %s %s", e.Direction, e.EdgeType, e.NodeName, e.NodeType))
	}
	diffLines(t, "OneHopEdges(iface)", got, []string{"in Implements billing Service"})

	if got := search.OneHopEdges(ctx, store, "dep"); got != nil {
		t.Errorf("OneHopEdges for a node with only unshown edge types = %v, want nil", got)
	}
	if got := search.OneHopEdges(ctx, store, "nowhere"); got != nil {
		t.Errorf("OneHopEdges for an unknown node = %v, want nil", got)
	}
}

// TestCharacterizeOneHopEdgesText pins the text form: four spaces, an arrow
// (→ outgoing, ← incoming), the edge type, the peer's name and its type in
// parentheses, one edge per line. It selects exactly what OneHopEdges does.
func TestCharacterizeOneHopEdgesText(t *testing.T) {
	store := oneHopFixture(t)
	ctx := context.Background()

	want := "    → Contains a.go (File)\n" +
		"    → Contains b.go (File)\n" +
		"    → Contains d.go (File)\n" +
		"    → Contains e.go (File)\n" +
		"    → Exposes POST /charge (APIEndpoint)\n" +
		"    ← Calls billing (Service)\n" +
		"    ← Calls Checkout (Function)\n" +
		"    ← Tests TestBilling (TestFunction)\n" +
		"    → Implements Biller (Interface)\n"
	if got := search.OneHopEdgesText(ctx, store, "svc"); got != want {
		t.Errorf("OneHopEdgesText:\ngot:\n%s\nwant:\n%s", got, want)
	}
	if got, want := search.OneHopEdgesText(ctx, store, "tst"), "    → Tests billing (Service)\n"; got != want {
		t.Errorf("OneHopEdgesText(tst) = %q, want %q", got, want)
	}
	if got := search.OneHopEdgesText(ctx, store, "dep"); got != "" {
		t.Errorf("OneHopEdgesText for a node with only unshown edge types = %q, want empty", got)
	}
	if got := search.OneHopEdgesText(ctx, store, "nowhere"); got != "" {
		t.Errorf("OneHopEdgesText for an unknown node = %q, want empty", got)
	}
}
