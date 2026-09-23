package worker

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/config"
)

// TestWatchPathsCoversRecordings is the test whose absence let a one-line
// omission reach a running service. WatchRoots knew about transcript
// directories while the watcher was still built from Repos alone, so nothing
// watched them -- and every other test passed, because they exercise
// Attribute and the tracker directly and never ask what is watched.
func TestWatchPathsCoversRecordings(t *testing.T) {
	p := Project{
		Name:           "home",
		Repos:          []string{"/home/u/Documents", "/home/u/Pictures"},
		TranscriptDirs: []string{"/home/u/.local/share/tomoe/sessions"},
	}

	got := WatchPaths(p)

	for _, want := range p.Repos {
		if !slices.Contains(got, want) {
			t.Errorf("WatchPaths() = %v, missing indexed directory %s", got, want)
		}
	}
	for _, want := range p.TranscriptDirs {
		if !slices.Contains(got, want) {
			t.Errorf("WatchPaths() = %v, missing recordings directory %s; "+
				"a new recording would trigger nothing", got, want)
		}
	}
	if len(got) != 3 {
		t.Errorf("WatchPaths() returned %d paths, want 3", len(got))
	}
}

func TestWatchPathsWithoutTranscriptsIsJustRepos(t *testing.T) {
	p := Project{Name: "code", Repos: []string{"/code"}}
	if got := WatchPaths(p); len(got) != 1 || got[0] != "/code" {
		t.Errorf("WatchPaths() = %v, want just the indexed directory", got)
	}
}

// writeProject lays out a project on disk the way a real one is.
func writeProject(t *testing.T, body string, dirs ...string) (root, cfgDir string) {
	t.Helper()
	root = t.TempDir()
	cfgDir = filepath.Join(root, config.ProjectDirName)
	for _, d := range append(dirs, cfgDir) {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(cfgDir, config.ProjectConfigFile),
		[]byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, cfgDir
}

// TestDiscoverReadsTranscriptsFromAConfig walks the whole path a real machine
// takes: a config on disk, through load, to the directories watched. The
// pieces were each covered; the wiring between them was not.
func TestDiscoverReadsTranscriptsFromAConfig(t *testing.T) {
	base := t.TempDir()
	docs := filepath.Join(base, "Documents")
	sessions := filepath.Join(base, "sessions")

	body := "repositories:\n  - path: " + docs + "\n" +
		"transcripts:\n  enabled: true\n  sessions_dir: " + sessions + "\n"
	root, cfgDir := writeProject(t, body, docs, sessions)

	p, err := load(config.ProjectEntry{Name: "t", Root: root, ConfigDir: cfgDir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if !slices.Contains(p.Repos, docs) {
		t.Errorf("Repos = %v, want the configured documents directory", p.Repos)
	}
	if !slices.Contains(p.TranscriptDirs, sessions) {
		t.Errorf("TranscriptDirs = %v, want the configured sessions directory", p.TranscriptDirs)
	}
	if got := WatchPaths(*p); !slices.Contains(got, sessions) {
		t.Errorf("WatchPaths() = %v; recordings would not be watched", got)
	}
}

// TestTranscriptsDisabledIsNotWatched: a directory named but switched off
// costs nothing, so a recorder writing there does not wake the worker.
func TestTranscriptsDisabledIsNotWatched(t *testing.T) {
	base := t.TempDir()
	docs := filepath.Join(base, "Documents")
	sessions := filepath.Join(base, "sessions")

	body := "repositories:\n  - path: " + docs + "\n" +
		"transcripts:\n  enabled: false\n  sessions_dir: " + sessions + "\n"
	root, cfgDir := writeProject(t, body, docs, sessions)

	p, err := load(config.ProjectEntry{Name: "t", Root: root, ConfigDir: cfgDir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(p.TranscriptDirs) != 0 {
		t.Errorf("TranscriptDirs = %v, want none when transcripts are disabled", p.TranscriptDirs)
	}
}

// TestSessionsDirMayBeAList covers the documented alternative spelling, where
// recordings arrive in more than one place.
func TestSessionsDirMayBeAList(t *testing.T) {
	base := t.TempDir()
	docs := filepath.Join(base, "Documents")
	a := filepath.Join(base, "sessionsA")
	b := filepath.Join(base, "sessionsB")

	body := "repositories:\n  - path: " + docs + "\n" +
		"transcripts:\n  enabled: true\n  sessions_dir:\n" +
		"    - " + a + "\n    - " + b + "\n"
	root, cfgDir := writeProject(t, body, docs, a, b)

	p, err := load(config.ProjectEntry{Name: "t", Root: root, ConfigDir: cfgDir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, want := range []string{a, b} {
		if !slices.Contains(p.TranscriptDirs, want) {
			t.Errorf("TranscriptDirs = %v, missing %s", p.TranscriptDirs, want)
		}
	}
}
