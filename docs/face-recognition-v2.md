# Face Recognition v2: Person-Centric Recognition, Scene Understanding & Progressive Learning

## Overview

A comprehensive image understanding and search system that:

1. **Scan-first workflow** — scan all photos, cluster faces, then create Persons from clusters via web UI
2. **Person as first-class entity** — stable identity with bio info (birth date, relationship); clusters and faces map to Person
3. **Rich searchable index** — date, person, objects (OpenCV), LLM-generated descriptions, topics all indexed for fast image search
4. **Progressive chronological learning** — process photos oldest-first, auto-grow training data, review after each run
5. **Metadata extraction** — EXIF, folder names, filenames for searchability and temporal ordering
6. **Sync integration** — image analysis as part of the sync pipeline with status tracking

### Problem Statement

The current pipeline detects faces and clusters them, but:
- Clusters are identified by integer IDs with optional string labels — there's no persistent "Person" identity
- Cluster corrections don't improve future results — merging/splitting/labeling is discarded on re-cluster
- No metadata is extracted — folder names like `Birthday_Hamza_20240315` contain rich data that's ignored
- No scene/object understanding — a photo at the beach with a guitar has no searchable attributes beyond filename
- CLI-only review is impractical — users must manually open images to verify identity
- Kids' faces change dramatically over years — a child at ages 3, 8, and 15 produces separate clusters each time
- Face scan is a separate manual step — not integrated into the sync pipeline

### Design Goals

- **Scan first, organize later** — scan all photos and cluster; user creates Persons from clusters in the web UI
- **Person is the anchor** — all face assignments, exemplars, and photo appearances flow through Person
- **Everything searchable** — date, people, objects, scene, location, event type all indexed for "find me photos of..."
- **Progressive learning** — cluster → create Persons → bootstrap through time → review → iterate
- **Human-in-the-loop** — every bootstrap run shows auto-accepted results for correction before proceeding
- **Sync-integrated** — face/object/description analysis runs as part of sync; `status` shows unassigned face count
- **Portable workflow** — any user starting fresh goes through the same cluster-first process

## 1. Person Domain Entity

### 1.1 Data Model

```go
// Person represents a known individual in the photo library.
type Person struct {
    ID           string    `json:"id"`            // stable unique ID (e.g., "person_hamza")
    Name         string    `json:"name"`          // display name (e.g., "Hamza")
    Aliases      []string  `json:"aliases"`       // alternate names / nicknames
    BirthDate    time.Time `json:"birth_date"`    // for age-aware search ("photos of Hamza at age 5")
    Relationship string    `json:"relationship"`  // "child", "spouse", "parent", "in-law", "friend", etc.
    Notes        string    `json:"notes"`         // free-text notes
    SourceCluster int      `json:"source_cluster"`// cluster ID this Person was created from (0 = manual)
    CreatedAt    time.Time `json:"created_at"`
    UpdatedAt    time.Time `json:"updated_at"`
}
```

Persons are typically created from clusters in the review web UI after the initial scan, not upfront via CLI. The CLI `person add` command is available for advanced users or scripting.

### 1.2 Storage (Main Graph Store)

Person data lives in the main graph store (BadgerDB `BranchStore`), not a separate face.db. This avoids dual-store sync issues and lets Persons participate in the knowledge graph natively.

Key prefixes (alongside existing `n:`, `e:`, `idx:` prefixes):

```
person:<id>                              → Person JSON
person:exemplar:<personID>:<hash>        → Exemplar JSON (seed + auto)
person:face:<personID>:<imagePath>:<idx> → face assignment record
person:idx:name:<lowercase_name>         → personID (name lookup index)
```

The existing `face.db` store (`internal/faces/store.go`) remains for face detection data (FaceRecord, BBox, embeddings, clusters). Person/Exemplar/Assignment data goes in the graph store to keep identity management centralized.

### 1.3 Store Methods

```go
func (s *Store) CreatePerson(p *Person) error
func (s *Store) GetPerson(id string) (*Person, error)
func (s *Store) GetPersonByName(name string) (*Person, error)
func (s *Store) ListPersons() ([]*Person, error)
func (s *Store) UpdatePerson(p *Person) error
func (s *Store) DeletePerson(id string) error

// Exemplar management (always via Person)
func (s *Store) AddExemplar(personID string, ex *Exemplar) error
func (s *Store) ExemplarsForPerson(personID string) ([]Exemplar, error)
func (s *Store) AllExemplars() (map[string][]Exemplar, error)  // personID → exemplars
func (s *Store) PurgeAutoExemplars(personID string) error
func (s *Store) PurgeAutoExemplarsAfter(personID string, after time.Time) error
func (s *Store) ResetToSeedExemplars() error

// Face-to-Person assignment
func (s *Store) AssignFaceToPerson(imagePath string, faceIdx int, personID string, confidence float32, provenance string) error
func (s *Store) FacesForPerson(personID string) ([]FaceAssignment, error)
func (s *Store) PersonForFace(imagePath string, faceIdx int) (string, error)  // returns personID
```

### 1.4 Exemplar (always linked to Person)

```go
type Exemplar struct {
    PersonID    string    `json:"person_id"`
    Embedding   []float32 `json:"embedding"`
    ImagePath   string    `json:"image_path"`
    FaceIdx     int       `json:"face_idx"`
    DateTaken   time.Time `json:"date_taken"`
    Provenance  string    `json:"provenance"`   // "seed" or "auto"
    SourceEvent string    `json:"source_event"` // event folder name (for auto)
    Confidence  float32   `json:"confidence"`   // classification confidence at acceptance
    AcceptedAt  time.Time `json:"accepted_at"`  // when added to exemplar pool
    Hash        string    `json:"hash"`         // SHA256 of embedding bytes (dedup key)
}
```

