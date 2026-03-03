# Non-Code File Support: Knowledge Graph + Vector Search

## Problem

CodeEagle silently skips any file without a registered language parser. Files like changelogs, design docs, CSVs, SVGs, config templates, and images are invisible to both the knowledge graph and semantic search. Users querying "authentication flow diagram" or "database schema" get nothing, even when relevant files exist in the repo.

## Goals

1. Index **all non-code files** into the knowledge graph and vector index — any file without a registered language parser is a candidate; only explicitly excluded extensions are skipped
2. Index **image files** (png, jpg, gif, webp, bmp, svg) using a multimodal LLM for content description — images are downscaled before LLM processing
3. Use **LLM-based topic extraction** to produce semantically rich embeddings — topics embed better than raw file content
4. Extract **topics as first-class graph nodes** (`NodeTopic`) connected to documents and code entities, enabling topic-based traversal and discovery
5. Build **directory hierarchy nodes** so file paths form a navigable tree in the knowledge graph (directory → subdirectory → file)
6. Connect non-code documents to code entities via **cross-reference linking**
7. Cache LLM results by content hash to avoid reprocessing unchanged files
8. Handle **large files gracefully** — summarize with LLM instead of skipping
9. **Detect faces** in images using OpenCV, extract face embeddings, and **cluster similar faces** so users can label identities once and auto-match across all photos
10. **Detect objects** in images using OpenCV YOLO, producing structured labels that feed into the topic system (e.g., "dog", "car", "beach")
11. Create **`NodePerson`** entities in the knowledge graph, connected to images via `EdgeAppearsIn`, enabling queries like "show all photos with Dad"

## Architecture

### Unified Pipeline

Every non-code file flows through one pipeline:

```
File changed (watcher/sync)
  │
  ├─ Has language parser? → existing code pipeline (unchanged)
  │
  ├─ Excluded extension? → skip
  │
  ├─ Check content hash in BadgerDB → unchanged? skip
  │
  ├─ Text-based file?
  │   └─ Read raw text (large files → LLM summarization, not skipping)
  │       └─ docs LLM configured?
  │           ├─ Yes → LLM topic extraction → enriched DocComment + NodeTopic nodes
  │           └─ No  → raw text as DocComment (still useful for RAG)
  │
  ├─ Image file?
  │   └─ Read + downscale (longest edge → max_resolution)
  │       ├─ docs LLM configured?
  │       │   ├─ Yes → multimodal LLM → description + topics → DocComment + NodeTopic nodes
  │       │   └─ No  → metadata-only DocComment (filename, dims)
  │       │
  │       └─ faces enabled? (OpenCV)
  │           ├─ Face detection → crop faces → extract embeddings → store in face.db
  │           ├─ Object detection (YOLO) → structured labels → NodeTopic nodes
  │           └─ After scan: DBSCAN clustering → face clusters → user labels → NodePerson + EdgeAppearsIn
  │
  ├─ Ensure directory hierarchy nodes exist
  │   └─ Create NodeDirectory for each ancestor dir, with Contains edges forming a tree
  │
  ├─ Create/update NodeDocument
  │   ├─ DocComment = LLM-extracted topics (or raw/metadata fallback)
  │   ├─ Properties = {content_hash, mime_type, original_size, ...}
  │   └─ Contains edge from parent NodeDirectory (or service/package)
  │
  ├─ Create/update NodeTopic nodes (when LLM extracts topics)
  │   ├─ One NodeTopic per extracted topic, deduplicated across documents
  │   └─ EdgeHasTopic from NodeDocument → NodeTopic
  │
  ├─ Cross-reference linker: scan topics/DocComment for known entities → Documents edges
  │
  └─ EmbeddableText() → chunking → embedding → vector index
```

The key insight: the multimodal docs LLM handles both image description AND text topic extraction. One model, one config, one pipeline. Topics become first-class graph nodes, enabling queries like "what documents discuss authentication?" to traverse the graph via topic nodes.

### Why Topic Extraction Before Embedding

Raw changelog text embedded:
```
v2.1.0 - Fixed bug in NewClient where provider lookup failed...
```

LLM topic-extracted result:
```json
{
  "summary": "Changelog entry describing a fix to LLM client initialization where provider registry lookup failed.",
  "topics": ["LLM client initialization", "provider registry pattern", "error handling"],
  "entities": ["NewClient", "CLIExecutor", "registry.go"]
}
```

The extracted version produces **three benefits**:

1. **Better embeddings**: the DocComment is semantically concentrated — queries like "provider factory pattern" match far better because the embedding captures abstracted meaning, not surface prose
2. **Graph traversal**: topics become `NodeTopic` nodes, enabling queries like "what documents discuss error handling?" to find all related files via graph edges
3. **Cross-reference linking**: extracted entities (NewClient, registry.go) are matched against known code symbols to create `EdgeDocuments` edges

## Configuration

### Remote Sources via SSHFS

CodeEagle indexes local filesystem paths. To include files from a remote server (e.g., a NAS, image server, or backup host), mount the remote filesystem locally using SSHFS, then add the mount point as a repository.

```bash
# Mount remote image server
mkdir -p ~/mnt/imageserver
sshfs user@imageserver:/photos ~/mnt/imageserver

# Mount NAS documents
mkdir -p ~/mnt/nas-docs
sshfs user@nas:/shared/documents ~/mnt/nas-docs
```

Then configure CodeEagle with each directory as a separate repository. Each repository path acts as an inclusion boundary — only files under listed paths are indexed:

```yaml
project:
  name: "personal-files"

repositories:
  - path: /home/user/Documents
    type: single
  - path: /home/user/Pictures
    type: single
  - path: /home/user/Videos
    type: single
  - path: /home/user/Downloads
    type: single
  - path: /home/user/mnt/imageserver
    type: single
  - path: /home/user/mnt/nas-docs
    type: single

watch:
  exclude:
    - "**/.cache/**"
    - "**/Thumbs.db"
    - "**/.DS_Store"
```

No `include` patterns are needed — `repositories` entries define what is indexed. `exclude` patterns filter within those boundaries.

**SSHFS notes:**
- `codeeagle sync` works normally over SSHFS mounts — file reads are transparent
- `codeeagle watch` uses fsnotify which has limited support over FUSE mounts — prefer `sync` with periodic re-runs for remote sources
- For large remote collections, consider mounting with caching: `sshfs -o cache=yes,cache_timeout=300 user@host:/path ~/mnt/point`
- SSHFS mounts must be active during sync/watch — if the mount is unavailable, those paths are skipped with a warning

### New `docs` config section

```yaml
# .CodeEagle/config.yaml

docs:
  provider: ollama              # or "vertex-ai"
  model: qwen3.5:9b             # multimodal model (text + images), ~10-14GB VRAM
  # model: gemini-2.0-flash     # for vertex-ai
  max_image_resolution: 1024    # downscale longest edge before LLM (pixels)
  context_window: 49152         # Ollama num_ctx — required for large files (default 49K)
  disable_thinking: false       # set true to add /no_think (saves tokens, may reduce quality)
  exclude_extensions:           # extensions to never index (only exclusion, no allowlist)
    - .lock
    - .min.js
    - .min.css
    - .map
    - .wasm
    - .pb.go

  faces:
    enabled: false                    # enable OpenCV face detection + object detection
    model_dir: ~/.codeeagle/models/   # directory for ONNX model files (auto-downloaded)
    min_face_size: 40                 # minimum face size in pixels (smaller faces ignored)
    similarity_threshold: 0.363       # cosine similarity threshold for same-person matching (SFace default)
    confidence_threshold: 0.7         # minimum detection confidence for faces
    object_detection: true            # enable YOLO object detection (labels → topics)
    object_confidence: 0.5            # minimum confidence for object labels
```

Design philosophy: **maximally inclusive**. Every file without a registered language parser is a candidate for non-code indexing. Only explicitly excluded extensions are skipped. There is no allowlist — if we can read it, we index it.

Mirrors the existing embedding config pattern — `provider` + `model` + provider-specific fields.

### Config struct addition

