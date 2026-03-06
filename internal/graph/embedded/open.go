package embedded

import (
	"fmt"
	"os"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/gitutil"
)

// OpenReadOnly opens a read-only BranchStore using the config. It resolves
// the DB path, detects the git branch, and returns the store + branch name.
// The caller must Close() the store when done.
//
// dbPathOverride can be set to override the resolved DB path (e.g. from a CLI flag).
func OpenReadOnly(cfg *config.Config, repoPaths []string, dbPathOverride string) (*BranchStore, string, error) {
	resolvedDBPath := cfg.ResolveDBPath(dbPathOverride)
	if resolvedDBPath == "" {
		return nil, "", fmt.Errorf("no graph database path configured")
	}

	if _, err := os.Stat(resolvedDBPath); os.IsNotExist(err) {
		return nil, "", fmt.Errorf("graph database not found at %s; run 'codeeagle sync' first to build the index", resolvedDBPath)
	}

	currentBranch, readBranches := gitutil.BuildReadBranches(repoPaths)
	store, err := NewReadOnlyBranchStore(resolvedDBPath, currentBranch, readBranches)
	if err != nil {
		return nil, "", fmt.Errorf("open graph store (read-only): %w", err)
	}
	return store, currentBranch, nil
}

// OpenReadWrite opens a read-write BranchStore using the config. It resolves
// the DB path, detects the git branch, and returns the store + branch name.
// The caller must Close() the store when done.
//
// dbPathOverride can be set to override the resolved DB path (e.g. from a CLI flag).
func OpenReadWrite(cfg *config.Config, repoPaths []string, dbPathOverride string) (*BranchStore, string, error) {
	resolvedDBPath := cfg.ResolveDBPath(dbPathOverride)
	if resolvedDBPath == "" {
		return nil, "", fmt.Errorf("no graph database path; run 'codeeagle init' or use --db-path")
	}

	currentBranch, readBranches := gitutil.BuildReadBranches(repoPaths)
	store, err := NewBranchStore(resolvedDBPath, currentBranch, readBranches)
	if err != nil {
		return nil, "", fmt.Errorf("open graph store: %w", err)
	}
	return store, currentBranch, nil
}
