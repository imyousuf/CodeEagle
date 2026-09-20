# Queue-Based Enrichment System with Auto-Throttle

## Overview

This spec defines a persistent, auto-throttled work queue that handles all computationally expensive enrichment during sync: document topic extraction, image LLM descriptions, face detection, face clustering, and KNN classification. It replaces the current single-threaded inline processing with a phased, resumable pipeline.

**Related spec**: [face-recognition-v2.md](face-recognition-v2.md) defines the Person domain model, KNN classifier, progressive chronological learning, and Faces review UI. This spec defines the **execution engine** that powers the face recognition pipeline described there.

### Problem Statement

- Sync processes all files sequentially — 22K images at 2-10s each means hours of blocking
- No deduplication — identical images at different paths each trigger full LLM + face detection
- No progress persistence — closing the app loses all progress
- No resource awareness — sync pegs CPU at 100%, making the machine unusable
- Face detection is a separate manual step, not integrated into sync
- No checkpoint/review flow for face clustering at scale

### Design Goals

- **Two-phase sync**: fast code/docs parsing, then queued enrichment
- **Content-hash dedup**: identical files processed once, results shared
- **Persistent queue**: resume after app restart without re-processing
- **Auto-throttle**: monitor system CPU, adjust worker count dynamically
- **Checkpoint-driven face review**: pause when 10 new clusters form, user labels in-app
- **Chronological processing**: sort images by date for progressive face learning

## 1. Queue Store (`queue.db`)

### 1.1 Storage

A dedicated BadgerDB instance at `{ConfigDir}/queue.db`, separate from graph.db, docs.db, vec.db, and face.db. Follows the same patterns: `opts.Logger = nil`, closure-based transactions, prefix scanning.

### 1.2 Key Prefixes

```
q:job:<jobID>                                    -> Job JSON
q:idx:<jobType>:<status>:<priority>:<jobID>      -> empty (scannable index)
q:meta:checkpoint                                -> checkpoint state JSON
q:meta:stats                                     -> aggregate stats JSON
```

### 1.3 Job Model

```go
type JobType string

const (
    JobDocExtract   JobType = "doc-extract"    // LLM topic extraction for documents
    JobImageDescribe JobType = "image-describe" // LLM image description
    JobFaceDetect   JobType = "face-detect"    // SSD detection + SFace embedding
    JobFaceCluster  JobType = "face-cluster"   // Re-cluster after checkpoint
)

type JobStatus string

const (
    StatusPending    JobStatus = "pending"
    StatusRunning    JobStatus = "running"
    StatusDone       JobStatus = "done"
    StatusFailed     JobStatus = "failed"
    StatusSkipped    JobStatus = "skipped"    // deduped or already processed
)

type Job struct {
    ID          string            `json:"id"`
    Type        JobType           `json:"type"`
    Status      JobStatus         `json:"status"`
    Priority    int               `json:"priority"`      // 0=highest; doc=10, image=20, face=30
    ContentHash string            `json:"content_hash"`   // sha256 for dedup
    FilePaths   []string          `json:"file_paths"`     // all paths with this content hash
    DateTaken   time.Time         `json:"date_taken"`     // EXIF/folder date (for chronological sort)
    Attempts    int               `json:"attempts"`
    MaxRetries  int               `json:"max_retries"`
    Error       string            `json:"error,omitempty"`
    Result      json.RawMessage   `json:"result,omitempty"`
    CreatedAt   time.Time         `json:"created_at"`
    UpdatedAt   time.Time         `json:"updated_at"`
}
```

### 1.4 Priority Ordering

Jobs are dequeued in priority order (lowest number first), then by `DateTaken` (oldest first within same priority):

| Priority | Type | Rationale |
|----------|------|-----------|
| 10 | `doc-extract` | Documents are fast, enable immediate search |
| 20 | `image-describe` | LLM descriptions, moderate cost |
| 30 | `face-detect` | CPU-intensive, depends on images being indexed |
| 40 | `face-cluster` | Runs after enough faces accumulate |

The index key `q:idx:<type>:<status>:<priority>:<jobID>` enables efficient prefix scans: dequeue iterates `q:idx:*:pending:` sorted by priority then date.

### 1.5 Store Interface

```go
type Store struct {
    db *badger.DB
}

func Open(dbPath string) (*Store, error)
func (s *Store) Close() error

// Core operations
func (s *Store) Enqueue(job *Job) error
func (s *Store) EnqueueBatch(jobs []*Job) error
func (s *Store) Dequeue(limit int) ([]*Job, error)          // lowest priority first
func (s *Store) Complete(jobID string, result json.RawMessage) error
func (s *Store) Fail(jobID string, errMsg string) error
func (s *Store) Skip(jobID string, reason string) error

// Dedup
func (s *Store) HasContentHash(contentHash string) bool     // check before enqueue

// Stats
func (s *Store) Stats() (*QueueStats, error)
func (s *Store) PendingCount() int
func (s *Store) CountByType(jobType JobType) (pending, done, failed int)

// Checkpoint
func (s *Store) SaveCheckpoint(state *CheckpointState) error
func (s *Store) LoadCheckpoint() (*CheckpointState, error)

// Cleanup
func (s *Store) PurgeCompleted() error                      // remove done/skipped jobs
```

