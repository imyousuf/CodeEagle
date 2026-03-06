package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// mockHandler implements Handler for testing.
type mockHandler struct {
	fn func(ctx context.Context, job *Job) (json.RawMessage, error)
}

func (m *mockHandler) Handle(ctx context.Context, job *Job) (json.RawMessage, error) {
	return m.fn(ctx, job)
}

func newWorkerTestPool(t *testing.T) (*WorkerPool, *Store) {
	t.Helper()
	s := openTestStore(t)
	th := NewThrottlerWithCPU(4, 70, fakeCPU(30))
	pool := NewWorkerPool(s, th, nil)
	t.Cleanup(func() { th.Stop() })
	return pool, s
}

func TestWorkerPool_ProcessesJobs(t *testing.T) {
	pool, store := newWorkerTestPool(t)

	var processed atomic.Int32
	pool.Register(JobDocExtract, &mockHandler{
		fn: func(_ context.Context, _ *Job) (json.RawMessage, error) {
			processed.Add(1)
			return json.RawMessage(`{"ok":true}`), nil
		},
	})

	store.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "wp1", MaxRetries: 3})
	store.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "wp2", MaxRetries: 3})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool.Run(ctx)

	if processed.Load() != 2 {
		t.Errorf("processed = %d, want 2", processed.Load())
	}
}

func TestWorkerPool_CompleteSetsResult(t *testing.T) {
	pool, store := newWorkerTestPool(t)

	pool.Register(JobDocExtract, &mockHandler{
		fn: func(_ context.Context, _ *Job) (json.RawMessage, error) {
			return json.RawMessage(`{"topics":["go"]}`), nil
		},
	})

	store.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "cr1", MaxRetries: 3})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool.Run(ctx)

	// Check all jobs are done.
	stats, err := store.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Done != 1 {
		t.Errorf("Done = %d, want 1", stats.Done)
	}
}

func TestWorkerPool_RetryOnError(t *testing.T) {
	pool, store := newWorkerTestPool(t)

	var attempts atomic.Int32
	pool.Register(JobDocExtract, &mockHandler{
		fn: func(_ context.Context, _ *Job) (json.RawMessage, error) {
			n := attempts.Add(1)
			if n < 3 {
				return nil, fmt.Errorf("transient error %d", n)
			}
			return json.RawMessage(`{"ok":true}`), nil
		},
	})

	store.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "retry1", MaxRetries: 5})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool.Run(ctx)

	if attempts.Load() < 3 {
		t.Errorf("attempts = %d, want >= 3", attempts.Load())
	}
}

func TestWorkerPool_MaxRetriesExceeded(t *testing.T) {
	pool, store := newWorkerTestPool(t)

	pool.Register(JobDocExtract, &mockHandler{
		fn: func(_ context.Context, _ *Job) (json.RawMessage, error) {
			return nil, fmt.Errorf("always fails")
		},
	})

	store.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "maxr1", MaxRetries: 2})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool.Run(ctx)

	stats, _ := store.Stats()
	if stats.Failed != 1 {
		t.Errorf("Failed = %d, want 1", stats.Failed)
	}
}

func TestWorkerPool_NoHandler(t *testing.T) {
	pool, store := newWorkerTestPool(t)

	// Register handler for doc-extract but enqueue image-describe.
	pool.Register(JobDocExtract, &mockHandler{
		fn: func(_ context.Context, _ *Job) (json.RawMessage, error) {
			return nil, nil
		},
	})

	store.Enqueue(&Job{Type: JobImageDescribe, Priority: 20, ContentHash: "nh1", MaxRetries: 3})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool.Run(ctx)

	stats, _ := store.Stats()
	if stats.Failed != 1 {
		t.Errorf("Failed = %d, want 1 (no handler)", stats.Failed)
	}
}

