package indexer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
	"github.com/imyousuf/CodeEagle/internal/watcher"
	"github.com/imyousuf/CodeEagle/pkg/llm"
)

// IndexerConfig holds configuration for the Indexer.
type IndexerConfig struct {
	GraphStore     graph.Store
	ParserRegistry *parser.Registry
	WatcherConfig  *watcher.WatcherConfig
	Verbose        bool
	Logger         func(format string, args ...any) // optional logger, defaults to fmt.Fprintf(os.Stderr, ...)
	LLMClient      llm.Client                       // optional LLM client for auto-summarization
	AutoSummarize  bool                              // enable post-index LLM summarization
}

// IndexStats holds statistics about the indexing state.
type IndexStats struct {
	FilesIndexed  int       `json:"files_indexed"`
	NodesTotal    int64     `json:"nodes_total"`
	EdgesTotal    int64     `json:"edges_total"`
	LastIndexTime time.Time `json:"last_index_time"`
	Errors        []string  `json:"errors,omitempty"`
}

// Indexer orchestrates file parsing and knowledge graph updates.
type Indexer struct {
	store         graph.Store
	registry      *parser.Registry
	wcfg          *watcher.WatcherConfig
	matcher       *watcher.GitIgnoreMatcher
	verbose       bool
	log           func(format string, args ...any)
	llmClient     llm.Client
	autoSummarize bool

	mu           sync.Mutex
	filesIndexed int
	errors       []string
	lastIndex    time.Time
}

// NewIndexer creates a new Indexer with the given configuration.
func NewIndexer(cfg IndexerConfig) *Indexer {
	allPatterns := make([]string, 0)
	if cfg.WatcherConfig != nil {
		allPatterns = append(allPatterns, cfg.WatcherConfig.ExcludePatterns...)
		allPatterns = append(allPatterns, cfg.WatcherConfig.GitIgnorePatterns...)
	}

	var paths []string
	if cfg.WatcherConfig != nil {
		paths = cfg.WatcherConfig.Paths
	}

	matcher := watcher.NewGitIgnoreMatcher(paths, allPatterns)
	_ = matcher.LoadPatterns()

	logFn := cfg.Logger
	if logFn == nil {
		logFn = func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		}
	}

	return &Indexer{
		store:         cfg.GraphStore,
		registry:      cfg.ParserRegistry,
		wcfg:          cfg.WatcherConfig,
		matcher:       matcher,
		verbose:       cfg.Verbose,
		log:           logFn,
		llmClient:     cfg.LLMClient,
		autoSummarize: cfg.AutoSummarize,
	}
}

// IndexFile parses a single file and updates the knowledge graph.
// If no parser is registered for the file extension, it silently returns nil.
func (idx *Indexer) IndexFile(ctx context.Context, filePath string) error {
	ext := filepath.Ext(filePath)
	p, ok := idx.registry.GetByExtension(ext)
	if !ok {
		return nil // no parser for this extension
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read file %s: %w", filePath, err)
	}

	if idx.verbose {
		idx.log("Parsing %s (%s)...", filePath, p.Language())
	}

	result, err := p.ParseFile(filePath, content)
	if err != nil {
		return fmt.Errorf("parse file %s: %w", filePath, err)
	}

	// Delete old nodes for this file to support incremental updates.
	if err := idx.store.DeleteByFile(ctx, filePath); err != nil {
		return fmt.Errorf("delete old nodes for %s: %w", filePath, err)
	}

	// Add new nodes.
	for _, node := range result.Nodes {
		if err := idx.store.AddNode(ctx, node); err != nil {
			return fmt.Errorf("add node %s: %w", node.ID, err)
		}
	}

	// Add new edges.
	for _, edge := range result.Edges {
		if err := idx.store.AddEdge(ctx, edge); err != nil {
			return fmt.Errorf("add edge %s: %w", edge.ID, err)
		}
	}

	idx.mu.Lock()
	idx.filesIndexed++
	idx.lastIndex = time.Now()
	idx.mu.Unlock()

	if idx.verbose {
		idx.log("  -> %d nodes, %d edges", len(result.Nodes), len(result.Edges))
	}

	return nil
}

