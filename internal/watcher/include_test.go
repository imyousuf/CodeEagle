package watcher

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIncludedAdmitsOnlyNamedFilesInItsRepository(t *testing.T) {
	m := NewExcludeMatcher([]string{"**/Downloads/mark/**"})
	m.SetIncludes(map[string][]string{
		"/home/u/Downloads":        {"*.md", "*.PDF", "*.docx"},
		"/home/u/Downloads/photos": {"*.jpg"},
	})
	for path, want := range map[string]bool{
		"/home/u/Downloads/notes.md":         true,
		"/home/u/Downloads/a/b/deep/plan.md": true,
		"/home/u/Downloads/Report.pdf":       true, // patterns ignore case
		"/home/u/Downloads/setup.zip":        false,
		"/home/u/Downloads/a/clip.mp4":       false,
		"/home/u/Downloads/photos/cat.jpg":   true, // the innermost repository decides
		"/home/u/Downloads/photos/notes.md":  false,
		"/home/u/Documents/anything.bin":     true, // no include patterns there
		"/home/u/Downloadsx/notes.bin":       true, // not under the repository
	} {
		if got := m.Included(path); got != want {
			t.Errorf("Included(%s) = %v, want %v", path, got, want)
		}
	}
	if !m.MatchFile("/home/u/Downloads/mark/plan.md") {
		t.Error("an excluded file was admitted because its name matched an include pattern")
	}
	if m.MatchFile("/home/u/Downloads/a/plan.md") || !m.MatchFile("/home/u/Downloads/a/plan.txt") {
		t.Error("MatchFile disagrees with the include patterns")
	}
}

func TestWatcherReportsOnlyIncludedFiles(t *testing.T) {
	root := t.TempDir()
	w, err := NewWatcher(WatcherConfig{Paths: []string{root}, Includes: map[string][]string{root: {"*.md"}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer w.Close()
	ch, err := w.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"skip.jpg", "keep.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ev := waitFor(t, ch, ".md", 3*time.Second)
	if filepath.Base(ev.Path) != "keep.md" {
		t.Fatalf("first event %s", ev.Path)
	}
	select {
	case ev := <-ch:
		if filepath.Ext(ev.Path) == ".jpg" {
			t.Errorf("an event for a file the include patterns leave out: %s", ev.Path)
		}
	case <-time.After(300 * time.Millisecond):
	}
}
