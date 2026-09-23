package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeRunner records which projects were synced, without spawning anything.
type fakeRunner struct {
	mu          sync.Mutex
	ran         []string
	meetings    []string
	err         error
	meetingsErr error
	block       chan struct{} // if non-nil, Sync waits on it
	inCall      chan string   // if non-nil, receives the project name on entry
}

func (f *fakeRunner) Meetings(_ context.Context, p Project) error {
	f.mu.Lock()
	f.meetings = append(f.meetings, p.Name)
	f.mu.Unlock()
	return f.meetingsErr
}

func (f *fakeRunner) meetingsRan() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.meetings...)
}

func (f *fakeRunner) Sync(_ context.Context, p Project) error {
	if f.inCall != nil {
		f.inCall <- p.Name
	}
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	f.ran = append(f.ran, p.Name)
	f.mu.Unlock()
	return f.err
}

func (f *fakeRunner) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ran...)
}

func testProjects() []Project {
	return []Project{
		{Name: "alpha", Root: "/p/alpha", ConfigDir: "/p/alpha/.CodeEagle",
			Repos: []string{"/p/alpha"}},
		{Name: "beta", Root: "/p/beta", ConfigDir: "/p/beta/.CodeEagle",
			Repos: []string{"/p/beta"}},
	}
}

// clock is a hand-wound clock so the settle and backoff rules can be
// exercised exactly, with no sleeping.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestSupervisor(r Runner, settle time.Duration) (*Supervisor, *clock) {
	c := &clock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	s := NewSupervisor(testProjects(), Options{
		Settle: settle, MaxConcurrent: 1, Runner: r, Now: c.Now,
	})
	return s, c
}

// TestEventsInStateDirectoriesNeverTriggerASync is the loop guard. A sync
// writes into .CodeEagle; if those writes counted as changes, finishing one
// sync would start the next, forever.
func TestEventsInStateDirectoriesNeverTriggerASync(t *testing.T) {
	r := &fakeRunner{}
	s, c := newTestSupervisor(r, time.Second)

	for _, p := range []string{
		"/p/alpha/.CodeEagle/graph.db/000001.sst",
		"/p/alpha/.CodeEagle/queue.db/MANIFEST",
		"/p/alpha/.git/index",
	} {
		s.onEvent(p)
	}

	c.advance(2 * time.Second)
	s.dispatch(context.Background())
	s.wg.Wait()

	if got := r.calls(); len(got) != 0 {
		t.Errorf("a sync ran for CodeEagle's own writes: %v", got)
	}
}

func TestAChangeSyncsTheOwningProjectOnly(t *testing.T) {
	r := &fakeRunner{}
	s, c := newTestSupervisor(r, time.Second)

	s.onEvent("/p/beta/src/main.go")
	c.advance(2 * time.Second)
	s.dispatch(context.Background())
	s.wg.Wait()

	got := r.calls()
	if len(got) != 1 || got[0] != "beta" {
		t.Errorf("synced %v, want exactly [beta]", got)
	}
}

func TestPathsNoProjectIndexesAreIgnored(t *testing.T) {
	r := &fakeRunner{}
	s, c := newTestSupervisor(r, time.Second)

	s.onEvent("/somewhere/else/file.go")
	c.advance(2 * time.Second)
	s.dispatch(context.Background())
	s.wg.Wait()

	if got := r.calls(); len(got) != 0 {
		t.Errorf("synced %v for a path no project indexes", got)
	}
}

func TestConcurrencyCapIsRespected(t *testing.T) {
	block := make(chan struct{})
	entered := make(chan string, 2)
	r := &fakeRunner{block: block, inCall: entered}
	s, c := newTestSupervisor(r, time.Second)

	s.onEvent("/p/alpha/a.go")
	s.onEvent("/p/beta/b.go")
	c.advance(2 * time.Second)
	s.dispatch(context.Background())

	// With a cap of one, exactly one sync may have started.
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("no sync started at all")
	}
	select {
	case name := <-entered:
		t.Fatalf("a second sync (%s) started despite a concurrency cap of 1", name)
	case <-time.After(200 * time.Millisecond):
	}

	close(block)
	s.wg.Wait()
}

