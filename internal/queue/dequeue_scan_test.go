package queue

import (
	"fmt"
	"testing"
	"time"
)

// TestDequeueIgnoresCompletedJobs is the shape of the defect rather than a
// timing assertion.
//
// The index key is "q:idx:<type>:<status>:<priority>:<id>", so status is the
// second component and a single scan of the whole index cannot skip it. The
// entries it could not skip were the completed ones, which accumulate until
// the queue is purged and come to outnumber the pending work by orders of
// magnitude -- every dispatch round read and parsed all of them to find a
// handful of jobs.
//
// Scanning per type against a prefix that includes the status fixes it. This
// checks the consequence that matters: a queue that is mostly finished still
// dequeues its pending work, and dequeues only that.
func TestDequeueIgnoresCompletedJobs(t *testing.T) {
	s := openTestStore(t)

	const finished = 500
	batch := make([]*Job, 0, finished+3)
	for i := range finished {
		batch = append(batch, &Job{
			Type:        JobDocExtract,
			Priority:    10,
			ContentHash: fmt.Sprintf("done-%d", i),
			DateTaken:   time.Now(),
		})
	}
	if err := s.EnqueueBatch(batch); err != nil {
		t.Fatalf("EnqueueBatch: %v", err)
	}

	// Drain and complete them, so the index fills with finished entries.
	for {
		jobs, err := s.Dequeue(100)
		if err != nil {
			t.Fatalf("Dequeue: %v", err)
		}
		if len(jobs) == 0 {
			break
		}
		for _, j := range jobs {
			if err := s.Complete(j.ID, nil); err != nil {
				t.Fatalf("Complete: %v", err)
			}
		}
	}

	// Now a little real work arrives.
	pending := []*Job{
		{Type: JobDocExtract, Priority: 10, ContentHash: "live-a", DateTaken: time.Now()},
		{Type: JobImageDescribe, Priority: 20, ContentHash: "live-b", DateTaken: time.Now()},
	}
	if err := s.EnqueueBatch(pending); err != nil {
		t.Fatalf("EnqueueBatch(pending): %v", err)
	}

	got, err := s.Dequeue(10)
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if len(got) != len(pending) {
		t.Fatalf("dequeued %d jobs, want %d", len(got), len(pending))
	}
	for _, j := range got {
		if j.Status != StatusRunning {
			t.Errorf("job %s status = %q, want running", j.ContentHash, j.Status)
		}
		if j.ContentHash != "live-a" && j.ContentHash != "live-b" {
			t.Errorf("dequeued a finished job: %s", j.ContentHash)
		}
	}

	// And nothing is left to claim.
	again, err := s.Dequeue(10)
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("dequeued %d jobs from an empty queue", len(again))
	}
}

// TestJobTypesCoversEveryConstant guards the list Dequeue scans.
//
// Dequeue iterates one index prefix per job type, so a type that exists but is
// missing from JobTypes is enqueued and then never picked up -- the job sits
// pending forever and the sync never finishes.
func TestJobTypesCoversEveryConstant(t *testing.T) {
	declared := []JobType{JobDocExtract, JobImageDescribe}
	listed := JobTypes()

	if len(listed) != len(declared) {
		t.Fatalf("JobTypes lists %d types, but %d are declared", len(listed), len(declared))
	}
	seen := make(map[JobType]bool, len(listed))
	for _, jt := range listed {
		seen[jt] = true
	}
	for _, jt := range declared {
		if !seen[jt] {
			t.Errorf("job type %q is declared but not listed in JobTypes", jt)
		}
	}
}
