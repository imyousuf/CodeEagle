package cli

import (
	"fmt"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/gitutil"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

// openBranchStore opens a BranchStore using the config and CLI flags.
// It resolves the DB path, detects the current git branch from the first
// repository, and builds the readBranches list.
// Returns the store, the current branch name, and any error.
func openBranchStore(cfg *config.Config) (*embedded.BranchStore, string, error) {
	resolvedDBPath := cfg.ResolveDBPath(dbPath)
	if resolvedDBPath == "" {
		return nil, "", fmt.Errorf("no graph database path; run 'codeeagle init' or use --db-path")
	}

	// Detect current branch from the first repository.
	currentBranch := "default"
	defaultBranch := "main"
	if len(cfg.Repositories) > 0 {
		repoPath := cfg.Repositories[0].Path
		branch, err := gitutil.GetCurrentBranch(repoPath)
		if err == nil && branch != "" {
			currentBranch = branch
		}
		info, err := gitutil.GetBranchInfo(repoPath)
		if err == nil {
			defaultBranch = info.DefaultBranch
		}
	}

	// Build read branch order: current branch first, then default branch.
	readBranches := []string{currentBranch}
	if currentBranch != defaultBranch {
		readBranches = append(readBranches, defaultBranch)
	}

	store, err := embedded.NewBranchStore(resolvedDBPath, currentBranch, readBranches)
	if err != nil {
		return nil, "", fmt.Errorf("open graph store: %w", err)
	}

	return store, currentBranch, nil
}
