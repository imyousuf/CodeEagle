//go:build app

package app

import (
	"context"
	"sync"

	"github.com/imyousuf/CodeEagle/internal/agents"
	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/vectorstore"
	"github.com/imyousuf/CodeEagle/pkg/llm"
)

// App is the Wails-bound application struct that provides the Go backend
// for the desktop app's Search and Ask features.
type App struct {
	ctx         context.Context
	cfg         *config.Config
	graphStore  graph.Store
	vectorStore *vectorstore.VectorStore
	llmClient   llm.Client
	ctxBuilder  *agents.ContextBuilder
	repoPaths   []string
	branch      string
	agentMu     sync.Mutex // serializes agent calls
}

// NewApp creates a new App with all required backend dependencies.
func NewApp(
	cfg *config.Config,
	graphStore graph.Store,
	vectorStore *vectorstore.VectorStore,
	llmClient llm.Client,
	ctxBuilder *agents.ContextBuilder,
	repoPaths []string,
	branch string,
) *App {
	return &App{
		cfg:         cfg,
		graphStore:  graphStore,
		vectorStore: vectorStore,
		llmClient:   llmClient,
		ctxBuilder:  ctxBuilder,
		repoPaths:   repoPaths,
		branch:      branch,
	}
}

// Startup is called by Wails when the app window is ready.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
}

// Shutdown is called by Wails when the app is closing.
func (a *App) Shutdown(_ context.Context) {
	if a.llmClient != nil {
		a.llmClient.Close()
	}
	if a.vectorStore != nil {
		a.vectorStore.Close()
	}
	if a.graphStore != nil {
		a.graphStore.Close()
	}
}
