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

// CheckFaceCheckpointFn is set by face_handlers.go init() when faces build tag
// is active. Called after each face-detect job completes to check if a
// checkpoint should fire. Nil by default (no-op).
var CheckFaceCheckpointFn func(wp *WorkerPool)

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

	// Checkpoint support.
	resumeCh chan struct{}
	pauseMu  sync.Mutex
	paused   bool
}

// NewWorkerPool creates a new worker pool.
func NewWorkerPool(queue *Store, throttler *Throttler, emit EventEmitter) *WorkerPool {
	if emit == nil {
		emit = func(string, ...any) {}
	}
	return &WorkerPool{
		queue:    queue,
		handlers: make(map[JobType]Handler),
		emit:     emit,
		throttler: throttler,
		resumeCh: make(chan struct{}, 1),
	}
}

// Register associates a handler with a job type. Not goroutine-safe; call before Run.
func (wp *WorkerPool) Register(jobType JobType, handler Handler) {
	wp.handlers[jobType] = handler
}

// Run starts the worker dispatch loop. Blocks until ctx is cancelled or all
// jobs are done (no pending + no running).
func (wp *WorkerPool) Run(ctx context.Context) {
	wp.ctx, wp.cancel = context.WithCancel(ctx)

	for {
		select {
		case <-wp.ctx.Done():
			wp.wg.Wait()
			return
		default:
		}

		// Check if paused for checkpoint.
		wp.pauseMu.Lock()
		if wp.paused {
			wp.pauseMu.Unlock()
			select {
			case <-wp.resumeCh:
				wp.pauseMu.Lock()
				wp.paused = false
				wp.pauseMu.Unlock()
				wp.emit("sync:resumed", nil)
			case <-wp.ctx.Done():
				wp.wg.Wait()
				return
			}
		} else {
			wp.pauseMu.Unlock()
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
	if wp.cancel != nil {
		wp.cancel()
	}
	wp.wg.Wait()
}

// Resume unblocks a checkpoint pause.
func (wp *WorkerPool) Resume() {
	select {
	case wp.resumeCh <- struct{}{}:
	default:
	}
}

// ActiveCount returns the number of currently active workers.
func (wp *WorkerPool) ActiveCount() int {
	return int(wp.activeCount.Load())
}

// IsPaused returns whether the pool is paused for a checkpoint.
func (wp *WorkerPool) IsPaused() bool {
	wp.pauseMu.Lock()
	defer wp.pauseMu.Unlock()
	return wp.paused
}

// PauseForCheckpoint saves checkpoint state, emits sync:checkpoint, and blocks
// until Resume is called.
func (wp *WorkerPool) PauseForCheckpoint(data map[string]any) {
	wp.pauseMu.Lock()
	wp.paused = true
	wp.pauseMu.Unlock()

	wp.emit("sync:checkpoint", data)
	wp.emit("notification:show", map[string]string{
		"title": "CodeEagle",
		"body":  fmt.Sprintf("%v new face groups found — review to continue sync", data["new_clusters"]),
	})
}

// processJob handles a single job in a goroutine.
func (wp *WorkerPool) processJob(job *Job) {
	defer wp.wg.Done()
	defer wp.activeCount.Add(-1)

	handler, ok := wp.handlers[job.Type]
	if !ok {
		_ = wp.queue.Fail(job.ID, fmt.Sprintf("no handler for %s", job.Type))
		return
	}

	result, err := handler.Handle(wp.ctx, job)
	if err != nil {
		// Record failure (persists incremented attempts in the store).
		_ = wp.queue.Fail(job.ID, fmt.Sprintf("%v", err))

		// Check persisted state to decide on retry.
		updated, _ := wp.queue.GetJob(job.ID)
		if updated != nil && updated.Attempts < job.MaxRetries {
			_ = wp.queue.Requeue(job.ID)
		}
		return
	}

	_ = wp.queue.Complete(job.ID, result)

	// Emit progress.
	wp.emit("sync:progress", map[string]any{
		"job_type":  string(job.Type),
		"completed": job.ID,
	})

	// Check face checkpoint if applicable.
	if job.Type == JobFaceDetect && CheckFaceCheckpointFn != nil {
		CheckFaceCheckpointFn(wp)
	}
}
