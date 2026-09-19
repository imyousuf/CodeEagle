package queue

import (
	"os"
	"path/filepath"
)

// resolveFilePath converts a path recorded in the graph back to one on disk.
//
// The graph stores a path relative to the repository that holds it, and for a
// directory indexed outside a git repository that path is prefixed with the
// directory's own name, so that two directories holding `photo.jpg` do not
// collide. Both forms have to be recognised here, because a job carries the
// stored path and the handler has to open the actual file.
//
// It lives with the queue rather than with face recognition, which is where it
// started: document extraction and image description need it just as much, and
// they are the job types that remain.
func resolveFilePath(relPath string, repoRoots []string) string {
	if filepath.IsAbs(relPath) {
		return relPath
	}
	for _, root := range repoRoots {
		// Direct join, for a path recorded relative to a git repository.
		abs := filepath.Join(root, relPath)
		if _, err := os.Stat(abs); err == nil {
			return abs
		}

		// Basename-prefixed, for a directory indexed on its own.
		base := filepath.Base(root)
		if prefix := base + "/"; len(relPath) > len(prefix) && relPath[:len(prefix)] == prefix {
			abs = filepath.Join(root, relPath[len(prefix):])
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	// Left as it came, so the failure surfaces at the open with a path the
	// reader can recognise rather than as a silent skip.
	return relPath
}
