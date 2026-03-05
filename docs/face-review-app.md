# CodeEagle Desktop App (Wails + React)

**Parent spec**: [face-recognition-v2.md](face-recognition-v2.md)

## Overview

A native desktop app that serves as the primary GUI for CodeEagle. Built with **Wails v2** (Go backend + React frontend in a system webview). Launched via `codeeagle app`.

### Features

| Feature | Description |
|---------|-------------|
| **Face Review** | Review clusters, create Persons, correct auto-accepted assignments (DnD) |
| **Search** | RAG-powered natural language search over the knowledge graph and photo library |
| **Ask** | Conversational interface to the ask/plan/design/review agents |

### Why a Full App (Not Just Review)

The review UI needs rich interactivity (DnD, thumbnails, dialogs). Once we have a desktop shell with a React frontend talking to the Go backend, adding search and ask is incremental — they share the same graph store, vector index, and agent infrastructure.

## Architecture

```
codeeagle app
  │
  ├── Go Backend (Wails-bound methods)
  │   ├── Face Review   → graph store (Persons), face.db (detections)
  │   ├── Search (RAG)  → vector store (HNSW), graph store
  │   └── Ask (Agents)  → planner/designer/reviewer/asker agents
  │
  └── React Frontend (Vite + TypeScript)
      ├── Face Review page  → DnD, thumbnails, Person management
      ├── Search page       → query input, results with face/doc cards
      └── Ask page          → chat interface, agent selection
```

### Why Wails + React

| | Wails+React | Wails+Vanilla JS | Electron+React | Localhost HTTP |
|---|---|---|---|---|
| Native feel | Yes (system webview) | Yes | Yes | No (browser tab) |
| DnD quality | Excellent (react-dnd) | Manual HTML5 DnD | Excellent | Excellent |
| Component reuse | Yes (shared UI kit) | No | Yes | Yes |
| Binary size | +~5MB | +~5MB | +~100MB | +0 |
| State mgmt | React hooks/context | Manual DOM | React hooks | Manual |
| Build tooling | Vite (fast) | None | Webpack | None |
| Multi-page app | React Router | Manual | React Router | Manual |

### System Requirements

- **Linux**: `libgtk-3-dev`, `libwebkit2gtk-4.0-dev`
- **macOS**: WebKit (bundled)
- **Windows**: WebView2 (bundled with Windows 10+)
- **Build**: Node.js 18+ (for React build, not bundled in output)

## Feature 1: Face Review

### Two Modes

**Mode A: Initial Setup (no Persons exist)**

After the first `codeeagle faces scan`, faces are in unsupervised clusters.

1. App shows face clusters sorted by size (largest first)
2. Each cluster shows face thumbnails sorted by date
3. User clicks "Create Person" on a cluster → dialog for name, birth date, relationship
4. All faces in that cluster become seed exemplars for the new Person
5. User can drag faces between clusters to fix grouping before creating Persons
6. Uninteresting clusters can be dismissed ("Not Interested")
7. "Save" commits Persons + seed exemplars to graph store

**Mode B: Post-Bootstrap Review (Persons exist)**

After `codeeagle faces bootstrap`, shows auto-accepted assignments for correction.

1. Shows Persons with their assigned faces (seed + auto-accepted)
2. "Pending Review" section shows faces between reject and auto-accept thresholds
3. User drags misclassified faces to correct Person
4. Can create new Persons from pending/unrecognized faces
5. "Save" updates assignments, rebuilds exemplar pool

### UI Layout