```go
// internal/config/config.go

type DocsConfig struct {
    Provider          string      `mapstructure:"provider" yaml:"provider,omitempty"`
    Model             string      `mapstructure:"model" yaml:"model,omitempty"`
    Project           string      `mapstructure:"project" yaml:"project,omitempty"`
    Location          string      `mapstructure:"location" yaml:"location,omitempty"`
    CredentialsFile   string      `mapstructure:"credentials_file" yaml:"credentials_file,omitempty"`
    BaseURL           string      `mapstructure:"base_url" yaml:"base_url,omitempty"`
    MaxImageRes       int         `mapstructure:"max_image_resolution" yaml:"max_image_resolution,omitempty"`
    ContextWindow     int         `mapstructure:"context_window" yaml:"context_window,omitempty"`     // Ollama num_ctx
    DisableThinking   bool        `mapstructure:"disable_thinking" yaml:"disable_thinking,omitempty"` // append /no_think to prompts
    ExcludeExtensions []string    `mapstructure:"exclude_extensions" yaml:"exclude_extensions,omitempty"`
    Faces             FacesConfig `mapstructure:"faces" yaml:"faces,omitempty"`
}

type FacesConfig struct {
    Enabled             bool    `mapstructure:"enabled" yaml:"enabled,omitempty"`
    ModelDir            string  `mapstructure:"model_dir" yaml:"model_dir,omitempty"`
    MinFaceSize         int     `mapstructure:"min_face_size" yaml:"min_face_size,omitempty"`
    SimilarityThreshold float64 `mapstructure:"similarity_threshold" yaml:"similarity_threshold,omitempty"`
    ConfidenceThreshold float64 `mapstructure:"confidence_threshold" yaml:"confidence_threshold,omitempty"`
    ObjectDetection     bool    `mapstructure:"object_detection" yaml:"object_detection,omitempty"`
    ObjectConfidence    float64 `mapstructure:"object_confidence" yaml:"object_confidence,omitempty"`
}
```

Added to `Config`:
```go
Docs DocsConfig `mapstructure:"docs" yaml:"docs"`
```

### Defaults

```go
// in setDefaults()
v.SetDefault("docs.max_image_resolution", 1024)
v.SetDefault("docs.context_window", 49152)  // Ollama num_ctx — 49K balances VRAM and file size
v.SetDefault("docs.exclude_extensions", []string{".lock", ".min.js", ".min.css", ".map", ".wasm", ".pb.go"})
v.SetDefault("docs.faces.enabled", false)
v.SetDefault("docs.faces.model_dir", "~/.codeeagle/models/")
v.SetDefault("docs.faces.min_face_size", 40)
v.SetDefault("docs.faces.similarity_threshold", 0.363)
v.SetDefault("docs.faces.confidence_threshold", 0.7)
v.SetDefault("docs.faces.object_detection", true)
v.SetDefault("docs.faces.object_confidence", 0.5)
```

## Docs LLM Provider

### Interface

```go
// internal/docs/provider.go
package docs

// ExtractionResult holds the structured output from LLM topic extraction.
type ExtractionResult struct {
    // Summary is a 2-3 sentence description of the content.
    Summary string
    // Topics is a list of extracted topic strings (e.g., "authentication", "database schema").
    Topics []string
    // Entities is a list of specific names (functions, classes, packages) mentioned.
    Entities []string
}

type Provider interface {
    // ExtractTopics processes text content and returns structured extraction.
    // For large text files, the provider should summarize before extracting.
    ExtractTopics(ctx context.Context, text string) (*ExtractionResult, error)

    // DescribeImage processes an image and returns structured extraction.
    DescribeImage(ctx context.Context, imageData []byte, mimeType string) (*ExtractionResult, error)

    // Name returns the provider name (e.g., "ollama", "vertex-ai").
    Name() string

    // ModelName returns the model identifier.
    ModelName() string
}
```

### Registry (same pattern as embedding)

```go
// internal/docs/provider.go

type ProviderFactory func(cfg Config) (Provider, error)

var registry   = make(map[string]ProviderFactory)
var registryMu sync.RWMutex

func RegisterProvider(name string, factory ProviderFactory)
func NewProvider(cfg Config) (Provider, error)
func IsProviderRegistered(name string) bool
```

```go
// internal/docs/config.go

type Config struct {
    Provider        string
    Model           string
    OllamaBaseURL   string
    Project         string // GCP (Vertex AI)
    Location        string // GCP (Vertex AI)
    CredentialsFile string
    MaxImageRes     int    // downscale longest edge
    ContextWindow   int    // Ollama num_ctx (default 49152)
    DisableThinking bool   // append /no_think to prompts
}
```

### Ollama Implementation

```go
// internal/docs/ollama.go

func init() {
    RegisterProvider("ollama", newOllamaProvider)
}
```

**ExtractTopics** — calls `POST /api/chat`:
```json
{
  "model": "qwen3.5:9b",
  "stream": false,
  "format": "json",
  "options": {"num_ctx": 49152},
  "messages": [
    {
      "role": "system",
      "content": "Extract topics, entities, and key concepts from the following text. If the text is very long, first summarize it then extract. Output JSON with: {\"summary\": \"2-3 sentence summary\", \"topics\": [\"topic1\", \"topic2\", ...], \"entities\": [\"FunctionName\", \"ClassName\", \"package_name\", ...]}. Be concise. Topics should be abstract themes, not specific names."
    },
    {
      "role": "user",
      "content": "<file text content>"
    }
  ]
}
```

**IMPORTANT**: `"options": {"num_ctx": 49152}` is required. Without it, Ollama uses a small default context window and silently produces empty/garbage output for files larger than ~15KB.

**Thinking mode**: thinking (chain-of-thought reasoning) is enabled by default — it improves extraction quality. If `disable_thinking` is set in config, the provider appends `/no_think` to the system prompt, which saves ~500-2000 tokens per call at the cost of potentially lower quality.

**Large file handling**: no file is skipped due to size. For text files that exceed ~80% of the context window (~160KB at 49K context), the provider truncates the input. The LLM naturally handles summarization — the prompt says "if the text is very long, first summarize it then extract". Tested: 105KB doc (30,610 tokens) processes in 25-40s. Retry logic handles the ~25% garbage-output rate (see Reliability section above).

**DescribeImage** — calls `POST /api/chat` with images field:
```json
{
  "model": "qwen3.5:9b",
  "stream": false,
  "format": "json",
  "messages": [
    {
      "role": "system",
      "content": "Describe this image in detail. If it is a diagram, describe the components, flow, and relationships. Extract any text visible in the image. Output JSON with: {\"summary\": \"2-3 sentence description\", \"topics\": [\"topic1\", \"topic2\", ...], \"entities\": [\"visible names, labels, identifiers\", ...]}."
    },
    {
      "role": "user",
      "content": "Describe this image.",
      "images": ["<base64-encoded-image>"]
    }
  ]
}
```

Images are always downscaled before sending (longest edge → `max_image_resolution`, default 1024px). Re-encoded as JPEG for smaller payload. A 10MB 8192x5464 photo downscales to ~75KB at 1024px.

### Tested Model Performance (qwen3.5:9b on RTX 3090 Ti)

Validated with real-world inputs from CodeEagle and Opal App codebases:

| Test Case | Input | Prompt Tokens | Duration | Output Quality |
|-----------|-------|---------------|----------|----------------|
| Short changelog text | 1 paragraph | ~200 | 17s | 5 topics, 4 entities, clean JSON |
| Large project doc (3KB) | CLAUDE.md excerpt | ~800 | 29s | 7 abstract topics, 21 entities |
| Permissions spec (2.6KB) | guardrails/permissions/01-use-chat.md | ~700 | 22s | 5 topics, 15 entities (Python/TS symbols) |
| Memory types brief (15KB) | memory/memory_types_tech_brief.md | ~4,500 | 25s | 10 topics, 20 entities (stores, patterns) |
| A2A server spec (60KB) | tech-spec/a2a/a2a-server.md | 19,366 | 46s | 10 topics, 33 entities (classes, services, deps) |
| GKE research doc (105KB) | gke-gvisor-vs-gce-filestore-research.md | 30,610 | 35s | 10 topics, 22 entities (GKE, gVisor, Filestore) |
| HTML presentation (59KB) | engineering_overview.html | 17,111 | 36s | 8 topics, 25 entities (handles raw HTML well) |
| Architecture diagram | 31KB PNG | ~500 | 15s | Accurate component/flow description |
| UI screenshot (RGBA PNG) | Opal_Chat.png (346KB → 66KB) | ~600 | 18s | 5 topics, 19 entities (read all labels) |
| Log screenshot w/ text | 46KB PNG | ~500 | 18s | Extracted timestamps, URLs, email addresses |
| Photo (downscaled) | 10MB JPG → 75KB | ~500 | 24s | Scene description, 4 topics |

