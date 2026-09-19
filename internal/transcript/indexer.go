package transcript

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
}

// IndexOptions configures a batch run.
type IndexOptions struct {
	// SessionsDir holds one directory per recording.
	SessionsDir string
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
	// Empty counts recordings that captured no speech.
	Empty    int
	Duration time.Duration
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
func DiscoverSessions(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read sessions directory: %w", err)
	}

	type candidate struct {
		path    string
		started time.Time
	}
	var found []candidate
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name(), SessionFileName)
		started, err := sessionStartTime(path)
		if err != nil {
			continue
		}
		found = append(found, candidate{path: path, started: started})
	}
	sort.Slice(found, func(i, j int) bool {
		if !found[i].started.Equal(found[j].started) {
			return found[i].started.Before(found[j].started)
		}
		return found[i].path < found[j].path
	})

	paths := make([]string, len(found))
	for i, c := range found {
		paths[i] = c.path
	}
	return paths, nil
}

// startTimeProbe reads only the timestamp, so ordering the corpus does not
// require decoding every segment of every transcript.
type startTimeProbe struct {
	CreatedAt time.Time `json:"created_at"`
}

// sessionStartTime returns when a recording began, falling back to the file's
// modification time if the transcript does not say.
func sessionStartTime(path string) (time.Time, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, err
	}
	var probe startTimeProbe
	if err := json.Unmarshal(data, &probe); err == nil && !probe.CreatedAt.IsZero() {
		return probe.CreatedAt, nil
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

// Run enriches and indexes every recording in the configured directory.
func (ix *Indexer) Run(ctx context.Context) (*RunReport, error) {
	start := time.Now()
	report := &RunReport{}

	paths, err := DiscoverSessions(ix.opts.SessionsDir)
	if err != nil {
		return nil, err
	}
	if ix.opts.Limit > 0 && len(paths) > ix.opts.Limit {
		paths = paths[:ix.opts.Limit]
	}
	ix.opts.Log("Found %d recordings in %s", len(paths), ix.opts.SessionsDir)

	// Decide what actually needs work before spending anything on it.
	var todo []string
	for _, p := range paths {
		needed, reason, err := ix.needsIndexing(ctx, p)
		if err != nil {
			report.Failures = append(report.Failures, Failure{Path: p, Err: err})
			continue
		}
		switch reason {
		case skipEmpty:
			report.Empty++
		case skipUnchanged:
			report.Skipped++
		}
		if needed {
			todo = append(todo, p)
		}
	}
	ix.opts.Log("%d to enrich, %d already indexed, %d empty", len(todo), report.Skipped, report.Empty)
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
)

// needsIndexing reports whether a recording still has to be processed.
//
// Transcripts are written once and then left alone, so a content hash decides
// this: an unchanged recording is skipped without loading a model, which is
// what makes re-running a 600-recording batch nearly free.
func (ix *Indexer) needsIndexing(ctx context.Context, path string) (bool, skipReason, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, skipNone, fmt.Errorf("read %s: %w", path, err)
	}
	if !LooksLikeSession(data) {
		return false, skipNone, fmt.Errorf("%s is not a meeting transcript", path)
	}
	s, err := Parse(data, path)
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

// EstimatedCost returns the dollar cost of a run at the given per-million
// token rates.
func (r *RunReport) EstimatedCost(inputRate, outputRate float64) float64 {
	return float64(r.Usage.InputTokens)/1e6*inputRate +
		float64(r.Usage.OutputTokens)/1e6*outputRate
}
