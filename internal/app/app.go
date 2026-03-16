//go:build app

package app

import (
	"context"
	"fmt"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/imyousuf/CodeEagle/internal/agents"
	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/vectorstore"
	"github.com/imyousuf/CodeEagle/pkg/llm"
)

// LLMClientFactory creates an LLM client from config. Injected by app_cmd.go
// so the app package doesn't need to import provider-specific packages.
type LLMClientFactory func(cfg *config.Config) (llm.Client, error)

// SyncFunction performs a full sync of the knowledge graph. Injected by
// app_cmd.go so the app package doesn't need to import parser packages.
type SyncFunction func(ctx context.Context, cfg *config.Config, repoPaths []string, full bool, logFn func(format string, args ...any), warnFn func(format string, args ...any)) error

// EventEmitter emits named events to the frontend. In production this wraps
// runtime.EventsEmit; in tests a no-op implementation can be used.
type EventEmitter func(event string, data ...any)

// App is the Wails-bound application struct that provides the Go backend
// for the desktop app's Search, Ask, Sync, and Settings features.
// Resources (graph store, vector store, LLM client) are opened per-request
// and closed when the request completes — no persistent state to go stale.
type App struct {
	ctx       context.Context
	cfg       *config.Config
	repoPaths []string
	branch    string

	llmFactory LLMClientFactory
	syncFunc   SyncFunction
	emit       EventEmitter

	agentMu sync.Mutex   // serializes agent calls
	syncMu  sync.RWMutex // write-locked during sync
	syncing bool         // quick check for sync state

	faceStoreOnce sync.Once // guards lazy face store init
	faceStoreInst any       // *faces.Store (typed in faces_handlers.go)
	faceStoreErr  error     // cached error from face store init

	shutdownHooks []func() // cleanup functions called on Shutdown
}

// NewApp creates a new App. No resources are opened at construction time.
func NewApp(cfg *config.Config, repoPaths []string, branch string, llmFactory LLMClientFactory, syncFunc SyncFunction) *App {
	return &App{
		cfg:        cfg,
		repoPaths:  repoPaths,
		branch:     branch,
		llmFactory: llmFactory,
		syncFunc:   syncFunc,
		emit:       func(string, ...any) {}, // no-op until Startup
	}
}

// Startup is called by Wails when the app window is ready.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	a.emit = func(event string, data ...any) {
		runtime.EventsEmit(ctx, event, data...)
	}
}

// Shutdown is called by Wails when the app is closing.
func (a *App) Shutdown(_ context.Context) {
	for _, fn := range a.shutdownHooks {
		fn()
	}
}

// graphResources holds a graph store opened for the duration of a request.
type graphResources struct {
	store      *embedded.BranchStore
	ctxBuilder *agents.ContextBuilder
	branch     string
}

// openGraph opens the graph store for a single request. The caller must call
// the returned close function when done. Returns an error if a sync is in
// progress. Uses read-write mode because BadgerDB's read-only mode fails when
// vlog files need compaction or are missing.
func (a *App) openGraph() (*graphResources, func(), error) {
	if a.syncing {
		return nil, nil, fmt.Errorf("sync in progress — please wait")
	}

	store, branch, err := embedded.OpenReadWrite(a.cfg, a.repoPaths, "")
	if err != nil {
		return nil, nil, err
	}

	gr := &graphResources{
		store:      store,
		ctxBuilder: agents.NewContextBuilder(store, a.repoPaths...),
		branch:     branch,
	}
	return gr, func() { store.Close() }, nil
}

// openGraphRW is an alias for openGraph (both use read-write mode).
func (a *App) openGraphRW() (*graphResources, func(), error) {
	return a.openGraph()
}

// openVector opens a read-only vector store for a single request. Returns
// (nil, noop) if vector search is unavailable — this is not an error.
func (a *App) openVector(bs *embedded.BranchStore, branch string) (*vectorstore.VectorStore, func()) {
	noop := func() {}
	vs, _ := vectorstore.OpenReadOnlyWithLoad(a.cfg, bs, branch)
	if vs == nil {
		return nil, noop
	}
	return vs, func() { vs.Close() }
}

// openLLM creates an LLM client for a single request. The caller must call
// the returned close function when done.
func (a *App) openLLM() (llm.Client, func(), error) {
	if a.llmFactory == nil {
		return nil, nil, fmt.Errorf("no LLM client factory configured")
	}
	client, err := a.llmFactory(a.cfg)
	if err != nil {
		return nil, nil, err
	}
	return client, func() { client.Close() }, nil
}