### 1.5 Face Assignment Record

```go
type FaceAssignment struct {
    ImagePath   string    `json:"image_path"`
    FaceIdx     int       `json:"face_idx"`
    PersonID    string    `json:"person_id"`
    Confidence  float32   `json:"confidence"`
    Provenance  string    `json:"provenance"`   // "seed", "auto", "manual"
    AssignedAt  time.Time `json:"assigned_at"`
    EventName   string    `json:"event_name"`
}
```

### 1.6 Relationship to Existing Structures

```
Person ──1:N──> Exemplar       (training data for KNN)
Person ──1:N──> FaceAssignment (which photos this person appears in)
Person ──1:1──> NodePerson     (knowledge graph node, for RAG/query integration)

FaceRecord (existing) ──> still stores detection data (bbox, embedding, cluster)
FaceAssignment (new)  ──> maps detected faces to Person identity
```

The existing `FaceRecord` and cluster system remain for initial unsupervised detection. Once faces are assigned to Persons (manually or via KNN), the Person is the authoritative identity.

## 2. Date-Ordered Image Index

### 2.1 Purpose

The image index is the foundation layer. Before any face detection runs, all images are indexed by date. This enables:
- Chronological bootstrap (process oldest-first)
- Temporal context for face classification
- Event-based browsing and search
- Tracking which images have been processed

### 2.2 Image Index Store (Main Graph Store)

Key prefixes (in the main graph store, alongside Person data):

```
img:meta:<imagePath>     → ImageMetadata JSON
img:idx:date:<YYYYMMDD>:<imagePath> → empty value (date index for range scans)
img:idx:event:<eventName>:<imagePath> → empty value (event index)
img:scanned:<imagePath>  → timestamp of last face scan
```

### 2.3 ImageMetadata

```go
type ImageMetadata struct {
    Path         string    `json:"path"`
    DateTaken    time.Time `json:"date_taken"`
    DateSource   string    `json:"date_source"`   // "exif", "folder", "filename", "mtime"
    EventType    string    `json:"event_type"`     // "Birthday", "Concert", "Family", etc.
    EventName    string    `json:"event_name"`     // full description
    FolderName   string    `json:"folder_name"`    // raw parent folder name
    CameraModel  string    `json:"camera_model"`
    GPSLat       float64   `json:"gps_lat,omitempty"`
    GPSLon       float64   `json:"gps_lon,omitempty"`
    Width        int       `json:"width,omitempty"`
    Height       int       `json:"height,omitempty"`
    IndexedAt    time.Time `json:"indexed_at"`
}
```

### 2.4 EXIF Extraction

**Library**: `github.com/rwcarlsen/goexif` (pure Go, no CGO)

| Field | EXIF Tag | Use |
|-------|----------|-----|
| DateTaken | DateTimeOriginal (36867) | Primary timestamp |
| CameraMake | Make (271) | Camera identification |
| CameraModel | Model (272) | Camera identification |
| LensModel | LensModel (42036) | Lens info |
| GPSLat/GPSLon | GPSLatitude/GPSLongitude | Location |
| Orientation | Orientation (274) | Rotation correction before detection |
| ImageWidth/Height | PixelXDimension/PixelYDimension | Original dimensions |

For non-JPEG formats (PNG, BMP, WebP) that lack EXIF: fall back to folder/filename/mtime.

### 2.5 Folder & Filename Parsing

The photo library uses `Event_Description_YYYYMMDD` folder naming:

```
Birthday_Hamza_20240315/
Family_CapeCod_20220528/
Concert_MahdiCMS_20241205/
SanFrancisco_CityTour_20180423/
```

**Extraction rules**:

1. **Date suffix**: Match trailing `_YYYYMMDD` or `-YYYYMMDD` → event date. Also `_YYYYMM` for month-only.
2. **Event type**: First segment before `_` → event category (Birthday, Concert, Family, Travel, etc.)
3. **Description**: Middle segments between event type and date.
4. **People hints**: Match known person names in folder segments (e.g., `Birthday_Hamza_20240315` → Hamza likely present).

**Filename patterns**: `IMG_20240315_200535.jpg` → extract `YYYYMMDD` as fallback when EXIF unavailable.

### 2.6 Date Priority

1. EXIF `DateTimeOriginal` (most accurate)
2. Folder name date suffix (`_YYYYMMDD`)
3. Filename date pattern (`IMG_YYYYMMDD_*`)
4. File modification time (least accurate)

### 2.7 Store Methods

```go
func (s *Store) IndexImage(meta *ImageMetadata) error
func (s *Store) GetImageMetadata(imagePath string) (*ImageMetadata, error)
func (s *Store) ImagesInDateRange(start, end time.Time) ([]ImageMetadata, error)
func (s *Store) ImagesForEvent(eventName string) ([]ImageMetadata, error)
func (s *Store) UnscannedImages() ([]ImageMetadata, error)
func (s *Store) MarkScanned(imagePath string) error
func (s *Store) ImageCount() (int, error)
func (s *Store) DateRange() (oldest, newest time.Time, err error)
```

## 3. Scene Understanding & Image Search (Existing + Enhancements)

