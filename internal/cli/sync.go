package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/indexer"
	"github.com/imyousuf/CodeEagle/internal/linker"
	"github.com/imyousuf/CodeEagle/internal/queue"
	"github.com/imyousuf/CodeEagle/pkg/llm"

	// Register LLM and embedding providers so their init() functions run.
	"github.com/imyousuf/CodeEagle/internal/docs"
	_ "github.com/imyousuf/CodeEagle/internal/embedding"
	_ "github.com/imyousuf/CodeEagle/internal/llm"
	"github.com/imyousuf/CodeEagle/internal/parser"
	csharpparser "github.com/imyousuf/CodeEagle/internal/parser/csharp"
	genericparser "github.com/imyousuf/CodeEagle/internal/parser/generic"
	"github.com/imyousuf/CodeEagle/internal/parser/golang"
	htmlparser "github.com/imyousuf/CodeEagle/internal/parser/html"
	"github.com/imyousuf/CodeEagle/internal/parser/java"
	"github.com/imyousuf/CodeEagle/internal/parser/javascript"
	makefileparser "github.com/imyousuf/CodeEagle/internal/parser/makefile"
	"github.com/imyousuf/CodeEagle/internal/parser/manifest"
	"github.com/imyousuf/CodeEagle/internal/parser/markdown"
	"github.com/imyousuf/CodeEagle/internal/parser/python"
	rubyparser "github.com/imyousuf/CodeEagle/internal/parser/ruby"
	rustparser "github.com/imyousuf/CodeEagle/internal/parser/rust"
	"github.com/imyousuf/CodeEagle/internal/parser/shell"
	"github.com/imyousuf/CodeEagle/internal/parser/terraform"
	"github.com/imyousuf/CodeEagle/internal/parser/typescript"
	yamlparser "github.com/imyousuf/CodeEagle/internal/parser/yaml"
	"github.com/imyousuf/CodeEagle/internal/watcher"
)

// SyncOption configures optional RunSync behavior.
type SyncOption func(*syncOptions)

type syncOptions struct {
	showProgress bool
}

// WithProgress enables progress reporting during sync.
func WithProgress() SyncOption {
	return func(o *syncOptions) { o.showProgress = true }
}

