package queue

import (
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func fakeCPU(pct float64) CPUPercent {
	return func(_ time.Duration, _ bool) ([]float64, error) {
		return []float64{pct}, nil
	}
}

func TestNewThrottler_Defaults(t *testing.T) {
	th := NewThrottlerWithCPU(0, 0, fakeCPU(50))
	defer th.Stop()

	expected := max(runtime.NumCPU()/2, 1)
	if th.maxWorkers != expected {
		t.Errorf("maxWorkers = %d, want %d", th.maxWorkers, expected)
	}
	if th.targetCPU != 70.0 {
		t.Errorf("targetCPU = %f, want 70", th.targetCPU)
	}
}

func TestThrottler_TargetWorkers_Initial(t *testing.T) {
	th := NewThrottlerWithCPU(4, 70, fakeCPU(50))
	defer th.Stop()

	if th.TargetWorkers() != 4 {
		t.Errorf("initial target = %d, want 4", th.TargetWorkers())
	}
}

func TestThrottler_ReduceUnderLoad(t *testing.T) {
	var cpuVal atomic.Int64
	cpuVal.Store(85) // high CPU

	cpuFn := func(_ time.Duration, _ bool) ([]float64, error) {
		return []float64{float64(cpuVal.Load())}, nil
	}

	th := NewThrottlerWithCPU(4, 70, cpuFn)
	th.Stop()
	th.done = make(chan struct{})
	th.sampleInterval = 10 * time.Millisecond
	go th.monitorLoop()
	defer th.Stop()

	// Wait for a few ticks.
	time.Sleep(100 * time.Millisecond)

	target := th.TargetWorkers()
	if target >= 4 {
		t.Errorf("target = %d, should have decreased from 4 under high CPU", target)
	}
}

func TestThrottler_IncreaseWhenIdle(t *testing.T) {
	th := NewThrottlerWithCPU(4, 70, fakeCPU(40))
	th.Stop()
	th.done = make(chan struct{})
	th.sampleInterval = 10 * time.Millisecond

	// Set initial target low.
	th.mu.Lock()
	th.currentTarget = 1
	th.mu.Unlock()

	go th.monitorLoop()
	defer th.Stop()

	time.Sleep(100 * time.Millisecond)

	target := th.TargetWorkers()
	if target <= 1 {
		t.Errorf("target = %d, should have increased from 1 under low CPU", target)
	}
}

func TestThrottler_HoldSteady(t *testing.T) {
	th := NewThrottlerWithCPU(4, 70, fakeCPU(60)) // between 50 and 70
	th.Stop()
	th.done = make(chan struct{})
	th.sampleInterval = 10 * time.Millisecond

	th.mu.Lock()
	th.currentTarget = 3
	th.mu.Unlock()

	go th.monitorLoop()
	defer th.Stop()

	time.Sleep(100 * time.Millisecond)

	if th.TargetWorkers() != 3 {
		t.Errorf("target = %d, should hold at 3 in sweet spot", th.TargetWorkers())
	}
}

func TestThrottler_MinFloor(t *testing.T) {
	th := NewThrottlerWithCPU(4, 70, fakeCPU(100))
	th.Stop()
	th.done = make(chan struct{})
	th.sampleInterval = 10 * time.Millisecond

	go th.monitorLoop()
	defer th.Stop()

	time.Sleep(200 * time.Millisecond)

	if th.TargetWorkers() < 1 {
		t.Errorf("target = %d, should never go below 1", th.TargetWorkers())
	}
}

func TestThrottler_MaxCeiling(t *testing.T) {
	th := NewThrottlerWithCPU(4, 70, fakeCPU(10))
	th.Stop()
	th.done = make(chan struct{})
	th.sampleInterval = 10 * time.Millisecond

	go th.monitorLoop()
	defer th.Stop()

	time.Sleep(200 * time.Millisecond)

	if th.TargetWorkers() > 4 {
		t.Errorf("target = %d, should never exceed maxWorkers=4", th.TargetWorkers())
	}
}

func TestThrottler_Stop(t *testing.T) {
	th := NewThrottlerWithCPU(4, 70, fakeCPU(50))
	th.Stop()

	// Double stop should not panic.
	th.Stop()
}
