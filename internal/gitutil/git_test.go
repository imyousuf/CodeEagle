package gitutil

import (
	"strings"
	"testing"
)

// These tests use the CodeEagle repository itself as the test subject.
// The repo is expected to be a git repository with "main" as the default branch.

const repoPath = "/media/files/projects/gopath/src/github.com/imyousuf/CodeEagle"

func TestRunGit(t *testing.T) {
	output, err := runGit(repoPath, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if output != "true" {
		t.Errorf("expected 'true', got %q", output)
	}
}

func TestRunGitInvalidRepo(t *testing.T) {
	_, err := runGit("/tmp/nonexistent-repo-path-12345", "status")
	if err == nil {
		t.Fatal("expected error for invalid repo path, got nil")
	}
}

func TestGetBranchInfo(t *testing.T) {
	info, err := GetBranchInfo(repoPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if info.CurrentBranch == "" {
		t.Error("expected non-empty current branch")
	}
	if info.DefaultBranch != "main" && info.DefaultBranch != "master" {
		t.Errorf("expected default branch to be 'main' or 'master', got %q", info.DefaultBranch)
	}
	// On main branch, IsFeatureBranch should be false.
	if info.CurrentBranch == info.DefaultBranch && info.IsFeatureBranch {
		t.Error("expected IsFeatureBranch to be false when on default branch")
	}
}

func TestDetectDefaultBranch(t *testing.T) {
	branch, err := detectDefaultBranch(repoPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if branch != "main" && branch != "master" {
		t.Errorf("expected 'main' or 'master', got %q", branch)
	}
}

func TestGetBranchDiff(t *testing.T) {
	diff, err := GetBranchDiff(repoPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if diff.CurrentBranch == "" {
		t.Error("expected non-empty current branch in diff")
	}
	if diff.DefaultBranch == "" {
		t.Error("expected non-empty default branch in diff")
	}
	// ChangedFiles may be empty if on default branch -- that's expected.
}

func TestGetFileHistory(t *testing.T) {
	commits, err := GetFileHistory(repoPath, "go.mod", 5)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(commits) == 0 {
		t.Error("expected at least 1 commit for go.mod")
	}
	for _, c := range commits {
		if c.Hash == "" {
			t.Error("expected non-empty commit hash")
		}
		if c.Author == "" {
			t.Error("expected non-empty commit author")
		}
		if c.Message == "" {
			t.Error("expected non-empty commit message")
		}
	}
}

func TestGetFileHistoryNonexistentFile(t *testing.T) {
	commits, err := GetFileHistory(repoPath, "nonexistent-file-xyz.go", 5)
	if err != nil {
		t.Fatalf("expected no error (git log for nonexistent file returns empty), got: %v", err)
	}
	if len(commits) != 0 {
		t.Errorf("expected 0 commits for nonexistent file, got %d", len(commits))
	}
}

func TestParseNameStatus(t *testing.T) {
	input := `A	new_file.go
M	modified_file.go
D	deleted_file.go
R100	old_name.go	new_name.go`

	result := parseNameStatus(input)

	if result["new_file.go"] != "added" {
		t.Errorf("expected 'added' for new_file.go, got %q", result["new_file.go"])
	}
	if result["modified_file.go"] != "modified" {
		t.Errorf("expected 'modified' for modified_file.go, got %q", result["modified_file.go"])
	}
	if result["deleted_file.go"] != "deleted" {
		t.Errorf("expected 'deleted' for deleted_file.go, got %q", result["deleted_file.go"])
	}
	if result["new_name.go"] != "renamed" {
		t.Errorf("expected 'renamed' for new_name.go, got %q", result["new_name.go"])
	}
}

func TestParseCommitLog(t *testing.T) {
	input := "abc123|John Doe|2025-01-15 10:30:00 +0000|Initial commit\ndef456|Jane Doe|2025-01-16 12:00:00 +0000|Add feature"
	commits := parseCommitLog(input)
	if len(commits) != 2 {
		t.Fatalf("expected 2 commits, got %d", len(commits))
	}
	if commits[0].Hash != "abc123" {
		t.Errorf("expected hash 'abc123', got %q", commits[0].Hash)
	}
	if commits[0].Author != "John Doe" {
		t.Errorf("expected author 'John Doe', got %q", commits[0].Author)
	}
	if commits[0].Message != "Initial commit" {
		t.Errorf("expected message 'Initial commit', got %q", commits[0].Message)
	}
	if commits[1].Hash != "def456" {
		t.Errorf("expected hash 'def456', got %q", commits[1].Hash)
	}
}

func TestParseCommitLogEmpty(t *testing.T) {
	commits := parseCommitLog("")
	if len(commits) != 0 {
		t.Errorf("expected 0 commits for empty input, got %d", len(commits))
	}
}

func TestParseNameStatusEmpty(t *testing.T) {
	result := parseNameStatus("")
	if len(result) != 0 {
		t.Errorf("expected empty map, got %d entries", len(result))
	}
}

func TestGetCommitsBetween(t *testing.T) {
	info, err := GetBranchInfo(repoPath)
	if err != nil {
		t.Fatalf("failed to get branch info: %v", err)
	}
	// If we're on the default branch, this should return 0 commits (HEAD..HEAD).
	// If on a feature branch, it returns commits not in the default branch.
	commits, err := GetCommitsBetween(repoPath, info.DefaultBranch)
	if err != nil {
		// On the default branch, this is expected to work (returns 0 commits).
		if !info.IsFeatureBranch {
			t.Fatalf("expected no error on default branch, got: %v", err)
		}
	}
	_ = commits // just verify no panic
}

func TestGetFileHistoryDefaultLimit(t *testing.T) {
	commits, err := GetFileHistory(repoPath, "go.mod", 0)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	// With limit 0, the function should default to 10.
	if len(commits) == 0 {
		t.Error("expected at least 1 commit for go.mod with default limit")
	}
}

func TestRunGitOutputTrimmed(t *testing.T) {
	output, err := runGit(repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	// Output should not have leading/trailing whitespace.
	if output != strings.TrimSpace(output) {
		t.Errorf("expected trimmed output, got %q", output)
	}
}