// RunSync performs the full sync pipeline: auto-import, parse, index, summarize,
// link, vector-index, cleanup, and stats. It is called by both the CLI command
// and the desktop app's sync handler.
//
// warnFn receives non-fatal warnings (e.g., provider detection failures).
// logFn receives progress messages.
func RunSync(cmdCtx context.Context, cfg *config.Config, paths []string, full, verboseMode bool, logFn func(format string, args ...any), warnFn func(format string, args ...any), opts ...SyncOption) error {
	// Open read-write store.
	store, currentBranch, err := embedded.OpenReadWrite(cfg, paths, "")
	if err != nil {
		return err
	}
	defer store.Close()

	// Auto-import if .CodeEagle.conf is available.
	if cfg.ProjectConf != nil && cfg.ProjectConfDir != "" {
		exportFilePath := config.ExportFilePath(cfg.ProjectConfDir, cfg.ProjectConf)
		statePath := cfg.ConfigDir + "/" + "sync.state"
		state, err := indexer.LoadSyncState(statePath)
		if err == nil {
			if err := indexer.AutoImportIfNeeded(cmdCtx, store, exportFilePath, state, logFn); err != nil {
				warnFn("Warning: auto-import failed: %v", err)
			} else {
				_ = state.Save(statePath)
			}
		}
	}

	// Build parser registry.
	registry := parser.NewRegistry()
	registry.Register(golang.NewParser())
	registry.Register(python.NewParser())
	registry.Register(typescript.NewParser())
	registry.Register(javascript.NewParser())
	registry.Register(java.NewParser())
	registry.Register(htmlparser.NewParser())
	registry.Register(markdown.NewParser())
	registry.Register(makefileparser.NewParser())
	registry.Register(shell.NewParser())
	registry.Register(terraform.NewParser())
	registry.Register(yamlparser.NewParser())
	registry.Register(rustparser.NewParser())
	registry.Register(rubyparser.NewParser())
	registry.Register(manifest.NewParser())
	registry.Register(csharpparser.NewParser())

	// Detect docs LLM provider for topic extraction.
	var docsProvider docs.Provider
	var docsCache *docs.Cache
	dp, dpErr := docs.DetectProvider(cfg)
	if dpErr != nil {
		warnFn("Warning: docs provider: %v", dpErr)
	}
	if dp != nil {
		docsProvider = dp
		logFn("[docs] Using %s (%s)", dp.Name(), dp.ModelName())
		cachePath := cfg.ConfigDir + "/docs.db"
		dc, dcErr := docs.OpenCache(cachePath)
		if dcErr != nil {
			warnFn("Warning: docs cache: %v", dcErr)
		} else {
			docsCache = dc
			defer docsCache.Close()
		}
	}

	// Register generic fallback parser for non-code files.
	registry.SetFallback(genericparser.NewGenericParser(cfg.Docs.ExcludeExtensions, docsProvider, docsCache, cfg.Docs.MaxImageRes))
	registry.SetExcludeExtensions(cfg.Docs.ExcludeExtensions)

	// Build watcher config for the matcher.
	wcfg := &watcher.WatcherConfig{
		Paths:           paths,
		ExcludePatterns: cfg.Watch.Exclude,
	}

	// Create LLM client if auto-summarize or auto-link is enabled.
	var llmClient llm.Client
	if cfg.Agents.AutoSummarize || cfg.Agents.AutoLink {
		c, err := createLLMClient(cfg)
		if err != nil {
			warnFn("Warning: LLM client creation failed: %v", err)
		} else {
			llmClient = c
			defer llmClient.Close()
		}
	}

	// Apply options.
	var sopts syncOptions
	for _, o := range opts {
		o(&sopts)
	}

	// Create indexer.
	idx := indexer.NewIndexer(indexer.IndexerConfig{
		GraphStore:     store,
		ParserRegistry: registry,
		WatcherConfig:  wcfg,
		RepoRoots:      paths,
		Verbose:        verboseMode,
		ShowProgress:   sopts.showProgress,
		Logger:         logFn,
		LLMClient:      llmClient,
		AutoSummarize:  cfg.Agents.AutoSummarize,
	})

	mode := "incremental"
	if full {
		mode = "full"
	}
	logFn("Syncing (%s) on branch %q...", mode, currentBranch)

	if err := indexer.SyncFiles(cmdCtx, idx, paths, cfg.ConfigDir, full, currentBranch); err != nil {
		return fmt.Errorf("sync: %w", err)
	}

	// Run LLM summarization if enabled, but only when files actually changed.
	if idx.HasChanges() {
		idx.RunSummarization(cmdCtx)
	} else if verboseMode {
		logFn("No files changed, skipping LLM summarization.")
	}

	// Run cross-service linker on full sync or when files changed.
	if idx.HasChanges() || full {
		var linkerLLM llm.Client
		if cfg.Agents.AutoLink {
			linkerLLM = llmClient
		}
		lnk := linker.NewLinker(store, linkerLLM, logFn, verboseMode)
		if err := lnk.RunAll(cmdCtx); err != nil {
			warnFn("Warning: linker failed: %v", err)
		}
	}

	// Queue-based enrichment for documents/images not yet processed.
	if idx.HasChanges() || full {
		if qErr := runQueueEnrichment(cmdCtx, cfg, store, docsProvider, docsCache, logFn, warnFn); qErr != nil {
			warnFn("Warning: queue enrichment: %v", qErr)
		}
	}

	// Run vector indexing if an embedding provider is available.
	if verboseMode {
		logFn("[vector] Detecting embedding provider...")
	}
	vs, vecErr := openVectorStore(cfg, store, currentBranch, logFn)
	if vecErr != nil {
		warnFn("Warning: vector store: %v", vecErr)
	}
	if vs != nil {
		defer vs.Close()
		if err := syncVectorIndex(vs, cfg, full, logFn); err != nil {
			warnFn("Warning: vector indexing failed: %v", err)
		}
	}

	// Cleanup stale branches.
	if len(cfg.Repositories) > 0 {
		statePath := cfg.ConfigDir + "/" + "sync.state"
		state, err := indexer.LoadSyncState(statePath)
		if err == nil {
			if err := indexer.CleanupStaleBranches(cmdCtx, store, cfg.Repositories[0].Path, state, logFn); err != nil {
				warnFn("Warning: branch cleanup failed: %v", err)
			}
			_ = state.Save(statePath)
		}
	}

	// Print stats.
	stats := idx.Stats()
	logFn("Sync complete: %d files indexed, %d nodes, %d edges",
		stats.FilesIndexed, stats.NodesTotal, stats.EdgesTotal)
	if len(stats.Errors) > 0 {
		logFn("  Errors: %d", len(stats.Errors))
	}

	return nil
}