```
┌─────────────────────────────────────────────────────────────────────┐
│  CodeEagle    [Face Review]  [Search]  [Ask]         [Save] [Exit] │
├─────────────────────────────────────────────────────────────────────┤
│ [Filter: All ▾] [Sort: By Date ▾] [Search: ________]              │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  ┌─ Hamza (child, b. 2010) ── 208 faces ── [Edit] ──────────────┐ │
│  │ ┌────────┐ ┌────────┐ ┌────────┐ ┌────────┐ ┌────────┐       │ │
│  │ │  face  │ │  face  │ │  face  │ │  face  │ │  face  │  ...  │ │
│  │ │ 2010   │ │ 2014   │ │ 2018   │ │ 2022   │ │ 2024   │       │ │
│  │ │ ● seed │ │ ○ auto │ │ ○ auto │ │ ○ auto │ │ ○ auto │       │ │
│  │ └────────┘ └────────┘ └────────┘ └────────┘ └────────┘       │ │
│  └───────────────────────────────────────────────────────────────┘ │
│                                                                     │
│  ┌─ Pending Review ── 358 faces ─────────────────────────────────┐ │
│  │ ┌────────┐ ┌────────┐ ┌────────┐ ┌────────┐                   │ │
│  │ │  face  │ │  face  │ │  face  │ │  face  │  ← drag to       │ │
│  │ │ 2023   │ │ 2024   │ │ 2022   │ │ 2024   │    Person above  │ │
│  │ │ c=0.42 │ │ c=0.38 │ │ c=0.51 │ │ c=0.33 │                   │ │
│  │ └────────┘ └────────┘ └────────┘ └────────┘                   │ │
│  │                                     [+ Create New Person]      │ │
│  └───────────────────────────────────────────────────────────────┘ │
│                                                                     │
│  ┌─ Unrecognized ── 533 faces ── [Show/Hide] ───────────────────┐ │
│  │ (collapsed by default)                                         │ │
│  └───────────────────────────────────────────────────────────────┘ │
├─────────────────────────────────────────────────────────────────────┤
│ Status: 3 unsaved changes │ 4271 faces, 10 persons                 │
└─────────────────────────────────────────────────────────────────────┘
```

### Face Thumbnail Card (React Component)

```tsx
<FaceCard
  imagePath="/path/to/img.jpg"
  faceIdx={0}
  date="2024-03"
  provenance="auto"       // seed | auto | manual
  confidence={0.62}
  personName="Hamza"
  draggable
/>
```

120x120px, date below, provenance dot (green=seed, blue=auto, yellow=manual). Hover tooltip: full path, exact date, event, confidence, top-3 KNN matches.

### Drag-and-Drop

Use **@dnd-kit/core** (React DnD library):
- `<DndContext>` wraps the review page
- Face cards are `<Draggable>` items
- Person sections and "Create New Person" are `<Droppable>` zones
- Visual feedback: drop target highlights, drag overlay shows face thumbnail
- On drop: call Go backend `AssignFaceToPerson()` via Wails binding

### Person Creation Dialog

```
┌─────────────────────────────────────┐
│ Create Person                       │
├─────────────────────────────────────┤
│ Name:         [________________]    │
│ Birth Date:   [____-__-__]          │
│ Relationship: [Child        ▾]      │
│               (child, spouse,       │
│                parent, in-law,      │
│                sibling, friend,     │
│                other)               │
│ Notes:        [________________]    │
├─────────────────────────────────────┤
│              [Cancel]  [Create]     │
└─────────────────────────────────────┘
```

## Feature 2: Search

### UI Layout

```
┌─────────────────────────────────────────────────────────────────────┐
│  CodeEagle    [Face Review]  [Search]  [Ask]                       │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │ 🔍 photos of Hamza at the beach                     [Search]│   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                     │
│  Filters: [People ▾] [Date Range ▾] [Event Type ▾] [Scene ▾]      │
│                                                                     │
│  Results (23 images):                                               │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐              │
│  │          │ │          │ │          │ │          │              │
│  │  [img]   │ │  [img]   │ │  [img]   │ │  [img]   │              │
│  │          │ │          │ │          │ │          │              │
│  │ Beach    │ │ Beach    │ │ Pool     │ │ Lake     │              │
│  │ 2022-06  │ │ 2023-07  │ │ 2024-08  │ │ 2021-05  │              │
│  │ Hamza,   │ │ Hamza    │ │ Hamza,   │ │ Hamza,   │              │
│  │ Mahdi    │ │          │ │ Mahdi    │ │ Sarah    │              │
│  └──────────┘ └──────────┘ └──────────┘ └──────────┘              │
│                                                                     │
│  Also found in code/docs:                                           │
│  ├── internal/faces/detector.go (face detection implementation)    │
│  └── docs/facescan-spec.md (face scan specification)               │
└─────────────────────────────────────────────────────────────────────┘
```