### 3.1 What Already Exists

CodeEagle already has comprehensive image understanding integrated into the sync pipeline:

**LLM Image Description** (`internal/parser/generic/parser.go` + `internal/docs/`):
- Generic parser classifies images (PNG, JPG, GIF, WebP, BMP, TIFF) and sends them to multimodal LLM
- Images downscaled to configurable max resolution (default 1024px), re-encoded as JPEG
- LLM extracts structured `ExtractionResult`: summary (2-3 sentences), topics (keywords), entities (visible text/names)
- Results cached by content hash in BadgerDB (`docs.db/`) to avoid redundant LLM calls
- Two LLM providers: **Ollama** (local, e.g., `qwen3.5:9b`) and **Vertex AI** (Gemini)
- Auto-detection: explicit config → local Ollama → Vertex AI → disabled

**Knowledge Graph Integration**:
- Images indexed as `NodeDocument` with `kind: "image"` property
- Summary stored in `DocComment`, MIME type in properties
- Extracted topics create `NodeTopic` nodes with `EdgeHasTopic` edges
- Already searchable via `codeeagle rag "photos of..."` and vector search (HNSW)

**Face Detection** (`internal/faces/`, `//go:build faces`):
- Multi-scale SSD face detection + SFace 128-dim embeddings
- Agglomerative clustering with cosine similarity
- Creates `NodePerson` + `EdgeAppearsIn` edges in knowledge graph
- Config flag `object_detection` exists but not fully implemented

### 3.2 Enhancements for Image Search

The existing infrastructure covers LLM description and topic extraction. The face recognition v2 enhancements add:

1. **Person-to-image linkage via KNN** — auto-creates `NodePerson → EdgeAppearsIn → NodeDocument` edges when faces are classified (not just when manually labeled)

2. **Metadata enrichment** — date, event type, event name (from EXIF + folder parsing) added to `NodeDocument` properties, making images findable by event/date

3. **Combined search** — face search results enriched with LLM-extracted scene context:

```bash
# Existing (already works)
codeeagle rag "photos at the beach"              # via LLM topics/description
codeeagle rag "birthday party photos"             # via LLM topics

# Enhanced (after face recognition v2)
codeeagle rag "photos of Hamza at the beach"      # Person + LLM topics
codeeagle rag "Hamza's birthday 2024"             # Person + date + event type
codeeagle faces search "Hamza" --after 2023-01-01 # Person + date filter
codeeagle faces search "Hamza" --event birthday   # Person + event type
```

4. **Status tracking** — `codeeagle status` shows unassigned face count alongside index stats:

```
Face scan: 21,900 images, 4,271 faces detected
  Assigned to Person: 3,380 (79.1%)
  Pending review:       358 (8.4%)
  Unassigned:           533 (12.5%)
  Persons: 10
```

### 3.3 LLM Description Prompt Enhancement

Update the image description prompt (`internal/docs/prompts.go`) to also extract scene and activity when available:

```json
{
  "summary": "2-3 sentence description",
  "topics": ["birthday", "party", "children", "cake", "indoor"],
  "entities": ["visible names, labels, identifiers"],
  "scene": "indoor",
  "activity": "birthday celebration"
}
```

The `scene` and `activity` fields are stored as additional properties on the `NodeDocument` node, making them searchable via RAG.

### 3.4 Object Detection

Deferred. The existing LLM topic extraction already covers most object-level search ("cake", "guitar", "car" appear as topics). A dedicated OpenCV DNN object detector (MobileNet-SSD, COCO 80 classes) would add bounding boxes and higher accuracy but is not needed until LLM topics prove insufficient.

## 4. KNN Classifier

### 4.1 Why KNN Over SVM

| Criterion | KNN | SVM |
|-----------|-----|-----|
| Training time | Zero (just store exemplars) | Requires fitting, kernel selection |
| Incremental updates | Add/remove exemplars instantly | Must retrain on each correction |
| Handles age variation | Yes — matches nearest exemplar across age range | Struggles with non-linear age boundaries |
| Implementation complexity | ~100 lines of Go | Requires cgo/libsvm or complex pure-Go impl |
| Accuracy for <50 classes | Equivalent to SVM with good embeddings | Marginal improvement not worth complexity |
| Confidence/rejection | Natural — distance to K-th neighbor | Requires Platt scaling for probabilities |

**Decision**: KNN. The SFace embeddings are the bottleneck (no alignment → noisy), not the classifier. More exemplars (from progressive learning) help more than a fancier classifier.

### 4.2 Algorithm

```
ClassifyFace(newEmbedding, exemplars, K=7, threshold=0.35):
  1. Compute cosine similarity between newEmbedding and ALL exemplars
  2. Sort by similarity, take top K
  3. same_photo_filter: exclude exemplars from the same image
  4. Majority vote among top K:
     - If majority label has >= ceil(K/2) votes AND
       average similarity of majority label's exemplars >= threshold:
       → classify as majority label, confidence = average similarity
     - Otherwise:
       → classify as "unknown", queue for review
  5. Return (personID, confidence, topK_details)
```

**Why K=7**: With ~30 people and hundreds of exemplars per person, K=7 provides robust voting without outlier domination. Odd K avoids ties.

**Why threshold=0.35**: Above clustering threshold (0.30) to avoid false positives. Better to leave ambiguous faces as "unknown" for review than misclassify.

### 4.3 The "Growing Up" Problem

Kids' faces change dramatically. KNN with all exemplars naturally handles this:

