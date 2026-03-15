//go:build faces

package queue

import (
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

// mockPersonStore implements PersonStore for testing.
type mockPersonStore struct {
	exemplars   []*embedded.Exemplar
	assignments []*embedded.FaceAssignment
	scanned     map[string]bool
	images      map[string]*embedded.ImageMetadata
}

func newMockPersonStore() *mockPersonStore {
	return &mockPersonStore{
		scanned: make(map[string]bool),
		images:  make(map[string]*embedded.ImageMetadata),
	}
}

func (m *mockPersonStore) AllExemplars() ([]*embedded.Exemplar, error) {
	return m.exemplars, nil
}

func (m *mockPersonStore) AssignFaceToPerson(fa *embedded.FaceAssignment) error {
	m.assignments = append(m.assignments, fa)
	return nil
}

func (m *mockPersonStore) AddExemplar(e *embedded.Exemplar) error {
	m.exemplars = append(m.exemplars, e)
	return nil
}

func (m *mockPersonStore) MarkImageScanned(imagePath string) error {
	m.scanned[imagePath] = true
	return nil
}

func (m *mockPersonStore) IsImageScanned(imagePath string) bool {
	return m.scanned[imagePath]
}

func (m *mockPersonStore) IndexImage(meta *embedded.ImageMetadata) error {
	m.images[meta.ImagePath] = meta
	return nil
}

func (m *mockPersonStore) ClearAllImageScanned() error {
	m.scanned = make(map[string]bool)
	return nil
}

func TestMarshalFaceResult(t *testing.T) {
	result := marshalFaceResult("detected", "/photo.jpg", 3)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	s := string(result)
	if s == "" {
		t.Error("expected non-empty JSON")
	}
}

func TestCheckFaceCheckpointNotPaused(t *testing.T) {
	qs := openTestStore(t)

	throttler := NewThrottlerWithCPU(2, 70, fakeCPU(30))
	defer throttler.Stop()
	pool := NewWorkerPool(qs, throttler, nil)

	// Should not panic with no face-detect stats.
	checkFaceCheckpoint(pool)
	if pool.IsPaused() {
		t.Error("should not be paused when no face-detect jobs exist")
	}
}

func TestCheckFaceCheckpointTriggersAfterThreshold(t *testing.T) {
	qs := openTestStore(t)

	// Enqueue and complete 10 face-detect jobs to trigger checkpoint.
	for i := range 10 {
		job := &Job{
			Type:        JobFaceDetect,
			Priority:    30,
			ContentHash: "hash" + string(rune('a'+i)),
			FilePaths:   []string{"/img" + string(rune('a'+i)) + ".jpg"},
			MaxRetries:  1,
		}
		if err := qs.Enqueue(job); err != nil {
			t.Fatal(err)
		}
		// Dequeue and complete.
		jobs, _ := qs.Dequeue(1)
		if len(jobs) > 0 {
			_ = qs.Complete(jobs[0].ID, nil)
		}
	}

	throttler := NewThrottlerWithCPU(2, 70, fakeCPU(30))
	defer throttler.Stop()

	var checkpointEmitted bool
	emit := func(event string, data ...any) {
		if event == "sync:checkpoint" {
			checkpointEmitted = true
		}
	}
	pool := NewWorkerPool(qs, throttler, emit)

	checkFaceCheckpoint(pool)
	if !checkpointEmitted {
		t.Error("expected checkpoint to be emitted after 10 face-detect completions")
	}
	if !pool.IsPaused() {
		t.Error("expected pool to be paused after checkpoint")
	}
}

func TestCheckFaceCheckpointSkipsWhenAlreadyPaused(t *testing.T) {
	qs := openTestStore(t)

	throttler := NewThrottlerWithCPU(2, 70, fakeCPU(30))
	defer throttler.Stop()
	pool := NewWorkerPool(qs, throttler, nil)

	// Manually pause.
	pool.pauseMu.Lock()
	pool.paused = true
	pool.pauseMu.Unlock()

	// Should return early without error.
	checkFaceCheckpoint(pool)
}

func TestMockPersonStoreRoundtrip(t *testing.T) {
	ps := newMockPersonStore()

	// Test exemplar.
	e := &embedded.Exemplar{PersonID: "p1", Hash: "h1", Embedding: []float32{1, 2, 3}, DateTaken: time.Now()}
	if err := ps.AddExemplar(e); err != nil {
		t.Fatal(err)
	}
	exs, _ := ps.AllExemplars()
	if len(exs) != 1 {
		t.Errorf("expected 1 exemplar, got %d", len(exs))
	}

	// Test scan state.
	if ps.IsImageScanned("/a.jpg") {
		t.Error("should not be scanned")
	}
	ps.MarkImageScanned("/a.jpg")
	if !ps.IsImageScanned("/a.jpg") {
		t.Error("should be scanned")
	}

	// Test image metadata.
	meta := &embedded.ImageMetadata{ImagePath: "/a.jpg", FaceCount: 2}
	ps.IndexImage(meta)
	if ps.images["/a.jpg"].FaceCount != 2 {
		t.Error("metadata not stored")
	}
}