func TestAFailedSyncIsNotRetriedImmediately(t *testing.T) {
	r := &fakeRunner{err: errors.New("boom")}
	c := &clock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	s := NewSupervisor(testProjects(), Options{
		Settle: time.Second, MaxConcurrent: 1, Backoff: time.Hour, Runner: r, Now: c.Now,
	})

	s.onEvent("/p/alpha/a.go")
	c.advance(2 * time.Second)
	s.dispatch(context.Background())
	s.wg.Wait()

	// The project stays dirty, but the backoff must hold it back.
	c.advance(2 * time.Second)
	s.dispatch(context.Background())
	s.wg.Wait()

	if got := r.calls(); len(got) != 1 {
		t.Errorf("ran %d time(s), want 1; a failing sync is spinning", len(got))
	}
}

func TestSyncOnceCoversEveryProject(t *testing.T) {
	r := &fakeRunner{}
	s, _ := newTestSupervisor(r, time.Hour) // settle is irrelevant to --once

	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if got := r.calls(); len(got) != 2 {
		t.Errorf("SyncOnce ran %v, want both projects", got)
	}
}

func TestSyncOnceReportsFailureButKeepsGoing(t *testing.T) {
	r := &fakeRunner{err: errors.New("boom")}
	s, _ := newTestSupervisor(r, time.Hour)

	if err := s.SyncOnce(context.Background()); err == nil {
		t.Error("SyncOnce returned nil despite every sync failing")
	}
	if got := r.calls(); len(got) != 2 {
		t.Errorf("stopped after %d project(s); one failure must not skip the rest", len(got))
	}
}

// TestARecordingRunsMeetingsSyncOnly is the point of the transcript split: a
// new recording must not drag an entire project through an ordinary sync.
func TestARecordingRunsMeetingsSyncOnly(t *testing.T) {
	r := &fakeRunner{}
	c := &clock{now: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)}
	projects := []Project{{
		Name: "home", Root: "/p/home", ConfigDir: "/p/home/.CodeEagle",
		Repos:          []string{"/p/home/Documents"},
		TranscriptDirs: []string{"/p/home/sessions"},
	}}
	s := NewSupervisor(projects, Options{
		Settle: time.Second, MaxConcurrent: 1, Runner: r, Now: c.Now,
	})

	s.onEvent("/p/home/sessions/abc/session.json")
	c.advance(2 * time.Second)
	s.dispatch(context.Background())
	s.wg.Wait()

	if got := r.calls(); len(got) != 0 {
		t.Errorf("an ordinary sync ran for a recording: %v", got)
	}
	if got := r.meetingsRan(); len(got) != 1 || got[0] != "home" {
		t.Errorf("meetings sync ran %v, want exactly [home]", got)
	}
}

func TestAnOrdinaryFileDoesNotRunMeetingsSync(t *testing.T) {
	r := &fakeRunner{}
	c := &clock{now: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)}
	projects := []Project{{
		Name: "home", Root: "/p/home", ConfigDir: "/p/home/.CodeEagle",
		Repos:          []string{"/p/home/Documents"},
		TranscriptDirs: []string{"/p/home/sessions"},
	}}
	s := NewSupervisor(projects, Options{
		Settle: time.Second, MaxConcurrent: 1, Runner: r, Now: c.Now,
	})

	s.onEvent("/p/home/Documents/notes.md")
	c.advance(2 * time.Second)
	s.dispatch(context.Background())
	s.wg.Wait()

	if got := r.calls(); len(got) != 1 {
		t.Errorf("sync ran %v, want exactly [home]", got)
	}
	if got := r.meetingsRan(); len(got) != 0 {
		t.Errorf("meetings sync ran for an ordinary file: %v", got)
	}
}

// TestBothRunWhenBothChanged covers a project where code and a recording both
// changed inside one settle window.
func TestBothRunWhenBothChanged(t *testing.T) {
	r := &fakeRunner{}
	c := &clock{now: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)}
	projects := []Project{{
		Name: "home", Root: "/p/home", ConfigDir: "/p/home/.CodeEagle",
		Repos:          []string{"/p/home/Documents"},
		TranscriptDirs: []string{"/p/home/sessions"},
	}}
	s := NewSupervisor(projects, Options{
		Settle: time.Second, MaxConcurrent: 1, Runner: r, Now: c.Now,
	})

	s.onEvent("/p/home/Documents/notes.md")
	s.onEvent("/p/home/sessions/abc/session.json")
	c.advance(2 * time.Second)
	s.dispatch(context.Background())
	s.wg.Wait()

	if got := r.calls(); len(got) != 1 {
		t.Errorf("sync ran %v, want once", got)
	}
	if got := r.meetingsRan(); len(got) != 1 {
		t.Errorf("meetings sync ran %v, want once", got)
	}
}