### Backend

Search uses the existing RAG infrastructure:
- `VectorStore.Search()` for semantic search across code, docs, and image descriptions
- Graph queries for Person → image lookups
- Image metadata filters (date, event type, scene)

```go
// Search combines RAG + face + metadata search
func (a *App) Search(query string, filters SearchFilters) SearchResults

type SearchFilters struct {
    People    []string  `json:"people"`     // Person names to filter by
    DateFrom  string    `json:"date_from"`
    DateTo    string    `json:"date_to"`
    EventType string    `json:"event_type"`
    Scene     string    `json:"scene"`
}

type SearchResults struct {
    Images []ImageResult `json:"images"`
    Code   []CodeResult  `json:"code"`   // from RAG
    Docs   []DocResult   `json:"docs"`   // from RAG
}

type ImageResult struct {
    Path        string   `json:"path"`
    Thumbnail   []byte   `json:"thumbnail"`   // downscaled JPEG
    DateTaken   string   `json:"date_taken"`
    EventName   string   `json:"event_name"`
    Description string   `json:"description"` // LLM-generated
    People      []string `json:"people"`      // Person names appearing
    Score       float32  `json:"score"`       // relevance
}
```

## Feature 3: Ask (Agent Chat)

### UI Layout

```
┌─────────────────────────────────────────────────────────────────────┐
│  CodeEagle    [Face Review]  [Search]  [Ask]                       │
├───────────────────────────┬─────────────────────────────────────────┤
│ Agent: [Planner ▾]       │                                         │
│                           │  "What services would be affected      │
│ History:                  │   if I change the auth middleware?"     │
│ ├── Auth impact analysis  │                                         │
│ ├── API review            │  Based on the knowledge graph, these   │
│ └── Test coverage check   │  services depend on auth middleware:   │
│                           │                                         │
│                           │  1. **api-gateway** — imports auth     │
│                           │     package, 3 endpoints use it        │
│                           │  2. **user-service** — calls auth      │
│                           │     validation on all routes           │
│                           │  3. **admin-panel** — uses auth        │
│                           │     decorators on 12 endpoints         │
│                           │                                         │
│                           │  Impact: 3 services, 47 files,         │
│                           │  18 endpoints                          │
│                           │                                         │
│                           │  ┌──────────────────────────────────┐  │
│                           │  │ Ask a follow-up...        [Send] │  │
│                           │  └──────────────────────────────────┘  │
└───────────────────────────┴─────────────────────────────────────────┘
```

### Backend

Uses the existing agent infrastructure:

```go
// Ask sends a query to the selected agent
func (a *App) Ask(agentType string, query string) (string, error)

// AskStream sends a query and streams the response (Wails events)
func (a *App) AskStream(agentType string, query string) error
// Emits "agent:chunk" events with partial response text

// GetAgentTypes returns available agents
func (a *App) GetAgentTypes() []AgentInfo

type AgentInfo struct {
    ID          string `json:"id"`          // "planner", "designer", "reviewer", "asker"
    Name        string `json:"name"`        // display name
    Description string `json:"description"` // what this agent does
}
```

Streaming uses Wails events (`runtime.EventsEmit`) to push chunks to the React frontend, which renders them as they arrive (markdown rendering via `react-markdown`).

## Go Backend API (Complete)

