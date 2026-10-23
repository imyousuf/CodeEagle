package indexer

import (
	"context"
	"os"
	"path"
	"sort"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser/generic"
)

// DocumentSectionsVersion is the section format for PDFs, decks and other
// prose documents that the backfill brings existing nodes up to.
const DocumentSectionsVersion = 1

// Bounds on one sync's share of the backfill. A long PDF takes seconds to
// read without poppler, and a home directory can hold thousands, so the
// work is spread over syncs rather than holding one up for an hour.
const (
	documentBackfillLimit  = 200
	documentBackfillBudget = 2 * time.Minute
)

// BackpopDocumentSections re-parses prose documents indexed before their
// text was kept in sections, in path order, starting after resume. It
// returns how many it re-parsed and where the next sync should resume, or
// "" when every document has been reached.
//
// Like the markdown backfill it rewrites only the scope being written, and
// it never calls a model for a document whose summary is cached: the
// summary is kept, only the sections are new.
func BackpopDocumentSections(ctx context.Context, idx *Indexer, repoRoots []string, resume string) (int, string, error) {
	store := idx.Store()
	writeScope := ""
	if ws, ok := store.(interface{ WriteBranch() string }); ok {
		writeScope = ws.WriteBranch()
	}
	nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeDocument})
	if err != nil {
		return 0, resume, err
	}
	byPath := make(map[string]*graph.Node)
	for _, n := range nodes {
		kind := n.Properties["kind"]
		if (kind != "text" && kind != "document") || n.Name != path.Base(n.FilePath) || !generic.MaySection(n.FilePath) {
			continue
		}
		if n.Properties["sections"] != "" || n.FilePath <= resume {
			continue
		}
		if src := n.Properties[graph.PropGraphSource]; writeScope != "" && src != "" && src != writeScope {
			continue
		}
		byPath[n.FilePath] = n
	}
	paths := make([]string, 0, len(byPath))
	for p := range byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	started := time.Now()
	count := 0
	for i, p := range paths {
		if count >= documentBackfillLimit || time.Since(started) > documentBackfillBudget {
			return count, resumeAt(paths, i, resume), nil
		}
		if err := ctx.Err(); err != nil {
			return count, resumeAt(paths, i, resume), err
		}
		n := byPath[p]
		abs := resolveAbsPath(p, repoRoots)
		if abs == "" {
			continue
		}
		updatedAt := n.UpdatedAt
		if updatedAt.IsZero() {
			info, err := os.Stat(abs)
			if err != nil {
				continue
			}
			updatedAt = info.ModTime()
		}
		if err := idx.reindexQuietly(ctx, abs, updatedAt); err != nil {
			idx.log("Warning: backfill %s: %v", p, err)
			continue
		}
		count++
	}
	return count, "", nil
}

// resumeAt is the resume point for stopping before paths[i]; before the
// first, it is where this run resumed from.
func resumeAt(paths []string, i int, resume string) string {
	if i == 0 {
		return resume
	}
	return paths[i-1]
}