- After progressive learning, exemplars span ages 3 through 15
- A new photo at age 10 matches exemplars from ages 8-12
- KNN finds nearest exemplars across the entire age range
- No single centroid averaging disparate ages

**Temporal weighting** (optional):

```
temporal_weight(exemplar_date, query_date) = 1.0 / (1.0 + years_between * 0.1)
weighted_similarity = cosine_similarity * temporal_weight
```

Soft 10%/year decay — old exemplars still contribute but recent ones count more.

### 4.4 Classifier Data Model

```go
type Classifier struct {
    Exemplars []Exemplar // all labeled exemplars from all Persons
}

type Classification struct {
    PersonID   string    // Person.ID or "unknown"
    PersonName string    // Person.Name (for display)
    Confidence float32   // average similarity of majority vote
    TopK       []Match   // top K matches for review/debugging
}

type Match struct {
    PersonID   string
    PersonName string
    Similarity float32
    ImagePath  string
    DateTaken  time.Time
}
```

## 5. Progressive Chronological Learning

### 5.1 Core Workflow (Cluster-First)

This is the primary workflow — every user goes through this process. The key insight: **scan and cluster first, then create Persons from clusters**, not the other way around.

```
Step 1: Scan & cluster all photos
  codeeagle faces scan ~/Pictures/ImageServer/
  → Detects faces in all images
  → Extracts EXIF + folder metadata
  → Runs unsupervised clustering
  → Output: "Scanned 21,900 images, 4,271 faces detected, 142 clusters formed"

Step 2: Review clusters & create Persons (web UI)
  codeeagle faces review
  → Opens web UI showing face clusters sorted by date
  → User creates Persons from clusters:
     - Click cluster → "Create Person" → enter name, birth date, relationship
     - Drag faces between clusters to fix grouping
     - Merge clusters that belong to same person
  → All faces in a Person's cluster become seed exemplars
  → Save & Exit commits Persons + seed exemplars

Step 3: Progressive bootstrap
  codeeagle faces bootstrap --auto-accept 0.55 --dry-run
  → Processes events oldest-first using seed exemplars from Step 2
  → Shows what WOULD be auto-accepted

  codeeagle faces bootstrap --auto-accept 0.55
  → Actually runs, shows per-event results
  → After completion, shows summary of all auto-accepted mappings

Step 4: Review auto-accepted results
  codeeagle faces review
  → Shows auto-accepted face→Person mappings
  → User corrects mistakes → updates exemplar pool
  → System warns about cascading effects if needed

Step 5: Iterate
  → Run bootstrap again with corrected exemplars
  → New photos arriving via sync are auto-classified

Step 6: Ongoing (sync integration)
  codeeagle status
  → Shows: "Faces: 4,271 total, 3,380 assigned, 358 pending review, 533 unassigned"
  → User runs `faces review` when pending count grows
```

### 5.2 Chronological Bootstrap Algorithm

```
ChronologicalBootstrap(persons, imageIndex, config):
  1. Load all exemplars for all Persons → exemplarPool
  2. Group images by event (folder), sort events by date
  3. For each event in chronological order:
     a. Detect faces in all images of this event
     b. Classify each face using KNN against exemplarPool
     c. Partition results into three buckets:
        - accepted:  confidence >= auto_accept_threshold (default 0.55)
        - review:    confidence in [reject_threshold, auto_accept_threshold)
        - rejected:  confidence < reject_threshold (default 0.30) or "unknown"
     d. Apply same-photo constraint:
        - If two faces in same image classified as same Person,
          keep higher-confidence one, demote other to review
     e. Cap auto-accepted exemplars per Person per event:
        - Max N (default 10) per Person per event
        - Keep highest-confidence faces
     f. For each accepted face:
        - Add to exemplarPool as Exemplar with provenance="auto"
        - Create FaceAssignment linking face to Person
     g. Log event summary
  4. Return: all results (accepted/review/rejected per event)
```

### 5.3 Post-Bootstrap Review Report

After every bootstrap run, display a clear summary for human verification:

```
=== Bootstrap Complete ===

Events processed: 139
Total faces detected: 4,271
  Auto-accepted:  2,847 (66.7%)
  Pending review:   891 (20.9%)
  Rejected:         533 (12.5%)

Per-Person breakdown:
  Person          Seed   Auto    Total   Avg Confidence
  ─────────────────────────────────────────────────────
  Hamza            23     185      208     0.612
  Mahdi            20     164      184     0.589
  Imran            12      98      110     0.634
  Sarah             8      72       80     0.601
  Anusha           10      89       99     0.592
  Mom              15      67       82     0.645
  Dad              14      73       87     0.651
  Rocky             5      41       46     0.578

Confidence trend (Hamza):
  2008-2012: avg 0.58 (12 events)
  2013-2016: avg 0.61 (18 events)
  2017-2020: avg 0.62 (25 events)
  2021-2024: avg 0.64 (15 events)

⚠ Review recommended: run `codeeagle faces review` to verify auto-accepted mappings
```

### 5.4 Correction Feedback Loop

When the user corrects an auto-accepted mapping in the review UI:

1. **Remove wrong assignment**: Delete FaceAssignment and Exemplar from wrong Person
2. **Add correct assignment**: Create FaceAssignment and Exemplar on correct Person (provenance="manual")
3. **Cascading fix**: Optionally re-run bootstrap from the corrected event forward, since downstream auto-accepted exemplars may have been influenced by the wrong one

