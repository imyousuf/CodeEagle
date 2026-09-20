package cli

import (
	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

// openBranchStore opens a read-write BranchStore using the config and CLI flags.
//
// Meetings are included as a fallback read scope. They are not branch-scoped —
// a meeting happened, and it belongs to no branch — so they should be visible
// whichever branch is checked out: semantic search, the agents, and `query`
// all benefit from reaching what was said as well as what was written. Writes
// still go to the current branch, so indexing code cannot disturb them.
func openBranchStore(cfg *config.Config) (*embedded.BranchStore, string, error) {
	return embedded.OpenReadWriteWithScopes(cfg, repoPaths(cfg), dbPath, embedded.MeetingScope)
}

// openReadOnlyBranchStore opens a read-only BranchStore for concurrent access.
func openReadOnlyBranchStore(cfg *config.Config) (*embedded.BranchStore, string, error) {
	return embedded.OpenReadOnlyWithScopes(cfg, repoPaths(cfg), dbPath, embedded.MeetingScope)
}

// repoPaths extracts repository paths from config.
func repoPaths(cfg *config.Config) []string {
	paths := make([]string, len(cfg.Repositories))
	for i, r := range cfg.Repositories {
		paths[i] = r.Path
	}
	return paths
}
