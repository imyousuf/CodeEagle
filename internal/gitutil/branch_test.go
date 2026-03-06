package gitutil

import "testing"

func TestDetectBranch_NoRepos(t *testing.T) {
	branch := DetectBranch(nil)
	if branch != "default" {
		t.Errorf("DetectBranch(nil) = %q, want %q", branch, "default")
	}
}

func TestDetectBranch_EmptySlice(t *testing.T) {
	branch := DetectBranch([]string{})
	if branch != "default" {
		t.Errorf("DetectBranch([]) = %q, want %q", branch, "default")
	}
}

func TestDetectBranch_InvalidPath(t *testing.T) {
	branch := DetectBranch([]string{"/nonexistent/path"})
	if branch != "default" {
		t.Errorf("DetectBranch(/nonexistent) = %q, want %q", branch, "default")
	}
}

func TestDetectDefaultBranch_NoRepos(t *testing.T) {
	branch := DetectDefaultBranch(nil)
	if branch != "main" {
		t.Errorf("DetectDefaultBranch(nil) = %q, want %q", branch, "main")
	}
}

func TestDetectDefaultBranch_InvalidPath(t *testing.T) {
	branch := DetectDefaultBranch([]string{"/nonexistent/path"})
	if branch != "main" {
		t.Errorf("DetectDefaultBranch(/nonexistent) = %q, want %q", branch, "main")
	}
}

func TestBuildReadBranches_NoRepos(t *testing.T) {
	current, branches := BuildReadBranches(nil)
	if current != "default" {
		t.Errorf("current = %q, want %q", current, "default")
	}
	// default branch detection returns "main", which differs from "default",
	// so we should get both.
	if len(branches) != 2 {
		t.Fatalf("branches len = %d, want 2", len(branches))
	}
	if branches[0] != "default" {
		t.Errorf("branches[0] = %q, want %q", branches[0], "default")
	}
	if branches[1] != "main" {
		t.Errorf("branches[1] = %q, want %q", branches[1], "main")
	}
}

func TestBuildReadBranches_InvalidPath(t *testing.T) {
	current, branches := BuildReadBranches([]string{"/nonexistent/path"})
	if current != "default" {
		t.Errorf("current = %q, want %q", current, "default")
	}
	if len(branches) != 2 {
		t.Fatalf("branches len = %d, want 2", len(branches))
	}
	if branches[0] != "default" {
		t.Errorf("branches[0] = %q, want %q", branches[0], "default")
	}
	if branches[1] != "main" {
		t.Errorf("branches[1] = %q, want %q", branches[1], "main")
	}
}
