package watcher

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Tiering holds an OS watch only on the directories in use, and finds changes
// in the rest by scanning them.
//
// Watching every directory under a tree holds one watch per directory for as
// long as the watcher runs, however long ago anything there changed. On one
// machine that was about 1,300 watches, most of them on photo albums untouched
// for years and on one folder per past meeting recording; on macOS each watch
// is also an open file. A directory whose contents changed within HotWindow is
// watched; the rest are scanned every ScanInterval, which costs a stat per
// entry and nothing in between. A scan that finds a change promotes the
// directory to a watch, and a watched directory quiet for HotWindow is
// released. The roots are always watched, so a new directory directly under
// one -- a new recording, say -- is seen at once.
//
// A change in a scanned directory is seen up to ScanInterval late, and only as
// changed files and the directory itself: a scan cannot tell which file was
// removed. That suits a consumer that re-syncs a project on any change, such
// as the worker, and not one that indexes the exact files an event names.
type Tiering struct {
	// HotWindow is how recently a directory must have changed to be watched.
	HotWindow time.Duration
	// ScanInterval is how often the scanned directories are checked.
	ScanInterval time.Duration
	// Budget, if set, caps the watches held across every watcher sharing it.
	Budget *Budget
	// Now supplies the current time; nil means time.Now.
	Now func() time.Time
}

// Defaults for tiering.
const (
	DefaultHotWindow    = 14 * 24 * time.Hour
	DefaultScanInterval = 10 * time.Minute
)

// Stats is what a watcher holds and what has failed.
type Stats struct {
	// Watched is the number of directories with an OS watch.
	Watched int
	// Scanned is the number found by scanning instead.
	Scanned int
	// Promoted and Demoted count moves between the two since the start.
	Promoted, Demoted int
	// AddFailures counts directories the system refused to watch, and
	// FirstAddError says which and why.
	AddFailures   int
	FirstAddError string
}

// Budget caps the watches held by the watchers that share it.
type Budget struct {
	mu        sync.Mutex
	max, used int
	exhausted bool
}

// NewBudget returns a budget of max watches; zero or less means no cap.
func NewBudget(max int) *Budget { return &Budget{max: max} }

// Max is the cap, zero for none.
func (b *Budget) Max() int { return b.max }

// Used is how many watches are held against it.
func (b *Budget) Used() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

// acquire takes one watch from the budget, reporting false when none is
// left. firstMiss is true the first time it runs out, so the shortage is
// reported once.
func (b *Budget) acquire() (ok, firstMiss bool) {
	if b == nil {
		return true, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.max > 0 && b.used >= b.max {
		first := !b.exhausted
		b.exhausted = true
		return false, first
	}
	b.used++
	return true, false
}

func (b *Budget) release() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used > 0 {
		b.used--
	}
}

// DefaultMaxWatches is a quarter of the system's per-user inotify limit,
// leaving the rest to editors, build tools and anything else that watches. Off
// Linux, where each watch is an open file, it is 512.
func DefaultMaxWatches() int {
	if data, err := os.ReadFile("/proc/sys/fs/inotify/max_user_watches"); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && n > 0 {
			return n / 4
		}
	}
	return 512
}

// signature is what a scan compares: the directory's own modification time,
// which changes when an entry is added, removed or renamed; the newest
// modification time of a file in it, which changes when one is written in
// place; and how many entries it has.
type signature struct {
	mod, newest time.Time
	entries     int
}

func (s signature) equal(o signature) bool {
	return s.mod.Equal(o.mod) && s.newest.Equal(o.newest) && s.entries == o.entries
}

func (s signature) latest() time.Time {
	if s.newest.After(s.mod) {
		return s.newest
	}
	return s.mod
}

// dirState is one directory's place in the tiering.
type dirState struct {
	root bool
	// active is when it last changed, for a watched directory.
	active time.Time
	// sig is what it looked like at the last scan, for a scanned one.
	sig signature
}

func readSignature(dir string) (signature, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return signature{}, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return signature{}, err
	}
	sig := signature{mod: info.ModTime(), entries: len(entries)}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if fi, err := e.Info(); err == nil && fi.ModTime().After(sig.newest) {
			sig.newest = fi.ModTime()
		}
	}
	return sig, nil
}

