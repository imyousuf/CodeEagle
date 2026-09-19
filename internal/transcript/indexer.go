package transcript

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// Indexer enriches a directory of recordings and projects them into the graph.
//
// Enrichment is network-bound and graph writing is not, so the two are
// separated: several sessions are analysed at once while a single goroutine
// applies the results. That keeps the person registry free of locks and means
// people resolve in a deterministic order regardless of which model call
// happens to return first.
type Indexer struct {
	analyzer *Analyzer
	writer   *Writer
	store    graph.Store
	opts     IndexOptions
	// minAge is how long a transcript must sit unmodified before it is treated
	// as finished. Set by Watch; zero in a one-shot run, where the caller has
	// decided the corpus is static.
	minAge time.Duration
}

// IndexOptions configures a batch run.
type IndexOptions struct {
	// SessionsDirs are the directories to search. Recordings accumulate in
	// more than one place — a recorder's own folder, a downloads folder, a
	// shared drive — and a single path would silently miss the rest.
	SessionsDirs []string
	// ExtraPaths are individual transcripts to index in addition, named rather
	// than discovered. Document indexing marks the transcripts it comes
	// across, and they arrive here, so a transcript committed beside the code
	// it concerns becomes a meeting without anyone configuring its directory.
	ExtraPaths []string
	// Concurrency is how many recordings are analysed at once.
	Concurrency int
	// Force re-enriches recordings that are already indexed and unchanged.
	Force bool
	// Limit caps how many recordings are processed, for trial runs.
	Limit int
	// Log receives progress lines.
	Log func(format string, args ...any)
}

// Failure records a recording that could not be processed. One bad recording
// must not end a run over hundreds of them.
type Failure struct {
	Path string
	ID   string
	Err  error
}

// RunReport summarizes a batch run.
type RunReport struct {
	Stats    Stats
	Usage    Usage
	Failures []Failure
	// Skipped counts recordings already indexed and unchanged.
	Skipped int
	// Pending counts recordings still being written.
	Pending int
	// Empty counts recordings that captured no speech.
	Empty int
	// NotTranscripts counts files that were looked at and turned out to be
	// something else. Pointing a scan at a folder of mixed downloads is a
	// normal thing to do, so these are counted rather than reported.
	NotTranscripts int
	Duration       time.Duration
}

const defaultConcurrency = 4

// NewIndexer creates an Indexer.
func NewIndexer(store graph.Store, analyzer *Analyzer, writer *Writer, opts IndexOptions) *Indexer {
	if opts.Concurrency <= 0 {
		opts.Concurrency = defaultConcurrency
	}
	if opts.Log == nil {
		opts.Log = func(string, ...any) {}
	}
	return &Indexer{analyzer: analyzer, writer: writer, store: store, opts: opts}
}

// DiscoverSessions returns the transcript files under a directory in the order
// the meetings happened.
//
// Chronological order is deliberate. A batch feeds the people it has
// identified back into later meetings, so processing in real time order means
// a colleague recognized in January is a known name by March. Sorting on file
// modification time would not do: copying a corpus rewrites every mtime at
// once, and the meeting's own timestamp is the only reliable ordering.
// DiscoverSessions finds the transcripts under one directory.
func DiscoverSessions(dir string) ([]string, error) {
	return DiscoverSessionsIn([]string{dir})
}

// DiscoverSessionsIn finds the transcripts under several directories, in the
// order the meetings happened.
//
// A file reachable from two of the directories — nested paths, or a symlink —
// is returned once, so overlapping configuration costs nothing.
func DiscoverSessionsIn(dirs []string) ([]string, error) {
	return DiscoverSessionsWith(dirs, nil)
}

