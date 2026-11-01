package watcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// clock is a settable time for tests of the hot window.
type clock struct{ t atomic.Int64 }

func newClock() *clock {
	c := &clock{}
	c.t.Store(time.Now().UnixNano())
	return c
}
func (c *clock) now() time.Time          { return time.Unix(0, c.t.Load()) }
func (c *clock) advance(d time.Duration) { c.t.Add(int64(d)) }

// tree makes a directory per name under root, each holding one file, with
// the directory and the file last modified age ago.
func tree(t *testing.T, root string, ages map[string]time.Duration) {
	t.Helper()
	for name, age := range ages {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(dir, "note.txt")
		if err := os.WriteFile(file, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		for _, p := range []string{file, dir} {
			if err := os.Chtimes(p, at, at); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func startTieredWatcher(t *testing.T, root string, tier *Tiering) (*Watcher, <-chan Event) {
	t.Helper()
	w, err := NewWatcher(WatcherConfig{Paths: []string{root}, Tiering: tier})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := w.Start(ctx)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		w.Close()
	})
	return w, ch
}

// waitFor returns the first event whose path ends with suffix, or fails.
func waitFor(t *testing.T, ch <-chan Event, suffix string, timeout time.Duration) Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("event channel closed waiting for %s", suffix)
			}
			if strings.HasSuffix(ev.Path, suffix) {
				return ev
			}
		case <-deadline:
			t.Fatalf("no event for %s within %s", suffix, timeout)
		}
	}
}

func isHot(w *Watcher, dir string) bool {
	w.tmu.Lock()
	defer w.tmu.Unlock()
	_, ok := w.hot[dir]
	return ok
}

const old, recent = 30 * 24 * time.Hour, time.Hour

func TestTieringWatchesRootsAndRecentDirectoriesOnly(t *testing.T) {
	root := t.TempDir()
	tree(t, root, map[string]time.Duration{"2021-album": old, "2022-album": old, "notes": recent})
	w, _ := startTieredWatcher(t, root, &Tiering{ScanInterval: time.Hour})

	st := w.Stats()
	if st.Watched != 2 || st.Scanned != 2 {
		t.Fatalf("watched %d, scanned %d; want the root and notes watched, the two albums scanned", st.Watched, st.Scanned)
	}
	if !isHot(w, root) || !isHot(w, filepath.Join(root, "notes")) {
		t.Error("the root or the recent directory is not watched")
	}
	watched, scanned, err := Survey(WatcherConfig{Paths: []string{root}}, time.Now(), 0)
	if err != nil || watched != 2 || scanned != 2 {
		t.Errorf("survey says %d watched, %d scanned (%v); want what the watcher did", watched, scanned, err)
	}
}