### 1.6 Content-Hash Dedup at Enqueue

When populating the queue, the enqueue logic groups files by content hash:

```go
// During queue population:
hashToFiles := map[string][]string{}  // contentHash -> []filePath
for _, file := range imageFiles {
    hash := sha256File(file)
    hashToFiles[hash] = append(hashToFiles[hash], file)
}

for hash, paths := range hashToFiles {
    if store.HasContentHash(hash) {
        continue // already queued or processed
    }
    job := &Job{
        Type:        JobImageDescribe,
        ContentHash: hash,
        FilePaths:   paths,   // all paths share one processing result
        DateTaken:   extractDate(paths[0]),
        Priority:    20,
    }
    store.Enqueue(job)
}
```

This means 22K images with many duplicates might produce only 8-10K unique jobs.

## 2. Worker Pool with Auto-Throttle

### 2.1 Architecture

```go
type WorkerPool struct {
    queue       *Store
    handlers    map[JobType]Handler
    emit        EventEmitter           // Wails events
    maxWorkers  int                    // upper bound
    activeCount atomic.Int32           // current active workers
    throttler   *Throttler
    ctx         context.Context
    cancel      context.CancelFunc
    wg          sync.WaitGroup
}

type Handler interface {
    Handle(ctx context.Context, job *Job) (json.RawMessage, error)
}
```

### 2.2 Handler Registry

```go
// Registered during sync setup:
pool.Register(JobDocExtract,    &DocExtractHandler{docsProvider, docsCache})
pool.Register(JobImageDescribe, &ImageDescribeHandler{docsProvider, docsCache, maxImageRes})
pool.Register(JobFaceDetect,    &FaceDetectHandler{detector, faceStore})
pool.Register(JobFaceCluster,   &FaceClusterHandler{faceStore, similarityThreshold})
```

### 2.3 Worker Loop

```go
func (wp *WorkerPool) Run(ctx context.Context) {
    wp.ctx, wp.cancel = context.WithCancel(ctx)

    for {
        select {
        case <-wp.ctx.Done():
            wp.wg.Wait()
            return
        default:
        }

        // Check throttle — how many workers should be active?
        target := wp.throttler.TargetWorkers()
        active := int(wp.activeCount.Load())

        if active >= target {
            time.Sleep(1 * time.Second)
            continue
        }

        // Dequeue work
        jobs, err := wp.queue.Dequeue(target - active)
        if err != nil || len(jobs) == 0 {
            time.Sleep(2 * time.Second) // idle backoff
            continue
        }

        for _, job := range jobs {
            wp.wg.Add(1)
            wp.activeCount.Add(1)
            go wp.processJob(job)
        }
    }
}

func (wp *WorkerPool) processJob(job *Job) {
    defer wp.wg.Done()
    defer wp.activeCount.Add(-1)

    handler, ok := wp.handlers[job.Type]
    if !ok {
        wp.queue.Fail(job.ID, fmt.Sprintf("no handler for %s", job.Type))
        return
    }

    result, err := handler.Handle(wp.ctx, job)
    if err != nil {
        job.Attempts++
        if job.Attempts < job.MaxRetries {
            // Re-enqueue as pending (retry)
            wp.queue.Fail(job.ID, err.Error())
        } else {
            wp.queue.Fail(job.ID, fmt.Sprintf("max retries: %v", err))
        }
        return
    }

    wp.queue.Complete(job.ID, result)

    // Emit progress
    wp.emit("sync:progress", map[string]any{
        "job_type":  string(job.Type),
        "completed": job.ID,
    })

    // Check if face checkpoint is needed
    if job.Type == JobFaceDetect {
        wp.checkFaceCheckpoint()
    }
}
```

### 2.4 Auto-Throttle via CPU Monitoring

Uses `github.com/shirou/gopsutil/v4/cpu` for cross-platform CPU usage of **other processes**.