```
codeeagle faces review
  → User moves face from "Hamza" to "Mahdi"
  → System removes Exemplar from Hamza, adds to Mahdi
  → System warns: "3 subsequent auto-accepted faces may be affected. Re-run bootstrap? [y/n]"
```

### 5.5 Safeguards Against Error Propagation

| Safeguard | Mechanism |
|-----------|-----------|
| High auto-accept threshold | Only confidence >= 0.55 becomes exemplar (vs 0.35 for classification) |
| Same-photo constraint | Can't auto-accept two faces as same Person in one image |
| Per-event cap | Max 10 auto-exemplars per Person per event prevents flooding |
| Majority vote (K=7) | A few bad exemplars outvoted by many good ones |
| Provenance tracking | Auto-exemplars can be purged and re-generated |
| Post-run review | Every run shows results for human verification |
| Confidence decay monitoring | If avg confidence drops across events, warn and suggest review |

### 5.6 Error Recovery

```bash
# Purge all auto-exemplars and re-run with tighter threshold
codeeagle faces bootstrap --purge-auto --auto-accept 0.60

# Purge auto-exemplars from specific date range
codeeagle faces bootstrap --purge-auto --after 2023-01-01

# Full reset to seed exemplars only
codeeagle faces bootstrap --reset-to-seed

# Purge auto-exemplars for a specific person
codeeagle faces bootstrap --purge-auto --person hamza
```

### 5.7 Config

```yaml
docs:
  faces:
    bootstrap:
      auto_accept_threshold: 0.55
      reject_threshold: 0.30
      max_exemplars_per_person_per_event: 10
      checkpoint_interval: 0          # pause for review every N events (0 = no pause)
      confidence_decay_warning: 0.10  # warn if avg confidence drops by this much
```

### 5.8 Validation (demonstrated)

Test results from prototype tool on real family photos:

**1-year gap (2021 training → 2022 XMas):**
- Hamza: 8/8 correct (avg confidence 0.631)
- Mahdi: 8/8 correct (avg confidence 0.604)

**3-year gap (2021 training → 2024 birthdays):**
- Hamza: 11 classified, confidence 0.435-0.628
- Mahdi: 5 classified, 4 unknown — gap too large for direct classification

**Chronological bootstrap across 15 events (2018-2024):**
- Pool grew from 88 seed → 196 total exemplars
- Hamza: 23 seed → 71 total (48 auto-accepted)
- Mahdi: 20 seed → 80 total (60 auto-accepted)
- Successfully bridged the 3-year gap through intermediate years

## 6. Review Web UI

### 6.1 Architecture

`codeeagle faces review` launches a native desktop app built with **Wails** (Go backend + web frontend):

```
┌──────────────────────────────────────────────────────────┐
│ codeeagle faces review                                    │
│                                                           │
│  Wails desktop app (native window, system webview)       │
│  ├── Go backend (bound methods, called from JS):         │
│  │   ├── GetPersons()            → Person list           │
│  │   ├── GetFacesForPerson(id)   → faces for a Person    │
│  │   ├── GetPendingFaces()       → unassigned faces      │
│  │   ├── GetFaceThumbnail(id)    → face thumbnail bytes  │
│  │   ├── AssignFace(faceID, personID)                    │
│  │   ├── UnassignFace(faceID)                            │
│  │   ├── GetClusters()           → clusters (Mode A)     │
│  │   ├── CreatePerson(name, birthDate, relationship)     │
│  │   ├── CreatePersonFromCluster(clusterID, bio)         │
│  │   ├── MergePersons(targetID, sourceIDs)               │
│  │   ├── DismissCluster(clusterID)                       │
│  │   ├── SaveAndExit()           → apply changes, quit   │
│  │   └── DiscardAndExit()        → discard changes, quit │
│  │                                                        │
│  └── Frontend (HTML/CSS/JS, embedded in binary):         │
│      ├── Drag-and-drop face reassignment                 │
│      ├── Person creation dialog (name, birth date, rel)  │
│      └── Cluster → Person conversion flow                │
└──────────────────────────────────────────────────────────┘
```

**Why Wails + React**: Native desktop feel with drag-and-drop (@dnd-kit), Go backend methods called directly from JS (no HTTP API), system webview (no bundled Chromium). React + TypeScript frontend with Vite for fast builds. The app also serves as the GUI for search (RAG) and agent chat — see [face-review-app.md](face-review-app.md) for full spec.

**Build tag**: Behind `//go:build faces` (same as all face code). Wails is only compiled when face support is enabled.

### 6.2 UI Modes

The review UI operates in two modes depending on state:

**Mode A: Initial Setup (no Persons exist yet)**
- Shows face clusters from unsupervised clustering
- Each cluster has a "Create Person" button
- User clicks → enters name, birth date, relationship
- All faces in that cluster become seed exemplars for the new Person
- Uninteresting clusters can be dismissed ("Not Interested")

**Mode B: Post-Bootstrap Review (Persons exist)**
- Shows Persons with their assigned faces (seed + auto-accepted)
- Pending review section for faces between reject and auto-accept thresholds
- User corrects misclassified faces by dragging between Persons
- Can create new Persons from pending/unrecognized faces

### 6.3 UI Layout

