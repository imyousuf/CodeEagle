package watcher

import (
	"context"
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
}

// Watcher watches file system paths for changes and emits debounced events.
type Watcher struct {
	cfg     WatcherConfig
	matcher *GitIgnoreMatcher
	fsw     *fsnotify.Watcher
	mu      sync.Mutex
	closed  bool
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
	w.mu.Unlock()

	// Recursively add directories.
	for _, root := range w.cfg.Paths {
		if err := w.addRecursive(root); err != nil {
			fsw.Close()
			return nil, err
		}
	}

	out := make(chan Event, 100)
	go w.eventLoop(ctx, fsw, out)
	return out, nil
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

func (w *Watcher) addRecursive(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible entries
		}
		if !info.IsDir() {
			return nil
		}
		if w.matcher.Match(path) {
			return filepath.SkipDir
		}
		return w.fsw.Add(path)
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
					_ = w.addRecursive(fsEvent.Name)
				}
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