**Critical finding — `num_ctx` must be set explicitly:**
- Ollama defaults `num_ctx` to a small value (2048-4096 depending on version)
- Files >15KB silently produce empty/garbage output without a larger context
- **Must pass `"options": {"num_ctx": <context_window>}` in every request**
- qwen3.5:9b supports up to 262K context, but VRAM scales linearly with context size

**VRAM usage by context size** (qwen3.5:9b, RTX 3090 Ti 24GB):

| `num_ctx` | VRAM Used | Max File Size (~) | Notes |
|-----------|-----------|-------------------|-------|
| default   | ~10 GB    | ~15 KB            | Fails silently on larger files |
| 32,768    | ~13 GB    | ~100 KB           | Previous default; tight for large docs |
| 49,152    | ~14 GB    | ~160 KB           | **Recommended default** — good balance |
| 65,536    | ~12 GB    | ~210 KB           | Works but no reliability improvement |
| 131,072   | ~15 GB    | ~420 KB           | Inconsistent output quality |
| 196,608   | ~18 GB    | ~640 KB           | High VRAM, quality degrades |
| 262,144   | ~21 GB    | ~850 KB           | Full context; output quality poor |

**Reliability finding — retry logic required:**
The model produces garbage output (~25-35% of attempts) regardless of context window size or thinking mode. This is an inherent qwen3.5:9b behavior — even with ample context headroom, some calls return empty/placeholder JSON (2 output tokens). The provider **must implement retry logic**:

```go
const maxExtractRetries = 4

func (p *ollamaProvider) extractWithRetry(ctx context.Context, messages []Message) (*ExtractionResult, error) {
    for attempt := range maxExtractRetries {
        result, err := p.callOllama(ctx, messages)
        if err != nil {
            return nil, err
        }
        // Validate: must have >2 topics and non-placeholder summary
        if len(result.Topics) > 2 && !strings.Contains(result.Summary, "...") && len(result.Summary) > 20 {
            return result, nil
        }
        // Garbage output — retry
    }
    // All 4 retries failed — mark as skipped for later retry
    return nil, ErrExtractionSkipped
}

var ErrExtractionSkipped = errors.New("extraction skipped after max retries")
```

Reliability tested (5 runs per file, `num_ctx: 49152`):

| File Size | Thinking ON | Thinking OFF |
|-----------|-------------|--------------|
| 15 KB     | 3/5 (60%)   | 2/5 (40%)    |
| 60 KB     | 4/5 (80%)   | 4/5 (80%)    |
| 107 KB    | 3/5 (60%)   | 5/5 (100%)   |
| **Overall** | **10/15 (67%)** | **11/15 (73%)** |

Both modes have similar reliability (~65-75% per attempt). At 70% per-attempt success rate, 4 retries gives ~99% overall success (1 - 0.30^4). Cost of retries is minimal since failed attempts produce only 2-20 output tokens. After 4 failed attempts, the file is skipped (raw text still indexed without LLM enrichment).

**Thinking mode**: enabled by default. Thinking (chain-of-thought reasoning) allows the model to reason about the content before extracting topics. Set `disable_thinking: true` in config to append `/no_think` to prompts — this saves ~500-2000 tokens per call but may reduce extraction quality on complex documents. The failure rate is inherent to the model and present in both modes; retry logic is the correct mitigation.

**Model sizing notes:**
- `qwen3.5:9b` at `num_ctx: 49152` requires ~14GB VRAM — fits on GPUs with 16GB+ VRAM
- `qwen3.5:27b` requires ~16GB+ base — may crash on 24GB GPUs with desktop compositor overhead; not recommended
- `format: "json"` produces parseable JSON when the model doesn't produce garbage
- First-call latency is ~55s (cold model load); subsequent warm calls: 17-46s depending on input size
- HTML files process well without preprocessing — the model extracts meaning from raw HTML

**Image handling notes:**
- PNG images with alpha channel (RGBA) must be converted to RGB before JPEG re-encoding (composite onto white background)
- Downscaling ratio: 346KB 1834x908 RGBA PNG → 66KB 1024x506 RGB JPEG (5x reduction)
- The model accurately reads text labels, identifies diagram components, and describes relationships in technical diagrams

**File size strategy:**
- Files up to ~160KB (at `num_ctx: 49152`): send directly to LLM
- Files exceeding ~80% of context window: truncate to fit (estimate: 1 token ≈ 3.5 chars)
- No file is skipped — truncated files still produce useful topic extraction from their content

## Face Detection & Recognition (OpenCV)

> **Standalone prototype**: The face detection pipeline has been prototyped and validated as a standalone CLI tool at `tools/facescan/`. See [facescan-spec.md](facescan-spec.md) for the full algorithm design, threshold rationale, and test results against ground truth data.

### Overview

Uses OpenCV (`gocv.io/x/gocv`) for precise face detection, face embedding extraction, and object detection in images. This is a **hybrid approach**: OpenCV handles the computer vision tasks (bounding boxes, embeddings, object labels), while Ollama handles the semantic scene description.

The workflow:
1. **Detection** — OpenCV DNN face detector finds faces in every indexed image (multi-scale tiling for small faces)
2. **Embedding** — OpenCV DNN face recognizer (SFace) extracts a 128-dim L2-normalized embedding per face
3. **Clustering** — Agglomerative clustering with average linkage groups similar embeddings, with same-photo constraint enforcement
4. **Labeling** — user assigns names to clusters via CLI (`codeeagle faces label`)
5. **Graph** — labeled clusters become `NodePerson` nodes, connected to images via `EdgeAppearsIn`

Object detection runs alongside face detection: YOLO labels (dog, car, tree, etc.) feed directly into the existing topic system as `NodeTopic` nodes.

### Dependencies

```
gocv.io/x/gocv  — Go bindings for OpenCV 4.x (requires CGO + system OpenCV)
```

**System requirements:**
- Linux: `sudo apt install libopencv-dev` (or build from source)
- macOS: `brew install opencv`
- Windows: OpenCV 4.x prebuilt binaries

**Build note**: `gocv` requires CGO. The build will use `CGO_ENABLED=1` when faces are compiled in. To support installations without OpenCV, face support is behind a build tag:

```go
//go:build faces

package faces
```

Users without OpenCV can build without the tag — face features are simply unavailable. The `docs.faces.enabled` config check happens at runtime, but the binary must be compiled with the `faces` build tag to include the OpenCV code.

```bash
# Build with face support:
make build-faces    # go build -tags faces ...

# Build without (default, no OpenCV needed):
make build          # go build ... (no faces tag)
```

### ONNX Models

Three model files, auto-downloaded on first `codeeagle faces scan`:

| Model | File | Size | Purpose |
|-------|------|------|---------|
| YuNet face detector | `face_detection_yunet_2023mar.onnx` | ~230 KB | Face detection (bounding boxes + landmarks) |
| SFace recognizer | `face_recognition_sface_2021dec.onnx` | ~37 MB | Face embedding extraction (128-dim vectors) |
| YOLOv8n | `yolov8n.onnx` | ~12 MB | Object detection (80 COCO classes) |

Stored at `~/.codeeagle/models/` (configurable via `docs.faces.model_dir`). Downloaded from OpenCV's GitHub releases / Ultralytics releases.

