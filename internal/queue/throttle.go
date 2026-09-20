package queue

import (
	"runtime"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
)

// CPUPercent is a function type for getting CPU usage. Allows test injection.
type CPUPercent func(interval time.Duration, perCPU bool) ([]float64, error)

// Throttler dynamically adjusts the target worker count based on system CPU usage.
type Throttler struct {
	maxWorkers     int
	minWorkers     int
	targetCPU      float64 // reduce workers above this %
	coolCPU        float64 // increase workers below this % (targetCPU - 20)
	sampleInterval time.Duration

	mu            sync.Mutex
	currentTarget int

	cpuPercent CPUPercent
	done       chan struct{}
	closeOnce  sync.Once
}

// NewThrottler creates and starts a Throttler with default CPU monitoring.
// maxWorkers=0 means runtime.NumCPU()/2 (min 1). targetCPU=0 means 70.0.
func NewThrottler(maxWorkers int, targetCPU float64) *Throttler {
	return NewThrottlerWithCPU(maxWorkers, targetCPU, cpu.Percent)
}

// NewThrottlerWithCPU creates a Throttler with an injectable CPU function.
func NewThrottlerWithCPU(maxWorkers int, targetCPU float64, cpuFn CPUPercent) *Throttler {
	t := newThrottler(maxWorkers, targetCPU, cpuFn, 5*time.Second)
	go t.monitorLoop()
	return t
}

// newThrottler creates a Throttler without starting the monitor loop.
// Used by tests that need a custom sample interval.
func newThrottler(maxWorkers int, targetCPU float64, cpuFn CPUPercent, interval time.Duration) *Throttler {
	if maxWorkers <= 0 {
		maxWorkers = max(runtime.NumCPU()/2, 1)
	}
	if targetCPU <= 0 {
		targetCPU = 70.0
	}
	return &Throttler{
		maxWorkers:     maxWorkers,
		minWorkers:     1,
		targetCPU:      targetCPU,
		coolCPU:        targetCPU - 20,
		sampleInterval: interval,
		currentTarget:  maxWorkers,
		cpuPercent:     cpuFn,
		done:           make(chan struct{}),
	}
}

// TargetWorkers returns the current target worker count.
func (t *Throttler) TargetWorkers() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.currentTarget
}

// Stop halts the monitor loop.
func (t *Throttler) Stop() {
	t.closeOnce.Do(func() { close(t.done) })
}

// monitorLoop periodically checks CPU and adjusts currentTarget.
func (t *Throttler) monitorLoop() {
	ticker := time.NewTicker(t.sampleInterval)
	defer ticker.Stop()

	for {
		select {
		case <-t.done:
			return
		case <-ticker.C:
			percents, err := t.cpuPercent(t.sampleInterval, false)
			if err != nil || len(percents) == 0 {
				continue
			}
			systemCPU := percents[0]

			t.mu.Lock()
			if systemCPU > t.targetCPU {
				t.currentTarget = max(t.minWorkers, t.currentTarget-1)
			} else if systemCPU < t.coolCPU {
				t.currentTarget = min(t.maxWorkers, t.currentTarget+1)
			}
			t.mu.Unlock()
		}
	}
}
