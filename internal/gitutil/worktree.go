package gitutil

import (
	"strings"
)

// GetWorkingTreeChanges returns paths that differ from HEAD right now:
// modified, staged, and untracked files, plus the ones that are gone.
//
// A commit-to-commit diff cannot see any of this. That matters because a file
// is usually written long before it is committed -- often never -- so an index
// that only moves when HEAD moves is stale for exactly as long as someone is
// working. Files ignored by .gitignore are not reported, since git does not
// report them and they are not indexed anyway.
func GetWorkingTreeChanges(repoPath string) (changed, deleted []string, err error) {
	// -z gives NUL-separated, unquoted paths. Without it git quotes and
	// escapes anything with a space or a non-ASCII character, and the path
	// would have to be unquoted again to be opened.
	out, err := runGitRaw(repoPath, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return nil, nil, err
	}

	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		entry := fields[i]
		// A status entry is "XY<space><path>", so anything shorter than four
		// characters is padding rather than a record.
		if len(entry) < 4 {
			continue
		}

		x, y := entry[0], entry[1]
		path := entry[3:]

		// A rename is recorded as the new path, with the old one following in
		// the next NUL-separated field.
		if x == 'R' || y == 'R' {
			if i+1 < len(fields) {
				if old := fields[i+1]; old != "" {
					deleted = append(deleted, old)
				}
				i++
			}
			changed = append(changed, path)
			continue
		}

		// Deleted in the index or the working tree.
		if x == 'D' || y == 'D' {
			deleted = append(deleted, path)
			continue
		}

		changed = append(changed, path)
	}
	return changed, deleted, nil
}