func TestAScanFindsAChangeInAColdDirectoryAndWatchesIt(t *testing.T) {
	root := t.TempDir()
	tree(t, root, map[string]time.Duration{"2021-album": old})
	album := filepath.Join(root, "2021-album")
	w, ch := startTieredWatcher(t, root, &Tiering{ScanInterval: 50 * time.Millisecond})
	if isHot(w, album) {
		t.Fatal("an old directory is watched")
	}

	if err := os.WriteFile(filepath.Join(album, "new.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, ch, "new.jpg", 3*time.Second)
	deadline := time.Now().Add(2 * time.Second)
	for !isHot(w, album) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !isHot(w, album) || w.Stats().Promoted != 1 {
		t.Fatalf("the changed directory was not promoted to a watch: %+v", w.Stats())
	}

	// Watched now, its next change arrives without waiting for a scan.
	if err := os.WriteFile(filepath.Join(album, "second.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, ch, "second.jpg", 3*time.Second)
}

func TestAQuietWatchedDirectoryIsReleased(t *testing.T) {
	root := t.TempDir()
	tree(t, root, map[string]time.Duration{"notes": recent})
	notes := filepath.Join(root, "notes")
	c := newClock()
	w, _ := startTieredWatcher(t, root, &Tiering{ScanInterval: 50 * time.Millisecond, Now: c.now})
	if !isHot(w, notes) {
		t.Fatal("a recent directory is not watched")
	}

	c.advance(15 * 24 * time.Hour)
	deadline := time.Now().Add(3 * time.Second)
	for isHot(w, notes) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	st := w.Stats()
	if isHot(w, notes) || st.Demoted != 1 || st.Watched != 1 || st.Scanned != 1 {
		t.Fatalf("after two quiet weeks: %+v; want notes released and scanned, the root still watched", st)
	}
}

func TestTheBudgetKeepsTheMostRecentlyChangedWatched(t *testing.T) {
	root := t.TempDir()
	tree(t, root, map[string]time.Duration{"a": 4 * time.Hour, "b": 3 * time.Hour, "c": 2 * time.Hour, "d": time.Hour})
	budget := NewBudget(3)
	var logged []string
	var mu sync.Mutex
	w, err := NewWatcher(WatcherConfig{Paths: []string{root}, Tiering: &Tiering{ScanInterval: time.Hour, Budget: budget},
		Logf: func(f string, a ...any) { mu.Lock(); logged = append(logged, f); mu.Unlock() }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer w.Close()
	if _, err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]bool{"d": true, "c": true, "b": false, "a": false} {
		if got := isHot(w, filepath.Join(root, name)); got != want {
			t.Errorf("%s watched = %v, want %v", name, got, want)
		}
	}
	if budget.Used() != 3 {
		t.Errorf("budget used %d, want 3", budget.Used())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logged) != 1 || !strings.Contains(logged[0], "budget") {
		t.Errorf("logged %q; want the budget reported once", logged)
	}
}

func TestAPromotionTakesTheQuietestWatchWhenTheBudgetIsFull(t *testing.T) {
	root := t.TempDir()
	tree(t, root, map[string]time.Duration{"notes": recent, "2021-album": old})
	album, notes := filepath.Join(root, "2021-album"), filepath.Join(root, "notes")
	w, ch := startTieredWatcher(t, root, &Tiering{ScanInterval: 50 * time.Millisecond, Budget: NewBudget(2)})

	if err := os.WriteFile(filepath.Join(album, "new.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, ch, "new.jpg", 3*time.Second)
	deadline := time.Now().Add(2 * time.Second)
	for !isHot(w, album) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !isHot(w, album) || isHot(w, notes) || !isHot(w, root) {
		t.Fatalf("album watched %v, notes watched %v, root watched %v; want the album to take the quieter notes' watch",
			isHot(w, album), isHot(w, notes), isHot(w, root))
	}
}

func TestANewRecordingFolderIsWatchedThroughTheRoot(t *testing.T) {
	root := t.TempDir()
	sessions := map[string]time.Duration{"latest": recent}
	for i := 0; i < 20; i++ {
		sessions["past-"+string(rune('a'+i))] = old
	}
	tree(t, root, sessions)
	w, ch := startTieredWatcher(t, root, &Tiering{ScanInterval: time.Hour})
	if st := w.Stats(); st.Watched != 2 || st.Scanned != 20 {
		t.Fatalf("watched %d, scanned %d; want only the root and the latest recording watched", st.Watched, st.Scanned)
	}

	session := filepath.Join(root, "new-session")
	if err := os.Mkdir(session, 0o755); err != nil {
		t.Fatal(err)
	}
	waitFor(t, ch, "new-session", 3*time.Second)
	deadline := time.Now().Add(2 * time.Second)
	for !isHot(w, session) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(session, "session.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, ch, "session.json", 3*time.Second)
}

func TestADirectoryThatCannotBeWatchedNoLongerEndsTheWalk(t *testing.T) {
	root := t.TempDir()
	tree(t, root, map[string]time.Duration{"a": recent, "b": recent, "c": recent})
	var logged int
	w, err := NewWatcher(WatcherConfig{Paths: []string{root}, Logf: func(string, ...any) { logged++ }})
	if err != nil {
		t.Fatal(err)
	}
	full := errors.New("no space left on device")
	w.addWatch = func(p string) error {
		if strings.HasSuffix(p, string(filepath.Separator)+"a") || strings.HasSuffix(p, string(filepath.Separator)+"b") {
			return full
		}
		return w.fsw.Add(p)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer w.Close()
	ch, err := w.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}

	st := w.Stats()
	if st.AddFailures != 2 || st.Watched != 2 || logged != 1 {
		t.Fatalf("%+v, logged %d; want two failures counted and logged once, the root and c still watched", st, logged)
	}
	// c comes after the failures in the walk, and is still watched.
	if err := os.WriteFile(filepath.Join(root, "c", "later.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, ch, "later.txt", 3*time.Second)
}