```go
// internal/faces/models.go

// ModelURLs maps model filenames to download URLs.
var ModelURLs = map[string]string{
    "face_detection_yunet_2023mar.onnx":     "https://github.com/opencv/opencv_zoo/raw/main/models/face_detection_yunet/face_detection_yunet_2023mar.onnx",
    "face_recognition_sface_2021dec.onnx":   "https://github.com/opencv/opencv_zoo/raw/main/models/face_recognition_sface/face_recognition_sface_2021dec.onnx",
    "yolov8n.onnx":                          "https://github.com/ultralytics/assets/releases/download/v8.3.0/yolov8n.onnx",
}

// EnsureModels downloads any missing model files to modelDir.
func EnsureModels(modelDir string, needYOLO bool) error
```

### Face Detection Pipeline

```go
// internal/faces/detector.go

// Detector wraps OpenCV DNN models for face and object detection.
type Detector struct {
    faceNet     gocv.Net          // YuNet face detector
    recognizer  gocv.Net          // SFace face recognizer
    yoloNet     gocv.Net          // YOLOv8n object detector (nil if disabled)
    cfg         Config
}

// FaceDetection represents a single detected face in an image.
type FaceDetection struct {
    BoundingBox image.Rectangle   // face location in original image
    Embedding   []float32         // 128-dim SFace embedding
    Confidence  float32           // detection confidence (0-1)
    Landmarks   [5]image.Point    // 5-point facial landmarks (eyes, nose, mouth corners)
}

// ObjectDetection represents a detected object.
type ObjectDetection struct {
    Label       string            // COCO class name (e.g., "person", "dog", "car")
    Confidence  float32           // detection confidence (0-1)
    BoundingBox image.Rectangle   // object location
}

// DetectResult holds all detections for a single image.
type DetectResult struct {
    Faces   []FaceDetection
    Objects []ObjectDetection
}

// Detect runs face detection + recognition + object detection on an image.
func (d *Detector) Detect(img gocv.Mat) (*DetectResult, error)

// Close releases OpenCV resources.
func (d *Detector) Close()
```

**Face detection flow:**
1. Load image into `gocv.Mat`
2. Run YuNet face detector → bounding boxes + landmarks + confidence scores
3. Filter by `min_face_size` and `confidence_threshold`
4. For each detected face:
   a. Crop and align face region (using landmarks for alignment)
   b. Resize to 112x112 (SFace input requirement)
   c. Run SFace recognizer → 128-dim L2-normalized embedding
5. Return `[]FaceDetection` with embeddings

**Object detection flow** (when `object_detection: true`):
1. Preprocess image for YOLOv8 (letterbox resize to 640x640, normalize)
2. Run YOLOv8n forward pass → detections with class IDs + confidence
3. Filter by `object_confidence` threshold
4. Map class IDs to COCO label names
5. Return `[]ObjectDetection`

### Face Embedding Storage

Face embeddings are stored in a dedicated BadgerDB at `.CodeEagle/face.db/` (separate from graph and vector stores):

```go
// internal/faces/store.go

// Store persists face embeddings and cluster assignments.
type Store struct {
    db *badger.DB
}

// Key prefixes:
// face:emb:<docNodeID>:<faceIdx>     → []float32 (128-dim embedding, binary-encoded)
// face:box:<docNodeID>:<faceIdx>     → JSON {x,y,w,h,confidence,landmarks}
// face:cluster:<docNodeID>:<faceIdx> → cluster ID (int)
// face:label:<clusterID>             → person name string
// face:person:<personName>           → list of (docNodeID, faceIdx) pairs

func NewStore(dbPath string) (*Store, error)

// StoreFaces saves all face detections for a document.
func (s *Store) StoreFaces(docNodeID string, faces []FaceDetection) error

// LoadAllEmbeddings returns all stored embeddings for clustering.
// Returns map[key]embedding where key is "docNodeID:faceIdx".
func (s *Store) LoadAllEmbeddings() (map[string][]float32, error)

// SetCluster assigns a cluster ID to a face.
func (s *Store) SetCluster(docNodeID string, faceIdx int, clusterID int) error

// LabelCluster assigns a person name to a cluster ID.
func (s *Store) LabelCluster(clusterID int, name string) error

// GetPersonFaces returns all (docNodeID, faceIdx) pairs for a named person.
func (s *Store) GetPersonFaces(name string) ([]FaceRef, error)

// ListClusters returns all cluster IDs with their face counts and labels.
func (s *Store) ListClusters() ([]ClusterInfo, error)
```

### Face Clustering (Agglomerative Average Linkage)

> See [facescan-spec.md](facescan-spec.md) for the full algorithm design, threshold rationale (empirically validated against 88 faces across 8 people), and ground truth test results.

```go
// internal/faces/cluster.go

// ClusterFaces runs agglomerative clustering with average linkage on all face embeddings
// and assigns cluster IDs. Same-photo constraint prevents merging clusters that share an image.
// simThreshold: minimum average cosine similarity to merge two clusters (default 0.30)
// minClusterSize: minimum faces per cluster (default 2 — singletons become noise, then absorbed)
func ClusterFaces(store *Store, simThreshold float64, minClusterSize int) (int, error)
```

**Why not DBSCAN**: DBSCAN uses single-linkage (transitive) chaining — if face A matches B, and B matches C, then A and C end up in the same cluster even if they look nothing alike. This is catastrophic for family photos where siblings can have cosine similarity up to 0.932. A single outlier pair chains entire families into one cluster. The `tools/facescan/` prototype validated this problem empirically.

**Agglomerative average linkage** considers ALL cross-cluster face pairs when deciding whether to merge. The merge criterion is the mean similarity across all face pairs, not just the best one. This prevents a single outlier pair from dominating the merge decision.

**Algorithm:**
1. Initialize: each face is its own cluster (N clusters)
2. Precompute N×N pairwise cosine similarity matrix
3. Loop:
   a. For every cluster pair (i, j): check same-photo constraint, compute average similarity
   b. Find the pair with highest average similarity
   c. If best average similarity >= threshold (0.30): merge them; otherwise: stop
4. Singletons (< minClusterSize) become noise (-1)
5. Single-pass noise absorption: assign noise faces to best-matching cluster (threshold = 75% of clustering threshold), no iteration to prevent chaining

**Same-photo constraint**: A person can only appear once per photo. If two clusters share an image, those clusters cannot be the same person. Enforced at every merge decision.

**Default parameters:**
- `simThreshold = 0.30` (empirically validated — 70.7% intra-person pair coverage with average linkage preventing false merges)
- `minClusterSize = 2` (single-occurrence faces become noise, then absorbed)

**Scalability**: Agglomerative clustering is O(N³) and the similarity matrix is O(N²) memory. For typical personal photo libraries (<1000 faces), this completes in milliseconds. For >1000 faces, approximate methods (HNSW for neighbor queries, mini-batch clustering) would be needed.

**DBSCAN retained for split**: The `split` command re-clusters a single cluster at a tighter threshold (default 0.75). Within a single cluster the faces are reasonably similar, so DBSCAN's transitive chaining is less problematic.

### Person Nodes in the Graph

After clustering and labeling, `NodePerson` nodes are created in the knowledge graph:

```go
// Person node
&graph.Node{
    Type:          graph.NodePerson,
    Name:          "Dad",                      // user-assigned label
    QualifiedName: "person:Dad",               // prefixed to avoid collisions
    Properties: map[string]string{
        "cluster_id":  "3",                    // face cluster ID
        "face_count":  "47",                   // total photos this person appears in
        "source":      "face_recognition",
    },
}

// AppearsIn edge (person → document)
&graph.Edge{
    Type:     graph.EdgeAppearsIn,
    SourceID: personNodeID,
    TargetID: documentNodeID,
    Properties: map[string]string{
        "face_idx":    "0",                    // which face in the image
        "confidence":  "0.92",                 // detection confidence
    },
}
```

### Object Detection → Topics

YOLO-detected objects feed directly into the existing topic system:

```go
// In GenericParser, after face/object detection:
for _, obj := range result.Objects {
    // "person", "dog", "car" → NodeTopic nodes
    topicName := normalizeTopicName(obj.Label)
    topicNode := getOrCreateTopic(topicName, "object_detection")
    createEdge(docNode.ID, topicNode.ID, graph.EdgeHasTopic)
}
```

