package queue

import (
	"encoding/json"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpen(t *testing.T) {
	s := openTestStore(t)
	if s == nil {
		t.Fatal("store is nil")
	}
}

func TestEnqueue(t *testing.T) {
	s := openTestStore(t)

	job := &Job{
		Type:        JobDocExtract,
		Priority:    10,
		ContentHash: "sha256:abc",
		FilePaths:   []string{"a.txt"},
		MaxRetries:  3,
	}
	if err := s.Enqueue(job); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if job.ID == "" {
		t.Error("ID not set")
	}
	if job.Status != StatusPending {
		t.Errorf("Status = %q, want pending", job.Status)
	}

	// Verify we can dequeue it.
	jobs, err := s.Dequeue(1)
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1", len(jobs))
	}
	if jobs[0].ID != job.ID {
		t.Errorf("ID mismatch")
	}
}

func TestEnqueueDedup(t *testing.T) {
	s := openTestStore(t)

	job1 := &Job{Type: JobDocExtract, ContentHash: "sha256:same", Priority: 10}
	if err := s.Enqueue(job1); err != nil {
		t.Fatalf("Enqueue first: %v", err)
	}

	job2 := &Job{Type: JobDocExtract, ContentHash: "sha256:same", Priority: 10}
	err := s.Enqueue(job2)
	if err == nil {
		t.Fatal("expected dedup error")
	}
}

func TestEnqueueBatch(t *testing.T) {
	s := openTestStore(t)

	var jobs []*Job
	for i := 0; i < 5; i++ {
		jobs = append(jobs, &Job{
			Type:        JobImageDescribe,
			Priority:    20,
			ContentHash: "hash" + string(rune('a'+i)),
			FilePaths:   []string{"img.jpg"},
			MaxRetries:  3,
		})
	}
	if err := s.EnqueueBatch(jobs); err != nil {
		t.Fatalf("EnqueueBatch: %v", err)
	}
	if s.PendingCount() != 5 {
		t.Errorf("PendingCount = %d, want 5", s.PendingCount())
	}
}

func TestEnqueueBatchDedup(t *testing.T) {
	s := openTestStore(t)

	jobs := []*Job{
		{Type: JobDocExtract, ContentHash: "dup", Priority: 10},
		{Type: JobDocExtract, ContentHash: "dup", Priority: 10},
		{Type: JobDocExtract, ContentHash: "unique", Priority: 10},
	}
	if err := s.EnqueueBatch(jobs); err != nil {
		t.Fatalf("EnqueueBatch: %v", err)
	}
	if s.PendingCount() != 2 {
		t.Errorf("PendingCount = %d, want 2 (one dup skipped)", s.PendingCount())
	}
}

func TestDequeueOrder(t *testing.T) {
	s := openTestStore(t)

	now := time.Now()
	jobs := []*Job{
		{Type: JobFaceDetect, Priority: 30, ContentHash: "c", DateTaken: now.Add(-1 * time.Hour)},
		{Type: JobDocExtract, Priority: 10, ContentHash: "a", DateTaken: now},
		{Type: JobImageDescribe, Priority: 20, ContentHash: "b", DateTaken: now.Add(-2 * time.Hour)},
	}
	if err := s.EnqueueBatch(jobs); err != nil {
		t.Fatal(err)
	}

	dequeued, err := s.Dequeue(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(dequeued) != 3 {
		t.Fatalf("got %d, want 3", len(dequeued))
	}

	// Priority order: 10, 20, 30.
	if dequeued[0].Priority != 10 {
		t.Errorf("first job priority = %d, want 10", dequeued[0].Priority)
	}
	if dequeued[1].Priority != 20 {
		t.Errorf("second job priority = %d, want 20", dequeued[1].Priority)
	}
	if dequeued[2].Priority != 30 {
		t.Errorf("third job priority = %d, want 30", dequeued[2].Priority)
	}
}

func TestDequeueLimit(t *testing.T) {
	s := openTestStore(t)

	for i := 0; i < 5; i++ {
		s.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "l" + string(rune('a'+i))})
	}

	dequeued, err := s.Dequeue(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(dequeued) != 2 {
		t.Fatalf("got %d, want 2", len(dequeued))
	}
	if s.PendingCount() != 3 {
		t.Errorf("remaining pending = %d, want 3", s.PendingCount())
	}
}

func TestComplete(t *testing.T) {
	s := openTestStore(t)

	s.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "comp"})
	jobs, _ := s.Dequeue(1)
	if len(jobs) == 0 {
		t.Fatal("no jobs dequeued")
	}

	result := json.RawMessage(`{"topics":["go"]}`)
	if err := s.Complete(jobs[0].ID, result); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	job, err := s.GetJob(jobs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != StatusDone {
		t.Errorf("Status = %q, want done", job.Status)
	}
	if string(job.Result) != `{"topics":["go"]}` {
		t.Errorf("Result = %s", job.Result)
	}
}

func TestFail(t *testing.T) {
	s := openTestStore(t)

	s.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "fail"})
	jobs, _ := s.Dequeue(1)

	if err := s.Fail(jobs[0].ID, "provider timeout"); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	job, _ := s.GetJob(jobs[0].ID)
	if job.Status != StatusFailed {
		t.Errorf("Status = %q, want failed", job.Status)
	}
	if job.Error != "provider timeout" {
		t.Errorf("Error = %q", job.Error)
	}
	if job.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", job.Attempts)
	}
}

func TestSkip(t *testing.T) {
	s := openTestStore(t)

	s.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "skip"})
	jobs, _ := s.Dequeue(1)

	if err := s.Skip(jobs[0].ID, "cached"); err != nil {
		t.Fatalf("Skip: %v", err)
	}

	job, _ := s.GetJob(jobs[0].ID)
	if job.Status != StatusSkipped {
		t.Errorf("Status = %q, want skipped", job.Status)
	}
}

