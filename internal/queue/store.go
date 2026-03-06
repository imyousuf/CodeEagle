package queue

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/google/uuid"
)

// JobType identifies the kind of enrichment work.
type JobType string

const (
	JobDocExtract    JobType = "doc-extract"
	JobImageDescribe JobType = "image-describe"
	JobFaceDetect    JobType = "face-detect"
	JobFaceCluster   JobType = "face-cluster"
)

// JobStatus represents the current state of a job.
type JobStatus string

const (
	StatusPending  JobStatus = "pending"
	StatusRunning  JobStatus = "running"
	StatusDone     JobStatus = "done"
	StatusFailed   JobStatus = "failed"
	StatusSkipped  JobStatus = "skipped"
)

// Job is a single enrichment work item in the queue.
type Job struct {
	ID          string          `json:"id"`
	Type        JobType         `json:"type"`
	Status      JobStatus       `json:"status"`
	Priority    int             `json:"priority"`
	ContentHash string          `json:"content_hash"`
	FilePaths   []string        `json:"file_paths"`
	DateTaken   time.Time       `json:"date_taken"`
	Attempts    int             `json:"attempts"`
	MaxRetries  int             `json:"max_retries"`
	Error       string          `json:"error,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// QueueStats holds aggregate job counts.
type QueueStats struct {
	Pending int                   `json:"pending"`
	Running int                   `json:"running"`
	Done    int                   `json:"done"`
	Failed  int                   `json:"failed"`
	Skipped int                   `json:"skipped"`
	ByType  map[JobType]TypeStats `json:"by_type"`
}

// TypeStats holds per-type job counts.
type TypeStats struct {
	Pending int `json:"pending"`
	Done    int `json:"done"`
	Failed  int `json:"failed"`
}

// CheckpointState persists the face detection checkpoint state.
type CheckpointState struct {
	LastReviewAt         time.Time `json:"last_review_at"`
	FacesAtLastReview    int       `json:"faces_at_last_review"`
	ClustersAtLastReview int       `json:"clusters_at_last_review"`
	ImagesProcessed      int       `json:"images_processed"`
	TotalImages          int       `json:"total_images"`
	Paused               bool      `json:"paused"`
}

// Key prefix constants.
const (
	prefixJob        = "q:job:"
	prefixIdx        = "q:idx:"
	prefixHash       = "q:hash:"
	prefixCheckpoint = "q:meta:checkpoint"
	prefixStats      = "q:meta:stats"
)

// Store is a persistent job queue backed by BadgerDB.
type Store struct {
	db *badger.DB
}

// Open opens a queue store at the given path.
func Open(dbPath string) (*Store, error) {
	opts := badger.DefaultOptions(dbPath)
	opts.Logger = nil
	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("open queue store: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the queue store.
func (s *Store) Close() error {
	return s.db.Close()
}

// indexKey builds the scannable secondary index key.
// Format: q:idx:<jobType>:<status>:<priority_padded>:<jobID>
func indexKey(jobType JobType, status JobStatus, priority int, jobID string) []byte {
	return fmt.Appendf(nil, "%s%s:%s:%03d:%s", prefixIdx, jobType, status, priority, jobID)
}

// jobKey returns the primary job key.
func jobKey(jobID string) []byte {
	return []byte(prefixJob + jobID)
}

// hashKey returns the dedup index key.
func hashKey(contentHash string) []byte {
	return []byte(prefixHash + contentHash)
}

// Enqueue inserts a new job. Sets ID, CreatedAt, UpdatedAt, Status.
// Returns an error if a job with the same content hash already exists.
func (s *Store) Enqueue(job *Job) error {
	if job.ID == "" {
		job.ID = uuid.New().String()
	}
	now := time.Now()
	job.Status = StatusPending
	job.CreatedAt = now
	job.UpdatedAt = now

	return s.db.Update(func(txn *badger.Txn) error {
		// Check dedup.
		if job.ContentHash != "" {
			_, err := txn.Get(hashKey(job.ContentHash))
			if err == nil {
				return fmt.Errorf("content hash %s already exists", job.ContentHash)
			}
		}

		data, err := json.Marshal(job)
		if err != nil {
			return fmt.Errorf("marshal job: %w", err)
		}

		// Write primary record.
		if err := txn.Set(jobKey(job.ID), data); err != nil {
			return err
		}

		// Write index.
		if err := txn.Set(indexKey(job.Type, job.Status, job.Priority, job.ID), nil); err != nil {
			return err
		}

		// Write hash index.
		if job.ContentHash != "" {
			if err := txn.Set(hashKey(job.ContentHash), []byte(job.ID)); err != nil {
				return err
			}
		}

		return nil
	})
}

// EnqueueBatch inserts multiple jobs. Jobs with duplicate content hashes
// are silently skipped. Uses batched transactions for performance.
func (s *Store) EnqueueBatch(jobs []*Job) error {
	const batchSize = 100
	for i := 0; i < len(jobs); i += batchSize {
		end := min(i+batchSize, len(jobs))
		batch := jobs[i:end]

		err := s.db.Update(func(txn *badger.Txn) error {
			for _, job := range batch {
				if job.ID == "" {
					job.ID = uuid.New().String()
				}
				now := time.Now()
				job.Status = StatusPending
				job.CreatedAt = now
				job.UpdatedAt = now

				// Check dedup — skip silently.
				if job.ContentHash != "" {
					_, err := txn.Get(hashKey(job.ContentHash))
					if err == nil {
						continue
					}
				}

				data, err := json.Marshal(job)
				if err != nil {
					return fmt.Errorf("marshal job: %w", err)
				}
				if err := txn.Set(jobKey(job.ID), data); err != nil {
					return err
				}
				if err := txn.Set(indexKey(job.Type, job.Status, job.Priority, job.ID), nil); err != nil {
					return err
				}
				if job.ContentHash != "" {
					if err := txn.Set(hashKey(job.ContentHash), []byte(job.ID)); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// Dequeue atomically claims up to limit pending jobs, changing them to running.
// Returns jobs sorted by priority (lowest first), then by DateTaken (oldest first).
func (s *Store) Dequeue(limit int) ([]*Job, error) {
	if limit <= 0 {
		return nil, nil
	}

	// Phase 1: collect candidate job IDs from the index (key-only scan).
	type candidate struct {
		jobID   string
		idxKey  []byte
		jobType JobType
	}
	var candidates []candidate

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = []byte(prefixIdx)
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek([]byte(prefixIdx)); it.Valid(); it.Next() {
			key := string(it.Item().Key())
			// Parse: q:idx:<jobType>:<status>:<priority>:<jobID>
			parts := strings.SplitN(strings.TrimPrefix(key, prefixIdx), ":", 4)
			if len(parts) != 4 {
				continue
			}
			if JobStatus(parts[1]) != StatusPending {
				continue
			}
			candidates = append(candidates, candidate{
				jobID:   parts[3],
				idxKey:  it.Item().KeyCopy(nil),
				jobType: JobType(parts[0]),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	// Phase 2: load jobs and sort by priority then DateTaken.
	type jobWithCandidate struct {
		job       *Job
		candidate candidate
	}
	var loaded []jobWithCandidate

	err = s.db.View(func(txn *badger.Txn) error {
		for _, c := range candidates {
			item, err := txn.Get(jobKey(c.jobID))
			if err != nil {
				continue
			}
			var job Job
			err = item.Value(func(val []byte) error {
				return json.Unmarshal(val, &job)
			})
			if err != nil || job.Status != StatusPending {
				continue
			}
			loaded = append(loaded, jobWithCandidate{job: &job, candidate: c})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(loaded, func(i, j int) bool {
		if loaded[i].job.Priority != loaded[j].job.Priority {
			return loaded[i].job.Priority < loaded[j].job.Priority
		}
		return loaded[i].job.DateTaken.Before(loaded[j].job.DateTaken)
	})

	if len(loaded) > limit {
		loaded = loaded[:limit]
	}

	// Phase 3: atomically transition selected jobs to running.
	var result []*Job
	err = s.db.Update(func(txn *badger.Txn) error {
		for _, lc := range loaded {
			job := lc.job
			job.Status = StatusRunning
			job.UpdatedAt = time.Now()

			data, err := json.Marshal(job)
			if err != nil {
				return err
			}
			if err := txn.Set(jobKey(job.ID), data); err != nil {
				return err
			}
			// Delete old index key, write new one.
			if err := txn.Delete(lc.candidate.idxKey); err != nil {
				return err
			}
			if err := txn.Set(indexKey(job.Type, StatusRunning, job.Priority, job.ID), nil); err != nil {
				return err
			}
			result = append(result, job)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// Complete marks a job as done and stores its result.
func (s *Store) Complete(jobID string, result json.RawMessage) error {
	return s.transitionJob(jobID, StatusDone, "", result)
}

// Fail marks a job as failed with an error message.
func (s *Store) Fail(jobID string, errMsg string) error {
	return s.transitionJob(jobID, StatusFailed, errMsg, nil)
}

// Skip marks a job as skipped.
func (s *Store) Skip(jobID string, reason string) error {
	return s.transitionJob(jobID, StatusSkipped, reason, nil)
}

// Requeue resets a failed or running job back to pending.
func (s *Store) Requeue(jobID string) error {
	return s.transitionJob(jobID, StatusPending, "", nil)
}

// transitionJob changes a job's status, updating the index keys.
func (s *Store) transitionJob(jobID string, newStatus JobStatus, errMsg string, result json.RawMessage) error {
	return s.db.Update(func(txn *badger.Txn) error {
		item, err := txn.Get(jobKey(jobID))
		if err != nil {
			return fmt.Errorf("job %s not found: %w", jobID, err)
		}

		var job Job
		err = item.Value(func(val []byte) error {
			return json.Unmarshal(val, &job)
		})
		if err != nil {
			return err
		}

		oldStatus := job.Status
		job.Status = newStatus
		job.UpdatedAt = time.Now()

		if errMsg != "" {
			job.Error = errMsg
			if newStatus == StatusFailed {
				job.Attempts++
			}
		}
		if result != nil {
			job.Result = result
		}

		data, err := json.Marshal(&job)
		if err != nil {
			return err
		}
		if err := txn.Set(jobKey(jobID), data); err != nil {
			return err
		}

		// Delete old index, write new index.
		// Index might not exist if status was already different; ignore error.
		_ = txn.Delete(indexKey(job.Type, oldStatus, job.Priority, jobID))
		return txn.Set(indexKey(job.Type, newStatus, job.Priority, jobID), nil)
	})
}

// HasContentHash returns true if a job with this content hash exists.
func (s *Store) HasContentHash(contentHash string) bool {
	err := s.db.View(func(txn *badger.Txn) error {
		_, err := txn.Get(hashKey(contentHash))
		return err
	})
	return err == nil
}

// Stats returns aggregate job counts by status and type.
func (s *Store) Stats() (*QueueStats, error) {
	stats := &QueueStats{
		ByType: make(map[JobType]TypeStats),
	}

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = []byte(prefixIdx)
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek([]byte(prefixIdx)); it.Valid(); it.Next() {
			key := string(it.Item().Key())
			parts := strings.SplitN(strings.TrimPrefix(key, prefixIdx), ":", 4)
			if len(parts) != 4 {
				continue
			}
			jobType := JobType(parts[0])
			status := JobStatus(parts[1])

			switch status {
			case StatusPending:
				stats.Pending++
			case StatusRunning:
				stats.Running++
			case StatusDone:
				stats.Done++
			case StatusFailed:
				stats.Failed++
			case StatusSkipped:
				stats.Skipped++
			}

			ts := stats.ByType[jobType]
			switch status {
			case StatusPending:
				ts.Pending++
			case StatusDone:
				ts.Done++
			case StatusFailed:
				ts.Failed++
			}
			stats.ByType[jobType] = ts
		}
		return nil
	})
	return stats, err
}

// PendingCount returns the number of pending jobs.
func (s *Store) PendingCount() int {
	count := 0
	_ = s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = []byte(prefixIdx)
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek([]byte(prefixIdx)); it.Valid(); it.Next() {
			key := string(it.Item().Key())
			parts := strings.SplitN(strings.TrimPrefix(key, prefixIdx), ":", 4)
			if len(parts) == 4 && JobStatus(parts[1]) == StatusPending {
				count++
			}
		}
		return nil
	})
	return count
}

// CountByType returns pending, done, and failed counts for a specific job type.
func (s *Store) CountByType(jobType JobType) (pending, done, failed int) {
	_ = s.db.View(func(txn *badger.Txn) error {
		prefix := []byte(prefixIdx + string(jobType) + ":")
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek(prefix); it.Valid(); it.Next() {
			key := string(it.Item().Key())
			parts := strings.SplitN(strings.TrimPrefix(key, prefixIdx), ":", 4)
			if len(parts) != 4 {
				continue
			}
			switch JobStatus(parts[1]) {
			case StatusPending:
				pending++
			case StatusDone:
				done++
			case StatusFailed:
				failed++
			}
		}
		return nil
	})
	return
}

// SaveCheckpoint persists checkpoint state.
func (s *Store) SaveCheckpoint(state *CheckpointState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(prefixCheckpoint), data)
	})
}

// LoadCheckpoint loads checkpoint state. Returns nil if not found.
func (s *Store) LoadCheckpoint() (*CheckpointState, error) {
	var state CheckpointState
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(prefixCheckpoint))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &state)
		})
	})
	if err == badger.ErrKeyNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &state, nil
}

// PurgeCompleted removes all done and skipped jobs and their index/hash keys.
func (s *Store) PurgeCompleted() error {
	// Collect keys to delete.
	type deleteSet struct {
		jobKey  []byte
		idxKey  []byte
		hashKey []byte
	}
	var toDelete []deleteSet

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = true
		opts.Prefix = []byte(prefixJob)
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek([]byte(prefixJob)); it.Valid(); it.Next() {
			var job Job
			err := it.Item().Value(func(val []byte) error {
				return json.Unmarshal(val, &job)
			})
			if err != nil {
				continue
			}
			if job.Status == StatusDone || job.Status == StatusSkipped {
				ds := deleteSet{
					jobKey: it.Item().KeyCopy(nil),
					idxKey: indexKey(job.Type, job.Status, job.Priority, job.ID),
				}
				if job.ContentHash != "" {
					ds.hashKey = hashKey(job.ContentHash)
				}
				toDelete = append(toDelete, ds)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Delete in batches.
	const batchSize = 100
	for i := 0; i < len(toDelete); i += batchSize {
		end := min(i+batchSize, len(toDelete))
		batch := toDelete[i:end]

		err := s.db.Update(func(txn *badger.Txn) error {
			for _, ds := range batch {
				_ = txn.Delete(ds.jobKey)
				_ = txn.Delete(ds.idxKey)
				if ds.hashKey != nil {
					_ = txn.Delete(ds.hashKey)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// RecoverStalled resets any jobs stuck in "running" status back to "pending".
// Called on startup to handle jobs orphaned by a crash.
func (s *Store) RecoverStalled() error {
	var stalledIDs []string

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = true
		opts.Prefix = []byte(prefixJob)
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek([]byte(prefixJob)); it.Valid(); it.Next() {
			var job Job
			err := it.Item().Value(func(val []byte) error {
				return json.Unmarshal(val, &job)
			})
			if err != nil {
				continue
			}
			if job.Status == StatusRunning {
				stalledIDs = append(stalledIDs, job.ID)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	for _, id := range stalledIDs {
		if err := s.Requeue(id); err != nil {
			return fmt.Errorf("requeue stalled job %s: %w", id, err)
		}
	}
	return nil
}

// GetJob retrieves a job by ID. Returns nil if not found.
func (s *Store) GetJob(jobID string) (*Job, error) {
	var job Job
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(jobKey(jobID))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &job)
		})
	})
	if err == badger.ErrKeyNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}
