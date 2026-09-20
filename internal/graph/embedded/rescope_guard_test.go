package embedded

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

func addNode(t *testing.T, s *BranchStore, typ graph.NodeType, name string) {
	t.Helper()
	err := s.AddNode(context.Background(), &graph.Node{
		ID:            graph.NewNodeID(string(typ), "f.go", name),
		Type:          typ,
		Name:          name,
		QualifiedName: name,
		FilePath:      "f.go",
	})
	if err != nil {
		t.Fatalf("add %s %q: %v", typ, name, err)
	}
}

// TestRescopeRefusesScopeHoldingCode covers the case that would lose a
// codebase.
//
// A key carries its scope but not its type, so a scope is moved whole; and the
// meeting scope is a fallback read for every branch. Moving a branch that was
// also used for `codeeagle sync` would therefore relocate that branch's entire
// code graph into the meeting scope, delete it from the branch, and leak it
// into every other branch's reads — silently.
func TestRescopeRefusesScopeHoldingCode(t *testing.T) {
	store, err := NewBranchStore(filepath.Join(t.TempDir(), "db"), "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	addNode(t, store, graph.NodeMeeting, "Standup")
	addNode(t, store, graph.NodeSpeaker, "Person 1")
	addNode(t, store, graph.NodeFunction, "ServeHTTP")
	addNode(t, store, graph.NodeFile, "server.go")

	_, err = store.Rescope("main", MeetingScope, false)
	if err == nil {
		t.Fatal("Rescope moved a scope holding a codebase; want a refusal")
	}
	for _, want := range []string{"Function", "File"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %s: %v", want, err)
		}
	}

	// Nothing may have moved.
	counts, err := store.ScopeNodeTypes("main")
	if err != nil {
		t.Fatal(err)
	}
	if counts[graph.NodeFunction] != 1 || counts[graph.NodeMeeting] != 1 {
		t.Errorf("source scope changed: %v", counts)
	}
	moved, err := store.ScopeNodeTypes(MeetingScope)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 0 {
		t.Errorf("target scope received %v, want nothing", moved)
	}
}

// TestRescopeAllowsMeetingOnlyScope covers the case the command exists for: a
// database used only for transcripts, where the corpus sits under a branch
// scope because it was indexed before meetings had their own.
func TestRescopeAllowsMeetingOnlyScope(t *testing.T) {
	store, err := NewBranchStore(filepath.Join(t.TempDir(), "db"), "old-branch", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	addNode(t, store, graph.NodeMeeting, "Standup")
	addNode(t, store, graph.NodeSpeaker, "Person 1")
	addNode(t, store, graph.NodeDecision, "Ship on Thursday")
	addNode(t, store, graph.NodeActionItem, "Add the index back")
	// Shared with code indexing, but not evidence of it on their own.
	addNode(t, store, graph.NodePerson, "Priya Raman")
	addNode(t, store, graph.NodeTopic, "retention job")
	addNode(t, store, graph.NodeDate, "2026-07-10")

	res, err := store.Rescope("old-branch", MeetingScope, false)
	if err != nil {
		t.Fatalf("Rescope refused a meeting-only scope: %v", err)
	}
	if res.Keys == 0 {
		t.Fatal("Rescope moved nothing")
	}

	moved, err := store.ScopeNodeTypes(MeetingScope)
	if err != nil {
		t.Fatal(err)
	}
	if moved[graph.NodeMeeting] != 1 || moved[graph.NodePerson] != 1 {
		t.Errorf("meeting scope holds %v, want the whole corpus", moved)
	}
	left, err := store.ScopeNodeTypes("old-branch")
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Errorf("source scope still holds %v", left)
	}
}