This means an image with a dog and a car automatically gets `NodeTopic("dog")` and `NodeTopic("car")` — shared with any other image containing those objects. Combined with Ollama's scene description topics (e.g., "park", "sunset"), this provides both structured labels and semantic understanding.

**COCO class labels** (80 classes, most relevant for personal photos):
- People: `person`
- Animals: `bird`, `cat`, `dog`, `horse`, `sheep`, `cow`, `elephant`, `bear`, `zebra`, `giraffe`
- Vehicles: `bicycle`, `car`, `motorcycle`, `airplane`, `bus`, `train`, `truck`, `boat`
- Outdoor: `traffic light`, `fire hydrant`, `stop sign`, `parking meter`, `bench`
- Food: `banana`, `apple`, `sandwich`, `orange`, `broccoli`, `carrot`, `hot dog`, `pizza`, `donut`, `cake`
- Indoor: `chair`, `couch`, `bed`, `dining table`, `toilet`, `tv`, `laptop`, `cell phone`, `book`, `clock`
- Sports: `frisbee`, `skis`, `snowboard`, `sports ball`, `kite`, `baseball bat`, `tennis racket`, `skateboard`, `surfboard`

### CLI Commands

```bash
# Face detection and clustering
codeeagle faces scan [--force]              # Detect faces, extract embeddings, run clustering
codeeagle faces clusters                     # List face clusters with counts and sample image paths
codeeagle faces extract                      # Extract face thumbnails grouped by cluster (for visual review)
codeeagle faces suggest                      # Show merge candidates (similar clusters) with overlap detection
codeeagle faces merge <id1> <id2> [id3...]   # Merge clusters (same person split apart)
codeeagle faces split <id> [--sim <float>]   # Re-cluster a single cluster at tighter threshold
codeeagle faces label <cluster-id> <name>    # Assign a person name to a face cluster
codeeagle faces search <name>                # Find all images containing a named person
codeeagle faces unlabeled                    # Show clusters that haven't been labeled yet

# Object detection is automatic during image indexing (when faces.enabled + faces.object_detection)
# Object labels appear as NodeTopic nodes — queryable via existing commands:
codeeagle query --type Topic --name "dog"
codeeagle query edges --node "dog" --type HasTopic
```

**`codeeagle faces scan`**:
1. Ensure ONNX models are downloaded
2. Query all `NodeDocument` nodes with `kind=image` property
3. For each image:
   a. Load image file
   b. Run face detection → embeddings
   c. Run object detection → labels (if enabled)
   d. Store face embeddings in `face.db`
   e. Create `NodeTopic` nodes for detected objects
4. Run agglomerative clustering (average linkage) on all embeddings + absorb noise
5. Print summary: "Detected X faces in Y images, formed Z clusters"

**`codeeagle faces clusters`**:
```
Cluster  Label      Faces  Sample Images
-------  ---------  -----  -----------------------------------------
1        (unlabeled)  47   ~/Pictures/vacation/beach.jpg, ~/Pictures/birthday/cake.jpg, ...
2        (unlabeled)  23   ~/Pictures/school/graduation.jpg, ~/Pictures/park.jpg, ...
3        (unlabeled)   5   ~/Pictures/work/team.jpg, ~/Pictures/conference.jpg, ...
-1       (noise)       12  (single-occurrence faces, not clustered)
```

**`codeeagle faces label 1 "Dad"`**:
1. Set label in `face.db`: `face:label:1 → "Dad"`
2. Create `NodePerson("Dad")` in the knowledge graph
3. For each face in cluster 1, create `EdgeAppearsIn` from person → document
4. Print: "Labeled cluster 1 as 'Dad' (47 images)"

**`codeeagle faces search "Dad"`**:
1. Look up person in graph: `QueryNodes(NodeFilter{Type: NodePerson, NamePattern: "Dad"})`
2. Follow `EdgeAppearsIn` edges to find all documents
3. Print image paths with face confidence scores

### Integration with Image Indexing Pipeline

Face/object detection runs **after** the image is indexed (NodeDocument created) and **after** LLM description (if configured). The flow for an image file:

1. Generic parser creates `NodeDocument` (existing flow)
2. If docs LLM configured: Ollama describes image → DocComment + semantic topics
3. If `faces.enabled`:
   a. OpenCV detects faces → stores embeddings in `face.db`
   b. OpenCV detects objects → creates `NodeTopic` nodes for labels
   c. (Clustering runs separately via `codeeagle faces scan`, not per-image)

During `codeeagle sync`, face detection runs per-image (step 3a-3b). Clustering runs at the end of sync (or on-demand via `codeeagle faces scan`). This separation is important because clustering requires all embeddings to be available.

After automatic clustering, the manual correction workflow (extract → suggest → merge/split → label) handles the remaining cases where cross-event appearance variation or family resemblance causes fragmentation or contamination. See [facescan-spec.md](facescan-spec.md) for the full correction workflow and ground truth test results.

### Full Graph Example (with faces)

For `~/Pictures/vacation/beach.jpg` with Dad and a dog:

```
NodeDirectory("~/Pictures")
  │ Contains
  └─ NodeDirectory("~/Pictures/vacation")
       │ Contains
       └─ NodeDocument("~/Pictures/vacation/beach.jpg")
            │ HasTopic
            ├─ NodeTopic("beach")           ← from Ollama scene description
            ├─ NodeTopic("sunset")          ← from Ollama scene description
            ├─ NodeTopic("person")          ← from YOLO object detection
            └─ NodeTopic("dog")             ← from YOLO object detection
            │
            │ AppearsIn (reverse)
            └─ NodePerson("Dad")            ← from face clustering + labeling

NodePerson("Dad") ─── AppearsIn ──→ NodeDocument("~/Pictures/vacation/beach.jpg")
NodePerson("Dad") ─── AppearsIn ──→ NodeDocument("~/Pictures/birthday/cake.jpg")
NodePerson("Dad") ─── AppearsIn ──→ NodeDocument("~/Pictures/park/family.jpg")
```

Enables queries:
- "Show all photos with Dad" → `NodePerson("Dad") → AppearsIn → documents`
- "Photos with Dad and a dog" → intersect `AppearsIn` from Dad with `HasTopic` from "dog"
- "What's in my vacation folder?" → `NodeDirectory("vacation") → Contains → documents`
- "Photos at the beach" → `NodeTopic("beach") → HasTopic (reverse) → documents`

### Vertex AI Implementation

```go
// internal/docs/vertex.go

func init() {
    RegisterProvider("vertex-ai", newVertexProvider)
}
```

Uses the Gemini API with `gemini-2.0-flash`. For images, sends the image as an inline content part with MIME type. For text, sends as a text content part. Same prompts as Ollama. JSON output mode is requested via `response_mime_type: "application/json"` in generation config.

### Auto-Detection

```go
// internal/docs/detect.go

func DetectProvider(cfg *config.Config) (Provider, error)
```

Same pattern as `embedding.DetectProvider`:
1. If `cfg.Docs.Provider` is set, use only that provider
2. Auto-detect: try Ollama (liveness probe + model check), then Vertex AI (if project+location set)
3. Return `nil, nil` if no provider available (graceful degradation)

## Generic File Parser

### File Classification

```go
// internal/parser/generic/classifier.go
package generic

// FileClass represents how a file should be processed.
type FileClass int

const (
    FileClassText  FileClass = iota // text-based: read and extract
    FileClassImage                  // image: describe with vision model
    FileClassSkip                   // explicitly excluded or binary
)

// Classify determines how to process a file.
// Design: maximally inclusive — any file not explicitly excluded is a candidate.
// Uses MIME type detection to distinguish text vs image vs binary.
func Classify(filePath string, excludeExts []string) FileClass
```

**Classification logic** (no allowlist — inclusive by default):