// IndexDirectory walks a directory tree and indexes all supported files.
func (idx *Indexer) IndexDirectory(ctx context.Context, dirPath string) error {
	if idx.verbose {
		idx.log("Scanning directory: %s", dirPath)
	}
	return filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible entries
		}

		// Check context cancellation.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Skip ignored directories.
		if info.IsDir() {
			if idx.matcher.Match(path) {
				if idx.verbose {
					idx.log("  Skipping directory: %s (excluded)", path)
				}
				return filepath.SkipDir
			}
			return nil
		}

		// Skip ignored files.
		if idx.matcher.Match(path) {
			return nil
		}

		if err := idx.IndexFile(ctx, path); err != nil {
			idx.mu.Lock()
			idx.errors = append(idx.errors, fmt.Sprintf("%s: %v", path, err))
			idx.mu.Unlock()
			// Continue indexing other files.
		}
		return nil
	})
}

// Start performs an initial full index of all configured paths, then starts
// watching for changes and processing them incrementally. It blocks until
// the context is cancelled.
func (idx *Indexer) Start(ctx context.Context) error {
	if idx.wcfg == nil {
		return fmt.Errorf("watcher config is required")
	}

	// Initial full index.
	for _, path := range idx.wcfg.Paths {
		if err := idx.IndexDirectory(ctx, path); err != nil {
			return fmt.Errorf("initial index of %s: %w", path, err)
		}
	}

	// Run auto-summarization if configured.
	if idx.autoSummarize && idx.llmClient != nil {
		idx.runSummarization(ctx)
	}

	// Start file watcher.
	w, err := watcher.NewWatcher(*idx.wcfg)
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer w.Close()

	events, err := w.Start(ctx)
	if err != nil {
		return fmt.Errorf("start watcher: %w", err)
	}

	// Process events until context is cancelled.
	for {
		select {
		case <-ctx.Done():
			return nil
		case evt, ok := <-events:
			if !ok {
				return nil
			}
			idx.handleEvent(ctx, evt)
		}
	}
}

func (idx *Indexer) handleEvent(ctx context.Context, evt watcher.Event) {
	switch evt.Op {
	case watcher.Create, watcher.Write:
		if err := idx.IndexFile(ctx, evt.Path); err != nil {
			idx.mu.Lock()
			idx.errors = append(idx.errors, fmt.Sprintf("index %s: %v", evt.Path, err))
			idx.mu.Unlock()
		}
	case watcher.Remove, watcher.Rename:
		if err := idx.store.DeleteByFile(ctx, evt.Path); err != nil {
			idx.mu.Lock()
			idx.errors = append(idx.errors, fmt.Sprintf("delete %s: %v", evt.Path, err))
			idx.mu.Unlock()
		}
	}
}

// Stats returns current indexing statistics.
func (idx *Indexer) Stats() IndexStats {
	idx.mu.Lock()
	stats := IndexStats{
		FilesIndexed:  idx.filesIndexed,
		LastIndexTime: idx.lastIndex,
		Errors:        make([]string, len(idx.errors)),
	}
	copy(stats.Errors, idx.errors)
	idx.mu.Unlock()

	// Get graph stats.
	ctx := context.Background()
	if gs, err := idx.store.Stats(ctx); err == nil {
		stats.NodesTotal = gs.NodeCount
		stats.EdgesTotal = gs.EdgeCount
	}

	return stats
}

// runSummarization queries all nodes, groups them by top-level directory,
// and uses the LLM to generate per-service and codebase-wide summaries.
func (idx *Indexer) runSummarization(ctx context.Context) {
	if idx.verbose {
		idx.log("Running LLM-assisted summarization...")
	}

	allNodes, err := idx.store.QueryNodes(ctx, graph.NodeFilter{})
	if err != nil {
		idx.log("Summarization: failed to query nodes: %v", err)
		return
	}
	if len(allNodes) == 0 {
		return
	}

	var basePaths []string
	if idx.wcfg != nil {
		basePaths = idx.wcfg.Paths
	}

	summarizer := NewSummarizer(idx.llmClient, idx.store)

	// Summarize each top-level directory group as a "service".
	groups := GroupNodesByTopDir(allNodes, basePaths)
	for groupName, nodes := range groups {
		if idx.verbose {
			idx.log("  Summarizing group: %s (%d nodes)", groupName, len(nodes))
		}
		if err := summarizer.SummarizeService(ctx, groupName, nodes); err != nil {
			idx.log("Summarization of %s failed: %v", groupName, err)
		}
	}

	// Summarize overall patterns.
	if idx.verbose {
		idx.log("  Summarizing codebase patterns...")
	}
	if err := summarizer.SummarizePatterns(ctx, allNodes); err != nil {
		idx.log("Pattern summarization failed: %v", err)
	}

	if idx.verbose {
		idx.log("Summarization complete.")
	}
}
