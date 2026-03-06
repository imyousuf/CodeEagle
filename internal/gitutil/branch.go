package gitutil

// DetectBranch returns the current git branch from the first repo path,
// or "default" if no repos are provided or detection fails.
func DetectBranch(repoPaths []string) string {
	if len(repoPaths) == 0 {
		return "default"
	}
	branch, err := GetCurrentBranch(repoPaths[0])
	if err != nil || branch == "" {
		return "default"
	}
	return branch
}

// DetectDefaultBranch returns the default branch name (e.g. "main") from
// the first repo path, or "main" if detection fails.
func DetectDefaultBranch(repoPaths []string) string {
	if len(repoPaths) == 0 {
		return "main"
	}
	info, err := GetBranchInfo(repoPaths[0])
	if err != nil {
		return "main"
	}
	return info.DefaultBranch
}

// BuildReadBranches returns the branch read order: current first, then
// default (if different). This is the standard order for BranchStore lookups.
func BuildReadBranches(repoPaths []string) (currentBranch string, readBranches []string) {
	currentBranch = DetectBranch(repoPaths)
	defaultBranch := DetectDefaultBranch(repoPaths)
	readBranches = []string{currentBranch}
	if currentBranch != defaultBranch {
		readBranches = append(readBranches, defaultBranch)
	}
	return currentBranch, readBranches
}
