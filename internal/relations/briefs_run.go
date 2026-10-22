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
	paths := make([]string, 0, len(rels))
	for path := range rels {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var due []string
	for _, path := range paths {
		rel, subj := rels[path], subjects[path]
		if subj == nil || len(rel.Accepted()) == 0 || InputsHash(rel, subj, w.client.Model()) == rel.BriefInputsHash {
			continue
		}
		if limit > 0 && len(due) >= limit {
			run.Pending++
			continue
		}
		due = append(due, path)
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