```go
type Throttler struct {
    maxWorkers    int
    minWorkers    int        // always at least 1
    sampleInterval time.Duration
    targetCPU     float64   // target: keep OTHER processes' CPU below this (default 70%)

    mu            sync.Mutex
    currentTarget int
}

func NewThrottler(maxWorkers int) *Throttler {
    if maxWorkers == 0 {
        maxWorkers = runtime.NumCPU() / 2
        if maxWorkers < 1 {
            maxWorkers = 1
        }
    }
    t := &Throttler{
        maxWorkers:     maxWorkers,
        minWorkers:     1,
        sampleInterval: 5 * time.Second,
        targetCPU:      70.0,
        currentTarget:  maxWorkers,
    }
    go t.monitorLoop()
    return t
}

func (t *Throttler) monitorLoop() {
    for {
        time.Sleep(t.sampleInterval)

        // Get per-CPU usage percentages
        percents, err := cpu.Percent(t.sampleInterval, false) // false = aggregate
        if err != nil || len(percents) == 0 {
            continue
        }
        systemCPU := percents[0]

        t.mu.Lock()
        if systemCPU > t.targetCPU {
            // System busy — reduce workers
            t.currentTarget = max(t.minWorkers, t.currentTarget-1)
        } else if systemCPU < t.targetCPU-20 {
            // System idle — increase workers
            t.currentTarget = min(t.maxWorkers, t.currentTarget+1)
        }
        // else: in sweet spot, hold steady
        t.mu.Unlock()
    }
}

func (t *Throttler) TargetWorkers() int {
    t.mu.Lock()
    defer t.mu.Unlock()
    return t.currentTarget
}
```

**Throttle behavior**:
- Sample system CPU every 5 seconds
- If system CPU > 70%: reduce active workers by 1 (min 1)
- If system CPU < 50%: increase active workers by 1 (up to max)
- Between 50-70%: hold steady
- Ramp is gradual (1 worker per sample) to avoid oscillation

## 3. Sync Pipeline Integration

### 3.1 Modified RunSync Flow

```
RunSync(ctx, cfg, repoPaths, full, logFn, warnFn)
  │
  ├─ Phase 1: Code & Docs Parsing (existing, fast, sequential)
  │   ├─ Open graph store (read-write)
  │   ├─ Auto-import from .CodeEagle.conf
  │   ├─ Build parser registry (15 language parsers)
  │   ├─ Run indexer.SyncFiles() — parse changed files → graph
  │   ├─ Run LLM summarization (if enabled + files changed)
  │   ├─ Run cross-service linker (if files changed)
  │   └─ Emit: sync:phase "Code & docs complete"
  │
  ├─ Phase 2: Queue Population (fast, I/O only)
  │   ├─ Open queue.db
  │   ├─ Scan indexed files for enrichment candidates
  │   ├─ For documents: compute content hash, enqueue doc-extract jobs
  │   ├─ For images:
  │   │   ├─ Compute content hash (dedup)
  │   │   ├─ Extract EXIF date / folder date / mtime
  │   │   ├─ Sort by date (oldest first)
  │   │   ├─ Enqueue image-describe jobs (priority 20)
  │   │   └─ Enqueue face-detect jobs (priority 30, if faces enabled)
  │   └─ Emit: sync:phase "Queued N jobs (M unique images)"
  │
  ├─ Phase 3: Queue Processing (auto-throttled workers)
  │   ├─ Start worker pool with throttler
  │   ├─ Process doc-extract jobs first (priority 10)
  │   ├─ Process image-describe jobs (priority 20)
  │   ├─ Process face-detect jobs (priority 30)
  │   ├─ Emit: sync:progress periodically
  │   ├─ On face checkpoint trigger → emit sync:checkpoint, pause
  │   ├─ Wait for user review (if checkpoint)
  │   └─ Resume after review
  │
  ├─ Phase 4: Vector Index Rebuild
  │   └─ (existing vector indexing)
  │
  └─ Phase 5: Cleanup & Stats
      ├─ Cleanup stale branches
      ├─ Purge completed queue jobs
      └─ Emit: sync:complete with stats
```

### 3.2 Queue Population Details

The population phase scans the graph for files that need enrichment:

```go
func populateQueue(ctx context.Context, queueStore *queue.Store, graphStore *embedded.BranchStore,
    cfg *config.Config, full bool, logFn func(string, ...any)) error {

    // Scan all document/image nodes from graph
    nodes, _ := graphStore.NodesByType(ctx, graph.NodeDocument)

    hashToFiles := map[string][]fileInfo{}  // content hash → file paths + metadata

    for _, node := range nodes {
        kind := node.Properties["kind"]       // "image", "text", "document", etc.
        hash := node.Properties["content_hash"]
        path := node.FilePath

        if hash == "" || kind == "" {
            continue
        }

        info := fileInfo{path: path, kind: kind, hash: hash}

        // Extract date for images
        if kind == "image" {
            info.dateTaken = extractImageDate(path)  // EXIF → folder → filename → mtime
        }

        hashToFiles[hash] = append(hashToFiles[hash], info)
    }

    // Enqueue unique jobs
    var jobs []*queue.Job
    for hash, files := range hashToFiles {
        if queueStore.HasContentHash(hash) && !full {
            continue
        }

        paths := make([]string, len(files))
        for i, f := range files { paths[i] = f.path }

        kind := files[0].kind
        switch kind {
        case "image":
            // Image description job
            jobs = append(jobs, &queue.Job{
                Type:        queue.JobImageDescribe,
                ContentHash: hash,
                FilePaths:   paths,
                DateTaken:   files[0].dateTaken,
                Priority:    20,
                MaxRetries:  3,
            })
            // Face detection job (if enabled)
            if cfg.Docs.Faces.Enabled {
                jobs = append(jobs, &queue.Job{
                    Type:        queue.JobFaceDetect,
                    ContentHash: hash,
                    FilePaths:   paths,
                    DateTaken:   files[0].dateTaken,
                    Priority:    30,
                    MaxRetries:  2,
                })
            }
        case "text", "document":
            jobs = append(jobs, &queue.Job{
                Type:        queue.JobDocExtract,
                ContentHash: hash,
                FilePaths:   paths,
                Priority:    10,
                MaxRetries:  3,
            })
        }
    }

    // Sort by priority, then by date (oldest first within same priority)
    sort.Slice(jobs, func(i, j int) bool {
        if jobs[i].Priority != jobs[j].Priority {
            return jobs[i].Priority < jobs[j].Priority
        }
        return jobs[i].DateTaken.Before(jobs[j].DateTaken)
    })

    logFn("Enqueuing %d enrichment jobs (%d unique content hashes)", len(jobs), len(hashToFiles))
    return queueStore.EnqueueBatch(jobs)
}
```

