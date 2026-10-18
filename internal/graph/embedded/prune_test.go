package embedded

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dgraph-io/badger/v4"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// pruneFixture is a graph with a directory that should not have been indexed
// (Downloads/mark), a sibling whose name merely starts the same way, and a
// file outside both, linked across the boundary. A second branch holds a node
// under the same path, which a prune of the default branch must not touch.
func pruneFixture(t *testing.T) *BranchStore {
	t.Helper()
	s := newTestStore(t)
	ctx := context.Background()

	nodes := []*graph.Node{
		{ID: "m1", Type: graph.NodeDocument, Name: "a.txt", FilePath: "Downloads/mark/a.txt"},
		{ID: "m2", Type: graph.NodeFunction, Name: "f", FilePath: "Downloads/mark/deep/x.go", Package: "x"},
		{ID: "m3", Type: graph.NodeFile, Name: "x.go", FilePath: "Downloads/mark/deep/x.go", Package: "x"},
		{ID: "k1", Type: graph.NodeDocument, Name: "plan.txt", FilePath: "Downloads/marketing/plan.txt"},
		{ID: "k2", Type: graph.NodeDocument, Name: "notes.md", FilePath: "Documents/notes.md"},
	}
	for _, n := range nodes {
		if err := s.AddNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	edges := []*graph.Edge{
		{ID: "e-inside", Type: graph.EdgeContains, SourceID: "m3", TargetID: "m2"},
		{ID: "e-into", Type: graph.EdgeDocuments, SourceID: "k2", TargetID: "m1"},
		{ID: "e-out", Type: graph.EdgeDuplicateOf, SourceID: "m1", TargetID: "k1"},
		{ID: "e-kept", Type: graph.EdgeDocuments, SourceID: "k2", TargetID: "k1"},
	}
	for _, e := range edges {
		if err := s.AddEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	other := &BranchStore{db: s.db, writeBranch: "meetings", readBranches: []string{"meetings"}}
	if err := other.AddNode(ctx, &graph.Node{ID: "o1", Type: graph.NodeDocument, Name: "a.txt", FilePath: "Downloads/mark/a.txt"}); err != nil {
		t.Fatal(err)
	}
	return s
}

// pruneInto runs PruneCopy into a fresh directory and opens the result.
func pruneInto(t *testing.T, s *BranchStore) (*BranchStore, PruneResult) {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "pruned")
	res, err := s.PruneCopy(context.Background(), dst, []string{"Downloads/mark"}, nil)
	if err != nil {
		t.Fatalf("PruneCopy: %v", err)
	}
	out, err := NewStore(dst)
	if err != nil {
		t.Fatalf("open the pruned copy: %v", err)
	}
	t.Cleanup(func() { out.Close() })
	return out, res
}

func TestPruneCopyLeavesOutOnlyThePrefix(t *testing.T) {
	s := pruneFixture(t)
	out, res := pruneInto(t, s)
	ctx := context.Background()

	if res.Files != 2 || res.Nodes != 3 || res.Edges != 3 {
		t.Errorf("result = %+v, want 2 files, 3 nodes, 3 edges", res)
	}

	for _, id := range []string{"m1", "m2", "m3"} {
		if _, err := out.GetNode(ctx, id); err == nil {
			t.Errorf("node %s under the prefix was copied", id)
		}
	}
	// "Downloads/marketing" shares the prefix as a string, not as a path.
	for _, id := range []string{"k1", "k2"} {
		if _, err := out.GetNode(ctx, id); err != nil {
			t.Errorf("node %s outside the prefix was left out: %v", id, err)
		}
	}

	// An edge from a kept node into the pruned tree goes with the tree, and
	// so do its index entries: one left behind would be returned by every
	// later traversal of k2, naming a node that does not exist.
	got, err := out.GetEdges(ctx, "k2", graph.EdgeDocuments)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "e-kept" {
		t.Errorf("k2's edges = %v, want only e-kept", got)
	}
	if in, _ := out.GetEdges(ctx, "k1", graph.EdgeDuplicateOf); len(in) != 0 {
		t.Errorf("an edge from the pruned tree into k1 survived: %v", in)
	}
	if nodes, _ := out.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeDocument}); len(nodes) != 2 {
		t.Errorf("the type index lists %d documents, want the 2 kept", len(nodes))
	}
	if left, _ := out.CountUnder("Downloads/mark"); left.Files != 0 || left.Nodes != 0 {
		t.Errorf("the file index still lists %+v under the prefix", left)
	}
}

func TestPruneCopyKeepsOtherBranches(t *testing.T) {
	s := pruneFixture(t)
	out, _ := pruneInto(t, s)

	other := &BranchStore{db: out.db, writeBranch: "meetings", readBranches: []string{"meetings"}}
	if _, err := other.GetNode(context.Background(), "o1"); err != nil {
		t.Errorf("a node on another branch, under the same path, was left out: %v", err)
	}
}

func TestPruneCopyLeavesTheSourceAlone(t *testing.T) {
	s := pruneFixture(t)
	pruneInto(t, s)

	for _, id := range []string{"m1", "m2", "m3", "k1", "k2"} {
		if _, err := s.GetNode(context.Background(), id); err != nil {
			t.Errorf("the source lost node %s", id)
		}
	}
}

// TestPruneCopyDropsDanglingEdgeIndexEntries covers an index entry whose edge
// is already gone -- the "Key not found" a sync logs when it later tries to
// delete through one.
func TestPruneCopyDropsDanglingEdgeIndexEntries(t *testing.T) {
	s := pruneFixture(t)
	ghost := indexEdgeKey(s.writeBranch, "k2", graph.EdgeCalls, "e-ghost")
	if err := s.db.Update(func(txn *badger.Txn) error { return txn.Set(ghost, nil) }); err != nil {
		t.Fatal(err)
	}

	out, res := pruneInto(t, s)
	if res.Dangling != 1 {
		t.Errorf("Dangling = %d, want 1", res.Dangling)
	}
	err := out.db.View(func(txn *badger.Txn) error {
		_, err := txn.Get(ghost)
		return err
	})
	if err == nil {
		t.Error("the dangling edge index entry was copied")
	}
}

func TestPruneCopyRefusesAnEmptyPrefix(t *testing.T) {
	s := pruneFixture(t)
	for _, prefix := range []string{"", "/", "."} {
		dst := filepath.Join(t.TempDir(), "pruned")
		if _, err := s.PruneCopy(context.Background(), dst, []string{prefix}, nil); err == nil {
			t.Errorf("PruneCopy(%q) did not refuse; it would leave out every file", prefix)
		}
		if _, err := s.CountUnder(prefix); err == nil {
			t.Errorf("CountUnder(%q) did not refuse", prefix)
		}
	}
}

func TestPruneCopyRefusesANonEmptyDestination(t *testing.T) {
	s := pruneFixture(t)
	dst := t.TempDir()
	if err := os.WriteFile(filepath.Join(dst, "keep-me"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PruneCopy(context.Background(), dst, []string{"Downloads/mark"}, nil); err == nil {
		t.Error("PruneCopy wrote into a directory that already held something")
	}
	if _, err := os.Stat(filepath.Join(dst, "keep-me")); err != nil {
		t.Errorf("the existing file is gone: %v", err)
	}
}

func TestCountUnder(t *testing.T) {
	s := pruneFixture(t)
	res, err := s.CountUnder("Downloads/mark/")
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 2 || res.Nodes != 3 {
		t.Errorf("CountUnder = %+v, want 2 files, 3 nodes", res)
	}
}
