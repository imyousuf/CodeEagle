// Package gitutil provides git-aware operations for branch tracking and diff analysis.
package gitutil

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// BranchInfo holds information about the current git branch state.
type BranchInfo struct {
	CurrentBranch   string
	DefaultBranch   string // main or master
	IsFeatureBranch bool   // true if current != default
	Ahead           int    // commits ahead of default
	Behind          int    // commits behind default
}

// ChangedFile represents a file changed between branches.
type ChangedFile struct {
	Path      string
	Status    string // "added", "modified", "deleted", "renamed"
	Additions int
	Deletions int
}

// BranchDiff contains branch info plus the list of changed files.
type BranchDiff struct {
	BranchInfo
	ChangedFiles []ChangedFile
}

// CommitInfo holds metadata for a single git commit.
type CommitInfo struct {
	Hash    string
	Author  string
	Date    string
	Message string
}

// GetBranchInfo returns information about the current branch relative to the default branch.
func GetBranchInfo(repoPath string) (*BranchInfo, error) {
	current, err := runGit(repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("get current branch: %w", err)
	}

	defaultBranch, err := detectDefaultBranch(repoPath)
	if err != nil {
		return nil, fmt.Errorf("detect default branch: %w", err)
	}

	info := &BranchInfo{
		CurrentBranch:   current,
		DefaultBranch:   defaultBranch,
		IsFeatureBranch: current != defaultBranch,
	}

	if info.IsFeatureBranch {
		ahead, behind, err := getAheadBehind(repoPath, defaultBranch, current)
		if err == nil {
			info.Ahead = ahead
			info.Behind = behind
		}
	}

	return info, nil
}

// GetBranchDiff returns the branch info plus all files changed compared to the default branch.
func GetBranchDiff(repoPath string) (*BranchDiff, error) {
	info, err := GetBranchInfo(repoPath)
	if err != nil {
		return nil, err
	}

	diff := &BranchDiff{BranchInfo: *info}

	if !info.IsFeatureBranch {
		return diff, nil
	}

	// Get changed files with stats using diff against the merge-base.
	mergeBase, err := runGit(repoPath, "merge-base", info.DefaultBranch, "HEAD")
	if err != nil {
		return diff, nil // return what we have without file details
	}

	numstatOutput, err := runGit(repoPath, "diff", "--numstat", mergeBase+"..HEAD")
	if err != nil {
		return diff, nil
	}

	nameStatusOutput, err := runGit(repoPath, "diff", "--name-status", mergeBase+"..HEAD")
	if err != nil {
		return diff, nil
	}

	// Parse name-status to get file status.
	statusMap := parseNameStatus(nameStatusOutput)

	// Parse numstat for additions/deletions.
	for _, line := range strings.Split(numstatOutput, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		adds, _ := strconv.Atoi(parts[0])
		dels, _ := strconv.Atoi(parts[1])
		path := parts[2]

		status := "modified"
		if s, ok := statusMap[path]; ok {
			status = s
		}

		diff.ChangedFiles = append(diff.ChangedFiles, ChangedFile{
			Path:      path,
			Status:    status,
			Additions: adds,
			Deletions: dels,
		})
	}

	return diff, nil
}

// GetFileHistory returns the most recent commits that touched the given file.
func GetFileHistory(repoPath, filePath string, limit int) ([]CommitInfo, error) {
	if limit <= 0 {
		limit = 10
	}
	output, err := runGit(repoPath, "log", fmt.Sprintf("-n%d", limit),
		"--format=%H|%an|%ai|%s", "--", filePath)
	if err != nil {
		return nil, fmt.Errorf("get file history for %s: %w", filePath, err)
	}
	return parseCommitLog(output), nil
}

// GetCommitsBetween returns the commits on the current branch that are not in baseBranch.
func GetCommitsBetween(repoPath, baseBranch string) ([]CommitInfo, error) {
	output, err := runGit(repoPath, "log", "--format=%H|%an|%ai|%s", baseBranch+"..HEAD")
	if err != nil {
		return nil, fmt.Errorf("get commits between %s and HEAD: %w", baseBranch, err)
	}
	return parseCommitLog(output), nil
}

// detectDefaultBranch checks whether the repository uses "main" or "master" as its default branch.
func detectDefaultBranch(repoPath string) (string, error) {
	// Try "main" first.
	_, err := runGit(repoPath, "rev-parse", "--verify", "refs/heads/main")
	if err == nil {
		return "main", nil
	}

	// Try "master".
	_, err = runGit(repoPath, "rev-parse", "--verify", "refs/heads/master")
	if err == nil {
		return "master", nil
	}

	return "", fmt.Errorf("no default branch found (tried main and master)")
}

// getAheadBehind returns how many commits the current branch is ahead/behind relative to base.
func getAheadBehind(repoPath, base, current string) (ahead, behind int, err error) {
	output, err := runGit(repoPath, "rev-list", "--left-right", "--count", base+"..."+current)
	if err != nil {
		return 0, 0, err
	}
	parts := strings.Fields(output)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("unexpected rev-list output: %q", output)
	}
	behind, _ = strconv.Atoi(parts[0])
	ahead, _ = strconv.Atoi(parts[1])
	return ahead, behind, nil
}

// parseNameStatus parses "git diff --name-status" output into a map of path -> status.
func parseNameStatus(output string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		statusCode := parts[0]
		path := parts[len(parts)-1] // use last field; for renames this is the new path

		switch {
		case strings.HasPrefix(statusCode, "A"):
			result[path] = "added"
		case strings.HasPrefix(statusCode, "D"):
			result[path] = "deleted"
		case strings.HasPrefix(statusCode, "R"):
			result[path] = "renamed"
		default:
			result[path] = "modified"
		}
	}
	return result
}

// parseCommitLog parses lines formatted as "hash|author|date|message".
func parseCommitLog(output string) []CommitInfo {
	var commits []CommitInfo
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 4 {
			continue
		}
		commits = append(commits, CommitInfo{
			Hash:    parts[0],
			Author:  parts[1],
			Date:    parts[2],
			Message: parts[3],
		})
	}
	return commits
}

// runGit executes a git command in the given repository path and returns trimmed stdout.
func runGit(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(output)), nil
}
