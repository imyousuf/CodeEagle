package indexer

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

// TestCleanupStaleBranchesSparesReservedScopes guards the meeting corpus
// against the branch cleaner.
//
// Cleanup deletes every scope git no longer lists as a branch. The meeting
// scope is deliberately named so that it can never be a git branch, which
// means it is never listed — so without an exemption it looks permanently
// stale, and indexing code silently destroys every meeting ever recorded.
func TestCleanupStaleBranchesSparesReservedScopes(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"commit", "--allow-empty", "-m", "first"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v (%s)", args, err, out)
		}
	}

	dbPath := filepath.Join(t.TempDir(), "graph.db")
	ctx := context.Background()

	// Badger holds an exclusive lock per directory, so the two scopes are
	// written one after the other rather than side by side.
	stale, err := embedded.NewBranchStore(dbPath, "gone-branch", []string{"gone-branch"})
	if err != nil {
		t.Fatalf("NewBranchStore(stale): %v", err)
	}
	if err := stale.AddNode(ctx, &graph.Node{
		ID: "file-1", Type: graph.NodeFile, Name: "main.go",
	}); err != nil {
		t.Fatalf("AddNode(stale): %v", err)
	}
	stale.Close()

	store, err := embedded.NewBranchStore(dbPath, embedded.MeetingScope,
		[]string{embedded.MeetingScope})
	if err != nil {
		t.Fatalf("NewBranchStore: %v", err)
	}
	defer store.Close()

	if err := store.AddNode(ctx, &graph.Node{
		ID: "meeting-1", Type: graph.NodeMeeting, Name: "Standup",
	}); err != nil {
		t.Fatalf("AddNode: %v", err)
	}

	state := &SyncState{BranchStates: map[string]*BranchSyncState{}}
	if err := CleanupStaleBranches(ctx, store, repo, state, nil); err != nil {
		t.Fatalf("CleanupStaleBranches: %v", err)
	}

	got, err := store.GetNode(ctx, "meeting-1")
	if err != nil {
		t.Fatalf("GetNode after cleanup: %v", err)
	}
	if got == nil {
		t.Fatal("the meeting scope was deleted by branch cleanup")
	}

	scopes, err := store.ListBranches()
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	for _, s := range scopes {
		if s == "gone-branch" {
			t.Error("cleanup no longer removes a genuinely dead branch")
		}
	}
}

func TestIsReservedScope(t *testing.T) {
	cases := map[string]bool{
		embedded.MeetingScope: true,
		"@anything":           true,
		"main":                false,
		"default":             false,
		"feature/at@sign":     false,
	}
	for name, want := range cases {
		if got := embedded.IsReservedScope(name); got != want {
			t.Errorf("IsReservedScope(%q) = %v, want %v", name, got, want)
		}
	}
}
