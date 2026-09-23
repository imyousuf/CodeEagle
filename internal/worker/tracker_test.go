package worker

import (
	"errors"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

func TestNotDueUntilQuiet(t *testing.T) {
	tr := newTracker(30 * time.Second)
	tr.Touch("a", KindCode, t0)

	if due := tr.Due(t0.Add(29 * time.Second)); len(due) != 0 {
		t.Errorf("due after 29s of a 30s settle: %v", due)
	}
	if due := tr.Due(t0.Add(30 * time.Second)); len(due) != 1 {
		t.Errorf("not due after the settle period: %v", due)
	}
}

func TestABurstOfChangesCostsOneSync(t *testing.T) {
	tr := newTracker(30 * time.Second)
	// A save storm: each change pushes the deadline out.
	for i := range 10 {
		tr.Touch("a", KindCode, t0.Add(time.Duration(i)*time.Second))
	}
	if due := tr.Due(t0.Add(35 * time.Second)); len(due) != 0 {
		t.Errorf("due too early; the last change was at t+9s: %v", due)
	}
	due := tr.Due(t0.Add(39 * time.Second))
	if len(due) != 1 || due[0] != "a" {
		t.Errorf("Due() = %v, want exactly [a]", due)
	}
}

func TestARunningProjectIsNotStartedTwice(t *testing.T) {
	tr := newTracker(time.Second)
	tr.Touch("a", KindCode, t0)
	_, _ = tr.Started("a")

	if due := tr.Due(t0.Add(time.Hour)); len(due) != 0 {
		t.Errorf("a project already syncing was offered again: %v", due)
	}
}

// TestAChangeDuringASyncSurvivesIt is the important one: the running sync may
// have already walked past that file, so folding the change into the in-flight
// run would lose it silently.
func TestAChangeDuringASyncSurvivesIt(t *testing.T) {
	tr := newTracker(30 * time.Second)
	tr.Touch("a", KindCode, t0)
	_, _ = tr.Started("a")

	mid := t0.Add(10 * time.Second)
	tr.Touch("a", KindCode, mid)
	tr.Finished("a", t0.Add(60*time.Second), nil, time.Minute, true, false)

	// Still within the settle window measured from the mid-run change.
	if due := tr.Due(mid.Add(29 * time.Second)); len(due) != 0 {
		t.Errorf("due before the mid-run change settled: %v", due)
	}
	due := tr.Due(mid.Add(31 * time.Second))
	if len(due) != 1 || due[0] != "a" {
		t.Errorf("Due() = %v; the change made during the sync was lost", due)
	}
}

func TestSuccessClearsTheProject(t *testing.T) {
	tr := newTracker(time.Second)
	tr.Touch("a", KindCode, t0)
	_, _ = tr.Started("a")
	tr.Finished("a", t0.Add(time.Minute), nil, time.Minute, true, false)

	if due := tr.Due(t0.Add(time.Hour)); len(due) != 0 {
		t.Errorf("a cleanly synced project is still due: %v", due)
	}
}

func TestFailureBacksOffAndRetries(t *testing.T) {
	tr := newTracker(time.Second)
	boom := errors.New("sync failed")

	tr.Touch("a", KindCode, t0)
	_, _ = tr.Started("a")
	done := t0.Add(10 * time.Second)
	tr.Finished("a", done, boom, time.Minute, true, false)

	// Held back for the backoff period rather than retried immediately.
	if due := tr.Due(done.Add(30 * time.Second)); len(due) != 0 {
		t.Errorf("retried inside the backoff window: %v", due)
	}
	if due := tr.Due(done.Add(61 * time.Second)); len(due) != 1 {
		t.Errorf("never retried after the backoff expired: %v", due)
	}
}

func TestBackoffGrowsWithConsecutiveFailures(t *testing.T) {
	tr := newTracker(time.Second)
	boom := errors.New("nope")
	now := t0

	for range 3 {
		tr.Touch("a", KindCode, now)
		_, _ = tr.Started("a")
		now = now.Add(time.Second)
		tr.Finished("a", now, boom, time.Minute, true, false)
	}
	// Third consecutive failure: 1m << 2 == 4m.
	if due := tr.Due(now.Add(3 * time.Minute)); len(due) != 0 {
		t.Errorf("backoff did not grow with repeated failures: %v", due)
	}
	if due := tr.Due(now.Add(5 * time.Minute)); len(due) != 1 {
		t.Errorf("never became eligible again: %v", due)
	}
}

func TestSuccessResetsTheBackoff(t *testing.T) {
	tr := newTracker(time.Second)
	now := t0

	tr.Touch("a", KindCode, now)
	_, _ = tr.Started("a")
	now = now.Add(time.Second)
	tr.Finished("a", now, errors.New("x"), time.Minute, true, false)

	tr.Touch("a", KindCode, now.Add(2*time.Minute))
	_, _ = tr.Started("a")
	now = now.Add(3 * time.Minute)
	tr.Finished("a", now, nil, time.Minute, true, false)

	// A later change must be eligible on the normal settle, not a backoff.
	tr.Touch("a", KindCode, now)
	if due := tr.Due(now.Add(2 * time.Second)); len(due) != 1 {
		t.Errorf("a success did not clear the earlier failure's backoff: %v", due)
	}
}

func TestOldestChangeIsOfferedFirst(t *testing.T) {
	tr := newTracker(time.Second)
	tr.Touch("late", KindCode, t0.Add(10*time.Second))
	tr.Touch("early", KindCode, t0)

	due := tr.Due(t0.Add(time.Minute))
	if len(due) != 2 || due[0] != "early" {
		t.Errorf("Due() = %v, want the oldest change first", due)
	}
}

func TestRunningCounts(t *testing.T) {
	tr := newTracker(time.Second)
	tr.Touch("a", KindCode, t0)
	tr.Touch("b", KindCode, t0)
	_, _ = tr.Started("a")

	if got := tr.Running(); got != 1 {
		t.Errorf("Running() = %d, want 1", got)
	}
	_, _ = tr.Started("b")
	if got := tr.Running(); got != 2 {
		t.Errorf("Running() = %d, want 2", got)
	}
	tr.Finished("a", t0, nil, time.Minute, true, false)
	if got := tr.Running(); got != 1 {
		t.Errorf("Running() = %d, want 1", got)
	}
}
