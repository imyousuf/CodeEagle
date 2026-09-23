package worker

import (
	"sort"
	"sync"
	"time"
)

// tracker decides which projects are due for a sync.
//
// It is deliberately free of timers and goroutines: every method takes the
// current time. Timing lives in the supervisor, so this -- the part with the
// interesting rules -- can be tested exactly rather than with sleeps.
type tracker struct {
	mu     sync.Mutex
	settle time.Duration
	state  map[string]*projectState
}

type projectState struct {
	// dirtyAt is when the most recent change arrived. A sync is due once
	// this has been quiet for the settle period, so a burst of saves costs
	// one sync rather than one per file.
	dirtyAt time.Time
	dirty   bool
	// needSync and needMeetings record which commands the pending change
	// calls for. A recording implies meetings enrichment only; forcing an
	// ordinary sync as well would walk every indexed directory for nothing.
	needSync     bool
	needMeetings bool
	// running means a sync is in flight; another must not start.
	running bool
	// againAt records a change that arrived while a sync was running. The
	// running sync may already have walked past that file, so the change
	// must survive the run rather than be folded into it.
	againAt time.Time
	again   bool
	// failures counts consecutive failed syncs, used to back off.
	failures int
	// nextEligible holds a project back after a failure.
	nextEligible time.Time
}

func newTracker(settle time.Duration) *tracker {
	return &tracker{settle: settle, state: make(map[string]*projectState)}
}

func (t *tracker) get(name string) *projectState {
	s, ok := t.state[name]
	if !ok {
		s = &projectState{}
		t.state[name] = s
	}
	return s
}

// Touch records that a project's files changed, and what kind of change it
// was.
func (t *tracker) Touch(name string, kind Kind, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	s := t.get(name)
	if s.running {
		s.again, s.againAt = true, now
		// Remember it across the run: a change arriving mid-sync must not
		// lose the fact that it was a recording rather than a source file.
		s.markKind(kind)
		return
	}
	s.dirty, s.dirtyAt = true, now
	s.markKind(kind)
}

func (s *projectState) markKind(kind Kind) {
	switch kind {
	case KindTranscript:
		s.needMeetings = true
	default:
		s.needSync = true
	}
}

// Due returns the projects that have been quiet long enough to sync, oldest
// change first so nothing starves behind a busier project.
func (t *tracker) Due(now time.Time) []string {
	t.mu.Lock()
	defer t.mu.Unlock()

	var due []string
	for name, s := range t.state {
		if !s.dirty || s.running {
			continue
		}
		if now.Before(s.nextEligible) {
			continue
		}
		if now.Sub(s.dirtyAt) < t.settle {
			continue
		}
		due = append(due, name)
	}
	sort.Slice(due, func(i, j int) bool {
		return t.state[due[i]].dirtyAt.Before(t.state[due[j]].dirtyAt)
	})
	return due
}

// Started marks a project as in flight and reports which commands the
// pending change calls for, clearing them.
func (t *tracker) Started(name string) (needSync, needMeetings bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	s := t.get(name)
	s.running = true
	s.dirty = false
	needSync, needMeetings = s.needSync, s.needMeetings
	s.needSync, s.needMeetings = false, false
	return needSync, needMeetings
}

// Finished records the outcome. A change that arrived mid-run becomes the new
// pending change, so it is never lost.
func (t *tracker) Finished(name string, now time.Time, err error, backoff time.Duration,
	needSync, needMeetings bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	s := t.get(name)
	s.running = false

	if err != nil {
		// A failed run leaves its work undone, so put back what it was for.
		if needSync {
			s.needSync = true
		}
		if needMeetings {
			s.needMeetings = true
		}
		s.failures++
		// Exponential, capped: a project whose sync is broken must not spin.
		wait := backoff << min(s.failures-1, 5)
		s.nextEligible = now.Add(wait)
		// A failed sync leaves the project unsynced, so it stays dirty and
		// will be retried once the backoff expires.
		s.dirty = true
		if s.dirtyAt.IsZero() {
			s.dirtyAt = now
		}
	} else {
		s.failures = 0
		s.nextEligible = time.Time{}
	}

	if s.again {
		s.dirty, s.dirtyAt = true, s.againAt
		s.again, s.againAt = false, time.Time{}
	}
}

// Running reports how many syncs are in flight.
func (t *tracker) Running() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	n := 0
	for _, s := range t.state {
		if s.running {
			n++
		}
	}
	return n
}