```
┌──────────────────────────────────────────────────────────────────┐
│  CodeEagle Face Review          [Save & Exit]  [Discard & Exit] │
├──────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌─ Hamza (child) ──── 208 faces ── [Edit Person] ──────────── │
│  │ ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐     ││
│  │ │ face │ │ face │ │ face │ │ face │ │ face │ │ face │     ││
│  │ │2008  │ │2012  │ │2016  │ │2020  │ │2022  │ │2024  │     ││
│  │ │ seed │ │ auto │ │ auto │ │ auto │ │ auto │ │ auto │     ││
│  │ └──────┘ └──────┘ └──────┘ └──────┘ └──────┘ └──────┘     ││
│  └─────────────────────────────────────────────────────────────┘│
│                                                                  │
│  ┌─ Mahdi (child) ──── 184 faces ── [Edit Person] ──────────── │
│  │ ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐               ││
│  │ │ face │ │ face │ │ face │ │ face │ │ face │               ││
│  │ │2010  │ │2014  │ │2018  │ │2022  │ │2024  │               ││
│  │ │ seed │ │ auto │ │ auto │ │ auto │ │ auto │               ││
│  │ └──────┘ └──────┘ └──────┘ └──────┘ └──────┘               ││
│  └─────────────────────────────────────────────────────────────┘│
│                                                                  │
│  ┌─ Pending Review ──── 891 faces ──────────────────────────── │
│  │ ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐                        ││
│  │ │ face │ │ face │ │ face │ │ face │  drag to Person above  ││
│  │ │2024  │ │2023  │ │2024  │ │2022  │  or create new Person  ││
│  │ │ conf │ │ conf │ │ conf │ │ conf │                        ││
│  │ │ 0.42 │ │ 0.38 │ │ 0.51 │ │ 0.33 │                        ││
│  │ └──────┘ └──────┘ └──────┘ └──────┘                        ││
│  └─────────────────────────────────────────────────────────────┘│
│                                                                  │
│  ┌─ Unrecognized ──── 533 faces ────────────────────────────── │
│  │ (below reject threshold — likely not persons of interest)    ││
│  │ [Show] to expand                                              ││
│  └─────────────────────────────────────────────────────────────┘│
└──────────────────────────────────────────────────────────────────┘
```

**Key interactions**:
- **Drag-and-drop** faces between Person sections (reassign to correct Person)
- **Drag from Pending** to a Person section to manually assign
- **Create new Person** from unassigned faces
- **Merge Persons** if same person was created twice
- **Face thumbnails** sorted by date within each Person (makes age progression visible)
- **Provenance badge** on each thumbnail: "seed" (green), "auto" (blue), "manual" (yellow)
- **Tooltip on hover**: full image path, date, event name, confidence score, provenance
- **Filter bar**: filter by Person, show only auto-accepted, show only pending review

### 6.4 Face Thumbnail Generation

Thumbnails generated on-the-fly (not pre-stored):

```go
func (s *Server) handleFaceThumbnail(w http.ResponseWriter, r *http.Request) {
    // 1. Read image from disk
    // 2. Crop face region using stored BBox (with padding)
    // 3. Resize to 120x120 thumbnail
    // 4. JPEG encode and serve
    // LRU cache (max 500 thumbnails, ~7.5MB) for fast scrolling
}
```

### 6.5 Change Tracking

Changes tracked in-memory as a change log:

```go
type ChangeLog struct {
    Assignments []AssignOp    // face assigned/reassigned to Person
    Unassigns   []UnassignOp  // face removed from Person
    NewPersons  []Person      // new Persons created during review
    Merges      []MergeOp     // Persons merged
}
```

Applied atomically on "Save & Exit". "Discard & Exit" throws away changes.

After saving, the system:
1. Updates all FaceAssignments
2. Rebuilds exemplar pool (adds manual corrections as provenance="manual")
3. Optionally triggers re-bootstrap from affected events

## 7. Sync Integration

### 7.1 Pipeline Position

```
codeeagle sync --full
  ├── 1. Index files (parse → graph)
  ├── 2. Linker (9 phases)
  ├── 3. LLM summarization (optional)
  ├── 4. Vector index (embeddings)
  └── 5. Face scan (optional, new)    ← gated by config + build tag
        ├── Index new images (EXIF + folder metadata)
        ├── Detect faces in new/modified images
        ├── Classify using KNN (if Persons with exemplars exist)
        └── Update graph (NodePerson → EdgeAppearsIn → NodeDocument)
```

### 7.2 Triggering

**Full sync** (`codeeagle sync --full`): Scans all image files. Skips already-scanned images (unless `--force`).

**Incremental sync** (`codeeagle sync`): Only scans images added/modified since last sync.

**Watch mode** (`codeeagle watch`): Queue new image files for face scan. Process periodically (every 30s or queue reaches 10 images).

### 7.3 New Image Flow (post-bootstrap)

When a new image arrives (via scan or sync):

```
1. Extract metadata (EXIF + folder) → IndexImage()
2. Detect faces → []FaceRecord with embeddings
3. For each face:
   a. If Persons with exemplars exist:
      - Run KNN classification against all exemplars
      - If classified with confidence >= auto_accept_threshold:
        → AssignFaceToPerson() with provenance="auto"
        → Add as new Exemplar (grows model for future images)
      - If confidence in [reject, auto_accept):
        → Mark as "pending review" (FaceAssignment with provenance="review")
      - If below reject threshold or "unknown":
        → No assignment (stays unassigned)
   b. If no Persons exist (first ever scan):
      - Run agglomerative clustering as today
      - Prompt user to create Persons and run bootstrap
4. Store faces + metadata
```

