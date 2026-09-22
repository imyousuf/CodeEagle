package worker

import (
	"path/filepath"
	"strings"
)

// selfWritten names directories a sync writes into itself.
//
// This is the difference between a worker and a runaway loop. A sync opens
// BadgerDBs under the project's .CodeEagle directory, and for most projects
// that directory sits inside a watched repository -- the project root is the
// repository. Every write would then look like a change, trigger another sync,
// and never stop. The guard is on the path rather than on timing because a
// quiet period cannot distinguish "the user saved a file" from "the sync is
// still flushing".
var selfWritten = []string{
	".CodeEagle", // graph.db, docs.db, queue.db, vec.db, sync.state
	".codeeagle", // the same, on a case-sensitive filesystem written either way
	".git",       // index churn on every checkout, and never indexed anyway
}

// Attribute returns the project that owns a changed path, and whether any
// does.
//
// Projects are searched most-specific-first, so a path inside a project nested
// within another project's tree is attributed to the inner one. Syncing the
// outer project for an inner project's change would index the same file under
// the wrong graph.
func Attribute(projects []Project, path string) (*Project, bool) {
	path = filepath.Clean(path)

	best := -1
	bestLen := -1
	for i := range projects {
		for _, repo := range projects[i].Repos {
			if !under(path, repo) {
				continue
			}
			if len(repo) > bestLen {
				best, bestLen = i, len(repo)
			}
		}
	}
	if best < 0 {
		return nil, false
	}
	return &projects[best], true
}

// Ignored reports whether a path is one CodeEagle writes itself, or otherwise
// must never trigger a sync.
func Ignored(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(filepath.Clean(path)), "/") {
		for _, skip := range selfWritten {
			if part == skip {
				return true
			}
		}
	}
	return false
}

// under reports whether path is dir itself or lies beneath it.
//
// A string prefix alone is wrong: "/home/a/Pictures2" starts with
// "/home/a/Pictures" without being inside it, which would attribute another
// project's files to this one.
func under(path, dir string) bool {
	if path == dir {
		return true
	}
	return strings.HasPrefix(path, dir+string(filepath.Separator))
}

// WatchRoots returns every directory the supervisor must watch, with any
// directory already covered by another removed.
//
// Two projects can legitimately name overlapping trees; watching both would
// deliver each event twice and start two syncs for one save.
func WatchRoots(projects []Project) []string {
	var all []string
	for _, p := range projects {
		all = append(all, p.Repos...)
	}
	return dedupeNested(all)
}
