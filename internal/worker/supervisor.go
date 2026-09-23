package worker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/imyousuf/CodeEagle/internal/watcher"
)

const (
	// DefaultSettle is how long a project must be quiet before it is synced.
	// Long enough that a build or a git checkout is one sync rather than
	// hundreds, short enough that a saved file is indexed while it is still
	// the thing being worked on.
	DefaultSettle = 30 * time.Second

	// DefaultBackoff is the first wait after a failed sync, doubling with
	// each consecutive failure.
	DefaultBackoff = 2 * time.Minute

	// DefaultMaxConcurrent bounds how many projects sync at once. A sync can
	// saturate a GPU or spend money, so the default is one: projects wait for
	// each other rather than competing.
	DefaultMaxConcurrent = 1

	// tickInterval is how often the tracker is asked what is due. It bounds
	// the lateness of a sync, not its promptness, so it can be coarse.
	tickInterval = 5 * time.Second
)

// Runner performs the sync for one project. It is an interface so the
// supervisor can be tested without spawning processes.
type Runner interface {
	Sync(ctx context.Context, p Project) error
	// Meetings enriches new recordings into meetings. It is a separate
	// command because indexing a transcript as a document and extracting the
	// speakers, decisions and follow-ups from it are separate passes.
	Meetings(ctx context.Context, p Project) error
}

// Options configures a Supervisor.
type Options struct {
	Settle        time.Duration
	Backoff       time.Duration
	MaxConcurrent int
	Runner        Runner
	Log           func(format string, args ...any)
	// Now supplies the current time. Nil means time.Now. It exists so the
	// settle and backoff rules can be tested without sleeping.
	Now func() time.Time
}

// Supervisor watches every project's files and syncs a project when its own
// files settle.
type Supervisor struct {
	projects []Project
	byName   map[string]Project
	tracker  *tracker
	opts     Options
	wg       sync.WaitGroup
}