### 3.3 EXIF Date Extraction

```go
// Uses github.com/rwcarlsen/goexif (pure Go, no CGO)
// As specified in face-recognition-v2.md Section 2.4

func extractImageDate(imagePath string) time.Time {
    // Priority order (per face-recognition-v2.md Section 2.6):
    // 1. EXIF DateTimeOriginal
    // 2. Folder name date suffix (_YYYYMMDD or -YYYYMMDD)
    // 3. Filename date pattern (IMG_YYYYMMDD_*)
    // 4. File modification time
}
```

## 4. Face Detection Checkpoint System

### 4.1 Checkpoint Trigger

As discussed: **pause when 10 new face clusters with 3+ faces each form since last review**.

```go
type CheckpointState struct {
    LastReviewAt       time.Time `json:"last_review_at"`
    FacesAtLastReview  int       `json:"faces_at_last_review"`
    ClustersAtLastReview int    `json:"clusters_at_last_review"`
    ImagesProcessed    int       `json:"images_processed"`
    TotalImages        int       `json:"total_images"`
    Paused             bool      `json:"paused"`
}

func (wp *WorkerPool) checkFaceCheckpoint() {
    // Count clusters with 3+ faces since last review
    allFaces, _ := wp.faceStore.AllFaces()
    clusters := clusterFaces(allFaces, wp.similarityThreshold)

    significantNew := 0
    for _, cluster := range clusters {
        if len(cluster) >= 3 && !cluster.ReviewedBefore(wp.checkpoint.LastReviewAt) {
            significantNew++
        }
    }

    if significantNew >= 10 {
        wp.pauseForCheckpoint(clusters)
    }
}

func (wp *WorkerPool) pauseForCheckpoint(clusters []FaceCluster) {
    // Save checkpoint state
    wp.checkpoint.Paused = true
    wp.queue.SaveCheckpoint(wp.checkpoint)

    // Emit checkpoint event to frontend
    wp.emit("sync:checkpoint", map[string]any{
        "new_clusters":     len(clusters),
        "total_faces":      totalFaces,
        "images_processed": wp.checkpoint.ImagesProcessed,
        "total_images":     wp.checkpoint.TotalImages,
    })

    // Block until user resumes (via channel)
    <-wp.resumeCh
}
```

### 4.2 Checkpoint UX (Inline on Sync Page)

When a checkpoint fires, the Sync page transforms:

```
┌──────────────────────────────────────────────────────┐
│ Sync Knowledge Graph                                  │
│ 1,247 / 22,000 images · Phase 3: Face Detection      │
├──────────────────────────────────────────────────────┤
│                                                        │
│  ┌─ Checkpoint: 10 new face groups found ──────────── │
│  │                                                      │
│  │  ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐    │
│  │  │ face │ │ face │ │ face │ │ face │ │ face │    │
│  │  │ 147  │ │  89  │ │  63  │ │  41  │ │  28  │    │
│  │  │[name]│ │[name]│ │[name]│ │[name]│ │[name]│    │
│  │  │[rel ]│ │[rel ]│ │[rel ]│ │[rel ]│ │[rel ]│    │
│  │  └──────┘ └──────┘ └──────┘ └──────┘ └──────┘    │
│  │                                                      │
│  │  ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐    │
│  │  │ face │ │ face │ │ face │ │ face │ │ face │    │
│  │  │  19  │ │  12  │ │   8  │ │   5  │ │   3  │    │
│  │  │[name]│ │[name]│ │[name]│ │[name]│ │[name]│    │
│  │  │[rel ]│ │[rel ]│ │[rel ]│ │[rel ]│ │[rel ]│    │
│  │  └──────┘ └──────┘ └──────┘ └──────┘ └──────┘    │
│  │                                                      │
│  │ [Dismiss clusters with < 3 photos]                  │
│  │                                                      │
│  │ [Continue Sync]              [Stop Sync]            │
│  └──────────────────────────────────────────────────── │
│                                                        │
│ ── Log ─────────────────────────────────────────────── │
│ [21:45:03] Processing Birthday_Hamza_20240315/...      │
│ [21:45:08] Detected 4 faces in F95A5151.JPG            │
│ [21:45:09] Checkpoint: 10 new clusters, pausing...     │
└──────────────────────────────────────────────────────┘
```

