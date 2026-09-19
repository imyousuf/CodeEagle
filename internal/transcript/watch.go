package transcript

import (
	"context"
	"errors"
	"time"
)

// DefaultWatchInterval is how often the sessions directory is swept.
//
// Sweeping is deliberately chosen over filesystem notifications. Transcripts
// are written by a separate recorder, so a notification arrives while a
// recording is still being written; handling that means debouncing events,
// tracking newly created directories, and guessing when a file is finished.
// A sweep sidesteps all of it: an unchanged recording is skipped on its content
// hash, which costs a file read and no model call, so polling is nearly free
// and cannot race a partial write. A minute of latency does not matter for
// meeting notes.
const DefaultWatchInterval = time.Minute

// DefaultSettleTime is how long a transcript must sit unmodified before it is
// considered finished. A recording still being written would otherwise be
// enriched half-complete, and then enriched again when it finishes.
const DefaultSettleTime = 30 * time.Second

// WatchOptions configures continuous indexing.
type WatchOptions struct {
	// Interval is the sweep period. Zero means DefaultWatchInterval.
	Interval time.Duration
	// Settle is how long a file must be unmodified. Zero means
	// DefaultSettleTime.
	Settle time.Duration
	// OnSweep is called after each sweep that indexed something.
	OnSweep func(report *RunReport)
}

// Watch indexes new recordings as they appear, until the context is cancelled.
//
// The first sweep happens immediately, so starting the watcher catches up on
// anything recorded while it was not running.
func (ix *Indexer) Watch(ctx context.Context, opts WatchOptions) error {
	interval := opts.Interval
	if interval <= 0 {
		interval = DefaultWatchInterval
	}
	settle := opts.Settle
	if settle <= 0 {
		settle = DefaultSettleTime
	}
	ix.minAge = settle

	ix.opts.Log("Watching %s every %s (a recording is indexed once it has been idle for %s)",
		ix.opts.SessionsDir, interval, settle)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		report, err := ix.Run(ctx)
		switch {
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return nil
		case err != nil:
			// A failed sweep must not end the watch: the cause is usually
			// transient, and the next sweep retries whatever was missed.
			ix.opts.Log("Sweep failed, will retry: %v", err)
		case report.Stats.Meetings > 0:
			ix.opts.Log("Indexed %d new recording(s): %d participants (%d identified), %d topics, %d decisions, %d follow-ups",
				report.Stats.Meetings, report.Stats.Speakers, report.Stats.Identified,
				report.Stats.Topics, report.Stats.Decisions, report.Stats.ActionItems)
			if opts.OnSweep != nil {
				opts.OnSweep(report)
			}
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// tooRecent reports whether a transcript was modified so recently that the
// recorder may still be writing it.
func (ix *Indexer) tooRecent(modTime time.Time) bool {
	if ix.minAge <= 0 {
		return false
	}
	return time.Since(modTime) < ix.minAge
}