func newSyncCmd() *cobra.Command {
	var full bool
	var exportGraph bool
	var importGraph bool
	var branch string
	var showProgress bool

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync the knowledge graph with file changes",
		Long: `Perform an on-demand sync of the knowledge graph.

By default, syncs incrementally using git diffs (or file modification times
for non-git directories). Use --full for a complete re-index.

Use --export to export the current branch's graph to a portable file, and
--import to import a previously exported graph. Use --branch to specify the
target branch for import.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if err := cfg.Validate(); err != nil {
				return fmt.Errorf("invalid config: %w", err)
			}

			if exportGraph && importGraph {
				return fmt.Errorf("cannot use --export and --import together")
			}

			// Handle export/import.
			if exportGraph || importGraph {
				return handleExportImport(cfg, exportGraph, branch, cmd.OutOrStdout())
			}

			// Normal sync — delegate to RunSync.
			paths := repoPaths(cfg)
			out := cmd.OutOrStdout()
			errOut := cmd.ErrOrStderr()
			logFn := func(format string, a ...any) {
				fmt.Fprintf(out, format+"\n", a...)
			}
			warnFn := func(format string, a ...any) {
				fmt.Fprintf(errOut, format+"\n", a...)
			}

			var syncOpts []SyncOption
			if showProgress {
				syncOpts = append(syncOpts, WithProgress())
			}
			return RunSync(ctx(cmd), cfg, paths, full, verbose, logFn, warnFn, syncOpts...)
		},
	}

	cmd.Flags().BoolVar(&full, "full", false, "full re-index of all files")
	cmd.Flags().BoolVar(&showProgress, "progress", false, "show sync progress")
	cmd.Flags().BoolVar(&exportGraph, "export", false, "export current branch graph to a file")
	cmd.Flags().BoolVar(&importGraph, "import", false, "import a graph export file")
	cmd.Flags().StringVar(&branch, "branch", "", "target branch for import (auto-detected if empty)")

	return cmd
}

// ctx returns the command's context or a background context.
func ctx(cmd *cobra.Command) context.Context {
	if c := cmd.Context(); c != nil {
		return c
	}
	return context.Background()
}

// runQueueEnrichment opens the queue store, populates it with unprocessed documents,
// and runs the worker pool to process them.
func runQueueEnrichment(
	ctx context.Context,
	cfg *config.Config,
	graphStore graph.Store,
	docsProvider docs.Provider,
	docsCache *docs.Cache,
	logFn func(format string, args ...any),
	warnFn func(format string, args ...any),
) error {
	queuePath := cfg.ConfigDir + "/queue.db"
	qs, err := queue.Open(queuePath)
	if err != nil {
		return fmt.Errorf("open queue: %w", err)
	}
	defer qs.Close()

	if err := qs.RecoverStalled(); err != nil {
		warnFn("Warning: queue recover stalled: %v", err)
	}

	retries := cfg.Queue.RetryAttempts
	if retries <= 0 {
		retries = 3
	}

	if err := populateQueue(ctx, graphStore, qs, cfg.Docs.Faces.Enabled, retries, logFn); err != nil {
		return fmt.Errorf("populate queue: %w", err)
	}

	pending := qs.PendingCount()
	if pending == 0 {
		return nil
	}

	throttler := queue.NewThrottler(cfg.Queue.MaxWorkers, float64(cfg.Queue.TargetCPU))
	defer throttler.Stop()

	// Track completions for periodic progress reporting.
	var completed atomic.Int32
	total := pending
	emitter := func(event string, data ...any) {
		switch event {
		case "sync:progress":
			completed.Add(1)
		case "job:failed":
			if len(data) > 0 {
				if m, ok := data[0].(map[string]string); ok {
					warnFn("[queue] %s failed for %s: %s", m["type"], m["file"], m["error"])
				}
			}
		}
	}

	pool := queue.NewWorkerPool(qs, throttler, emitter)
	paths := repoPaths(cfg)
	pool.Register(queue.JobDocExtract, queue.NewDocExtractHandler(docsProvider, docsCache, graphStore, paths))
	pool.Register(queue.JobImageDescribe, queue.NewImageDescribeHandler(docsProvider, docsCache, graphStore, cfg.Docs.MaxImageRes, paths))

	cleanupFaces := registerFaceHandlers(pool, cfg, graphStore, warnFn)
	defer cleanupFaces()

	maxW := cfg.Queue.MaxWorkers
	if maxW <= 0 {
		maxW = throttler.TargetWorkers()
	}
	logFn("[queue] Processing %d jobs (max %d workers, target CPU %d%%)", total, maxW, cfg.Queue.TargetCPU)

	// Periodic progress reporter.
	progressDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-progressDone:
				return
			case <-ticker.C:
				done := int(completed.Load())
				active := pool.ActiveCount()
				remaining := total - done
				logFn("[queue] Progress: %d/%d done, %d active, %d remaining", done, total, active, remaining)
			}
		}
	}()

	pool.Run(ctx)
	close(progressDone)

	stats, _ := qs.Stats()
	logFn("[queue] Enrichment complete: %d done, %d failed, %d skipped", stats.Done, stats.Failed, stats.Skipped)

	if err := qs.PurgeCompleted(); err != nil {
		warnFn("Warning: queue purge completed: %v", err)
	}
	if err := qs.PurgeFailed(); err != nil {
		warnFn("Warning: queue purge failed: %v", err)
	}

	return nil
}

// populateQueue scans the graph for NodeDocument nodes and enqueues enrichment jobs
// for documents that haven't been processed yet.
func populateQueue(
	ctx context.Context,
	graphStore graph.Store,
	qs *queue.Store,
	facesEnabled bool,
	retries int,
	logFn func(format string, args ...any),
) error {
	nodes, err := graphStore.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeDocument})
	if err != nil {
		return fmt.Errorf("query documents: %w", err)
	}

	type hashGroup struct {
		contentHash string
		filePaths   []string
		kind        string
	}
	groups := make(map[string]*hashGroup)

	for _, node := range nodes {
		hash := node.Properties["content_hash"]
		if hash == "" {
			continue
		}
		// Skip already-extracted documents.
		if node.Properties["extraction_status"] == "success" {
			continue
		}
		kind := node.Properties["kind"]
		g, ok := groups[hash]
		if !ok {
			g = &hashGroup{contentHash: hash, kind: kind}
			groups[hash] = g
		}
		g.filePaths = append(g.filePaths, node.FilePath)
	}

	var jobs []*queue.Job
	for _, g := range groups {
		switch g.kind {
		case "text", "document":
			jobs = append(jobs, &queue.Job{
				Type:        queue.JobDocExtract,
				Priority:    10,
				ContentHash: g.contentHash,
				FilePaths:   g.filePaths,
				MaxRetries:  retries,
			})
		case "image":
			jobs = append(jobs, &queue.Job{
				Type:        queue.JobImageDescribe,
				Priority:    20,
				ContentHash: g.contentHash,
				FilePaths:   g.filePaths,
				MaxRetries:  retries,
			})
			if facesEnabled {
				jobs = append(jobs, &queue.Job{
					Type:        queue.JobFaceDetect,
					Priority:    30,
					ContentHash: g.contentHash + ":face",
					FilePaths:   g.filePaths,
					MaxRetries:  retries,
				})
			}
		}
	}

	if len(jobs) == 0 {
		return nil
	}

	// Count per-type breakdown for log.
	typeCounts := make(map[queue.JobType]int)
	for _, j := range jobs {
		typeCounts[j.Type]++
	}
	parts := make([]string, 0, len(typeCounts))
	for jt, c := range typeCounts {
		parts = append(parts, fmt.Sprintf("%d %s", c, jt))
	}
	logFn("[queue] Enqueuing %d jobs (%s)", len(jobs), strings.Join(parts, ", "))
	return qs.EnqueueBatch(jobs)
}

func handleExportImport(cfg *config.Config, isExport bool, targetBranch string, out io.Writer) error {
	if cfg.ConfigDir == "" {
		return fmt.Errorf("no config directory found; run 'codeeagle init' first")
	}

	// Determine export path: from .CodeEagle.conf if available, else default.
	exportPath := cfg.ConfigDir + "/graph.export"
	if cfg.ProjectConf != nil && cfg.ProjectConfDir != "" {
		exportPath = config.ExportFilePath(cfg.ProjectConfDir, cfg.ProjectConf)
	}

	store, currentBranch, err := openBranchStore(cfg)
	if err != nil {
		return err
	}
	defer store.Close()

	ctx := context.Background()

	if isExport {
		f, err := os.Create(exportPath)
		if err != nil {
			return fmt.Errorf("create export file: %w", err)
		}
		defer f.Close()

		if err := store.ExportBranch(ctx, f, currentBranch); err != nil {
			return fmt.Errorf("export: %w", err)
		}

		fmt.Fprintf(out, "Exported branch %q to %s\n", currentBranch, exportPath)
	} else {
		f, err := os.Open(exportPath)
		if err != nil {
			return fmt.Errorf("open export file: %w", err)
		}

		// If no target branch specified, read it from the export.
		if targetBranch == "" {
			exportBranch, err := embedded.ReadExportBranch(f)
			f.Close()
			if err != nil {
				return fmt.Errorf("read export branch: %w", err)
			}
			if exportBranch == "" {
				targetBranch = "main" // legacy export
			} else {
				targetBranch = exportBranch
			}

			// Re-open for actual import.
			f, err = os.Open(exportPath)
			if err != nil {
				return fmt.Errorf("re-open export file: %w", err)
			}
		}
		defer f.Close()

		sourceBranch, err := store.ImportIntoBranch(ctx, f, targetBranch)
		if err != nil {
			return fmt.Errorf("import: %w", err)
		}

		stats, _ := store.Stats(ctx)
		if sourceBranch != "" {
			fmt.Fprintf(out, "Imported graph (source: %q) into branch %q: %d nodes, %d edges\n",
				sourceBranch, targetBranch, stats.NodeCount, stats.EdgeCount)
		} else {
			fmt.Fprintf(out, "Imported graph into branch %q: %d nodes, %d edges\n",
				targetBranch, stats.NodeCount, stats.EdgeCount)
		}
	}

	return nil
}
