package watcher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// EventOp represents the type of file system operation.
type EventOp int

const (
	Create EventOp = iota
	Write
	Remove
	Rename
)

// String returns the string representation of EventOp.
func (op EventOp) String() string {
	switch op {
	case Create:
		return "Create"
	case Write:
		return "Write"
	case Remove:
		return "Remove"
	case Rename:
		return "Rename"
	default:
		return "Unknown"
	}
}

// Event represents a file system change event.
type Event struct {
	Path string
	Op   EventOp
	Time time.Time
}

// WatcherConfig holds configuration for the file system watcher.
type WatcherConfig struct {
	Paths             []string
	ExcludePatterns   []string
	GitIgnorePatterns []string
	// Tiering, when set, holds a watch only on directories in use and finds
	// changes in the rest by scanning them; see Tiering. Unset, every
	// directory under Paths is watched for as long as the watcher runs.
	Tiering *Tiering
	// Logf receives what the watcher has to report: a directory it could not
	// watch, a watch budget reached. Nil discards it.
	Logf func(format string, args ...any)
}

// Watcher watches file system paths for changes and emits debounced events.
type Watcher struct {
	cfg     WatcherConfig
	matcher *GitIgnoreMatcher
	fsw     *fsnotify.Watcher
	mu      sync.Mutex
	closed  bool

	// addWatch and removeWatch default to the fsnotify watcher's; tests
	// replace them to make a watch fail.
	addWatch    func(string) error
	removeWatch func(string) error

	// tmu guards the tiering state and the statistics.
	tmu   sync.Mutex
	hot   map[string]*dirState
	cold  map[string]*dirState
	stats Stats
}

// NewWatcher creates a new file system watcher with the given configuration.
func NewWatcher(cfg WatcherConfig) (*Watcher, error) {
	allPatterns := make([]string, 0, len(cfg.ExcludePatterns)+len(cfg.GitIgnorePatterns))
	allPatterns = append(allPatterns, cfg.ExcludePatterns...)
	allPatterns = append(allPatterns, cfg.GitIgnorePatterns...)

	matcher := NewGitIgnoreMatcher(cfg.Paths, allPatterns)
	if err := matcher.LoadPatterns(); err != nil {
		return nil, err
	}

	return &Watcher{
		cfg:     cfg,
		matcher: matcher,
	}, nil
}

// Start begins watching configured paths and returns a channel of debounced events.
// It blocks until the context is cancelled or an error occurs during setup.
func (w *Watcher) Start(ctx context.Context) (<-chan Event, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	w.mu.Lock()
	w.fsw = fsw
	if w.addWatch == nil {
		w.addWatch = fsw.Add
	}
	if w.removeWatch == nil {
		w.removeWatch = fsw.Remove
	}
	w.mu.Unlock()

	out := make(chan Event, 100)
	if w.cfg.Tiering != nil {
		w.startTiered()
		go w.scanLoop(ctx, out)
	} else {
		for _, root := range w.cfg.Paths {
			w.addRecursive(root)
		}
	}
	go w.eventLoop(ctx, fsw, out)
	return out, nil
}

// Stats reports how many directories are watched and scanned, and what has
// failed. Without tiering, Scanned is always zero.
func (w *Watcher) Stats() Stats {
	w.tmu.Lock()
	defer w.tmu.Unlock()
	st := w.stats
	if w.cfg.Tiering != nil {
		st.Watched, st.Scanned = len(w.hot), len(w.cold)
	}
	return st
}

func (w *Watcher) logf(format string, args ...any) {
	if w.cfg.Logf != nil {
		w.cfg.Logf(format, args...)
	}
}

// addFailed records a directory that could not be watched. Only the first is
// logged: past the system's limit every remaining directory fails the same
// way, and thousands of identical lines say less than one with a count.
func (w *Watcher) addFailed(dir string, err error) {
	w.tmu.Lock()
	w.stats.AddFailures++
	first := w.stats.AddFailures == 1
	if first {
		w.stats.FirstAddError = fmt.Sprintf("%s: %v", dir, err)
	}
	w.tmu.Unlock()
	if first {
		w.logf("Warning: cannot watch %s: %v; directories that cannot be watched are not seen to change "+
			"(see fs.inotify.max_user_watches, or run the worker with tiering)", dir, err)
	}
}