func (w *Watcher) now() time.Time {
	if t := w.cfg.Tiering; t != nil && t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

func (w *Watcher) hotWindow() time.Duration {
	if d := w.cfg.Tiering.HotWindow; d > 0 {
		return d
	}
	return DefaultHotWindow
}

func (w *Watcher) scanInterval() time.Duration {
	if d := w.cfg.Tiering.ScanInterval; d > 0 {
		return d
	}
	return DefaultScanInterval
}

// candidate is a directory found by a walk, before it is placed.
type candidate struct {
	path string
	root bool
	sig  signature
}

// walkCandidates lists every directory under root that is not excluded.
func (w *Watcher) walkCandidates(root string, isRoot func(string) bool) []candidate {
	var out []candidate
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if w.matcher.Match(path) {
			return filepath.SkipDir
		}
		sig, err := readSignature(path)
		if err != nil {
			return nil
		}
		out = append(out, candidate{path: path, root: isRoot(path), sig: sig})
		return nil
	})
	return out
}

// startTiered places every directory: roots and those changed within the hot
// window are watched, most recently changed first while the budget lasts, and
// the rest are scanned.
func (w *Watcher) startTiered() {
	roots := make(map[string]bool, len(w.cfg.Paths))
	for _, p := range w.cfg.Paths {
		roots[filepath.Clean(p)] = true
	}
	var all []candidate
	for _, root := range w.cfg.Paths {
		all = append(all, w.walkCandidates(root, func(p string) bool { return roots[p] })...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].root != all[j].root {
			return all[i].root
		}
		return all[i].sig.latest().After(all[j].sig.latest())
	})

	w.tmu.Lock()
	w.hot = make(map[string]*dirState)
	w.cold = make(map[string]*dirState)
	w.tmu.Unlock()

	now, window := w.now(), w.hotWindow()
	for _, c := range all {
		if (c.root || now.Sub(c.sig.latest()) < window) && w.watch(c.path, false) {
			w.tmu.Lock()
			w.hot[c.path] = &dirState{root: c.root, active: c.sig.latest()}
			w.tmu.Unlock()
			continue
		}
		w.tmu.Lock()
		w.cold[c.path] = &dirState{root: c.root, sig: c.sig}
		w.tmu.Unlock()
	}
}

// watch adds an OS watch on dir within the budget. With evict, a watched
// directory that has been quiet longest gives up its watch to make room; it
// is used for a directory that just changed, which is by definition more in
// use than any other.
func (w *Watcher) watch(dir string, evict bool) bool {
	ok, firstMiss := w.cfg.Tiering.Budget.acquire()
	if !ok && evict && w.evictQuietest() {
		ok, firstMiss = w.cfg.Tiering.Budget.acquire()
	}
	if firstMiss {
		w.logf("Watch budget of %d reached; the least recently changed directories are scanned every %s instead",
			w.cfg.Tiering.Budget.Max(), w.scanInterval())
	}
	if !ok {
		return false
	}
	if err := w.addWatch(dir); err != nil {
		w.cfg.Tiering.Budget.release()
		w.addFailed(dir, err)
		return false
	}
	return true
}

// evictQuietest moves the watched directory that changed longest ago, other
// than a root, back to scanning.
func (w *Watcher) evictQuietest() bool {
	w.tmu.Lock()
	var victim string
	var oldest time.Time
	for dir, st := range w.hot {
		if st.root {
			continue
		}
		if victim == "" || st.active.Before(oldest) {
			victim, oldest = dir, st.active
		}
	}
	w.tmu.Unlock()
	if victim == "" {
		return false
	}
	w.demote(victim)
	return true
}

// demote releases dir's watch and scans it from now on.
func (w *Watcher) demote(dir string) {
	sig, err := readSignature(dir)
	w.tmu.Lock()
	st, ok := w.hot[dir]
	if !ok {
		w.tmu.Unlock()
		return
	}
	delete(w.hot, dir)
	if err == nil {
		w.cold[dir] = &dirState{root: st.root, sig: sig}
	}
	w.stats.Demoted++
	w.tmu.Unlock()
	_ = w.removeWatch(dir)
	w.cfg.Tiering.Budget.release()
}

// promote watches a scanned directory that has changed, if the budget allows.
func (w *Watcher) promote(dir string, sig signature) {
	if !w.watch(dir, true) {
		w.tmu.Lock()
		if st, ok := w.cold[dir]; ok {
			st.sig = sig
		}
		w.tmu.Unlock()
		return
	}
	w.tmu.Lock()
	root := false
	if st, ok := w.cold[dir]; ok {
		root = st.root
		delete(w.cold, dir)
	}
	w.hot[dir] = &dirState{root: root, active: w.now()}
	w.stats.Promoted++
	w.tmu.Unlock()
}

