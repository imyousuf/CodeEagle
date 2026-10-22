package embedded

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// TestLeasedFreesTheLockBetweenUses covers a long-lived reader such as an MCP
// server: it holds the database while queries run, and lets a writer in once
// it has been idle.
func TestLeasedFreesTheLockBetweenUses(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	w, err := NewBranchStore(dir, "main", []string{"main"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddNode(ctx, &graph.Node{ID: "n1", Type: graph.NodeDocument, Name: "spec.md"}); err != nil {
		t.Fatal(err)
	}
	w.Close()

	l := NewLeased(func() (*BranchStore, error) { return NewReadOnlyBranchStore(dir, "main", []string{"main"}) }, 50*time.Millisecond)
	defer l.Close()
	if n, err := l.GetNode(ctx, "n1"); err != nil || n == nil {
		t.Fatalf("GetNode: %v", err)
	}
	if !l.Held() {
		t.Fatal("the database was closed straight after a query; a burst of queries would reopen it each time")
	}
	if _, err := NewBranchStore(dir, "main", []string{"main"}); !IsLocked(err) {
		t.Fatalf("a writer opened the database while a reader held it (err %v); the test cannot show anything", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for l.Held() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	w, err = NewBranchStore(dir, "main", []string{"main"})
	if err != nil {
		t.Fatalf("a writer could not open the database after the reader went idle: %v", err)
	}
	if err := w.AddNode(ctx, &graph.Node{ID: "n2", Type: graph.NodeDocument, Name: "later.md"}); err != nil {
		t.Fatal(err)
	}
	w.Close()

	if n, err := l.GetNode(ctx, "n2"); err != nil || n == nil {
		t.Errorf("the reader did not see what was written while it was idle: %v", err)
	}
}

// TestLeasedReportsBusyWhileAWriterHoldsTheDatabase covers a sync that holds
// the database for longer than a reader waits.
func TestLeasedReportsBusyWhileAWriterHoldsTheDatabase(t *testing.T) {
	dir := t.TempDir()
	w, err := NewBranchStore(dir, "main", []string{"main"})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	l := NewLeased(func() (*BranchStore, error) { return NewReadOnlyBranchStore(dir, "main", []string{"main"}) }, time.Second)
	defer l.Close()
	if _, err := l.GetNode(context.Background(), "n1"); !errors.Is(err, ErrBusy) {
		t.Errorf("err = %v, want ErrBusy", err)
	}
}