func TestWorkerPool_EmitsProgress(t *testing.T) {
	store := openTestStore(t)
	th := NewThrottlerWithCPU(4, 70, fakeCPU(30))
	defer th.Stop()

	var progressEvents atomic.Int32
	emit := func(event string, _ ...any) {
		if event == "sync:progress" {
			progressEvents.Add(1)
		}
	}

	pool := NewWorkerPool(store, th, emit)
	pool.Register(JobDocExtract, &mockHandler{
		fn: func(_ context.Context, _ *Job) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		},
	})

	store.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "pe1", MaxRetries: 3})
	store.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "pe2", MaxRetries: 3})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool.Run(ctx)

	if progressEvents.Load() != 2 {
		t.Errorf("progress events = %d, want 2", progressEvents.Load())
	}
}

func TestWorkerPool_Stop(t *testing.T) {
	pool, store := newWorkerTestPool(t)

	pool.Register(JobDocExtract, &mockHandler{
		fn: func(ctx context.Context, _ *Job) (json.RawMessage, error) {
			// Simulate slow handler.
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(100 * time.Millisecond):
				return json.RawMessage(`{}`), nil
			}
		},
	})

	for i := range 10 {
		store.Enqueue(&Job{
			Type:        JobDocExtract,
			Priority:    10,
			ContentHash: fmt.Sprintf("stop%d", i),
			MaxRetries:  3,
		})
	}

	done := make(chan struct{})
	go func() {
		pool.Run(t.Context())
		close(done)
	}()

	// Give it time to pick up jobs then stop.
	time.Sleep(200 * time.Millisecond)
	pool.Stop()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pool did not stop within timeout")
	}
}

func TestWorkerPool_ActiveCount(t *testing.T) {
	store := openTestStore(t)
	th := NewThrottlerWithCPU(4, 70, fakeCPU(30))
	defer th.Stop()

	pool := NewWorkerPool(store, th, nil)

	// Initially zero.
	if pool.ActiveCount() != 0 {
		t.Errorf("initial ActiveCount = %d, want 0", pool.ActiveCount())
	}

	started := make(chan struct{})
	block := make(chan struct{})

	pool.Register(JobDocExtract, &mockHandler{
		fn: func(_ context.Context, _ *Job) (json.RawMessage, error) {
			started <- struct{}{}
			<-block
			return json.RawMessage(`{}`), nil
		},
	})

	store.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "ac1", MaxRetries: 3})

	go pool.Run(t.Context())

	// Wait for handler to start.
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not start")
	}

	if pool.ActiveCount() < 1 {
		t.Errorf("ActiveCount during work = %d, want >= 1", pool.ActiveCount())
	}

	close(block)
	pool.Stop()
}

func TestWorkerPool_PauseResume(t *testing.T) {
	store := openTestStore(t)
	th := NewThrottlerWithCPU(4, 70, fakeCPU(30))
	defer th.Stop()

	var events []string
	emit := func(event string, _ ...any) {
		events = append(events, event)
	}

	pool := NewWorkerPool(store, th, emit)

	// Pause the pool.
	pool.PauseForCheckpoint(map[string]any{"new_clusters": 5})

	if !pool.IsPaused() {
		t.Error("expected pool to be paused")
	}

	// Register a handler and enqueue a job.
	var processed atomic.Int32
	pool.Register(JobDocExtract, &mockHandler{
		fn: func(_ context.Context, _ *Job) (json.RawMessage, error) {
			processed.Add(1)
			return json.RawMessage(`{}`), nil
		},
	})
	store.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "pr1", MaxRetries: 3})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		pool.Run(ctx)
		close(done)
	}()

	// Give it time to hit the pause.
	time.Sleep(200 * time.Millisecond)

	// Nothing should be processed yet.
	if processed.Load() != 0 {
		t.Errorf("processed = %d before resume, want 0", processed.Load())
	}

	// Resume.
	pool.Resume()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pool did not finish after resume")
	}

	if processed.Load() != 1 {
		t.Errorf("processed = %d after resume, want 1", processed.Load())
	}

	if pool.IsPaused() {
		t.Error("pool should not be paused after resume")
	}
}