// NewSupervisor prepares a supervisor over the given projects.
func NewSupervisor(projects []Project, opts Options) *Supervisor {
	if opts.Settle <= 0 {
		opts.Settle = DefaultSettle
	}
	if opts.Backoff <= 0 {
		opts.Backoff = DefaultBackoff
	}
	if opts.MaxConcurrent <= 0 {
		opts.MaxConcurrent = DefaultMaxConcurrent
	}
	if opts.Log == nil {
		opts.Log = func(string, ...any) {}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	byName := make(map[string]Project, len(projects))
	for _, p := range projects {
		byName[p.Name] = p
	}
	return &Supervisor{
		projects: projects,
		byName:   byName,
		tracker:  newTracker(opts.Settle),
		opts:     opts,
	}
}

// Run watches until the context is cancelled, then waits for any sync still
// in flight. A sync is not abandoned half-written just because the worker was
// asked to stop.
func (s *Supervisor) Run(ctx context.Context) error {
	if len(WatchRoots(s.projects)) == 0 {
		return fmt.Errorf("no watchable directories across %d project(s)", len(s.projects))
	}

	// One watcher per project, each with that project's own excludes. A
	// single watcher would have to merge every project's patterns, and a
	// merged exclude would stop another project's directory being watched at
	// all -- a miss nothing downstream can recover from.
	events := make(chan string, 256)
	var watched int
	for _, p := range s.projects {
		// Both kinds: a project's indexed directories and, where it has
		// them, the directories its recordings arrive in. Attribute sorts out
		// which command a given event implies.
		paths := append(append([]string(nil), p.Repos...), p.TranscriptDirs...)
		w, err := watcher.NewWatcher(watcher.WatcherConfig{
			Paths:           paths,
			ExcludePatterns: p.Excludes,
		})
		if err != nil {
			return fmt.Errorf("create watcher for %s: %w", p.Name, err)
		}
		defer w.Close()

		ch, err := w.Start(ctx)
		if err != nil {
			return fmt.Errorf("start watcher for %s: %w", p.Name, err)
		}
		watched += len(paths)

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			for ev := range ch {
				select {
				case events <- ev.Path:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	s.opts.Log("Watching %d director(ies) for %d project(s); settle %s, %d sync(s) at a time",
		watched, len(s.projects), s.opts.Settle, s.opts.MaxConcurrent)

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.opts.Log("Stopping; waiting for %d running sync(s)", s.tracker.Running())
			s.wg.Wait()
			return nil

		case path, ok := <-events:
			if !ok {
				s.wg.Wait()
				return nil
			}
			s.onEvent(path)

		case <-ticker.C:
			s.dispatch(ctx)
		}
	}
}

func (s *Supervisor) onEvent(path string) {
	if Ignored(path) {
		return
	}
	p, kind, ok := Attribute(s.projects, path)
	if !ok {
		return
	}
	s.tracker.Touch(p.Name, kind, s.opts.Now())
}

// dispatch starts syncs for whatever is due, up to the concurrency cap.
func (s *Supervisor) dispatch(ctx context.Context) {
	for _, name := range s.tracker.Due(s.opts.Now()) {
		if s.tracker.Running() >= s.opts.MaxConcurrent {
			return
		}
		p, ok := s.byName[name]
		if !ok {
			continue
		}

		needSync, needMeetings := s.tracker.Started(name)
		s.wg.Add(1)
		go func(p Project, needSync, needMeetings bool) {
			defer s.wg.Done()

			start := s.opts.Now()
			var err error

			if needSync {
				s.opts.Log("Syncing %s (%s)", p.Name, p.Root)
				err = s.opts.Runner.Sync(ctx, p)
			}
			// Enrichment is skipped when the sync before it failed: a
			// half-indexed project is a poor base to extract meetings from,
			// and the retry will run both.
			if err == nil && needMeetings {
				s.opts.Log("Enriching meetings for %s", p.Name)
				err = s.opts.Runner.Meetings(ctx, p)
			}

			took := s.opts.Now().Sub(start).Round(time.Second)
			if err != nil && ctx.Err() == nil {
				s.opts.Log("Sync %s failed after %s: %v", p.Name, took, err)
			} else if err == nil {
				s.opts.Log("Synced %s in %s", p.Name, took)
			}
			s.tracker.Finished(p.Name, s.opts.Now(), err, s.opts.Backoff, needSync, needMeetings)
		}(p, needSync, needMeetings)
	}
}

// SyncOnce runs one sync per project, ignoring the watch entirely. It is what
// `worker --once` does, and the cheapest way to see the whole thing work.
func (s *Supervisor) SyncOnce(ctx context.Context) error {
	var firstErr error
	for _, p := range s.projects {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.opts.Log("Syncing %s (%s)", p.Name, p.Root)
		if err := s.opts.Runner.Sync(ctx, p); err != nil {
			s.opts.Log("Sync %s failed: %v", p.Name, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if len(p.TranscriptDirs) > 0 {
			s.opts.Log("Enriching meetings for %s", p.Name)
			if err := s.opts.Runner.Meetings(ctx, p); err != nil {
				s.opts.Log("Meetings sync %s failed: %v", p.Name, err)
				if firstErr == nil {
					firstErr = err
				}
			}
		}
	}
	return firstErr
}

// ExecRunner runs `codeeagle sync` as a subprocess, in the project's root and
// against the project's configuration.
//
// Re-executing this same binary rather than a `codeeagle` found on PATH means
// the worker and the sync are always the same build. A worker left running
// across an upgrade would otherwise start driving a different version than the
// one it was built alongside.
type ExecRunner struct {
	// Bin is the binary to run. Empty means this executable.
	Bin string
	// Stdout and Stderr receive the sync's output; nil discards it.
	Stdout, Stderr *os.File
}

// Sync runs one project's sync to completion.
func (r ExecRunner) Sync(ctx context.Context, p Project) error {
	return r.run(ctx, p, "sync")
}

// Meetings runs one project's meeting enrichment to completion.
func (r ExecRunner) Meetings(ctx context.Context, p Project) error {
	return r.run(ctx, p, "meetings", "sync")
}

func (r ExecRunner) run(ctx context.Context, p Project, args ...string) error {
	bin := r.Bin
	if bin == "" {
		self, err := os.Executable()
		if err != nil {
			return fmt.Errorf("locate this executable: %w", err)
		}
		bin = self
	}

	cfg := filepath.Join(p.ConfigDir, "config.yaml")
	full := append([]string{"--config", cfg}, args...)
	cmd := exec.CommandContext(ctx, bin, full...)
	// The working directory matters beyond tidiness: a sync resolves relative
	// repository paths and discovers the project registry from where it runs.
	cmd.Dir = p.Root
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", strings.Join(args, " "), p.Name, err)
	}
	return nil
}
