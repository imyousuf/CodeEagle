package faces

import (
	"os"
	"path/filepath"
)

// ResolveFilePath converts a relative path to absolute using repo roots.
// Handles basename-prefixed paths (e.g., "Pictures/sub/photo.jpg" where root
// is "/home/user/Pictures") by stripping the matching basename prefix.
// If the path is already absolute or no roots match, returns the path as-is.
func ResolveFilePath(relPath string, repoRoots []string) string {
	if filepath.IsAbs(relPath) {
		return relPath
	}
	for _, root := range repoRoots {
		// Direct join (works for git repos with non-prefixed paths).
		abs := filepath.Join(root, relPath)
		if _, err := os.Stat(abs); err == nil {
			return abs
		}

		// Basename-prefix: relPath starts with basename(root) + "/".
		base := filepath.Base(root)
		if prefix := base + "/"; len(relPath) > len(prefix) && relPath[:len(prefix)] == prefix {
			abs = filepath.Join(root, relPath[len(prefix):])
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	return relPath // fallback to original (will fail at I/O with clear error)
}