```go
type App struct {
    ctx         context.Context
    graphStore  graph.Store
    faceStore   *faces.Store
    vectorStore *vectorstore.VectorStore
    config      *config.Config
    changeLog   *ChangeLog
    thumbCache  *lru.Cache
}

// Wails lifecycle
func (a *App) Startup(ctx context.Context)
func (a *App) Shutdown(ctx context.Context)

// --- Face Review ---
func (a *App) GetMode() string                    // "setup" or "review"
func (a *App) GetClusters() []ClusterInfo
func (a *App) CreatePersonFromCluster(clusterID int, name, birthDate, relationship string) (*Person, error)
func (a *App) DismissCluster(clusterID int) error
func (a *App) MoveFaceBetweenClusters(faceID string, targetClusterID int) error
func (a *App) GetPersons() []PersonInfo
func (a *App) GetFacesForPerson(personID string, page, pageSize int) []FaceInfo
func (a *App) GetPendingFaces(page, pageSize int) []FaceInfo
func (a *App) GetUnrecognizedFaces(page, pageSize int) []FaceInfo
func (a *App) AssignFaceToPerson(faceID, personID string) error
func (a *App) UnassignFace(faceID string) error
func (a *App) CreatePerson(name, birthDate, relationship string) (*Person, error)
func (a *App) EditPerson(personID, name, birthDate, relationship string) error
func (a *App) MergePersons(targetID string, sourceIDs []string) error
func (a *App) GetFaceThumbnail(imagePath string, faceIdx int) []byte
func (a *App) GetImageThumbnail(imagePath string, maxSize int) []byte
func (a *App) SaveChanges() error
func (a *App) DiscardChanges()
func (a *App) GetReviewStats() ReviewStats

// --- Search ---
func (a *App) Search(query string, filters SearchFilters) SearchResults
func (a *App) SearchFaces(personName string, dateFrom, dateTo string) []ImageResult

// --- Ask (Agents) ---
func (a *App) GetAgentTypes() []AgentInfo
func (a *App) Ask(agentType, query string) (string, error)
func (a *App) AskStream(agentType, query string) error  // streams via Wails events

// --- Status & Navigation ---
func (a *App) GetStatus() AppStatus
func (a *App) GetUnseenReviewCount() int          // faces needing review since last session
func (a *App) MarkReviewSeen()                     // update last_review_timestamp
func (a *App) GetLandingPage() string              // "review" if unseen faces, else "search"
```

## Frontend Structure (React + TypeScript + Vite)

```
internal/faces/review/
├── app.go               # Wails App struct, lifecycle
├── face_handlers.go     # Face review Go methods
├── search_handlers.go   # Search Go methods
├── ask_handlers.go      # Agent chat Go methods
├── thumbnail.go         # Face/image thumbnail generation + LRU cache
├── changelog.go         # In-memory change tracking + atomic save
├── frontend/
│   ├── index.html
│   ├── package.json     # React, @dnd-kit/core, react-markdown, react-router-dom
│   ├── tsconfig.json
│   ├── vite.config.ts
│   ├── wailsjs/         # Auto-generated Wails bindings (Go→JS)
│   └── src/
│       ├── main.tsx             # App entry, router setup
│       ├── App.tsx              # Layout, nav tabs
│       ├── pages/
│       │   ├── FaceReview.tsx   # Face review page (Mode A + B)
│       │   ├── Search.tsx       # Search page
│       │   └── Ask.tsx          # Agent chat page
│       ├── components/
│       │   ├── FaceCard.tsx     # Draggable face thumbnail card
│       │   ├── PersonSection.tsx # Droppable person section with faces
│       │   ├── ClusterSection.tsx # Cluster view (Mode A)
│       │   ├── PersonDialog.tsx # Create/edit Person dialog
│       │   ├── SearchBar.tsx    # Search input with filters
│       │   ├── ImageGrid.tsx    # Search results grid
│       │   ├── ChatMessage.tsx  # Agent response bubble
│       │   └── NavBar.tsx       # Top navigation
│       ├── hooks/
│       │   ├── useFaceReview.ts # Face review state management
│       │   ├── useSearch.ts     # Search state
│       │   └── useAgent.ts      # Agent chat with streaming
│       └── types.ts             # TypeScript types matching Go structs
└── review_test.go       # Tests for Go backend methods
```

