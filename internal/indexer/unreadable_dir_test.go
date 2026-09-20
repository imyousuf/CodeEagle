package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/watcher"
)

// TestParallelWalkReportsUnreadableDirectories is what stops a dropped mount
// from emptying the graph.
//
// The walk cannot tell a directory it may not read from one whose files were
// all deleted, and the caller deletes graph nodes for everything it did not
// see. Reporting the difference is the only thing that keeps those two apart.
func TestParallelWalkReportsUnreadableDirectories(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every directory regardless of mode")
	}

	root := t.TempDir()
	readable := filepath.Join(root, "readable")
	locked := filepath.Join(root, "locked")
	for _, d := range []string{readable, locked} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(readable, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	matcher := watcher.NewGitIgnoreMatcher([]string{root}, nil)

	results, unreadable, err := parallelWalkDir(context.Background(), root, matcher, 4)
	if err != nil {
		t.Fatalf("parallelWalkDir: %v", err)
	}

	if len(unreadable) != 1 {
		t.Fatalf("expected 1 unreadable directory, got %d: %v", len(unreadable), unreadable)
	}
	if filepath.Base(unreadable[0]) != "locked" {
		t.Errorf("wrong directory reported: %s", unreadable[0])
	}

	var sawReadable bool
	for _, r := range results {
		if filepath.Base(r.absPath) == "a.txt" {
			sawReadable = true
		}
		if filepath.Base(r.absPath) == "b.txt" {
			t.Error("read a file from a directory that should be unreadable")
		}
	}
	if !sawReadable {
		t.Error("the readable subtree was not walked")
	}
}

func TestUnderAny(t *testing.T) {
	prefixes := []string{"docs/private/", "media/archive/"}
	cases := map[string]bool{
		"docs/private/a.md":   true,
		"media/archive/x.png": true,
		"docs/public/a.md":    false,
		"docs/privateer/a.md": false, // prefix must be a directory boundary
		"other/file.txt":      false,
	}
	for path, want := range cases {
		if got := underAny(path, prefixes); got != want {
			t.Errorf("underAny(%q) = %v, want %v", path, got, want)
		}
	}
	if underAny("anything", nil) {
		t.Error("underAny with no prefixes must be false")
	}
}
