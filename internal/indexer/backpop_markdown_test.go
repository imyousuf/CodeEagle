package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/parser"
	"github.com/imyousuf/CodeEagle/internal/parser/markdown"
	"github.com/imyousuf/CodeEagle/internal/watcher"
)

const backfillDoc = "# Retry policy\n\nAt most three retries per call.\n"

// markdownIndexer builds an indexer over store whose markdown parser stores
// section text or not.
func markdownIndexer(store graph.Store, root string, sectionText bool) *Indexer {
	registry := parser.NewRegistry()
	registry.Register(markdown.NewParser(markdown.WithSectionText(sectionText)))
	return NewIndexer(IndexerConfig{
		GraphStore:     store,
		ParserRegistry: registry,
		RepoRoots:      []string{root},
		WatcherConfig:  &watcher.WatcherConfig{Paths: []string{root}},
		Logger:         func(string, ...any) {},
	})
}

func writeDoc(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "docs", "retry.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(backfillDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func sectionNamed(t *testing.T, store graph.Store, name string) *graph.Node {
	t.Helper()
	nodes, err := store.QueryNodes(context.Background(), graph.NodeFilter{Type: graph.NodeDocument})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.Name == name && n.Properties["kind"] == "section" {
			return n
		}
	}
	return nil
}

// TestBackfillGivesOldMarkdownItsTextQuietly covers markdown indexed before
// sections carried text. It must gain the text, keep its timestamp, and not
// be reported as changed: a changed file sets off summarization, the linker
// and the enrichment queue, and one upgrade must not become a sync of
// everything.
func TestBackfillGivesOldMarkdownItsTextQuietly(t *testing.T) {
	root := t.TempDir()
	path := writeDoc(t, root)
	store, err := embedded.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	stamp := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	if err := markdownIndexer(store, root, false).IndexFileWithTimestamp(ctx, path, stamp); err != nil {
		t.Fatal(err)
	}
	if s := sectionNamed(t, store, "Retry policy"); s == nil || s.DocComment != "" {
		t.Fatalf("setup: the heading-only section should have no text, got %+v", s)
	}

	idx := markdownIndexer(store, root, true)
	n, err := BackpopMarkdownSections(ctx, idx, []string{root})
	if err != nil {
		t.Fatalf("BackpopMarkdownSections: %v", err)
	}
	if n != 1 {
		t.Errorf("re-parsed %d files, want 1", n)
	}

	s := sectionNamed(t, store, "Retry policy")
	if s == nil || s.DocComment != "At most three retries per call." {
		t.Fatalf("section text after backfill = %+v", s)
	}
	if !s.UpdatedAt.Equal(stamp) {
		t.Errorf("UpdatedAt = %v, want the original %v", s.UpdatedAt, stamp)
	}
	if idx.HasChanges() || len(idx.ChangedFiles()) != 0 {
		t.Errorf("the backfill reported changes: %v", idx.ChangedFiles())
	}
}

// TestSyncFilesBackfillsOncePerBranch covers the flag that keeps the
// backfill from scanning on every sync, and that it is kept per branch.
func TestSyncFilesBackfillsOncePerBranch(t *testing.T) {
	root := t.TempDir()
	path := writeDoc(t, root)
	configDir := t.TempDir()
	store, err := embedded.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := markdownIndexer(store, root, false).IndexFileWithTimestamp(ctx, path, info.ModTime()); err != nil {
		t.Fatal(err)
	}

	idx := markdownIndexer(store, root, true)
	if err := SyncFiles(ctx, idx, []string{root}, configDir, false, "default"); err != nil {
		t.Fatalf("SyncFiles: %v", err)
	}
	if s := sectionNamed(t, store, "Retry policy"); s == nil || s.DocComment == "" {
		t.Fatal("SyncFiles did not backfill section text")
	}
	if idx.HasChanges() {
		t.Errorf("an unchanged file was reported changed: %v", idx.ChangedFiles())
	}

	state, err := LoadSyncState(filepath.Join(configDir, syncStateFile))
	if err != nil {
		t.Fatal(err)
	}
	if got := state.GetBranchState("default").MarkdownSectionsVersion; got != MarkdownSectionsVersion {
		t.Errorf("default branch backfill version = %d, want %d", got, MarkdownSectionsVersion)
	}
	if got := state.GetBranchState("feature").MarkdownSectionsVersion; got != 0 {
		t.Errorf("a branch never synced was marked backfilled (%d)", got)
	}
}

