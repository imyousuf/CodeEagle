package worker

import (
	"path/filepath"
	"testing"
)

func projects() []Project {
	// Deliberately mirrors a real machine: a broad "home" project whose
	// repositories do not contain the narrower projects, plus a project
	// nested inside another project's tree.
	return []Project{
		{Name: "opal-app", Root: "/home/u/projects/opal-app",
			Repos: []string{"/home/u/projects/opal-app"}},
		{Name: "nested", Root: "/home/u/projects/opal-app/vendor/lib",
			Repos: []string{"/home/u/projects/opal-app/vendor/lib"}},
		{Name: "home", Root: "/home/u",
			Repos: []string{"/home/u/Documents", "/home/u/Pictures"}},
	}
}

func TestAttributePicksTheMostSpecificProject(t *testing.T) {
	ps := projects()
	tests := []struct {
		path string
		want string
	}{
		{"/home/u/projects/opal-app/main.go", "opal-app"},
		// Inside the nested project, which is also inside opal-app. Syncing
		// opal-app here would file the change under the wrong graph.
		{"/home/u/projects/opal-app/vendor/lib/x.go", "nested"},
		{"/home/u/Documents/notes.md", "home"},
		{"/home/u/Pictures/a.png", "home"},
		// The repository directory itself.
		{"/home/u/Documents", "home"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got, ok := Attribute(ps, tt.path)
			if !ok {
				t.Fatalf("no project claimed %s", tt.path)
			}
			if got.Name != tt.want {
				t.Errorf("%s attributed to %q, want %q", tt.path, got.Name, tt.want)
			}
		})
	}
}

func TestAttributeRejectsPathsNoProjectIndexes(t *testing.T) {
	ps := projects()
	// /home/u is a project ROOT but not one of its repositories, so a file
	// sitting directly in the home directory is indexed by nobody.
	for _, path := range []string{
		"/home/u/.bashrc",
		"/home/u/projects",
		"/etc/passwd",
		// The classic prefix trap: shares a prefix with /home/u/Pictures
		// without being inside it.
		"/home/u/Pictures2/a.png",
		"/home/u/Documents-old/x",
	} {
		t.Run(path, func(t *testing.T) {
			if p, ok := Attribute(ps, path); ok {
				t.Errorf("%s was attributed to %q, want no project", path, p.Name)
			}
		})
	}
}

func TestIgnoredCoversWhatSyncWritesItself(t *testing.T) {
	// Without these the first sync's own writes would trigger the second,
	// forever.
	ignored := []string{
		"/home/u/proj/.CodeEagle/graph.db/000001.vlog",
		"/home/u/proj/.CodeEagle/config.yaml",
		"/home/u/proj/.codeeagle/docs.db/MANIFEST",
		"/home/u/proj/.git/index",
		"/home/u/proj/sub/.git/objects/ab/cd",
	}
	for _, p := range ignored {
		t.Run("ignored"+p, func(t *testing.T) {
			if !Ignored(p) {
				t.Errorf("Ignored(%s) = false; a sync would retrigger itself", p)
			}
		})
	}

	kept := []string{
		"/home/u/proj/main.go",
		"/home/u/proj/docs/CodeEagle.md",
		// Not a path segment of its own.
		"/home/u/proj/my.CodeEagle.backup",
		"/home/u/proj/gitignore.txt",
	}
	for _, p := range kept {
		t.Run("kept"+p, func(t *testing.T) {
			if Ignored(p) {
				t.Errorf("Ignored(%s) = true; a real change would be dropped", p)
			}
		})
	}
}

func TestWatchRootsDropsCoveredDirectories(t *testing.T) {
	ps := []Project{
		{Name: "outer", Repos: []string{"/a"}},
		{Name: "inner", Repos: []string{"/a/b", "/a/b/c"}},
		{Name: "other", Repos: []string{"/d"}},
	}
	got := WatchRoots(ps)

	want := []string{"/a", "/d"}
	if len(got) != len(want) {
		t.Fatalf("WatchRoots() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("WatchRoots()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDedupeNestedKeepsSiblings(t *testing.T) {
	// A sibling sharing a prefix must survive: /a/bc is not inside /a/b.
	got := dedupeNested([]string{"/a/b", "/a/bc"})
	if len(got) != 2 {
		t.Errorf("dedupeNested(/a/b, /a/bc) = %v, want both kept", got)
	}
}

func TestUnderDoesNotConfusePrefixWithParent(t *testing.T) {
	if under(filepath.Clean("/a/bc"), filepath.Clean("/a/b")) {
		t.Error("/a/bc must not count as being under /a/b")
	}
	if !under(filepath.Clean("/a/b/c"), filepath.Clean("/a/b")) {
		t.Error("/a/b/c must count as being under /a/b")
	}
}