// Close shuts down the watcher and releases resources.
func (w *Watcher) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}
	w.closed = true

	if w.fsw != nil {
		return w.fsw.Close()
	}
	return nil
}

// addRecursive watches every directory under root that is not excluded.
//
// A directory that cannot be watched no longer ends the walk. It used to:
// the first failure, typically the system's watch limit, returned from the
// walk, so every directory after it went unwatched without a word.
func (w *Watcher) addRecursive(root string) {
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible entries
		}
		if !info.IsDir() {
			return nil
		}
		if w.matcher.Match(path) {
			return filepath.SkipDir
		}
		if err := w.addWatch(path); err != nil {
			w.addFailed(path, err)
			return nil
		}
		w.tmu.Lock()
		w.stats.Watched++
		w.tmu.Unlock()
		return nil
	})
}

const debounceWindow = 100 * time.Millisecond

func (w *Watcher) eventLoop(ctx context.Context, fsw *fsnotify.Watcher, out chan<- Event) {
	defer close(out)

	// Debounce state: map from path to pending event and timer.
	type pending struct {
		event Event
		timer *time.Timer
	}
	pendingEvents := make(map[string]*pending)
	var mu sync.Mutex

	emit := func(evt Event) {
		select {
		case out <- evt:
		case <-ctx.Done():
		}
	}

	for {
		select {
		case <-ctx.Done():
			// Drain pending timers.
			mu.Lock()
			for _, p := range pendingEvents {
				p.timer.Stop()
			}
			mu.Unlock()
			return

		case fsEvent, ok := <-fsw.Events:
			if !ok {
				return
			}

			// Filter ignored paths.
			if w.matcher.Match(fsEvent.Name) {
				continue
			}

			// Convert fsnotify op to our EventOp.
			op, valid := convertOp(fsEvent.Op)
			if !valid {
				continue
			}

			evt := Event{
				Path: fsEvent.Name,
				Op:   op,
				Time: time.Now(),
			}

			// If a new directory is created, add it to the watcher.
			if op == Create {
				if info, err := os.Stat(fsEvent.Name); err == nil && info.IsDir() {
					if w.cfg.Tiering != nil {
						w.addTree(fsEvent.Name)
					} else {
						w.addRecursive(fsEvent.Name)
					}
				}
			}
			if w.cfg.Tiering != nil {
				w.touch(fsEvent.Name, op)
			}

			// Debounce: reset the timer for this path.
			mu.Lock()
			if p, exists := pendingEvents[fsEvent.Name]; exists {
				p.timer.Stop()
				p.event = evt
				p.timer = time.AfterFunc(debounceWindow, func() {
					mu.Lock()
					e := pendingEvents[fsEvent.Name]
					delete(pendingEvents, fsEvent.Name)
					mu.Unlock()
					if e != nil {
						emit(e.event)
					}
				})
			} else {
				p := &pending{event: evt}
				p.timer = time.AfterFunc(debounceWindow, func() {
					mu.Lock()
					e := pendingEvents[fsEvent.Name]
					delete(pendingEvents, fsEvent.Name)
					mu.Unlock()
					if e != nil {
						emit(e.event)
					}
				})
				pendingEvents[fsEvent.Name] = p
			}
			mu.Unlock()

		case _, ok := <-fsw.Errors:
			if !ok {
				return
			}
			// Log errors but continue watching.
		}
	}
}

func convertOp(op fsnotify.Op) (EventOp, bool) {
	switch {
	case op.Has(fsnotify.Create):
		return Create, true
	case op.Has(fsnotify.Write):
		return Write, true
	case op.Has(fsnotify.Remove):
		return Remove, true
	case op.Has(fsnotify.Rename):
		return Rename, true
	default:
		return 0, false
	}
}