Each cluster tile shows:
- Representative face thumbnail (most central embedding)
- Photo count in cluster
- Name input field (mandatory to create Person)
- Relationship dropdown (mandatory to create Person)
- Leave blank to skip / label later

**Interactions**:
- Type name + select relationship → creates Person from cluster (per [face-recognition-v2.md](face-recognition-v2.md) Section 1)
- All faces in labeled cluster become **seed exemplars** for KNN
- "Dismiss clusters with < 3 photos" — bulk dismiss noise
- "Continue Sync" — resumes queue processing; KNN now uses newly created Persons
- "Stop Sync" — saves checkpoint, stops queue; resume on next sync

### 4.3 KNN After Checkpoint

After the user labels clusters at a checkpoint, subsequent face-detect jobs use KNN classification against the labeled Persons' exemplars. This is the progressive learning loop from [face-recognition-v2.md](face-recognition-v2.md) Section 5:

```go
// In FaceDetectHandler.Handle():
func (h *FaceDetectHandler) Handle(ctx context.Context, job *Job) (json.RawMessage, error) {
    // 1. Detect faces (SSD + SFace)
    result, err := h.detector.Detect(job.FilePaths[0])

    // 2. If Persons exist with exemplars, classify via KNN
    exemplars, _ := h.personStore.AllExemplars()
    if len(exemplars) > 0 {
        for _, face := range result.Faces {
            classification := h.classifier.Classify(face.Embedding, exemplars)
            if classification.Confidence >= h.autoAcceptThreshold {
                // Auto-assign to Person
                h.personStore.AssignFaceToPerson(face, classification.PersonID,
                    classification.Confidence, "auto")
                // Add as auto-exemplar (grows model for future images)
                h.personStore.AddExemplar(classification.PersonID, &Exemplar{
                    Embedding:  face.Embedding,
                    ImagePath:  face.ImagePath,
                    DateTaken:  job.DateTaken,
                    Provenance: "auto",
                    Confidence: classification.Confidence,
                })
            }
            // Faces below threshold remain unassigned → next checkpoint
        }
    }

    // 3. Store face records
    for _, face := range result.Faces {
        h.faceStore.StoreFace(&face)
    }
    h.faceStore.MarkScanned(job.FilePaths[0])

    return json.Marshal(map[string]any{
        "faces_detected":   len(result.Faces),
        "auto_classified":  autoCount,
        "pending_review":   pendingCount,
    })
}
```

## 5. Person Domain Model

As defined in [face-recognition-v2.md](face-recognition-v2.md) Section 1, with the following refinements:

### 5.1 Person Entity

```go
type Person struct {
    ID            string    `json:"id"`             // stable unique ID (e.g., "person_hamza")
    Name          string    `json:"name"`           // MANDATORY — display name
    Relationship  string    `json:"relationship"`   // MANDATORY — from dropdown
    Email         string    `json:"email"`          // optional — for future sharing
    Aliases       []string  `json:"aliases"`        // optional — alternate names
    BirthDate     time.Time `json:"birth_date"`     // optional — for age-aware search
    Notes         string    `json:"notes"`          // optional — free-text
    SourceCluster int       `json:"source_cluster"` // cluster ID this Person was created from
    CreatedAt     time.Time `json:"created_at"`
    UpdatedAt     time.Time `json:"updated_at"`
}
```

### 5.2 Relationship Values

```go
var Relationships = []string{
    // Self
    "Self",
    // Children
    "Son", "Daughter",
    // Spouse
    "Spouse", "Ex-Spouse",
    // Parents
    "Father", "Mother",
    // Siblings
    "Brother", "Sister",
    // In-laws (spouse's family)
    "Father-in-law", "Mother-in-law",
    "Brother-in-law", "Sister-in-law",
    "Son-in-law", "Daughter-in-law",
    // Paternal family
    "Paternal Uncle", "Paternal Aunt",
    "Paternal Grandfather", "Paternal Grandmother",
    // Maternal family
    "Maternal Uncle", "Maternal Aunt",
    "Maternal Grandfather", "Maternal Grandmother",
    // Extended
    "Cousin", "Nephew", "Niece",
    "Grandson", "Granddaughter",
    // Non-family
    "Friend", "Colleague", "Neighbor",
    // Catch-all
    "Other",
}
```

### 5.3 Storage

Person data lives in the **main graph store** (BadgerDB `BranchStore`), not face.db. Key prefixes as defined in [face-recognition-v2.md](face-recognition-v2.md) Section 1.2:

