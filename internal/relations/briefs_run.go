package relations

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// DefaultBriefWorkers is how many briefs are written at once. A brief is one
// model call of several seconds, and a bulk run writes hundreds.
const DefaultBriefWorkers = 4

// BriefRun is what one pass over the briefs did and cost.
type BriefRun struct {
	Written       int
	Pending       int
	Dropped       int
	TokensIn      int
	TokensOut     int
	JudgeRequests int
	// Unverified lists, per document, the quotes that failed their check.
	Unverified map[string][]string
	// Failed counts briefs that could not be written; the next run retries
	// them.
	Failed int
	// WrittenPaths are the documents whose briefs were rewritten, so their
	// relations can be embedded again.
	WrittenPaths []string
}

// WriteBriefs rewrites the briefs of relations whose inputs changed, at most
// limit of them, workers at a time, and stores each as it is written. The
// rest are counted as pending for a later run.
func WriteBriefs(ctx context.Context, store graph.Store, w *BriefWriter, rels map[string]*Relation,
	subjects map[string]*Subject, meetings map[string]*Meeting, limit, workers int) (BriefRun, error) {
	run := BriefRun{Unverified: make(map[string][]string)}
	if workers <= 0 {
		workers = DefaultBriefWorkers
	}
	// Briefs whose meetings changed come first, then the most recently
	// changed documents: within a run's bound, what was just discussed or
	// just edited is what a reader is likeliest to ask about.
	var due []string
	for path, rel := range rels {
		if subj := subjects[path]; subj != nil && InputsHash(rel, subj, w.client.Model()) != rel.BriefInputsHash {
			due = append(due, path)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		a, b := rels[due[i]], rels[due[j]]
		am, bm := len(a.Accepted()) > 0, len(b.Accepted()) > 0
		if am != bm {
			return am
		}
		if ta, tb := subjects[due[i]].UpdatedAt, subjects[due[j]].UpdatedAt; !ta.Equal(tb) {
			return ta.After(tb)
		}
		return due[i] < due[j]
	})
	if limit > 0 && len(due) > limit {
		run.Pending = len(due) - limit
		due = due[:limit]
	}

	relater := NewRelater(store, nil, RelaterOptions{})
	var (
		mu       sync.Mutex
		firstErr error
		wg       sync.WaitGroup
		jobs     = make(chan string)
	)
	for range min(workers, len(due)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				rel, subj := rels[path], subjects[path]
				rep, err := w.Update(ctx, rel, subj, meetings)
				var werr error
				if err == nil && rep.Written {
					werr = relater.Write(ctx, rel, subj)
				}
				mu.Lock()
				run.TokensIn += rep.InputTokens
				run.TokensOut += rep.OutputTokens
				run.JudgeRequests += rep.JudgeRequests
				switch {
				case err != nil:
					run.Failed++
				case werr != nil:
					if firstErr == nil {
						firstErr = fmt.Errorf("store brief for %s: %w", path, werr)
					}
				case rep.Written:
					run.Written++
					run.WrittenPaths = append(run.WrittenPaths, path)
					run.Dropped += rep.Dropped
					if len(rep.Unverified) > 0 {
						run.Unverified[path] = rep.Unverified
					}
				}
				mu.Unlock()
			}
		}()
	}
	for _, path := range due {
		if ctx.Err() != nil {
			break
		}
		jobs <- path
	}
	close(jobs)
	wg.Wait()
	return run, firstErr
}