// DiscoverSessionsWith finds the transcripts under several directories and
// merges in individually named ones, in the order the meetings happened.
//
// The named transcripts are the ones document indexing marked, and they are
// sorted in among the discovered files rather than appended after them.
// Chronological order is not cosmetic here: a batch feeds the people it has
// identified into later meetings, so a recording processed out of order loses
// the names the earlier ones would have taught it.
func DiscoverSessionsWith(dirs []string, extra []string) ([]string, error) {
	var found []export
	seen := make(map[string]bool)

	for _, dir := range dirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		batch, err := discoverOne(dir, seen)
		if err != nil {
			return nil, err
		}
		found = append(found, batch...)
	}
	found = append(found, namedExports(extra, seen)...)

	sort.Slice(found, func(i, j int) bool {
		if !found[i].started.Equal(found[j].started) {
			return found[i].started.Before(found[j].started)
		}
		return found[i].path < found[j].path
	})
	return preferOneExportPerMeeting(found), nil
}

// namedExports turns individually named transcripts into exports, skipping any
// that discovery already found so a file both configured and marked is indexed
// once.
func namedExports(paths []string, seen map[string]bool) []export {
	var out []export
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		key := p
		if abs, err := filepath.Abs(p); err == nil {
			key = abs
		}
		if seen[key] {
			continue
		}
		started, err := sessionStartTime(p)
		if err != nil {
			// Unreadable or not a transcript after all. Planning reports this
			// per file; dropping it here would hide it.
			continue
		}
		seen[key] = true
		out = append(out, export{path: p, started: started})
	}
	return out
}

// discoverOne walks a single directory, skipping anything already found.
func discoverOne(dir string, seen map[string]bool) ([]export, error) {
	var found []export

	// Recordings arrive either as a directory per session, as the local
	// recorder writes them, or as loose exports downloaded from a conferencing
	// tool. Walking handles both, and the name filter keeps the cost to a
	// directory listing for everything that is not a transcript.
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A missing or unreadable root is a real configuration problem and
			// must be reported; a single unreadable entry beneath it is not,
			// and is skipped so one bad directory cannot stop the scan.
			if path == dir {
				return err
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if d.Name() != filepath.Base(dir) && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !MayBeTranscript(path) {
			return nil
		}
		// Resolve before recording it, so the same file reached through two
		// configured directories is not indexed twice.
		key := path
		if abs, err := filepath.Abs(path); err == nil {
			key = abs
		}
		if seen[key] {
			return nil
		}
		started, err := sessionStartTime(path)
		if err != nil {
			return nil
		}
		seen[key] = true
		found = append(found, export{path: path, started: started})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read sessions directory %s: %w", dir, err)
	}
	return found, nil
}

// searchedDescription names where the recordings came from, for the log.
//
// With no directory configured they came from the document index alone, and
// naming an empty list of directories would read as though nothing was
// searched.
func (ix *Indexer) searchedDescription() string {
	dirs := strings.Join(ix.opts.SessionsDirs, ", ")
	switch {
	case dirs == "" && len(ix.opts.ExtraPaths) > 0:
		return "indexed documents"
	case dirs == "":
		return "no configured directory"
	case len(ix.opts.ExtraPaths) > 0:
		return dirs + " and indexed documents"
	default:
		return dirs
	}
}

// export is one transcript file and when its meeting began.
type export struct {
	path    string
	started time.Time
}

// formatPreference ranks formats for the same meeting, best first.
//
// A meeting exported twice — say a caption file and a Word document of one
// call — should be indexed once. The local recorder's own file wins because it
// alone carries the microphone channel that identifies the owner; between the
// conferencing exports, captions win because they timestamp every cue while a
// document records only when each utterance began.
var formatPreference = map[string]int{
	"tomoe":      0,
	"webvtt":     1,
	"srt":        2,
	"teams-docx": 3,
}

// sameMeetingWindow is how far apart two exports of one meeting may be dated
// and still be recognized as the same.
//
// It is not zero because the two files rarely agree. A Word export carries the
// meeting's own timestamp, while a caption file often carries none at all and
// falls back to when it was downloaded — a day or two later. It is not large
// either: a standing meeting recurs weekly, and collapsing two instances of it
// would silently lose one.
const sameMeetingWindow = 48 * time.Hour