1. Check if extension is in `excludeExts` → `FileClassSkip`
2. Check if extension matches known image types (`.png`, `.jpg`, `.jpeg`, `.gif`, `.webp`, `.bmp`, `.ico`, `.tiff`, `.tif`) → `FileClassImage`
3. Attempt to detect if the file is text via `net/http.DetectContentType` or by reading the first 512 bytes and checking for null bytes → `FileClassText` or `FileClassSkip` (true binary)
4. Extensionless files (`README`, `CHANGELOG`, `LICENSE`, `Dockerfile`, etc.) → `FileClassText`

**Default exclude list** (configurable via `docs.exclude_extensions`):
`.lock`, `.min.js`, `.min.css`, `.map`, `.wasm`, `.pb.go`

The exclude list is the **only** filtering mechanism. Everything else is indexed.

### Parser Implementation

```go
// internal/parser/generic/parser.go

type GenericParser struct {
    docsProvider docs.Provider // nil = no LLM, use raw text
}

func NewGenericParser(dp docs.Provider) *GenericParser
```

The generic parser implements `parser.FilenameParser` (since it matches by filename patterns, not a single language extension). It handles both text and image files.

**For text files:**
1. Read file content (no size limit — all files are processed)
2. If docs provider available: call `ExtractTopics(ctx, content)` → `*ExtractionResult`
   - Provider handles large files internally (summarization pass or context-window truncation)
3. If not: use raw content as DocComment, no topic nodes created
4. Ensure directory hierarchy nodes exist (see Directory Hierarchy section)
5. Create `NodeDocument` with DocComment = result.Summary + topic text
6. Create `NodeTopic` nodes for each extracted topic (deduplicated)
7. Create `EdgeHasTopic` from document → each topic

**For image files:**
1. Read file
2. If docs provider available:
   a. Downscale image (longest edge → `max_image_resolution`)
   b. Call `DescribeImage(ctx, imageData, mimeType)` → `*ExtractionResult`
   c. Create `NodeDocument` with DocComment = result.Summary
   d. Create `NodeTopic` nodes + `EdgeHasTopic` edges
3. If not: create `NodeDocument` with metadata-only DocComment (filename, dimensions)

**Image downscaling** — uses Go stdlib `image` + `golang.org/x/image/draw`:
```go
import (
    "bytes"
    "image"
    "image/jpeg"
    _ "image/png"
    _ "image/gif"
    _ "golang.org/x/image/webp"
    "golang.org/x/image/draw"
)

// downscaleImage decodes an image, downscales if longest edge > maxRes,
// converts RGBA/NRGBA to RGB (JPEG doesn't support alpha), and re-encodes as JPEG.
func downscaleImage(data []byte, maxRes int) ([]byte, string, error) {
    src, _, err := image.Decode(bytes.NewReader(data))
    if err != nil {
        return nil, "", err
    }
    bounds := src.Bounds()
    w, h := bounds.Dx(), bounds.Dy()
    longest := max(w, h)

    // Determine target dimensions.
    newW, newH := w, h
    if longest > maxRes {
        ratio := float64(maxRes) / float64(longest)
        newW, newH = int(float64(w)*ratio), int(float64(h)*ratio)
    }

    // Always draw onto an opaque RGBA canvas (white background) to handle
    // PNG/WebP alpha channels — JPEG cannot encode transparency.
    dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
    // Fill with white background.
    for i := range dst.Pix {
        dst.Pix[i] = 0xff
    }
    draw.BiLinear.Scale(dst, dst.Bounds(), src, bounds, draw.Over, nil)

    var buf bytes.Buffer
    jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85})
    return buf.Bytes(), "image/jpeg", nil
}
```

Tested: 10MB 8192x5464 JPG → 75KB 1024x683 JPEG. The JPEG re-encoding keeps the LLM payload small.

### Integration with Indexer

The generic parser is registered as a **fallback** in the parser registry — it runs only when no language parser matches. This requires a small change to `Registry.ParserForFile()`:

```go
// internal/parser/registry.go

func (r *Registry) ParserForFile(filePath string) (Parser, bool) {
    // ... existing extension and filename lookup ...

    // Fallback: generic parser for any file not handled by language parsers
    if r.fallback != nil {
        if generic.Classify(filePath, r.excludeExts) != generic.FileClassSkip {
            return r.fallback, true
        }
    }
    return nil, false
}

func (r *Registry) SetFallback(p Parser) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.fallback = p
}

func (r *Registry) SetExcludeExtensions(exts []string) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.excludeExts = exts
}
```

The `excludeExts` are loaded from `docs.exclude_extensions` config.

### Node Structure

#### New Node Types

```go
// internal/graph/schema.go — additions

const (
    NodeDirectory NodeType = "Directory" // directory in the file tree
    NodeTopic     NodeType = "Topic"     // LLM-extracted topic
    NodePerson    NodeType = "Person"    // identified person (face recognition)
)

const (
    EdgeHasTopic  EdgeType = "HasTopic"  // document → topic
    EdgeAppearsIn EdgeType = "AppearsIn" // person → document (person appears in this image)
)
```

#### Directory Hierarchy Nodes

File paths form a tree of `NodeDirectory` nodes with `EdgeContains` edges. For a file at `docs/design/auth-flow.png`:

```
NodeDirectory("docs")
    │ Contains
    └─ NodeDirectory("docs/design")
           │ Contains
           └─ NodeDocument("docs/design/auth-flow.png")
```

```go
// Directory node
&graph.Node{
    Type:          graph.NodeDirectory,
    Name:          "design",                   // directory name
    QualifiedName: "docs/design",              // relative path
    FilePath:      "docs/design",
    Package:       "docs",                     // parent directory
}
```

Directories are created lazily — when a file is indexed, all ancestor directories that don't already exist as nodes are created. This avoids scanning empty directories.