// TestBackfillSkipsTurnedOffSectionText covers docs.markdown_sections: false:
// with nothing to add, the backfill must not run or mark itself done, so
// turning the feature on later still backfills.
func TestBackfillSkipsTurnedOffSectionText(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root)
	configDir := t.TempDir()
	store, err := embedded.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := SyncFiles(context.Background(), markdownIndexer(store, root, false), []string{root}, configDir, false, "default"); err != nil {
		t.Fatal(err)
	}
	state, err := LoadSyncState(filepath.Join(configDir, syncStateFile))
	if err != nil {
		t.Fatal(err)
	}
	if got := state.GetBranchState("default").MarkdownSectionsVersion; got != 0 {
		t.Errorf("backfill marked done with section text off (%d)", got)
	}
}

// TestBackfillRewritesOnlyTheWriteScope covers a feature branch, which sees
// the default branch's documents through the fallback read. Re-parsing those
// would copy every default-branch document into the feature branch's scope.
func TestBackfillRewritesOnlyTheWriteScope(t *testing.T) {
	root := t.TempDir()
	path := writeDoc(t, root)
	dbPath := t.TempDir()
	ctx := context.Background()

	def, err := embedded.NewBranchStore(dbPath, "default", []string{"default"})
	if err != nil {
		t.Fatal(err)
	}
	if err := markdownIndexer(def, root, false).IndexFileWithTimestamp(ctx, path, time.Now()); err != nil {
		t.Fatal(err)
	}
	def.Close()

	feature, err := embedded.NewBranchStore(dbPath, "feature", []string{"feature", "default"})
	if err != nil {
		t.Fatal(err)
	}
	n, err := BackpopMarkdownSections(ctx, markdownIndexer(feature, root, true), []string{root})
	feature.Close()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("re-parsed %d default-branch files from a feature branch", n)
	}

	only, err := embedded.NewBranchStore(dbPath, "feature", []string{"feature"})
	if err != nil {
		t.Fatal(err)
	}
	defer only.Close()
	nodes, err := only.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeDocument})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 {
		t.Errorf("%d default-branch nodes were copied into the feature scope", len(nodes))
	}
}

// TestResolveAbsPathFindsFilesUnderANamedRoot covers the paths a non-git
// root records, which start with the root's own name. Joined to the root
// they name a file that does not exist, which hid every such file from the
// backfills.
func TestResolveAbsPathFindsFilesUnderANamedRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "Downloads")
	if err := os.MkdirAll(filepath.Join(root, "specs"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "specs", "retry.md")
	if err := os.WriteFile(file, []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"Downloads/specs/retry.md", "specs/retry.md"} {
		if got := ResolveAbsPath(rel, []string{root}); got != file {
			t.Errorf("ResolveAbsPath(%q) = %q, want %q", rel, got, file)
		}
	}
	if got := ResolveAbsPath("Downloads/missing.md", []string{root}); got != "" {
		t.Errorf("a missing file resolved to %q", got)
	}
}

// TestBackfillReachesMarkdownUnderANamedRoot is the backfill over the same
// path shape: indexed with the root's name in front, as the home project's
// Documents and Downloads are.
func TestBackfillReachesMarkdownUnderANamedRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "Documents")
	path := writeDoc(t, root)
	store, err := embedded.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	named := func(sectionText bool) *Indexer {
		registry := parser.NewRegistry()
		registry.Register(markdown.NewParser(markdown.WithSectionText(sectionText)))
		return NewIndexer(IndexerConfig{
			GraphStore: store, ParserRegistry: registry, RepoRoots: []string{root},
			NonGitRoots:   map[string]bool{root: true},
			WatcherConfig: &watcher.WatcherConfig{Paths: []string{root}},
			Logger:        func(string, ...any) {},
		})
	}
	if err := named(false).IndexFileWithTimestamp(ctx, path, time.Now()); err != nil {
		t.Fatal(err)
	}
	n, err := BackpopMarkdownSections(ctx, named(true), []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("re-parsed %d files under a named root, want 1", n)
	}
	if s := sectionNamed(t, store, "Retry policy"); s == nil || s.DocComment == "" {
		t.Error("the section under a named root did not gain its text")
	}
}