// preferOneExportPerMeeting drops duplicate exports of the same meeting,
// keeping the format that carries the most.
//
// Two files are the same meeting when their names agree, once the extension and
// the tool's suffixes are removed, *and* their dates are close. The name alone
// is not enough: a weekly standup exports to the same filename every week, so
// matching on it would keep one instance and discard the rest.
func preferOneExportPerMeeting(exports []export) []string {
	type pick struct {
		path    string
		started time.Time
		rank    int
	}
	// Grouped by name; each group holds the instances kept so far, since a
	// recurring meeting legitimately has several.
	groups := make(map[string][]pick, len(exports))
	var order []string
	seenKey := make(map[string]bool, len(exports))

	rankOf := func(path string) int {
		rank, known := formatPreference[formatNameForPath(path)]
		if !known {
			return len(formatPreference)
		}
		return rank
	}

	for _, e := range exports {
		key := meetingKeyFromPath(e.path)
		if !seenKey[key] {
			seenKey[key] = true
			order = append(order, key)
		}

		instances := groups[key]
		matched := false
		for i, inst := range instances {
			if absDuration(inst.started.Sub(e.started)) > sameMeetingWindow {
				continue
			}
			// Same meeting: keep whichever format carries more.
			if rankOf(e.path) < inst.rank {
				instances[i] = pick{path: e.path, started: inst.started, rank: rankOf(e.path)}
			}
			matched = true
			break
		}
		if !matched {
			instances = append(instances, pick{path: e.path, started: e.started, rank: rankOf(e.path)})
		}
		groups[key] = instances
	}

	out := make([]string, 0, len(exports))
	for _, key := range order {
		for _, inst := range groups[key] {
			out = append(out, inst.path)
		}
	}
	return out
}