// addTree places a directory that has just appeared, and everything under it:
// all of it is new, so all of it is watched while the budget allows.
func (w *Watcher) addTree(dir string) {
	for _, c := range w.walkCandidates(dir, func(string) bool { return false }) {
		w.tmu.Lock()
		_, known := w.hot[c.path]
		_, scanned := w.cold[c.path]
		w.tmu.Unlock()
		if known || scanned {
			continue
		}
		if w.watch(c.path, true) {
			w.tmu.Lock()
			w.hot[c.path] = &dirState{active: w.now()}
			w.tmu.Unlock()
			continue
		}
		w.tmu.Lock()
		w.cold[c.path] = &dirState{sig: c.sig}
		w.tmu.Unlock()
	}
}

// touch records activity in the watched directory an event came from, and
// forgets a directory that was removed or renamed away.
func (w *Watcher) touch(path string, op EventOp) {
	now := w.now()
	released := false
	w.tmu.Lock()
	if op == Remove || op == Rename {
		if _, ok := w.hot[path]; ok {
			delete(w.hot, path)
			released = true
		}
		delete(w.cold, path)
	}
	if st, ok := w.hot[filepath.Dir(path)]; ok {
		st.active = now
	}
	if st, ok := w.hot[path]; ok {
		st.active = now
	}
	w.tmu.Unlock()
	if released {
		// The system has usually dropped a removed directory's watch
		// already; removing it again only fails.
		_ = w.removeWatch(path)
		w.cfg.Tiering.Budget.release()
	}
}

// scanLoop scans the cold directories and releases quiet watches every scan
// interval until the context ends.
func (w *Watcher) scanLoop(ctx context.Context, out chan<- Event) {
	ticker := time.NewTicker(w.scanInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.scanOnce(ctx, out)
		}
	}
}

// scanOnce checks every scanned directory against its last signature, emits
// events for what changed and promotes it, then releases the watches of
// directories quiet for the hot window.
func (w *Watcher) scanOnce(ctx context.Context, out chan<- Event) {
	w.tmu.Lock()
	type entry struct {
		dir string
		sig signature
	}
	cold := make([]entry, 0, len(w.cold))
	for dir, st := range w.cold {
		cold = append(cold, entry{dir, st.sig})
	}
	w.tmu.Unlock()

	emit := func(evt Event) bool {
		select {
		case out <- evt:
			return true
		case <-ctx.Done():
			return false
		}
	}
	for _, e := range cold {
		sig, err := readSignature(e.dir)
		if err != nil {
			w.tmu.Lock()
			delete(w.cold, e.dir)
			w.tmu.Unlock()
			if os.IsNotExist(err) && !emit(Event{Path: e.dir, Op: Remove, Time: w.now()}) {
				return
			}
			continue
		}
		if sig.equal(e.sig) {
			continue
		}
		if !w.emitChanges(e.dir, e.sig, emit) {
			return
		}
		w.promote(e.dir, sig)
	}

	now, window := w.now(), w.hotWindow()
	var quiet []string
	w.tmu.Lock()
	for dir, st := range w.hot {
		if !st.root && now.Sub(st.active) >= window {
			quiet = append(quiet, dir)
		}
	}
	w.tmu.Unlock()
	for _, dir := range quiet {
		w.demote(dir)
	}
}

// emitChanges reports what a scan found changed in dir since prev: each file
// written since, each new directory (which is then placed), and the directory
// itself, which stands for anything removed or renamed.
func (w *Watcher) emitChanges(dir string, prev signature, emit func(Event) bool) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true
	}
	since := prev.latest()
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if w.matcher.Match(path) {
			continue
		}
		fi, err := e.Info()
		if err != nil || !fi.ModTime().After(since) {
			continue
		}
		op := Write
		if e.IsDir() {
			op = Create
			w.addTree(path)
		}
		if !emit(Event{Path: path, Op: op, Time: w.now()}) {
			return false
		}
	}
	return emit(Event{Path: dir, Op: Write, Time: w.now()})
}

// Survey reports how a tiered watcher over cfg would start: how many
// directories it would watch and how many it would scan, before any budget.
// It holds no watch.
func Survey(cfg WatcherConfig, now time.Time, hotWindow time.Duration) (watched, scanned int, err error) {
	w, err := NewWatcher(cfg)
	if err != nil {
		return 0, 0, err
	}
	if hotWindow <= 0 {
		hotWindow = DefaultHotWindow
	}
	roots := make(map[string]bool, len(cfg.Paths))
	for _, p := range cfg.Paths {
		roots[filepath.Clean(p)] = true
	}
	for _, root := range cfg.Paths {
		for _, c := range w.walkCandidates(root, func(p string) bool { return roots[p] }) {
			if c.root || now.Sub(c.sig.latest()) < hotWindow {
				watched++
			} else {
				scanned++
			}
		}
	}
	return watched, scanned, nil
}
