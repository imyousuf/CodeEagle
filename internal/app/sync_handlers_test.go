//go:build app

package app

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/config"
)

func TestIsSyncing_Initially(t *testing.T) {
	a := NewApp(&config.Config{}, nil, "", nil, nil)
	if a.IsSyncing() {
		t.Error("IsSyncing should be false initially")
	}
}

func TestStartSync_NoFactory(t *testing.T) {
	a := NewApp(&config.Config{}, nil, "", nil, nil)
	err := a.StartSync(false)
	if err == nil {
		t.Fatal("StartSync should fail when syncFunc is nil")
	}
	if !strings.Contains(err.Error(), "not available") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStartSync_ConcurrentReject(t *testing.T) {
	started := make(chan struct{})
	done := make(chan struct{})
	syncFn := func(_ context.Context, _ *config.Config, _ []string, _ bool, _ func(string, ...any), _ func(string, ...any)) error {
		close(started)
		<-done // block until test releases
		return nil
	}

	a := NewApp(&config.Config{}, nil, "", nil, syncFn)
	// Provide a context so EventsEmit doesn't panic (it's nil-safe for testing).
	a.ctx = context.Background()

	if err := a.StartSync(false); err != nil {
		t.Fatalf("first StartSync failed: %v", err)
	}

	// Wait for the goroutine to actually start.
	<-started

	// Second call should be rejected.
	err := a.StartSync(false)
	if err == nil {
		t.Fatal("second StartSync should fail while syncing")
	}
	if !strings.Contains(err.Error(), "already in progress") {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify IsSyncing is true.
	if !a.IsSyncing() {
		t.Error("IsSyncing should be true during sync")
	}

	// Release the sync goroutine.
	close(done)

	// Wait for syncing to become false.
	for i := 0; i < 100; i++ {
		if !a.IsSyncing() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("IsSyncing should be false after sync completes")
}

func TestWithGraph_BlockedDuringSync(t *testing.T) {
	a := NewApp(&config.Config{}, nil, "", nil, nil)
	a.syncing = true

	err := a.withGraph(func(gr *graphResources) error {
		t.Error("callback should not be called during sync")
		return nil
	})
	if err == nil {
		t.Fatal("withGraph should fail during sync")
	}
	if !strings.Contains(err.Error(), "sync in progress") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStartSync_CallsSyncFunc(t *testing.T) {
	var called atomic.Bool
	syncFn := func(_ context.Context, _ *config.Config, _ []string, _ bool, _ func(string, ...any), _ func(string, ...any)) error {
		called.Store(true)
		return nil
	}

	a := NewApp(&config.Config{}, []string{"/tmp"}, "", nil, syncFn)
	a.ctx = context.Background()

	if err := a.StartSync(true); err != nil {
		t.Fatalf("StartSync failed: %v", err)
	}

	// Wait for async completion.
	for i := 0; i < 100; i++ {
		if called.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !called.Load() {
		t.Error("syncFunc was not called")
	}
}