```
person:<id>                              -> Person JSON
person:exemplar:<personID>:<hash>        -> Exemplar JSON
person:face:<personID>:<imagePath>:<idx> -> FaceAssignment JSON
person:idx:name:<lowercase_name>         -> personID
```

## 6. Image Metadata & Location

### 6.1 ImageMetadata

As defined in [face-recognition-v2.md](face-recognition-v2.md) Section 2.3, with location editing support:

```go
type ImageMetadata struct {
    Path         string    `json:"path"`
    ContentHash  string    `json:"content_hash"`
    DateTaken    time.Time `json:"date_taken"`
    DateSource   string    `json:"date_source"`    // "exif", "folder", "filename", "mtime"
    EventType    string    `json:"event_type"`
    EventName    string    `json:"event_name"`
    FolderName   string    `json:"folder_name"`
    CameraModel  string    `json:"camera_model"`
    Location     string    `json:"location"`       // free-text place name (user-editable)
    GPSLat       float64   `json:"gps_lat,omitempty"`
    GPSLon       float64   `json:"gps_lon,omitempty"`
    Width        int       `json:"width,omitempty"`
    Height       int       `json:"height,omitempty"`
    IndexedAt    time.Time `json:"indexed_at"`
}
```

### 6.2 Location Editing

**Per-image**: click an image in the Faces page detail view, edit the Location field.

**Bulk edit**: select images by event folder or date range, set location for all:

```go
// Backend methods bound to Wails
func (a *App) UpdateImageLocation(imagePath, location string, lat, lon float64) error
func (a *App) BulkUpdateLocation(imagePaths []string, location string, lat, lon float64) error
```

**Auto-extraction from EXIF GPS**: if EXIF contains GPS coordinates, the Location field is auto-populated via reverse geocoding. Initially store as `"lat,lon"` with the option to resolve to a place name via the LLM or a geocoding API.

**Auto-extraction from folder name**: folder names like `SanFrancisco_CityTour_20180423` hint at location. The folder parsing logic extracts candidate location strings.

## 7. Faces Page (Desktop App)

### 7.1 Tab Structure

```
Search | Ask | Sync | Faces | Settings
```

The Faces page is a first-class page for ongoing face curation. The Sync page shows inline checkpoint review (Section 4.2), while the Faces page provides the full management UI.

### 7.2 Faces Page Layout

```
┌──────────────────────────────────────────────────────┐
│ Faces    12 labeled · 3 new · 847 dismissed           │
│                                                        │
│ ── People ────────────────────────────────────────── │
│ ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐       │
│ │ face │ │ face │ │ face │ │ face │ │ face │       │
│ │      │ │      │ │      │ │      │ │      │       │
│ │ Hamza│ │ Mahdi│ │ Sarah│ │ Mom  │ │ Dad  │       │
│ │ Son  │ │ Son  │ │Daugh.│ │Mother│ │Father│       │
│ │ 347  │ │ 201  │ │ 156  │ │ 89   │ │  82  │       │
│ └──────┘ └──────┘ └──────┘ └──────┘ └──────┘       │
│                                                        │
│ ── New (unlabeled clusters, 3+ faces) ────────────── │
│ ┌──────┐ ┌──────┐ ┌──────┐                           │
│ │  ?   │ │  ?   │ │  ?   │                           │
│ │  24  │ │  11  │ │   7  │                           │
│ │[name]│ │[name]│ │[name]│                           │
│ │[rel ]│ │[rel ]│ │[rel ]│                           │
│ └──────┘ └──────┘ └──────┘                           │
│                                                        │
│ [Dismiss clusters with < 3 photos]                    │
│                                                        │
│ ── Pending Review (auto-classified, needs verify) ── │
│ (faces auto-assigned by KNN, sorted by confidence)    │
│                                                        │
│ ── Stats ─────────────────────────────────────────── │
│ 21,900 images indexed · 4,271 faces · 10 persons     │
│ Assigned: 3,380 (79%) · Pending: 358 · Unassigned: 533│
└──────────────────────────────────────────────────────┘
```

### 7.3 Person Detail View

Clicking a Person tile opens a detail view:

- **Face timeline**: all face thumbnails sorted by date (shows age progression)
- **Provenance badges**: seed (green), auto (blue), manual (yellow)
- **Edit Person**: name, relationship, email, birth date, notes
- **Image list**: all images this person appears in, with location editing
- **Bulk location edit**: select images by event, set location for all

### 7.4 Backend Handlers

