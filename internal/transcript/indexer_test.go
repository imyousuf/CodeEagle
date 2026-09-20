package transcript

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

// writeSession writes a transcript into its own session directory.
func writeSession(t *testing.T, root, id string, started time.Time, turns ...[3]string) string {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	s := build(turns...)
	s.ID = id
	s.CreatedAt = started
	s.EndedAt = started.Add(10 * time.Minute)
	s.Title = "Unknown Meeting " + started.Format("2006-01-02 15:04")

	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := filepath.Join(dir, SessionFileName)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestDiscoverSessionsOrdersByMeetingTime(t *testing.T) {
	root := t.TempDir()

	// Written newest-first, and all with near-identical modification times —
	// which is what copying a corpus produces. Only the meeting's own timestamp
	// gives a usable order.
	march := writeSession(t, root, "march", time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
		[3]string{"You", SourceMic, "march"})
	january := writeSession(t, root, "january", time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC),
		[3]string{"You", SourceMic, "january"})
	february := writeSession(t, root, "february", time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC),
		[3]string{"You", SourceMic, "february"})

	got, err := DiscoverSessions(root)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	want := []string{january, february, march}
	if len(got) != len(want) {
		t.Fatalf("got %d sessions, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d = %s, want %s", i, filepath.Base(filepath.Dir(got[i])), filepath.Base(filepath.Dir(want[i])))
		}
	}
}

func TestDiscoverSessionsIgnoresStrayFiles(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "real", time.Now(), [3]string{"You", SourceMic, "hello"})

	// A loose file and a directory with no transcript must both be ignored.
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "empty-dir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got, err := DiscoverSessions(root)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d sessions, want 1: %v", len(got), got)
	}
}

func TestDiscoverSessionsMissingDirectory(t *testing.T) {
	if _, err := DiscoverSessions(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected an error for a missing directory")
	}
}

