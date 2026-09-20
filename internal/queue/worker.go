package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Handler processes a single job and returns a JSON result.
type Handler interface {
	Handle(ctx context.Context, job *Job) (json.RawMessage, error)
}

// EventEmitter emits named events (same signature as app.EventEmitter).
type EventEmitter func(event string, data ...any)

// WorkerPool dispatches queue jobs to registered handlers with auto-throttle.
type WorkerPool struct {
	queue       *Store
	handlers    map[JobType]Handler
	emit        EventEmitter
	throttler   *Throttler
	activeCount atomic.Int32
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
	// mu guards ctx, cancel and running, which Run sets and Stop reads.
	mu      sync.Mutex
	running bool
	// done closes when Run returns, so Stop can wait for the dispatch loop
	// itself and not merely for the jobs it started.
	done chan struct{}
}

// NewWorkerPool creates a new worker pool.
func NewWorkerPool(queue *Store, throttler *Throttler, emit EventEmitter) *WorkerPool {
	if emit == nil {
		emit = func(string, ...any) {}
	}
	return &WorkerPool{
		queue:     queue,
		handlers:  make(map[JobType]Handler),
		emit:      emit,
		throttler: throttler,
		done:      make(chan struct{}),
	}
}

// Register associates a handler with a job type. Not goroutine-safe; call before Run.
func (wp *WorkerPool) Register(jobType JobType, handler Handler) {
	wp.handlers[jobType] = handler
}

// Run starts the worker dispatch loop. Blocks until ctx is cancelled or all
// jobs are done (no pending + no running).
func (wp *WorkerPool) Run(ctx context.Context) {
	wp.mu.Lock()
	wp.ctx, wp.cancel = context.WithCancel(ctx)
	wp.running = true
	wp.mu.Unlock()
	defer close(wp.done)

	for {
		select {
		case <-wp.ctx.Done():
			wp.wg.Wait()
			return
		default:
		}

		// How many workers should be active?
		target := wp.throttler.TargetWorkers()
		active := int(wp.activeCount.Load())

		if active >= target {
			time.Sleep(500 * time.Millisecond)
			continue
		}

		// Dequeue work.
		jobs, err := wp.queue.Dequeue(target - active)
		if err != nil || len(jobs) == 0 {
			// Shutdown can land between the check at the top of the loop and
			// here, and whoever stops the pool usually closes the store right
			// afterwards. Asking a closed store how much work is left panics,
			// so the context is re-read before touching it again.
			if wp.ctx.Err() != nil {
				wp.wg.Wait()
				return
			}
			// Check if we're done: no pending jobs and no active workers.
			if wp.queue.PendingCount() == 0 && wp.activeCount.Load() == 0 {
				wp.wg.Wait()
				return
			}
			time.Sleep(1 * time.Second)
			continue
		}

		for _, job := range jobs {
			wp.wg.Add(1)
			wp.activeCount.Add(1)
			go wp.processJob(job)
		}
	}
}

// Stop signals the worker pool to stop and waits for active workers to finish.
func (wp *WorkerPool) Stop() {
	wp.mu.Lock()
	cancel := wp.cancel
	running := wp.running
	wp.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	// Waiting on the jobs alone let Stop return while the dispatch loop was
	// still polling, so a caller that closed the store immediately afterwards
	// raced it.
	if running {
		<-wp.done
	}
	wp.wg.Wait()
}

// ActiveCount returns the number of currently active workers.
func (wp *WorkerPool) ActiveCount() int {
	return int(wp.activeCount.Load())
}

// processJob handles a single job in a goroutine.
func (wp *WorkerPool) processJob(job *Job) {
	defer wp.wg.Done()
	defer wp.activeCount.Add(-1)

	// Recover from panics so one bad job doesn't kill the process.
	defer func() {
		if r := recover(); r != nil {
			errMsg := fmt.Sprintf("panic: %v", r)
			_ = wp.queue.Fail(job.ID, errMsg)
			filePath := ""
			if len(job.FilePaths) > 0 {
				filePath = job.FilePaths[0]
			}
			wp.emit("job:failed", map[string]string{
				"type":  string(job.Type),
				"file":  filePath,
				"error": errMsg,
			})
		}
	}()

	handler, ok := wp.handlers[job.Type]
	if !ok {
		_ = wp.queue.Fail(job.ID, fmt.Sprintf("no handler for %s", job.Type))
		return
	}

	result, err := handler.Handle(wp.ctx, job)
	if err != nil {
		errMsg := fmt.Sprintf("%v", err)
		// Record failure (persists incremented attempts in the store).
		_ = wp.queue.Fail(job.ID, errMsg)

		// Check persisted state to decide on retry.
		updated, _ := wp.queue.GetJob(job.ID)
		if updated != nil && updated.Attempts < job.MaxRetries {
			_ = wp.queue.Requeue(job.ID)
		} else {
			// Final failure — emit for logging.
			filePath := ""
			if len(job.FilePaths) > 0 {
				filePath = job.FilePaths[0]
			}
			wp.emit("job:failed", map[string]string{
				"type":  string(job.Type),
				"file":  filePath,
				"error": errMsg,
			})
		}
		return
	}

	_ = wp.queue.Complete(job.ID, result)

	// Emit progress.
	wp.emit("sync:progress", map[string]any{
		"job_type":  string(job.Type),
		"completed": job.ID,
	})
}