### 7.4 Graph Integration

When faces are assigned to Persons, create knowledge graph edges:

```
NodePerson("Hamza") --EdgeAppearsIn--> NodeDocument("Birthday_Hamza_20240315/F95A5151.JPG")
```

Enables `codeeagle query --type Person` and `codeeagle rag "photos of Hamza"` via knowledge graph and vector search.

### 7.5 Status Integration

`codeeagle status` includes face scan statistics when faces are enabled:

```
Knowledge Graph:
  Nodes: 12,847 (Function: 4,231, File: 892, ...)
  Edges: 18,234 (Contains: 7,892, Calls: 4,123, ...)

Face Recognition:
  Images indexed: 21,900 (2008-03 to 2024-12)
  Faces detected: 4,271
  Persons: 10
  Assigned:       3,380 (79.1%)
  Pending review:   358 (8.4%)    ← run `faces review` to assign
  Unassigned:       533 (12.5%)
```

This lets users see at a glance how many faces need attention without running a separate command.

### 7.6 Config

```yaml
docs:
  faces:
    enabled: true
    scan_on_sync: true               # face scan during sync (default: true when enabled)
    auto_classify: true              # use KNN for new faces (default: true)
    classify_threshold: 0.35         # KNN confidence threshold
    classify_k: 7                    # KNN K parameter
    temporal_weight: 0.1             # per-year decay (0 = disabled)
    model_dir: ""                    # auto: .CodeEagle/models/
    similarity_threshold: 0.30       # clustering threshold
    confidence_threshold: 0.50       # detection confidence
    min_face_size: 30
    max_image_resolution: 1600
    review_port: 0                   # 0 = random port
    bootstrap:
      auto_accept_threshold: 0.55
      reject_threshold: 0.30
      max_exemplars_per_person_per_event: 10
      checkpoint_interval: 0
      confidence_decay_warning: 0.10
```

## 8. Implementation Plan

### Phase 1: Person Entity & Image Index (foundation)

| File | Changes |
|------|---------|
| `go.mod` | Add `github.com/rwcarlsen/goexif` dependency |
| `internal/faces/person.go` | **New**: `Person` struct, `Exemplar` struct, `FaceAssignment` struct |
| `internal/faces/store.go` | Add Person CRUD, exemplar management, face assignment, image metadata methods; new key prefixes |
| `internal/faces/store_test.go` | Tests for Person operations, exemplar management, face assignments |
| `internal/faces/metadata.go` | **New**: EXIF extraction, folder/filename parsing, `ImageMetadata` struct |
| `internal/faces/metadata_test.go` | **New**: tests for EXIF extraction and folder parsing |
| `internal/cli/faces.go` | Add `faces person add/list/remove/edit` subcommands, `faces index` command |

### Phase 2: KNN Classifier

| File | Changes |
|------|---------|
| `internal/faces/classifier.go` | **New**: `Classifier` struct, `ClassifyFace()`, majority vote, temporal weighting |
| `internal/faces/classifier_test.go` | **New**: tests for KNN classification, confidence rejection, same-photo constraint |
| `internal/cli/faces.go` | Update `faces scan` to use classifier; add `faces classify` for manual trigger |

### Phase 3: Chronological Bootstrap

| File | Changes |
|------|---------|
| `internal/faces/bootstrap.go` | **New**: `ChronologicalBootstrap()`, event sorting, auto-accept logic, provenance, same-photo constraint, per-event cap, post-run summary |
| `internal/faces/bootstrap_test.go` | **New**: tests for bootstrap loop, error propagation safeguards, rollback |
| `internal/cli/faces.go` | Add `faces bootstrap` subcommand with flags, `faces seed` command |

### Phase 4: Desktop App (Wails + React)

**See separate spec**: [`face-review-app.md`](face-review-app.md)

The desktop app covers face review, RAG search, and agent chat — not just review.

| File | Changes |
|------|---------|
| `internal/faces/review/` | **New directory**: Wails app with Go backend + React/TypeScript frontend |
| `internal/cli/app.go` | **New**: `codeeagle app` command launching the desktop app |

### Phase 5: Sync & Status Integration

| File | Changes |
|------|---------|
| `internal/config/config.go` | Add new fields to `FacesConfig` |
| `internal/cli/sync.go` | Add face scan step after vector index |
| `internal/indexer/indexer.go` | Add `PostFaceScanHook` for watch mode |
| `internal/cli/watch.go` | Queue image files for batched face scan |
| `internal/cli/status.go` | Add face stats to `codeeagle status` output |
| `internal/docs/prompts.go` | Enhance image description prompt to extract scene/activity |

### Phase 6: Polish & Scale

| Task | Details |
|------|---------|
| EXIF orientation | Apply orientation tag before face detection |
| Batch face scan | Multiple images per OpenCV model load |
| Progress bar | `X/Y images, Z faces found` |
| Export/import | `faces export/import` for backup and migration |
| HNSW KNN | If N > 5000 exemplars, switch to approximate KNN via existing HNSW |

## 9. CLI Changes Summary