```go
// internal/app/faces_handlers.go (//go:build app && faces)

func (a *App) GetPersons() ([]PersonInfo, error)
func (a *App) CreatePerson(name, relationship string) (*Person, error)
func (a *App) CreatePersonFromCluster(clusterID int, name, relationship string) (*Person, error)
func (a *App) UpdatePerson(id string, updates PersonUpdate) error
func (a *App) DeletePerson(id string) error
func (a *App) GetFaceClusters() ([]ClusterInfo, error)
func (a *App) GetFaceThumbnail(imagePath string, faceIdx int) ([]byte, error)
func (a *App) DismissSmallClusters(minSize int) error
func (a *App) GetPendingReviewFaces() ([]FaceReviewItem, error)
func (a *App) AssignFaceToPerson(imagePath string, faceIdx int, personID string) error
func (a *App) GetFaceStats() (*FaceStats, error)
func (a *App) ResumeSync() error  // unblocks checkpoint
```

## 8. System Notifications

Desktop notifications via Wails `runtime.MessageDialog` or OS-level notifications:

| Event | Notification |
|-------|-------------|
| Checkpoint reached | "CodeEagle: 10 new face groups found — review to continue sync" |
| Sync complete | "CodeEagle: Sync complete — 1,247 images processed, 89 faces auto-classified" |
| Sync error | "CodeEagle: Sync paused — face detection error" |
| Queue idle | "CodeEagle: All enrichment jobs complete" |

Notifications fire even when the app is minimized/backgrounded.

```go
// In sync_handlers.go, when checkpoint fires:
a.emit("sync:checkpoint", checkpointData)

// Also send OS notification
a.emit("notification:show", map[string]string{
    "title": "CodeEagle",
    "body":  fmt.Sprintf("%d new face groups found — review to continue sync", newClusters),
})
```

Frontend listens for `notification:show` and calls `Notification.requestPermission()` + `new Notification(...)`.

## 9. Config Changes

### 9.1 New Config Section

```yaml
docs:
  faces:
    enabled: true
    model_dir: ~/.codeeagle/models/
    min_face_size: 40
    similarity_threshold: 0.30
    confidence_threshold: 0.7
    # New fields:
    checkpoint_clusters: 10          # pause after N new clusters with 3+ faces
    auto_accept_threshold: 0.55      # KNN confidence for auto-assign
    reject_threshold: 0.30           # below this → unassigned
    classify_k: 7                    # KNN K parameter
    max_exemplars_per_event: 10      # cap auto-exemplars per Person per event

queue:
  max_workers: 0                     # 0 = auto-detect (NumCPU / 2)
  target_cpu: 70                     # throttle when system CPU > this %
  retry_attempts: 3
```

### 9.2 Config Struct Additions

```go
// In internal/config/config.go

type FacesConfig struct {
    // ... existing fields ...
    CheckpointClusters     int     `yaml:"checkpoint_clusters"`
    AutoAcceptThreshold    float64 `yaml:"auto_accept_threshold"`
    RejectThreshold        float64 `yaml:"reject_threshold"`
    ClassifyK              int     `yaml:"classify_k"`
    MaxExemplarsPerEvent   int     `yaml:"max_exemplars_per_event"`
}

type QueueConfig struct {
    MaxWorkers    int `yaml:"max_workers"`
    TargetCPU     int `yaml:"target_cpu"`
    RetryAttempts int `yaml:"retry_attempts"`
}

type Config struct {
    // ... existing fields ...
    Queue QueueConfig `mapstructure:"queue" yaml:"queue"`
}
```

## 10. CLI Changes

### 10.1 Commands Dropped

Per discussion, these are replaced by the desktop app UI:
- ~~`codeeagle faces bootstrap`~~ — progressive learning happens automatically during sync
- ~~`codeeagle faces review`~~ — replaced by Faces page in desktop app

### 10.2 Commands Retained

```bash
codeeagle faces scan [dirs...]        # standalone face detection (no queue)
codeeagle faces clusters              # list clusters
codeeagle faces search <name>         # find images by person
codeeagle faces person add <name> --relationship <rel>  # CLI person creation
codeeagle faces person list
codeeagle faces merge <id1> <id2>     # merge clusters
codeeagle faces suggest               # suggest merges
```

### 10.3 New Commands

```bash
codeeagle queue status                # show queue stats (pending/done/failed by type)
codeeagle queue purge                 # remove completed jobs
```

## 11. Frontend Events

### 11.1 Sync Events (Extended)

```typescript
// Existing
sync:log         → { message: string }
sync:complete    → {}
sync:error       → { message: string }

// New
sync:phase       → { phase: string, detail: string }
sync:progress    → { type: string, processed: number, total: number, current: string }
sync:checkpoint  → { new_clusters: number, total_faces: number, images_processed: number, total_images: number }
sync:resumed     → {}

// Notifications
notification:show → { title: string, body: string }
```

### 11.2 Frontend Types

```typescript
// In types.ts
interface CheckpointData {
  new_clusters: number;
  total_faces: number;
  images_processed: number;
  total_images: number;
  clusters: ClusterInfo[];
}

interface ClusterInfo {
  id: number;
  face_count: number;
  thumbnail: string;    // base64 JPEG of representative face
  sample_images: string[];
}

interface PersonInfo {
  id: string;
  name: string;
  relationship: string;
  email: string;
  face_count: number;
  thumbnail: string;
}

interface FaceStats {
  images_indexed: number;
  faces_detected: number;
  persons: number;
  assigned: number;
  pending_review: number;
  unassigned: number;
}
```