func TestRequeue(t *testing.T) {
	s := openTestStore(t)

	s.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "req"})
	jobs, _ := s.Dequeue(1)
	s.Fail(jobs[0].ID, "error")

	if err := s.Requeue(jobs[0].ID); err != nil {
		t.Fatalf("Requeue: %v", err)
	}

	job, _ := s.GetJob(jobs[0].ID)
	if job.Status != StatusPending {
		t.Errorf("Status = %q, want pending", job.Status)
	}

	// Should be dequeue-able again.
	requeued, _ := s.Dequeue(1)
	if len(requeued) != 1 {
		t.Fatalf("got %d, want 1 requeued job", len(requeued))
	}
}

func TestHasContentHash(t *testing.T) {
	s := openTestStore(t)

	if s.HasContentHash("nope") {
		t.Error("should not have hash before enqueue")
	}

	s.Enqueue(&Job{Type: JobDocExtract, ContentHash: "exists", Priority: 10})
	if !s.HasContentHash("exists") {
		t.Error("should have hash after enqueue")
	}
}

func TestStats(t *testing.T) {
	s := openTestStore(t)

	s.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "s1"})
	s.Enqueue(&Job{Type: JobImageDescribe, Priority: 20, ContentHash: "s2"})
	s.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "s3"})

	// Complete one doc-extract.
	jobs, _ := s.Dequeue(1)
	s.Complete(jobs[0].ID, nil)

	stats, err := s.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Pending != 2 {
		t.Errorf("Pending = %d, want 2", stats.Pending)
	}
	if stats.Done != 1 {
		t.Errorf("Done = %d, want 1", stats.Done)
	}

	docStats := stats.ByType[JobDocExtract]
	if docStats.Pending != 1 {
		t.Errorf("doc pending = %d, want 1", docStats.Pending)
	}
	if docStats.Done != 1 {
		t.Errorf("doc done = %d, want 1", docStats.Done)
	}
}

func TestPendingCount(t *testing.T) {
	s := openTestStore(t)

	if s.PendingCount() != 0 {
		t.Error("initial pending should be 0")
	}

	s.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "pc1"})
	s.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "pc2"})
	if s.PendingCount() != 2 {
		t.Errorf("PendingCount = %d, want 2", s.PendingCount())
	}
}

func TestCountByType(t *testing.T) {
	s := openTestStore(t)

	s.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "ct1"})
	s.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "ct2"})
	s.Enqueue(&Job{Type: JobImageDescribe, Priority: 20, ContentHash: "ct3"})

	p, d, f := s.CountByType(JobDocExtract)
	if p != 2 || d != 0 || f != 0 {
		t.Errorf("doc-extract: pending=%d, done=%d, failed=%d", p, d, f)
	}

	p, _, _ = s.CountByType(JobImageDescribe)
	if p != 1 {
		t.Errorf("image-describe: pending=%d, want 1", p)
	}
}

func TestSaveLoadCheckpoint(t *testing.T) {
	s := openTestStore(t)

	// No checkpoint initially.
	cp, err := s.LoadCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	if cp != nil {
		t.Error("initial checkpoint should be nil")
	}

	state := &CheckpointState{
		LastReviewAt:    time.Now(),
		ImagesProcessed: 100,
		TotalImages:     500,
		Paused:          true,
	}
	if err := s.SaveCheckpoint(state); err != nil {
		t.Fatal(err)
	}

	loaded, err := s.LoadCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil {
		t.Fatal("loaded checkpoint is nil")
	}
	if loaded.ImagesProcessed != 100 {
		t.Errorf("ImagesProcessed = %d", loaded.ImagesProcessed)
	}
	if !loaded.Paused {
		t.Error("should be paused")
	}
}

func TestPurgeCompleted(t *testing.T) {
	s := openTestStore(t)

	s.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "p1"})
	s.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "p2"})
	s.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "p3"})

	// Complete first, fail second, leave third pending.
	jobs, _ := s.Dequeue(2)
	s.Complete(jobs[0].ID, nil)
	s.Fail(jobs[1].ID, "err")

	if err := s.PurgeCompleted(); err != nil {
		t.Fatal(err)
	}

	stats, _ := s.Stats()
	if stats.Done != 0 {
		t.Errorf("Done = %d, want 0 after purge", stats.Done)
	}
	if stats.Failed != 1 {
		t.Errorf("Failed = %d, want 1 (not purged)", stats.Failed)
	}
	if stats.Pending != 1 {
		t.Errorf("Pending = %d, want 1 (not purged)", stats.Pending)
	}
}

func TestRecoverStalled(t *testing.T) {
	s := openTestStore(t)

	s.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "rs1"})
	s.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "rs2"})
	s.Dequeue(2) // Both now running.

	if s.PendingCount() != 0 {
		t.Errorf("pending before recover = %d", s.PendingCount())
	}

	if err := s.RecoverStalled(); err != nil {
		t.Fatal(err)
	}

	if s.PendingCount() != 2 {
		t.Errorf("pending after recover = %d, want 2", s.PendingCount())
	}
}

func TestGetJob(t *testing.T) {
	s := openTestStore(t)

	// Not found.
	job, err := s.GetJob("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if job != nil {
		t.Error("expected nil for nonexistent job")
	}

	// Found.
	s.Enqueue(&Job{Type: JobDocExtract, Priority: 10, ContentHash: "gj"})
	jobs, _ := s.Dequeue(1)
	got, err := s.GetJob(jobs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected non-nil job")
	}
	if got.ContentHash != "gj" {
		t.Errorf("ContentHash = %q", got.ContentHash)
	}
}
