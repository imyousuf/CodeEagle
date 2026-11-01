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

	// WatchAll holds a watch on every directory for as long as the worker
	// runs, as it once did. Otherwise only directories changed within
	// HotWindow are watched and the rest are scanned every ScanInterval,
	// within MaxWatches across all projects; see watcher.Tiering.
	WatchAll     bool
	HotWindow    time.Duration
	ScanInterval time.Duration
	// MaxWatches is the watch budget; zero means watcher.DefaultMaxWatches.
	MaxWatches int
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
	var budget *watcher.Budget
	if !s.opts.WatchAll {
		max := s.opts.MaxWatches
		if max <= 0 {
			max = watcher.DefaultMaxWatches()
		}
		budget = watcher.NewBudget(max)
	}
	var watchers []*watcher.Watcher
	for _, p := range s.projects {
		paths := WatchPaths(p)
		cfg := watcher.WatcherConfig{
			Paths:           paths,
			ExcludePatterns: p.Excludes,
			Logf: func(format string, args ...any) {
				s.opts.Log("%s: "+format, append([]any{p.Name}, args...)...)
			},
		}
		if budget != nil {
			cfg.Tiering = &watcher.Tiering{HotWindow: s.opts.HotWindow, ScanInterval: s.opts.ScanInterval, Budget: budget}
		}
		w, err := watcher.NewWatcher(cfg)
		if err != nil {
			return fmt.Errorf("create watcher for %s: %w", p.Name, err)
		}
		defer w.Close()

		ch, err := w.Start(ctx)
		if err != nil {
			return fmt.Errorf("start watcher for %s: %w", p.Name, err)
		}
		watchers = append(watchers, w)

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

	last := s.logWatches(watchers, budget, watcher.Stats{}, true)

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	// What is watched changes as directories go quiet or come back into use;
	// it is reported when it does, at most once a scan.
	statsEvery := s.opts.ScanInterval
	if statsEvery <= 0 {
		statsEvery = watcher.DefaultScanInterval
	}
	statsTicker := time.NewTicker(statsEvery)
	defer statsTicker.Stop()

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

		case <-statsTicker.C:
			last = s.logWatches(watchers, budget, last, false)
		}
	}
}

// logWatches reports how many directories are watched and scanned across
// every project, when that has changed since prev or first is set, and
// returns the totals.
func (s *Supervisor) logWatches(watchers []*watcher.Watcher, budget *watcher.Budget, prev watcher.Stats, first bool) watcher.Stats {
	var total watcher.Stats
	for _, w := range watchers {
		st := w.Stats()
		total.Watched += st.Watched
		total.Scanned += st.Scanned
		total.AddFailures += st.AddFailures
	}
	if !first && total.Watched == prev.Watched && total.Scanned == prev.Scanned && total.AddFailures == prev.AddFailures {
		return total
	}
	failed := ""
	if total.AddFailures > 0 {
		failed = fmt.Sprintf("; %d could not be watched", total.AddFailures)
	}
	if budget == nil {
		s.opts.Log("Watching %d director(ies) for %d project(s)%s; settle %s, %d sync(s) at a time",
			total.Watched, len(s.projects), failed, s.opts.Settle, s.opts.MaxConcurrent)
		return total
	}
	scan := s.opts.ScanInterval
	if scan <= 0 {
		scan = watcher.DefaultScanInterval
	}
	s.opts.Log("Watching %d director(ies) and scanning %d more every %s for %d project(s) (watch budget %d)%s; "+
		"settle %s, %d sync(s) at a time",
		total.Watched, total.Scanned, HumanDuration(scan), len(s.projects), budget.Max(), failed, s.opts.Settle, s.opts.MaxConcurrent)
	return total
}

// HumanDuration writes a duration the way a person would: whole days as
// days, anything shorter as Go writes it, without trailing zero units.
func HumanDuration(d time.Duration) string {
	if d >= 24*time.Hour && d%(24*time.Hour) == 0 {
		days := int(d / (24 * time.Hour))
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	}
	s := d.String()
	for _, unit := range []string{"m0s", "h0m"} {
		if strings.HasSuffix(s, unit) {
			s = strings.TrimSuffix(s, unit[1:])
		}
	}
	return s
}

// WatchPaths returns every directory watched on behalf of one project:
// the directories it indexes, and where it has them, the directories its
// recordings arrive in. Attribute sorts out which command a given event
// implies.
//
// Exported and separate from Run so a test can see it. Computed inline it was
// unreachable, because Run opens real watchers -- and that is precisely how
// transcript directories came to be known to WatchRoots, which only reports,
// and not to the watcher, which is what actually matters.
func WatchPaths(p Project) []string {
	return append(append([]string(nil), p.Repos...), p.TranscriptDirs...)
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
// Re-executing the binary the worker was started from, rather than a
// `codeeagle` found on PATH, means a sync never runs some other installation
// that happens to be first on PATH. After an upgrade in place it is the new
// build, which is what the upgrade was for.
type ExecRunner struct {
	// Bin is the binary to run. NewExecRunner sets it to this executable.
	Bin string
	// Stdout and Stderr receive the sync's output; nil discards it.
	Stdout, Stderr *os.File
}

// NewExecRunner returns a runner for this executable, located now rather than
// when each sync starts.
//
// Asking later is wrong once the binary is replaced under a running worker,
// which the worker itself causes: every sync it starts checks for an update,
// and the updater renames the running file to codeeagle.old, installs the new
// one and deletes the old. os.Executable then follows this process's own
// inode and names the deleted codeeagle.old, so every later sync failed to
// start. The path found at startup still names what is installed there.
func NewExecRunner(stdout, stderr *os.File) (ExecRunner, error) {
	self, err := os.Executable()
	if err != nil {
		return ExecRunner{}, fmt.Errorf("locate this executable: %w", err)
	}
	return ExecRunner{Bin: self, Stdout: stdout, Stderr: stderr}, nil
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
	// No fallback to os.Executable here: by the time a sync runs, that may
	// name a file the updater has already deleted. See NewExecRunner.
	if r.Bin == "" {
		return fmt.Errorf("%s %s: no binary to run", strings.Join(args, " "), p.Name)
	}

	cfg := filepath.Join(p.ConfigDir, "config.yaml")
	full := append([]string{"--config", cfg}, args...)
	cmd := exec.CommandContext(ctx, r.Bin, full...)
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
