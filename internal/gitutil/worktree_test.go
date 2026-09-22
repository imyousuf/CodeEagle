package gitutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// newRepo makes a throwaway repository with one commit.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v: %s", err, out)
		}
	}

	write(t, dir, "tracked.go", "package main\n")
	write(t, dir, ".gitignore", "ignored.txt\nbuild/\n")
	commit(t, dir, "initial")
	return dir
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, dir, msg string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", msg}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func TestCleanTreeReportsNothing(t *testing.T) {
	dir := newRepo(t)

	changed, deleted, err := GetWorkingTreeChanges(dir)
	if err != nil {
		t.Fatalf("GetWorkingTreeChanges: %v", err)
	}
	if len(changed) != 0 || len(deleted) != 0 {
		t.Errorf("clean tree reported changed=%v deleted=%v", changed, deleted)
	}
}

// TestUntrackedFileIsReported is the case the worker depends on: a file just
// written and never committed.
func TestUntrackedFileIsReported(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "brand-new.md", "# new\n")

	changed, _, err := GetWorkingTreeChanges(dir)
	if err != nil {
		t.Fatalf("GetWorkingTreeChanges: %v", err)
	}
	if !slices.Contains(changed, "brand-new.md") {
		t.Errorf("changed = %v, want it to contain brand-new.md", changed)
	}
}

func TestModifiedAndStagedFilesAreReported(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "tracked.go", "package main\n// edited\n")

	changed, _, err := GetWorkingTreeChanges(dir)
	if err != nil {
		t.Fatalf("GetWorkingTreeChanges: %v", err)
	}
	if !slices.Contains(changed, "tracked.go") {
		t.Errorf("changed = %v, want tracked.go", changed)
	}

	// Staging it must not hide it.
	cmd := exec.Command("git", "add", "tracked.go")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	changed, _, err = GetWorkingTreeChanges(dir)
	if err != nil {
		t.Fatalf("GetWorkingTreeChanges: %v", err)
	}
	if !slices.Contains(changed, "tracked.go") {
		t.Errorf("a staged file disappeared from changed = %v", changed)
	}
}

func TestDeletedFileIsReportedAsDeleted(t *testing.T) {
	dir := newRepo(t)
	if err := os.Remove(filepath.Join(dir, "tracked.go")); err != nil {
		t.Fatal(err)
	}

	changed, deleted, err := GetWorkingTreeChanges(dir)
	if err != nil {
		t.Fatalf("GetWorkingTreeChanges: %v", err)
	}
	if !slices.Contains(deleted, "tracked.go") {
		t.Errorf("deleted = %v, want tracked.go", deleted)
	}
	if slices.Contains(changed, "tracked.go") {
		t.Errorf("a deleted file was also reported as changed: %v", changed)
	}
}

// TestGitignoredFilesAreNotReported matters because build output would
// otherwise be indexed and would wake the worker on every build.
func TestGitignoredFilesAreNotReported(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "ignored.txt", "junk\n")
	write(t, dir, "build/out.bin", "junk\n")

	changed, _, err := GetWorkingTreeChanges(dir)
	if err != nil {
		t.Fatalf("GetWorkingTreeChanges: %v", err)
	}
	for _, p := range changed {
		if p == "ignored.txt" || p == "build/out.bin" {
			t.Errorf("a gitignored path was reported: %s", p)
		}
	}
}

// TestPathsWithSpacesAreNotMangled covers why -z is used: without it git
// quotes such a path, and the quoted form cannot be opened.
func TestPathsWithSpacesAreNotMangled(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "a file with spaces.md", "x\n")

	changed, _, err := GetWorkingTreeChanges(dir)
	if err != nil {
		t.Fatalf("GetWorkingTreeChanges: %v", err)
	}
	if !slices.Contains(changed, "a file with spaces.md") {
		t.Errorf("changed = %v, want the unquoted path with spaces", changed)
	}
	for _, p := range changed {
		if len(p) > 0 && p[0] == '"' {
			t.Errorf("path is still git-quoted: %s", p)
		}
	}
}

func TestRenameReportsBothSides(t *testing.T) {
	dir := newRepo(t)
	if err := os.Rename(filepath.Join(dir, "tracked.go"),
		filepath.Join(dir, "renamed.go")); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}

	changed, deleted, err := GetWorkingTreeChanges(dir)
	if err != nil {
		t.Fatalf("GetWorkingTreeChanges: %v", err)
	}
	if !slices.Contains(changed, "renamed.go") {
		t.Errorf("changed = %v, want the new name", changed)
	}
	// The old node must go, or the graph keeps a file that no longer exists.
	if !slices.Contains(deleted, "tracked.go") {
		t.Errorf("deleted = %v, want the old name", deleted)
	}
}