The top-level directory (repo root's immediate children) gets a `Contains` edge from the `NodeRepository` or `NodeService` node, connecting the directory tree to the existing graph hierarchy.

#### Document Nodes

All non-code files produce `NodeDocument` nodes:

```go
&graph.Node{
    Type:          graph.NodeDocument,
    Name:          "auth-flow.png",            // filename
    QualifiedName: "docs/design/auth-flow.png", // relative path
    FilePath:      "docs/design/auth-flow.png",
    Package:       "docs/design",              // parent directory
    Language:      "",                         // no language
    DocComment:    "<LLM summary + topic text>",
    Properties: map[string]string{
        "mime_type":     "image/png",
        "content_hash":  "sha256:abc123...",
        "kind":          "image",              // or "text", "data"
        "original_size": "1048576",            // bytes
    },
}
```

#### Topic Nodes

LLM-extracted topics become first-class nodes in the graph:

```go
// Topic node
&graph.Node{
    Type:          graph.NodeTopic,
    Name:          "authentication",           // normalized topic name
    QualifiedName: "topic:authentication",     // prefixed to avoid collisions
    Properties: map[string]string{
        "source": "llm",                       // how this topic was created
    },
}
```

Topics are **deduplicated by normalized name** — if two documents both mention "authentication", they share the same `NodeTopic` node. Normalization: lowercase, trim whitespace, collapse spaces.

**Topic → Document edges** (`EdgeHasTopic`):
```go
&graph.Edge{
    Type:     graph.EdgeHasTopic,
    SourceID: documentNodeID,
    TargetID: topicNodeID,
}
```

**Topic → Code entity edges** (`EdgeDocuments`): the cross-reference linker can also connect topics to code entities when topic names match known symbol names, package names, or file paths. For example, a topic "LLM client" might connect to the `llm` package node.

#### Full Graph Example

For a repo with `docs/design/auth-flow.png` that produces topics ["authentication", "middleware", "JWT"]:

```
NodeRepository("my-app")
  │ Contains
  ├─ NodeDirectory("docs")
  │    │ Contains
  │    └─ NodeDirectory("docs/design")
  │         │ Contains
  │         └─ NodeDocument("docs/design/auth-flow.png")
  │              │ HasTopic
  │              ├─ NodeTopic("authentication")
  │              ├─ NodeTopic("middleware")
  │              └─ NodeTopic("jwt")
  │
  ├─ NodePackage("auth")
  │    │ Contains
  │    └─ NodeFunction("ValidateToken")
  │
  └─ ... (existing code nodes)

  NodeTopic("authentication") ─── Documents ──→ NodeFunction("ValidateToken")
  NodeTopic("middleware")     ─── Documents ──→ NodeFunction("AuthMiddleware")
```

This enables powerful graph traversals:
- "What documents discuss authentication?" → follow HasTopic edges from topic node
- "What code is related to this diagram?" → HasTopic → NodeTopic → Documents → code entities
- "What's in the docs/ directory?" → traverse Contains edges from NodeDirectory

## Content Hash Caching

### Storage in BadgerDB

Stored in the existing graph BadgerDB (same as nodes/edges), using new key prefixes:

```
doc:hash:<relative-file-path>   → SHA-256 of file content
doc:result:<relative-file-path> → JSON-encoded ExtractionResult (summary, topics, entities)
doc:skip:<relative-file-path>   → SHA-256 of file content (extraction failed, pending retry)
```

### Cache Check Flow

```go
// internal/docs/cache.go

type Cache struct {
    store graph.Store  // or direct BadgerDB access
}

// Check returns the cached extraction result if the file hasn't changed.
// Returns (nil, false) if the file is new or modified.
func (c *Cache) Check(filePath string, content []byte) (*ExtractionResult, bool)

// Store saves the content hash and extraction result for a file.
// Clears any skip marker for this file.
func (c *Cache) Store(filePath string, content []byte, result *ExtractionResult) error

// MarkSkipped records that extraction failed for this file (content hash stored).
// The file will be retried on the next sync.
func (c *Cache) MarkSkipped(filePath string, content []byte) error

// IsSkipped returns true if this file was previously skipped AND its content
// hasn't changed. If content changed, the skip marker is stale and cleared.
func (c *Cache) IsSkipped(filePath string, content []byte) bool

// ListSkipped returns all file paths with pending skip markers.
func (c *Cache) ListSkipped() ([]string, error)
```

During sync:
1. Read file content, compute SHA-256
2. Check `doc:hash:<path>` — if hash matches, read `doc:result:<path>` and use cached result
3. If hash differs (or missing) OR file has a `doc:skip:<path>` marker: call docs LLM
4. On success: store hash + extraction result, clear any skip marker
5. On `ErrExtractionSkipped`: call `MarkSkipped` — stores hash under `doc:skip:<path>`, indexes raw text as fallback
6. From the `ExtractionResult`, create/update NodeDocument (DocComment = summary) and NodeTopic nodes

**Skip → retry flow**: skipped files are automatically retried on every subsequent `codeeagle sync`. The skip marker stores the content hash, so if the file changes between syncs, the stale marker is cleared and extraction is attempted fresh. This means transient LLM failures self-heal over time without user intervention.

This means only new, modified, or previously-skipped files trigger LLM calls. Unchanged files with successful extractions reuse cached results. The cached result includes the full structured data (topics, entities), so topic nodes can be recreated without re-calling the LLM.

### Integration point

The cache lives in the graph's BadgerDB. Since `BranchStore.db` is unexported, the cache either:
- Gets a new method on `graph.Store` interface (e.g., `GetMeta/SetMeta` for arbitrary key-value)
- Or uses a separate small BadgerDB at `.CodeEagle/docs.db/` (same pattern as `vec.db/`)

**Recommendation**: separate `docs.db/` — keeps concerns decoupled and avoids polluting the graph store interface with non-graph operations.

## Cross-Reference Linker Phase

### New linker phase: `documents`

```go
// internal/linker/documents.go

func (l *Linker) linkDocuments(ctx context.Context) (int, error)
```

Scans all `NodeDocument` nodes (non-code files), then for each:

1. Tokenize the DocComment into potential entity references
2. For each token, check if it matches a known node name in the graph:
   - Exact match: `store.QueryNodes(ctx, NodeFilter{NamePattern: token})`
   - File path match: `store.QueryNodes(ctx, NodeFilter{FilePath: "*" + token})`
3. For matches: create `EdgeDocuments` from the Document node to the code entity
4. Skip self-references and already-existing edges

**Matching strategies:**
- **Identifier names**: `NewClient`, `AuthMiddleware`, `ValidateToken` — match against Function/Method/Struct/Interface names
- **File paths**: `internal/llm/client.go`, `pkg/llm/` — match against File node paths
- **Package names**: `llm`, `parser`, `config` — match against Package nodes

This phase is added to `Phases()` in `linker.go` and runs after `tests` (last structural phase, before LLM phases).

## Sync/Watch Integration

### sync.go changes

After the existing parser registry setup, create and register the generic parser:

```go
// Create docs provider (if configured)
docsProvider, err := docs.DetectProvider(cfg)
// ... error handling (nil is OK — graceful degradation) ...

// Create generic parser with docs provider (may be nil)
genericParser := generic.NewGenericParser(docsProvider)
registry.SetFallback(genericParser)
registry.SetExcludeExtensions(cfg.Docs.ExcludeExtensions)
```

The rest of the sync flow is unchanged — `SyncFiles` calls `IndexFile` which calls `ParserForFile`, which now falls back to the generic parser. The generic parser internally handles:
- Directory hierarchy node creation (lazy, deduped)
- Topic node creation from `ExtractionResult` (when LLM available)
- Content hash caching (skip unchanged files)

After linker runs, the new `documents` phase creates cross-reference edges (document → code, topic → code).

### watch.go changes

Same generic parser setup. The watcher's `PostIndexHook` already runs the linker, which will include the new `documents` phase.

The watcher needs one change: it currently filters events by known extensions. It needs to also accept files that `generic.Classify` returns non-skip for. Since classification is now inclusive by default (only exclude list), most files will pass through.

## File Structure

```
internal/
├── docs/
│   ├── provider.go       # Provider interface, ExtractionResult, registry, Config
│   ├── ollama.go          # Ollama implementation (multimodal /api/chat, JSON output)
│   ├── vertex.go          # Vertex AI implementation (Gemini Flash)
│   ├── detect.go          # Auto-detection (same pattern as embedding)
│   ├── cache.go           # Content hash cache (BadgerDB)
│   └── prompts.go         # System prompts for topic extraction / image description
├── faces/
│   ├── detector.go        # OpenCV DNN face detection + SFace embedding + YOLO objects
│   ├── store.go           # BadgerDB storage for embeddings, clusters, labels (face.db)
│   ├── cluster.go         # DBSCAN clustering with HNSW-accelerated neighbor queries
│   ├── models.go          # ONNX model download + management
│   ├── coco_labels.go     # COCO class name mapping for YOLO
│   └── detector_test.go   # Tests (build tag: faces)
├── cli/
│   └── faces.go           # CLI commands: faces scan, clusters, label, search, unlabeled
├── parser/
│   ├── generic/
│   │   ├── classifier.go  # FileClass (text/image/skip), exclude-only filtering
│   │   ├── parser.go      # GenericParser: Document nodes, directory hierarchy, topic nodes
│   │   ├── text.go        # Text extraction (plain, SVG, CSV, JSON, etc.)
│   │   ├── image.go       # Image reading, downscaling, metadata extraction
│   │   ├── directory.go   # Directory hierarchy node creation (lazy, deduped)
│   │   ├── topics.go      # Topic node creation, normalization, deduplication
│   │   └── parser_test.go # Tests
│   └── registry.go        # Add SetFallback(), SetExcludeExtensions(), fallback field
├── graph/
│   └── schema.go          # Add NodeDirectory, NodeTopic, NodePerson, EdgeHasTopic, EdgeAppearsIn
├── linker/
│   ├── documents.go       # New linkDocuments phase (+ topic → code entity linking)
│   └── linker.go          # Add "documents" to Phases()
├── vectorstore/
│   └── chunk.go           # Add NodeDirectory, NodeTopic, NodePerson to EmbeddableTypes
├── config/
│   └── config.go          # Add DocsConfig + FacesConfig structs
└── indexer/
    └── indexer.go          # No changes (generic parser integrates via registry)
```

## Implementation Phases

### Phase A: Text files + graph structure (no LLM dependency)

1. `internal/graph/schema.go` — add `NodeDirectory`, `NodeTopic`, `EdgeHasTopic` constants
2. `internal/parser/generic/classifier.go` — file classification (inclusive, exclude-only)
3. `internal/parser/generic/text.go` — text extraction per format (SVG strip tags, CSV headers, plain passthrough)
4. `internal/parser/generic/parser.go` — GenericParser creating Document nodes with raw text + directory hierarchy nodes
5. `internal/parser/registry.go` — add fallback support + exclude extensions
6. `internal/cli/sync.go` + `internal/cli/watch.go` — register generic parser as fallback
7. `internal/config/config.go` — add DocsConfig (exclude_extensions only, no allowlist, no size limits)
8. `internal/linker/documents.go` — cross-reference linker phase
9. `internal/vectorstore/chunk.go` — add `NodeDirectory`, `NodeTopic`, `NodePerson` to `EmbeddableTypes`

**Result**: all non-code text files indexed in graph with directory hierarchy + RAG with raw text embeddings. No LLM needed.

### Phase B: Docs LLM provider + topic extraction + topic nodes

1. `internal/docs/provider.go` — Provider interface (returns `*ExtractionResult`) + registry
2. `internal/docs/ollama.go` — Ollama implementation (multimodal, JSON output)
3. `internal/docs/vertex.go` — Vertex AI implementation (Gemini Flash)
4. `internal/docs/detect.go` — auto-detection
5. `internal/docs/prompts.go` — extraction prompts (with large-file summarization instruction)
6. `internal/docs/cache.go` — content hash caching
7. Update GenericParser to:
   a. Use docs provider for topic extraction
   b. Create `NodeTopic` nodes from `ExtractionResult.Topics`
   c. Create `EdgeHasTopic` edges from document → topics
   d. Deduplicate topics by normalized name
8. Update sync/watch to create docs provider and pass to GenericParser
9. `internal/config/config.go` — add provider/model fields to DocsConfig
10. Update cross-reference linker to also connect topics to code entities

**Result**: LLM-enriched topic extraction for text files. Topics as first-class graph nodes. Much better embeddings.

### Phase C: Image support

1. `internal/parser/generic/image.go` — image reading, downscaling (always, no size skip), metadata extraction
2. Update classifier to handle image extensions
3. Update GenericParser to call `DescribeImage` for image files → `ExtractionResult` → topic nodes
4. Dependencies: `golang.org/x/image/draw` for downscaling, `golang.org/x/image/webp` for WebP decode

**Result**: images indexed with vision model descriptions + topic nodes.

### Phase D: Skill + docs updates

1. Update `skills/codeeagle/SKILL.md` — document non-code file support, new node types (`NodeDirectory`, `NodeTopic`), new edge types (`EdgeHasTopic`)
2. Update `CLAUDE.md` — architecture section, config section
3. Bump plugin version

### Phase E: Face detection, recognition, and object detection

Requires: Phase C (image support) complete. Requires system OpenCV 4.x installation.

1. `internal/faces/models.go` — ONNX model download + path management
2. `internal/faces/detector.go` — OpenCV DNN face detection (YuNet) + face embedding (SFace) + YOLO object detection
3. `internal/faces/store.go` — BadgerDB store for embeddings, bounding boxes, clusters, labels (`face.db`)
4. `internal/faces/cluster.go` — DBSCAN clustering with HNSW-accelerated neighbor queries
5. `internal/faces/coco_labels.go` — YOLO class ID → name mapping
6. `internal/cli/faces.go` — CLI commands (`faces scan`, `faces clusters`, `faces label`, `faces search`, `faces unlabeled`)
7. `internal/graph/schema.go` — add `NodePerson`, `EdgeAppearsIn`
8. `internal/config/config.go` — add `FacesConfig` struct
9. Update GenericParser image flow — run face/object detection after image indexing (when `faces.enabled`)
10. Update `internal/vectorstore/chunk.go` — add `NodePerson` to `EmbeddableTypes`
11. `Makefile` — add `build-faces` target with `-tags faces`
12. All `internal/faces/*.go` files use `//go:build faces` build tag

**Result**: face detection, clustering, labeling, person nodes in graph, YOLO object labels as topics. Build without OpenCV still works (default `make build`).

## Verification

### Phase A
```bash
make build && make test
codeeagle sync --full  # on a repo with .txt, .csv, .svg files
codeeagle query --type Document  # should show non-code files
codeeagle query --type Directory  # should show directory hierarchy
codeeagle query edges --node "docs" --type Contains  # directory children
codeeagle rag "database schema"  # should find .svg or .csv files
codeeagle query edges --node "changelog.txt" --type Documents  # cross-references
```

### Phase B
```bash
# With Ollama qwen3.5:27b running:
codeeagle sync --full
codeeagle query --type Topic  # should show extracted topics
codeeagle query edges --node "authentication" --type HasTopic  # topic → document connections
codeeagle rag "authentication flow"  # topic-extracted embeddings
codeeagle rag "provider pattern" --no-docs  # should NOT find docs, only code
codeeagle rag "provider pattern"  # should find both docs AND code
```

### Phase C
```bash
# With Ollama qwen3.5:9b running:
codeeagle sync --full  # processes images (downscaled automatically)
codeeagle query --type Document --name "*.png"  # image documents
codeeagle query --type Topic  # topics from images should appear too
codeeagle rag "architecture diagram"  # should find described images
```

### Phase E
```bash
# Requires: OpenCV 4.x installed, built with `make build-faces`
codeeagle faces scan                          # detect faces + objects in all images
codeeagle faces clusters                      # list clusters — should show grouped faces
codeeagle faces label 1 "Dad"                 # name a cluster
codeeagle faces label 2 "Mom"                 # name another
codeeagle faces search "Dad"                  # find all photos with Dad
codeeagle query --type Person                 # should show Dad, Mom
codeeagle query edges --node "Dad" --type AppearsIn  # images with Dad
codeeagle query --type Topic --name "dog"     # YOLO-detected topics
codeeagle rag "photos with Dad at the beach"  # semantic search combining person + topic
```

## Open Questions

1. **Concurrency for LLM calls**: during initial sync, many files need processing. Should we batch or parallelize LLM calls? Ollama is single-threaded by default, so serial is fine. Vertex AI could benefit from concurrent calls (with a semaphore).

2. **Incremental sync for docs**: when a text file changes, should we re-extract topics? Yes — the hash cache handles this. Only changed files trigger LLM calls.

3. **Existing Markdown parser overlap**: the Markdown parser already creates Document nodes. Should the generic parser skip `.md` files? Yes — the generic parser is a fallback, only used when no language parser matches. Markdown already has a parser, so it won't be affected.

4. **Topic granularity**: how many topics should the LLM extract per file? Too few = coarse; too many = noisy. The prompt should guide toward 3-8 abstract topics per file. Topic deduplication by normalized name keeps the graph from exploding.

5. **Topic node embeddings**: should `NodeTopic` nodes themselves be embedded in the vector index? Yes — this allows semantic search to find topics directly ("what topics relate to security?"), then traverse edges to find documents and code. `EmbeddableText` for a topic node would return the topic name + any connected document summaries.

6. **Directory node cleanup**: when a file is deleted, should orphaned (empty) directory nodes be pruned? This can be deferred — empty directories don't cause harm and pruning adds complexity.

7. **Face re-clustering**: when new photos are added, should clustering re-run automatically? Current design: re-run `codeeagle faces scan` manually (or at end of sync when `faces.enabled`). Incremental clustering (adding new faces to existing clusters without re-clustering everything) is a future optimization.

8. **Face cluster merging**: two clusters may represent the same person (different angles, lighting, ages). Should there be a `codeeagle faces merge <cluster1> <cluster2>` command? Useful but can be deferred.

9. **EXIF metadata extraction**: personal photos contain GPS coordinates, camera info, and timestamps in EXIF data. Extracting this into `NodeDocument` properties (e.g., `location_lat`, `location_lon`, `taken_at`) would enable location-based and time-based queries. Natural extension but separate from face detection — could be Phase F.

10. **OpenCV version compatibility**: `gocv` targets specific OpenCV versions. Need to document and test minimum OpenCV version (4.5+ recommended for YuNet/SFace support). The DNN module's ONNX runtime compatibility may vary across versions.