### Key Dependencies

```json
{
  "dependencies": {
    "react": "^18",
    "react-dom": "^18",
    "react-router-dom": "^6",
    "@dnd-kit/core": "^6",
    "@dnd-kit/sortable": "^8",
    "react-markdown": "^9"
  },
  "devDependencies": {
    "@vitejs/plugin-react": "^4",
    "typescript": "^5",
    "vite": "^5"
  }
}
```

### State Management

React Context + hooks (no Redux — app state is small enough):

```tsx
// FaceReviewContext — tracks Persons, faces, pending changes
// SearchContext — query, filters, results
// AgentContext — selected agent, chat history, streaming state
```

## Build Integration

### Build Commands

```bash
# Development (hot reload for frontend, live Go rebuild)
cd internal/faces/review && wails dev

# Production build
make build-faces
# Runs: cd internal/faces/review/frontend && npm install && npm run build
# Then: CGO_ENABLED=1 go build -tags faces -o bin/codeeagle ./cmd/codeeagle
```

### CLI Integration

```bash
codeeagle app                    # Launch desktop app
```

No `--page` flags. The app decides what to show on launch:

### Smart Landing Logic

On startup, the app evaluates state and lands on the right page:

```
1. If unseen pending review faces exist (new since last review session):
   → Land on Face Review with a notification badge/banner:
     "47 new faces need review since your last session"

2. Otherwise:
   → Land on Search (the most common ongoing use case)
```

**"Unseen" tracking**: The app stores a `last_review_timestamp` in the graph store. Faces with `AssignedAt` or `DetectedAt` after this timestamp are "unseen". The timestamp updates when the user visits the Face Review page (not on save — just on viewing).

**Notification badge**: The nav bar always shows a badge on "Face Review" if unseen faces exist, regardless of which page the user is on:

```
[Face Review (47)] [Search] [Ask]
```

This way the user is never surprised by a pile of unreviewed faces — they always know, and the app surfaces it without being intrusive.

## Implementation Plan

### Phase 1: Wails Scaffold + Face Review
1. Scaffold Wails app with React + TypeScript + Vite
2. Implement Go backend face review methods
3. Build FaceCard, PersonSection, ClusterSection components
4. Implement DnD with @dnd-kit
5. Person creation dialog
6. Change tracking + save/discard

### Phase 2: Search
1. Implement Go backend Search methods (RAG + face + metadata)
2. Build Search page with query input, filters, image grid
3. Image thumbnail generation for search results

### Phase 3: Ask (Agent Chat)
1. Implement Go backend Ask methods with Wails event streaming
2. Build chat UI with agent selector, message bubbles, markdown rendering
3. Chat history (in-memory, persisted to graph store optionally)

### Phase 4: Polish
1. Keyboard shortcuts (Ctrl+S save, Escape, Ctrl+F search)
2. Responsive layout for different window sizes
3. Pagination for large face sets
4. Loading states, error handling, empty states
5. Dark mode (system preference detection)

## Verification

```bash
# Build
CGO_ENABLED=1 go build -tags faces -o bin/codeeagle ./cmd/codeeagle

# First launch (no Persons) → lands on Face Review (Mode A)
codeeagle faces scan ~/Pictures/ImageServer/SomeAlbum/
codeeagle app
# Expected: lands on Face Review showing clusters, "Create Person" buttons

# After bootstrap → lands on Face Review with unseen badge
codeeagle faces bootstrap --auto-accept 0.55
codeeagle app
# Expected: lands on Face Review, banner "47 new faces need review"
# Nav shows: [Face Review (47)] [Search] [Ask]

# After reviewing → next launch lands on Search
codeeagle app
# Expected: lands on Search (no unseen faces), badge cleared

# Search
# Type: "photos of Hamza at the beach"
# Expected: image results with thumbnails, date, people tags

# Ask
# Navigate to Ask tab, select Planner
# Type: "What would change if I modify the auth middleware?"
# Expected: streaming response with impact analysis
```