// newTestIndexer builds an Indexer with no LLM, which is enough to exercise the
// scheduling decisions that do not need one.
func newTestIndexer(t *testing.T, root string) (*Indexer, graph.Store) {
	t.Helper()
	store, err := embedded.NewStore(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	people, err := LoadPersonRegistry(context.Background(), store)
	if err != nil {
		t.Fatalf("load people: %v", err)
	}
	writer := NewWriter(store, people, WriterOptions{})
	ix := NewIndexer(store, nil, writer, IndexOptions{SessionsDirs: []string{root}})
	return ix, store
}

func TestNeedsIndexingSkipsEmptyRecordings(t *testing.T) {
	root := t.TempDir()
	path := writeSession(t, root, "silent", time.Now(), [3]string{"You", SourceMic, "   "})

	ix, _ := newTestIndexer(t, root)
	needed, reason, err := ix.needsIndexing(context.Background(), path)
	if err != nil {
		t.Fatalf("needsIndexing: %v", err)
	}
	if needed || reason != skipEmpty {
		t.Errorf("needed=%v reason=%v, want false/skipEmpty", needed, reason)
	}
}

func TestNeedsIndexingSkipsRecordingStillBeingWritten(t *testing.T) {
	root := t.TempDir()
	path := writeSession(t, root, "live", time.Now(), [3]string{"You", SourceMic, "still talking"})

	ix, _ := newTestIndexer(t, root)
	// A recording that has only just been touched may still be growing.
	ix.minAge = time.Hour

	needed, reason, err := ix.needsIndexing(context.Background(), path)
	if err != nil {
		t.Fatalf("needsIndexing: %v", err)
	}
	if needed || reason != skipPending {
		t.Errorf("needed=%v reason=%v, want false/skipPending", needed, reason)
	}

	// Once it has settled, it is ready.
	ix.minAge = 0
	needed, _, err = ix.needsIndexing(context.Background(), path)
	if err != nil {
		t.Fatalf("needsIndexing: %v", err)
	}
	if !needed {
		t.Error("a settled recording should be indexed")
	}
}

func TestNeedsIndexingSkipsUnchangedRecording(t *testing.T) {
	root := t.TempDir()
	path := writeSession(t, root, "done", time.Now(), [3]string{"You", SourceMic, "hello there"})
	ctx := context.Background()

	ix, store := newTestIndexer(t, root)

	// Simulate a completed index: write the meeting and stamp its content hash,
	// which is what lets a re-run skip it without loading a model.
	s, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	res := &Result{Session: s}
	if _, err := ix.writer.Write(ctx, res); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := ix.markIndexed(ctx, res); err != nil {
		t.Fatalf("markIndexed: %v", err)
	}

	needed, reason, err := ix.needsIndexing(ctx, path)
	if err != nil {
		t.Fatalf("needsIndexing: %v", err)
	}
	if needed || reason != skipUnchanged {
		t.Errorf("needed=%v reason=%v, want false/skipUnchanged", needed, reason)
	}

	// --force overrides the skip.
	ix.opts.Force = true
	if needed, _, _ := ix.needsIndexing(ctx, path); !needed {
		t.Error("--force should re-index an unchanged recording")
	}
	ix.opts.Force = false

	// Editing the transcript must bring it back into scope.
	data, _ := os.ReadFile(path)
	edited := append(data[:len(data)-1], []byte(`,"language":"en"}`)...)
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if needed, _, _ := ix.needsIndexing(ctx, path); !needed {
		t.Error("a changed recording should be re-indexed")
	}

	_ = store
}

func TestNeedsIndexingRejectsNonTranscript(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "bogus")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, SessionFileName)
	if err := os.WriteFile(path, []byte(`{"name":"not-a-transcript"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	ix, _ := newTestIndexer(t, root)
	// Pointing a scan at a folder of mixed downloads is a normal thing to do,
	// so a file that turns out to be something else is skipped rather than
	// reported as a failure.
	needed, reason, err := ix.needsIndexing(context.Background(), path)
	if err != nil {
		t.Fatalf("a non-transcript should be skipped, not fail: %v", err)
	}
	if needed || reason != skipNotTranscript {
		t.Errorf("needed=%v reason=%v, want false/skipNotTranscript", needed, reason)
	}
}

func TestEstimatedCost(t *testing.T) {
	r := &RunReport{Usage: Usage{InputTokens: 1_000_000, OutputTokens: 500_000}}
	got := r.EstimatedCost(0.30, 1.20)
	if math.Abs(got-0.90) > 1e-9 {
		t.Errorf("EstimatedCost() = %v, want 0.9", got)
	}
}

func TestUsageAdd(t *testing.T) {
	var u Usage
	u.Add(10, 5)
	u.Add(1, 2)
	if u.InputTokens != 11 || u.OutputTokens != 7 || u.Requests != 2 {
		t.Errorf("usage = %+v", u)
	}
}

func TestStatsAdd(t *testing.T) {
	a := Stats{Meetings: 1, Speakers: 2, Identified: 1, Topics: 3, Edges: 4}
	a.Add(Stats{Meetings: 1, Speakers: 3, Unidentified: 2, Decisions: 1, Edges: 5})
	if a.Meetings != 2 || a.Speakers != 5 || a.Identified != 1 || a.Unidentified != 2 ||
		a.Topics != 3 || a.Decisions != 1 || a.Edges != 9 {
		t.Errorf("stats = %+v", a)
	}
}

func TestWatchStopsOnContextCancel(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "one", time.Now(), [3]string{"You", SourceMic, "   "})

	ix, _ := newTestIndexer(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// A cancelled watch returns cleanly rather than reporting the cancellation
	// as a failure: stopping is the expected way a watch ends.
	done := make(chan error, 1)
	go func() { done <- ix.Watch(ctx, WatchOptions{Interval: time.Hour}) }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Watch() = %v, want nil on cancel", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Watch did not return after cancellation")
	}
}

func TestDiscoverSessionsAcrossDirectories(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	writeSession(t, a, "january", time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC),
		[3]string{"You", SourceMic, "january"})
	writeSession(t, b, "february", time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC),
		[3]string{"You", SourceMic, "february"})

	got, err := DiscoverSessionsIn([]string{a, b})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d sessions across two directories, want 2: %v", len(got), got)
	}
	// Still ordered by when the meetings happened, not by which directory.
	if !strings.Contains(got[0], "january") || !strings.Contains(got[1], "february") {
		t.Errorf("order = %v, want january then february", got)
	}
}

func TestDiscoverSessionsDeduplicatesOverlappingDirectories(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "one", time.Now(), [3]string{"You", SourceMic, "hello"})

	// The same directory named twice, and once as its own parent: a file
	// reachable more than once must still be indexed once.
	got, err := DiscoverSessionsIn([]string{root, root})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d, want 1 despite overlapping directories: %v", len(got), got)
	}
}

func TestDiscoverSessionsSkipsEmptyDirEntries(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "one", time.Now(), [3]string{"You", SourceMic, "hello"})

	// A blank entry in configuration must not be treated as the root of the
	// filesystem.
	got, err := DiscoverSessionsIn([]string{"", root, "  "})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d, want 1: %v", len(got), got)
	}
}