```
# Person management (CLI — most users create Persons via `faces review` web UI)
codeeagle faces person add <name> [--relationship <rel>] [--birth-date YYYY-MM-DD] [--notes <text>]
codeeagle faces person list
codeeagle faces person remove <name>
codeeagle faces person edit <name> [--name <new>] [--relationship <rel>] [--birth-date YYYY-MM-DD]

# Scanning (primary entry point — scan first, organize later)
codeeagle faces scan [dirs...]            # Detect faces + extract metadata + cluster
                                          # If Persons exist: also classify via KNN
codeeagle faces info <image-path>        # Show metadata + faces for image

# Seeding (CLI alternative to creating Persons in web UI)
codeeagle faces seed --person <name> --dir <thumbnails-dir>
codeeagle faces seed --person <name> --images <img1> <img2>

# Progressive bootstrap
codeeagle faces bootstrap                 # Run chronological bootstrapping
  --auto-accept <float>                    # Auto-accept threshold (default 0.55)
  --reject <float>                         # Reject threshold (default 0.30)
  --checkpoint <int>                       # Pause every N events for review
  --dry-run                                # Show what would happen without changes
  --purge-auto                             # Remove auto-exemplars before running
  --reset-to-seed                          # Reset to seed exemplars only
  --person <name>                          # Only bootstrap this Person
  --after <date>                           # Only process events after this date

# Review (web UI — primary way to create Persons and correct assignments)
codeeagle faces review                    # Mode A: cluster → Person creation
                                          # Mode B: review auto-accepted assignments

# Search
codeeagle faces search <name>            # Find images by Person name
codeeagle faces search --topic <keyword>  # Find by LLM-extracted topic
codeeagle faces search --event <type>     # Find by event type
codeeagle faces search <name> --after <date> --before <date>  # Date-filtered

# Status (integrated into main status)
codeeagle status                          # Includes face scan stats

# Legacy commands (still work, but web UI preferred)
codeeagle faces clusters                  # List clusters
codeeagle faces label <id> <name>        # Creates Person if needed
codeeagle faces merge <id1> <id2>        # Merge clusters
codeeagle faces split <id>              # Split cluster
```

## 10. Verification

### Step 1: Scan & Cluster (first-time setup)
```bash
codeeagle faces scan ~/Pictures/ImageServer/
# Expected: "Scanned 21,900 images, 4,271 faces detected, 142 clusters formed"
# Also extracts EXIF dates and folder metadata
```

### Step 2: Create Persons from Clusters (web UI)
```bash
codeeagle faces review
# Expected: browser opens showing clusters (Mode A)
# User creates Persons from clusters, enters name/birth date/relationship
# Save & Exit: "Created 10 Persons, 88 seed exemplars"
```

### Step 3: Person Management (CLI alternative)
```bash
codeeagle faces person add "Hamza" --relationship child --birth-date 2010-05-15
codeeagle faces person list
# Expected: Persons listed with face counts

# Seed from thumbnails (alternative to web UI)
codeeagle faces seed --person hamza --dir ~/.facescan/ideal_clusters/cluster_02_hamza/
# Expected: "Added 23 seed exemplars for Hamza"
```

### Step 4: Bootstrap
```bash
# Dry run first
codeeagle faces bootstrap --dry-run
# Expected: per-event summary showing what would be accepted

# Real run
codeeagle faces bootstrap
# Expected: full summary with per-Person breakdown, confidence trends

# Review auto-accepted results
codeeagle faces review
# Expected: browser opens showing Persons with auto-accepted faces (Mode B)
```

### Step 5: Status
```bash
codeeagle status
# Expected: includes face scan stats (images, faces, persons, assigned/pending/unassigned)
```

### Step 6: Sync Integration
```bash
# Enable in config: docs.faces.enabled: true, docs.faces.scan_on_sync: true
codeeagle sync --full
# Expected: "Face scan: 150 new images, 312 faces, 280 auto-classified, 32 pending"
```

### Step 7: Search
```bash
codeeagle faces search "Hamza"
codeeagle rag "photos of Hamza at the beach"
codeeagle rag "birthday party 2024"
```

## 11. Prototype / Spike Code

`tools/facescan/` contains standalone spike code used to validate the KNN and bootstrap approaches:
- `knn_test_cmd.go` — KNN test harness (validated 1-year and 3-year gap classification)
- `bootstrap_cmd.go` — chronological bootstrap prototype (validated pool growth 88→196 exemplars)

This code should be ported to `internal/faces/` during implementation and `tools/facescan/` removed once the production code is validated.

## 12. Known Limitations & Future Work

1. **Face alignment**: SFace without alignment produces noisy embeddings (effective threshold 0.30 vs nominal 0.637). A 5-point landmark detector would improve quality but adds complexity. Deferred unless KNN accuracy is insufficient.

2. **Video support**: Face detection in video files (extract key frames). Requires ffmpeg integration. Deferred.

3. **Duplicate image detection**: Images appearing in multiple folders. Could use perceptual hashing (pHash). Deferred.

4. **Multi-GPU / batched DNN**: OpenCV DNN runs single-image inference. Batching face crops through SFace would speed up embedding extraction.

5. **O(N) exemplar scan**: KNN scans all exemplars linearly. For >5000 exemplars, switch to HNSW-based approximate KNN (reuse existing vectorstore). The current library size (~22K images, ~30 Persons) is expected to produce ~2000-3000 exemplars, well within linear KNN performance.

6. **Privacy**: Face embeddings and thumbnails stored locally. No data sent to external services. Review app runs locally only.

7. **Object detection**: Dedicated OpenCV DNN object detector (MobileNet-SSD, COCO 80 classes) for object-level bounding boxes. Deferred — existing LLM topic extraction covers most search use cases.

7. **Cross-device sync**: Person/exemplar data is local to one machine. Future: export/import or shared storage for multi-device setups.