## 12. New Dependencies

| Package | Purpose | License |
|---------|---------|---------|
| `github.com/shirou/gopsutil/v4` | Cross-platform CPU monitoring | BSD-3 |
| `github.com/rwcarlsen/goexif` | EXIF metadata extraction (pure Go) | BSD-2 |

Both are pure Go (no CGO), cross-platform, and permissively licensed.

## 13. Files Summary

### New Files

| File | Purpose |
|------|---------|
| `internal/queue/store.go` | Queue BadgerDB store (Job, Enqueue, Dequeue, Stats) |
| `internal/queue/store_test.go` | Queue store tests |
| `internal/queue/worker.go` | WorkerPool, Handler interface, job processing loop |
| `internal/queue/worker_test.go` | Worker pool tests |
| `internal/queue/throttle.go` | CPU monitoring, dynamic worker count adjustment |
| `internal/queue/throttle_test.go` | Throttle tests |
| `internal/queue/handlers.go` | DocExtractHandler, ImageDescribeHandler, FaceDetectHandler |
| `internal/queue/checkpoint.go` | CheckpointState, face cluster trigger logic |
| `internal/faces/person.go` | Person struct, Exemplar, FaceAssignment (from face-recognition-v2) |
| `internal/faces/classifier.go` | KNN classifier (from face-recognition-v2 Section 4) |
| `internal/faces/metadata.go` | EXIF extraction, folder/filename date parsing |
| `internal/app/faces_handlers.go` | Wails-bound face management methods (build tag: app && faces) |
| `frontend/src/pages/Faces.tsx` | Faces page — person grid, clusters, review |
| `frontend/src/hooks/useFaces.ts` | React hook for face data and events |

### Modified Files

| File | Change |
|------|--------|
| `internal/cli/sync.go` | Add Phase 2 (queue population) and Phase 3 (queue processing) to RunSync |
| `internal/config/config.go` | Add QueueConfig, extend FacesConfig with checkpoint/KNN fields |
| `internal/app/app.go` | Add queue/face store fields, ResumeSync method |
| `internal/app/sync_handlers.go` | Checkpoint pause/resume logic, face detection integration |
| `frontend/src/App.tsx` | Add /faces route |
| `frontend/src/components/NavBar.tsx` | Add Faces nav link |
| `frontend/src/types.ts` | Add checkpoint, cluster, person, face types |
| `frontend/src/pages/Sync.tsx` | Add checkpoint inline review panel |
| `frontend/src/hooks/useSync.ts` | Add checkpoint event handling, resume callback |
| `go.mod` | Add gopsutil, goexif dependencies |

## 14. Implementation Order

### Phase 1: Queue Foundation
1. `internal/queue/store.go` — BadgerDB queue with enqueue/dequeue/stats
2. `internal/queue/worker.go` — Worker pool with handler registry
3. `internal/queue/throttle.go` — CPU monitoring and dynamic throttle
4. Unit tests for all three

### Phase 2: Handlers + Pipeline Integration
5. `internal/queue/handlers.go` — Doc/Image/Face handlers
6. `internal/faces/metadata.go` — EXIF + folder date extraction
7. Modify `internal/cli/sync.go` — integrate queue population + processing into RunSync
8. `internal/config/config.go` — QueueConfig additions

### Phase 3: Person Domain + KNN
9. `internal/faces/person.go` — Person entity, store methods
10. `internal/faces/classifier.go` — KNN classifier
11. `internal/queue/checkpoint.go` — cluster trigger, pause/resume

### Phase 4: Desktop App UI
12. `internal/app/faces_handlers.go` — Wails-bound methods
13. `frontend/src/pages/Faces.tsx` — full Faces page
14. `frontend/src/pages/Sync.tsx` — checkpoint inline panel
15. System notifications

## 15. Verification

```bash
# 1. Unit tests
make test-fast

# 2. Queue store test
go test ./internal/queue/ -v

# 3. Manual sync with images
codeeagle sync --full -v
# Expected: "Enqueuing 8,432 enrichment jobs (8,432 unique of 22,000 files)"
# Expected: "Phase 3: Processing... [auto-throttle: 4 workers, CPU 52%]"
# Expected: "Checkpoint: 10 new face groups, pausing..."

# 4. Desktop app
codeeagle app
# Navigate to Sync → start sync → checkpoint appears inline
# Navigate to Faces → see labeled persons, unlabeled clusters
# Bulk edit location on images

# 5. Resume after restart
codeeagle app
# Sync resumes from checkpoint without re-processing completed jobs

# 6. CLI queue status
codeeagle queue status
# Expected: "face-detect: 234 pending, 8198 done, 0 failed"
```
