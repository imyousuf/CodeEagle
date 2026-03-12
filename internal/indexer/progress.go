package indexer

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// syncProgress tracks and reports progress during long-running file operations.
type syncProgress struct {
	total     int
	processed int
	skipped   int
	indexed   int
	startTime time.Time
	lastLog   time.Time
	interval  time.Duration
	logFn     func(format string, args ...any)
	label     string
}

func newSyncProgress(total int, label string, logFn func(format string, args ...any)) *syncProgress {
	now := time.Now()
	return &syncProgress{
		total:     total,
		startTime: now,
		lastLog:   now,
		interval:  30 * time.Second,
		logFn:     logFn,
		label:     label,
	}
}

// tick records one file processed. didIndex indicates whether the file was
// actually indexed (true) or skipped as unchanged (false).
func (p *syncProgress) tick(didIndex bool) {
	p.processed++
	if didIndex {
		p.indexed++
	} else {
		p.skipped++
	}
	p.maybeReport()
}

// tickIndexed records one file processed and indexed (no skip tracking).
func (p *syncProgress) tickIndexed() {
	p.processed++
	p.indexed++
	p.maybeReport()
}

func (p *syncProgress) maybeReport() {
	now := time.Now()
	if p.total > 0 && (now.Sub(p.lastLog) >= p.interval || p.processed == p.total) {
		p.report(now)
	}
}

func (p *syncProgress) report(now time.Time) {
	elapsed := now.Sub(p.startTime).Round(time.Second)
	pct := float64(p.processed) / float64(p.total) * 100

	msg := fmt.Sprintf("[%s] %d/%d (%.1f%%)", p.label, p.processed, p.total, pct)

	if p.skipped > 0 {
		msg += fmt.Sprintf(" | %d indexed, %d skipped", p.indexed, p.skipped)
	}

	msg += fmt.Sprintf(" | %s elapsed", elapsed)

	if p.processed < p.total && p.processed > 0 {
		remaining := time.Duration(float64(elapsed) / float64(p.processed) * float64(p.total-p.processed))
		msg += fmt.Sprintf(", ~%s remaining", remaining.Round(time.Second))
	}

	p.logFn(msg)
	p.lastLog = now
}

// countWalkableFiles quickly counts all regular files in a directory tree.
func countWalkableFiles(dirPath string) int {
	count := 0
	_ = filepath.Walk(dirPath, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			count++
		}
		return nil
	})
	return count
}
