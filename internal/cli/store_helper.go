package cli

import (
	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

// openBranchStore opens a read-write BranchStore using the config and CLI flags.
func openBranchStore(cfg *config.Config) (*embedded.BranchStore, string, error) {
	return embedded.OpenReadWrite(cfg, repoPaths(cfg), dbPath)
}

// openReadOnlyBranchStore opens a read-only BranchStore for concurrent access.
func openReadOnlyBranchStore(cfg *config.Config) (*embedded.BranchStore, string, error) {
	return embedded.OpenReadOnly(cfg, repoPaths(cfg), dbPath)
}

// repoPaths extracts repository paths from config.
func repoPaths(cfg *config.Config) []string {
	paths := make([]string, len(cfg.Repositories))
	for i, r := range cfg.Repositories {
		paths[i] = r.Path
	}
	return paths
}