// absDuration returns the magnitude of a duration.
func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// meetingKeyFromPath reduces a filename to what identifies the meeting,
// discarding the extension and the suffixes exporters append.
func meetingKeyFromPath(path string) string {
	base := slugify(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	for _, suffix := range []string{"-transcript", "-recording", "-meeting-recording", "-captions"} {
		base = strings.TrimSuffix(base, suffix)
	}
	// A session directory names the meeting when the file inside does not.
	if base == "session" {
		return slugify(filepath.Base(filepath.Dir(path)))
	}
	return base
}

// formatNameForPath reports which format claims a path, by name alone.
func formatNameForPath(path string) string {
	for _, f := range Formats() {
		if f.Matches(path) {
			return f.Name()
		}
	}
	return ""
}

// startTimeProbe reads only the timestamp, so ordering the corpus does not
// require decoding every segment of every transcript.
type startTimeProbe struct {
	CreatedAt time.Time `json:"created_at"`
}

// sessionStartTime returns when a recording began.
//
// The transcript's own timestamp is preferred, then one embedded in the
// filename by the tool that exported it, and only then the file's modification
// time — which reflects when the file was written or downloaded rather than
// when the meeting happened.
func sessionStartTime(path string) (time.Time, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, err
	}

	var probe startTimeProbe
	if err := json.Unmarshal(data, &probe); err == nil && !probe.CreatedAt.IsZero() {
		return probe.CreatedAt, nil
	}
	if t, ok := timeFromName(path); ok {
		return t, nil
	}
	// A format with its own header may still carry the time; parsing is the
	// only way to find it, and the result is discarded apart from the time.
	if s, err := ParseAny(path, data); err == nil && !s.StartedAt().IsZero() {
		return s.StartedAt(), nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

// job carries one recording through the pipeline.
type job struct {
	path string
	res  *Result
	err  error
}

// Plan is what a run would do: the recordings that need enriching, and the
// reasons the rest were left out.
type Plan struct {
	// Todo are the recordings that would be enriched, in the order they would
	// be processed.
	Todo []string
	// Skipped were already indexed and unchanged.
	Skipped int
	// Empty parsed but contain no speech.
	Empty int
	// Pending were still being written and are left for the next sweep.
	Pending int
	// NotTranscripts are ordinary files that discovery's filename filter let
	// through. Being something else is not a failure.
	NotTranscripts int
	// Failures are files that claimed to be transcripts and could not be read.
	Failures []Failure
}

// Plan decides what a run would enrich, without spending anything.
//
// Run uses this too, so `--dry-run` reports what will actually happen rather
// than an estimate that can drift away from it.
func (ix *Indexer) Plan(ctx context.Context) (*Plan, error) {
	paths, err := DiscoverSessionsWith(ix.opts.SessionsDirs, ix.opts.ExtraPaths)
	if err != nil {
		return nil, err
	}
	ix.opts.Log("Found %d recordings in %s", len(paths), ix.searchedDescription())

	plan := &Plan{}
	for _, p := range paths {
		needed, reason, err := ix.needsIndexing(ctx, p)
		if err != nil {
			plan.Failures = append(plan.Failures, Failure{Path: p, Err: err})
			continue
		}
		switch reason {
		case skipEmpty:
			plan.Empty++
		case skipUnchanged:
			plan.Skipped++
		case skipPending:
			plan.Pending++
		case skipNotTranscript:
			plan.NotTranscripts++
		}
		if needed {
			plan.Todo = append(plan.Todo, p)
		}
	}

	// Limit what gets enriched, not what was discovered. Truncating the
	// discovered list pinned every run to the same oldest N recordings, so
	// working through a backlog with `--limit` never advanced past the first
	// batch — and with `--force` it paid for those same recordings again on
	// every invocation.
	if ix.opts.Limit > 0 && len(plan.Todo) > ix.opts.Limit {
		plan.Todo = plan.Todo[:ix.opts.Limit]
	}
	return plan, nil
}

// Run enriches and indexes every recording in the configured directory.
func (ix *Indexer) Run(ctx context.Context) (*RunReport, error) {
	start := time.Now()

	plan, err := ix.Plan(ctx)
	if err != nil {
		return nil, err
	}
	report := &RunReport{
		Skipped:        plan.Skipped,
		Empty:          plan.Empty,
		Pending:        plan.Pending,
		NotTranscripts: plan.NotTranscripts,
		Failures:       plan.Failures,
	}
	todo := plan.Todo

	ix.opts.Log("%d to enrich, %d already indexed, %d empty%s%s", len(todo), report.Skipped, report.Empty,
		pendingSuffix(report.Pending), notTranscriptSuffix(report.NotTranscripts))
	if len(todo) == 0 {
		report.Duration = time.Since(start)
		return report, nil
	}

	jobs := make(chan string)
	results := make(chan job)

	var wg sync.WaitGroup
	for i := 0; i < ix.opts.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				res, err := ix.enrich(ctx, path)
				select {
				case results <- job{path: path, res: res, err: err}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, p := range todo {
			select {
			case jobs <- p:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	// A single consumer applies results, so the person registry needs no
	// locking and identity resolution stays deterministic.
	done := 0
	for r := range results {
		done++
		if r.err != nil {
			report.Failures = append(report.Failures, Failure{Path: r.path, Err: r.err})
			ix.opts.Log("[%d/%d] %s: %v", done, len(todo), filepath.Base(filepath.Dir(r.path)), r.err)
			continue
		}

		report.Usage.InputTokens += r.res.Usage.InputTokens
		report.Usage.OutputTokens += r.res.Usage.OutputTokens
		report.Usage.Requests += r.res.Usage.Requests

		st, err := ix.writer.Write(ctx, r.res)
		if err != nil {
			report.Failures = append(report.Failures, Failure{Path: r.path, ID: r.res.Session.ID, Err: err})
			continue
		}
		if err := ix.markIndexed(ctx, r.res); err != nil {
			ix.opts.Log("warning: could not record index state for %s: %v", r.res.Session.ID, err)
		}
		report.Stats.Add(st)

		title := r.res.Session.Title
		if r.res.Analysis != nil && r.res.Analysis.Title != "" {
			title = r.res.Analysis.Title
		}
		ix.opts.Log("[%d/%d] %s — %d speakers (%d named), %d topics, %d decisions, %d actions",
			done, len(todo), truncate(title, 60),
			st.Speakers, st.Identified, st.Topics, st.Decisions, st.ActionItems)
	}

	report.Stats.People = ix.writer.people.Created()
	report.Duration = time.Since(start)
	return report, ctx.Err()
}

// enrich loads and analyses one recording.
func (ix *Indexer) enrich(ctx context.Context, path string) (*Result, error) {
	s, err := Load(path)
	if err != nil {
		return nil, err
	}
	res, err := ix.analyzer.Enrich(ctx, s)
	if err != nil {
		return nil, err
	}
	res.Session.Path = path
	return res, nil
}

// skipReason explains why a recording needs no work.
type skipReason int

const (
	skipNone skipReason = iota
	skipUnchanged
	skipEmpty
	// skipPending means the recorder may still be writing the transcript.
	skipPending
	// skipNotTranscript means the file is something else entirely.
	skipNotTranscript
)

// needsIndexing reports whether a recording still has to be processed.
//
// Transcripts are written once and then left alone, so a content hash decides
// this: an unchanged recording is skipped without loading a model, which is
// what makes re-running a 600-recording batch nearly free.
func (ix *Indexer) needsIndexing(ctx context.Context, path string) (bool, skipReason, error) {
	// A recording still being written would be enriched half-complete, and then
	// again once it finishes. Leave it for the next sweep.
	if info, err := os.Stat(path); err == nil && ix.tooRecent(info.ModTime()) {
		return false, skipPending, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return false, skipNone, fmt.Errorf("read %s: %w", path, err)
	}
	// A directory of downloads holds all sorts of JSON and Word files. Being
	// something other than a transcript is not an error — only a file that
	// claims to be one and then cannot be read is.
	if FormatFor(path, data) == nil {
		return false, skipNotTranscript, nil
	}
	s, err := ParseAny(path, data)
	if err != nil {
		return false, skipNone, err
	}
	if s.IsEmpty() {
		return false, skipEmpty, nil
	}
	if ix.opts.Force {
		return true, skipNone, nil
	}

	existing, err := ix.store.GetNode(ctx, graph.NewNodeID(string(graph.NodeMeeting), path, s.ID))
	if err != nil || existing == nil {
		// A missing node is the normal "not indexed yet" case.
		return true, skipNone, nil
	}
	if existing.Properties[graph.PropContentHash] == contentHash(data) {
		return false, skipUnchanged, nil
	}
	return true, skipNone, nil
}

// markIndexed stamps the meeting with the hash of the transcript it came from.
func (ix *Indexer) markIndexed(ctx context.Context, res *Result) error {
	path := res.Session.Path
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	id := graph.NewNodeID(string(graph.NodeMeeting), path, res.Session.ID)
	node, err := ix.store.GetNode(ctx, id)
	if err != nil {
		return err
	}
	if node == nil {
		return errors.New("meeting node missing after write")
	}
	if node.Properties == nil {
		node.Properties = make(map[string]string)
	}
	node.Properties[graph.PropContentHash] = contentHash(data)
	node.Properties[graph.PropMimeType] = "application/json"
	return ix.store.UpdateNode(ctx, node)
}

// contentHash returns the hash recorded on an indexed meeting.
func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// notTranscriptSuffix mentions files that turned out to be something else.
func notTranscriptSuffix(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(", %d not transcripts", n)
}

// pendingSuffix mentions recordings held back for the next sweep, and says
// nothing when there are none.
func pendingSuffix(pending int) string {
	if pending == 0 {
		return ""
	}
	return fmt.Sprintf(", %d still being written", pending)
}

// EstimatedCost returns the dollar cost of a run at the given per-million
// token rates.
func (r *RunReport) EstimatedCost(inputRate, outputRate float64) float64 {
	return float64(r.Usage.InputTokens)/1e6*inputRate +
		float64(r.Usage.OutputTokens)/1e6*outputRate
}
