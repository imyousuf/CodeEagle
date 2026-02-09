package indexer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	RepoRoots      []string // repository root paths for abs→rel path conversion
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
	repoRoots     []string
	verbose       bool
	log           func(format string, args ...any)
	llmClient     llm.Client
	autoSummarize bool

	mu           sync.Mutex
	filesIndexed int
	errors       []string
	lastIndex    time.Time
	changedFiles map[string]struct{} // tracks relative paths of files changed since last reset
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
		repoRoots:     cfg.RepoRoots,
		verbose:       cfg.Verbose,
		log:           logFn,
		llmClient:     cfg.LLMClient,
		autoSummarize: cfg.AutoSummarize,
		changedFiles:  make(map[string]struct{}),
	}
}

// Store returns the underlying graph store used by this Indexer.
func (idx *Indexer) Store() graph.Store {
	return idx.store
}

// HasChanges returns true if any files have been indexed since the Indexer was created.
func (idx *Indexer) HasChanges() bool {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	return len(idx.changedFiles) > 0
}

// ChangedFiles returns a copy of the relative paths of files that were indexed.
func (idx *Indexer) ChangedFiles() []string {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	files := make([]string, 0, len(idx.changedFiles))
	for f := range idx.changedFiles {
		files = append(files, f)
	}
	return files
}

// toRelativePath converts an absolute file path to a path relative to the
// first matching repo root. If no repo root matches, the path is returned as-is.
func (idx *Indexer) toRelativePath(absPath string) string {
	for _, root := range idx.repoRoots {
		rel, err := filepath.Rel(root, absPath)
		if err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return absPath
}

// IndexFile parses a single file and updates the knowledge graph.
// filePath must be an absolute path (for reading from disk). It is converted
// to a relative path (relative to repo roots) before passing to the parser
// and graph store.
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

	relPath := idx.toRelativePath(filePath)

	if idx.verbose {
		idx.log("Parsing %s (%s)...", relPath, p.Language())
	}

	result, err := p.ParseFile(relPath, content)
	if err != nil {
		return fmt.Errorf("parse file %s: %w", relPath, err)
	}

	// Classify nodes with architectural roles, design patterns, and layer tags.
	classifier := parser.NewClassifier()
	result = classifier.Classify(result)

	// Delete old nodes for this file to support incremental updates.
	if err := idx.store.DeleteByFile(ctx, relPath); err != nil {
		return fmt.Errorf("delete old nodes for %s: %w", relPath, err)
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
	idx.changedFiles[relPath] = struct{}{}
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

	dirStart := time.Now()
	startFiles := idx.filesIndexed
	fileCount := 0

	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
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

		fileCount++
		if idx.verbose && fileCount%100 == 0 {
			idx.log("  Progress: %d files indexed...", fileCount)
		}

		return nil
	})

	if idx.verbose {
		elapsed := time.Since(dirStart)
		newFiles := idx.filesIndexed - startFiles
		idx.log("  Directory complete: %s (%d files indexed in %s)", dirPath, newFiles, elapsed)
	}

	return err
}

// Start performs an initial full index of all configured paths, then starts
// watching for changes and processing them incrementally. It blocks until
// the context is cancelled.
func (idx *Indexer) Start(ctx context.Context) error {
	if idx.wcfg == nil {
		return fmt.Errorf("watcher config is required")
	}

	// Initial full index.
	indexStart := time.Now()
	for _, path := range idx.wcfg.Paths {
		if err := idx.IndexDirectory(ctx, path); err != nil {
			return fmt.Errorf("initial index of %s: %w", path, err)
		}
	}

	if idx.verbose {
		elapsed := time.Since(indexStart)
		stats := idx.Stats()
		idx.log("Initial indexing complete: %d files, %d nodes, %d edges in %s",
			stats.FilesIndexed, stats.NodesTotal, stats.EdgesTotal, elapsed)
	}

	// Run auto-summarization if configured (full index: all groups).
	if idx.autoSummarize && idx.llmClient != nil {
		idx.runSummarization(ctx, nil)
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
		relPath := idx.toRelativePath(evt.Path)
		if err := idx.store.DeleteByFile(ctx, relPath); err != nil {
			idx.mu.Lock()
			idx.errors = append(idx.errors, fmt.Sprintf("delete %s: %v", relPath, err))
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

// RunSummarization runs LLM-assisted summarization if auto-summarize is enabled
// and an LLM client is available. Safe to call externally after sync operations.
// It scopes summarization to groups affected by changed files.
func (idx *Indexer) RunSummarization(ctx context.Context) {
	if idx.autoSummarize && idx.llmClient != nil {
		idx.runSummarization(ctx, idx.ChangedFiles())
	}
}

// runSummarization queries all nodes, groups them by top-level directory,
// and uses the LLM to generate per-service and codebase-wide summaries.
// If changedFiles is non-nil, only groups containing changed files are summarized.
// Pass nil to summarize all groups (e.g., after a full index).
func (idx *Indexer) runSummarization(ctx context.Context, changedFiles []string) {
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

	// Compute affected groups from changed files.
	var affectedGroups map[string]struct{}
	if changedFiles != nil {
		affectedGroups = make(map[string]struct{})
		for _, fp := range changedFiles {
			parts := strings.SplitN(fp, string(filepath.Separator), 2)
			if len(parts) > 0 && parts[0] != "" {
				affectedGroups[parts[0]] = struct{}{}
			} else {
				affectedGroups["(root)"] = struct{}{}
			}
		}
	}

	summarizer := NewSummarizer(idx.llmClient, idx.store, idx.log, idx.verbose)

	// Summarize each top-level directory group as a "service".
	groups := GroupNodesByTopDir(allNodes, basePaths)
	for groupName, nodes := range groups {
		// Skip groups unaffected by changes (when scoping is active).
		if affectedGroups != nil {
			if _, ok := affectedGroups[groupName]; !ok {
				if idx.verbose {
					idx.log("  Skipping unchanged group: %s", groupName)
				}
				continue
			}
		}
		if idx.verbose {
			idx.log("  Summarizing group: %s (%d nodes)", groupName, len(nodes))
		}
		if err := summarizer.SummarizeService(ctx, groupName, nodes); err != nil {
			idx.log("Summarization of %s failed: %v", groupName, err)
		}
		// Architecture analysis per service group.
		if err := summarizer.SummarizeArchitecture(ctx, groupName, nodes); err != nil {
			idx.log("Architecture analysis of %s failed: %v", groupName, err)
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
