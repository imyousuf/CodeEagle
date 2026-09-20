package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/embedding"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/vectorstore"
)

// federatedIndex is one other CodeEagle directory searched alongside the local
// one.
type federatedIndex struct {
	// Name is how results from here are labelled.
	Name string
	// Dir is the .CodeEagle directory it was opened from.
	Dir string

	store *embedded.BranchStore
	vec   *vectorstore.VectorStore
}

// Close releases both stores.
func (f *federatedIndex) Close() {
	if f.vec != nil {
		f.vec.Close()
	}
	if f.store != nil {
		f.store.Close()
	}
}

// openFederated opens every index the configuration federates with, skipping
// any that cannot be searched and saying why.
//
// Read-only throughout. A running `codeeagle watch` or `vectorindex` holds the
// write lock on its database, and a query that blocked behind an hour-long
// rebuild would be worse than one that searched a little less.
func openFederated(
	cfg *config.Config, local *vectorstore.VectorStore, warn func(format string, args ...any),
) []*federatedIndex {
	localMeta := local.Meta()

	var out []*federatedIndex
	for _, dir := range cfg.Federate {
		dir = expandPath(strings.TrimSpace(dir))
		if dir == "" {
			continue
		}
		// Naming the directory this configuration already uses would open the
		// same database twice, read-only, for no gain.
		if sameDir(dir, cfg.ConfigDir) {
			continue
		}

		idx, err := openOneFederated(dir, localMeta, local.Embedder())
		if err != nil {
			warn("Skipping %s: %v", dir, err)
			continue
		}
		out = append(out, idx)
	}
	return out
}

// openOneFederated opens a single federated index, refusing one whose vectors
// cannot be ranked against the local index's.
func openOneFederated(
	dir string, localMeta *vectorstore.VectorIndexMeta, embedder embedding.Provider,
) (*federatedIndex, error) {
	dbPath := filepath.Join(dir, config.DefaultDBDir)
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("no graph database")
	}

	store, err := embedded.NewReadOnlyBranchStore(dbPath, "default",
		[]string{"default", embedded.MeetingScope})
	if err != nil {
		return nil, fmt.Errorf("open graph: %w", err)
	}

	// The same embedder as the local index: a query must be embedded with the
	// model the vectors were built from, and comparability already guarantees
	// that is the same model.
	vec, err := vectorstore.New(store, embedder, "default",
		filepath.Join(dir, "vec.idx"), filepath.Join(dir, "vec.db"))
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("open vector index: %w", err)
	}

	meta, err := vec.LoadMetaOnly()
	if err != nil || meta == nil {
		vec.Close()
		store.Close()
		return nil, fmt.Errorf("no vector index; run `codeeagle vectorindex` there")
	}

	// The check that matters. A similarity score means something only against
	// other scores from the same embedding model; two models place text in
	// different spaces, so ranking across them yields an order that looks
	// authoritative and is arbitrary. Better to say so than to rank anyway.
	if !localMeta.Comparable(meta) {
		vec.Close()
		store.Close()
		return nil, fmt.Errorf("embedded with %s, this index uses %s — scores are not comparable",
			meta.Describe(), localMeta.Describe())
	}

	if _, err := vec.Load(); err != nil {
		vec.Close()
		store.Close()
		return nil, fmt.Errorf("load vector index: %w", err)
	}

	return &federatedIndex{Name: federatedName(dir), Dir: dir, store: store, vec: vec}, nil
}

// federatedName labels results from an index by the directory holding it,
// since ".CodeEagle" alone would name every one of them.
func federatedName(dir string) string {
	parent := filepath.Dir(dir)
	if name := filepath.Base(parent); name != "" && name != "." && name != string(filepath.Separator) {
		if home, err := os.UserHomeDir(); err == nil && sameDir(parent, home) {
			return "home"
		}
		return name
	}
	return dir
}

// sameDir reports whether two paths name one directory.
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	return errA == nil && errB == nil && absA == absB
}

// searchFederated searches every federated index and prints what it found,
// grouped and labelled by where it came from.
//
// Deliberately not interleaved into one ranking. Scores from corpora that were
// never calibrated against one another do not share units, and ranking by
// nearness would bury a single decisive meeting under fifty near-miss code
// hits. Grouping lets the reader judge, which a blended number cannot.
func searchFederated(
	ctx context.Context, out io.Writer, indices []*federatedIndex, query string, limit int,
) {
	for _, idx := range indices {
		results, err := idx.vec.Search(ctx, query, limit)
		if err != nil {
			fmt.Fprintf(out, "\n── %s ──\n  search failed: %v\n", idx.Name, err)
			continue
		}
		if len(results) == 0 {
			continue
		}

		fmt.Fprintf(out, "\n── also in %s ──\n", idx.Name)
		for i, r := range results {
			fmt.Fprintf(out, " %2d. [%s] %s  (%.0f%%)\n",
				i+1, r.Node.Type, truncateText(r.Node.Name, 70), r.Score*100)
			if r.Node.FilePath != "" {
				fmt.Fprintf(out, "     File: %s\n", r.Node.FilePath)
			}
		}
	}
}

// shouldFederate decides whether to search beyond the local index.
//
// Configured federation is on by default, because someone who listed other
// indices wants them searched; the flags are there to override that either
// way for one command.
func shouldFederate(cfg *config.Config, federate, noFederate bool) bool {
	if noFederate {
		return false
	}
	return federate || len(cfg.Federate) > 0
}
