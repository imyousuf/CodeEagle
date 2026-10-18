package embedded

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/dgraph-io/ristretto/v2/z"

	"github.com/imyousuf/CodeEagle/internal/badgerutil"
)

// PruneResult reports what a prune leaves out, or would.
type PruneResult struct {
	Files    int   // distinct file paths under the prefix
	Nodes    int   // nodes recorded against those files
	Edges    int64 // edges touching those nodes; counted only when copying
	Indexes  int64 // index entries left out with them
	Dangling int64 // edge index entries naming an edge that no longer exists
	Kept     int64 // keys copied
}

// CountUnder reports how many files and nodes in the write branch lie at or
// below prefix, without changing anything.
func (s *BranchStore) CountUnder(prefix string) (PruneResult, error) {
	var res PruneResult
	prefix, err := prunePrefix(prefix)
	if err != nil {
		return res, err
	}
	files, err := s.filesUnder(prefix)
	if err != nil {
		return res, err
	}
	res.Files = len(files)
	for _, ids := range files {
		res.Nodes += len(ids)
	}
	return res, nil
}

// PruneCopy writes a copy of this database to dstPath that leaves out every
// node in the write branch whose file lies at or below one of prefixes, every edge
// touching one, and every index entry naming either. Everything else -- other
// branches, the meeting scope, person and image records -- is copied as it
// is.
//
// It copies what stays rather than deleting what goes because the case it
// exists for is a directory indexed by mistake, which can be most of the
// database: deleting tens of millions of keys one transaction at a time
// leaves as many tombstones for every later scan to step over, and the space
// is not returned until a compaction rewrites everything anyway. A copy reads
// the database once and comes out compact. The source is only read, so it
// remains as it was until the caller replaces it.
//
// Each prefix is a graph path, as recorded on nodes (e.g. "Downloads/mark"),
// and matches whole path components: "Downloads/mark" does not match
// "Downloads/marketing". dstPath must not exist, or be an empty directory.
// Edge index entries whose edge is already gone are left out too: they name
// nothing, and they are what makes a later delete fail with "Key not found".
func (s *BranchStore) PruneCopy(ctx context.Context, dstPath string, prefixes []string, progress func(PruneResult)) (PruneResult, error) {
	var res PruneResult
	if len(prefixes) == 0 {
		return res, fmt.Errorf("no prefix to prune")
	}
	if entries, err := os.ReadDir(dstPath); err == nil && len(entries) > 0 {
		return res, fmt.Errorf("%s already exists and is not empty", dstPath)
	}

	drop := make(map[string]struct{})
	for _, raw := range prefixes {
		prefix, err := prunePrefix(raw)
		if err != nil {
			return res, err
		}
		files, err := s.filesUnder(prefix)
		if err != nil {
			return res, err
		}
		res.Files += len(files)
		for _, ids := range files {
			for _, id := range ids {
				drop[id] = struct{}{}
			}
		}
	}
	res.Nodes = len(drop)

	dst, err := badger.Open(badgerutil.TunedOptions(dstPath, badgerutil.DBRolePrimary))
	if err != nil {
		return res, fmt.Errorf("open %s: %w", dstPath, err)
	}
	sw := dst.NewStreamWriter()
	if err := sw.Prepare(); err != nil {
		_ = dst.Close()
		return res, fmt.Errorf("prepare %s: %w", dstPath, err)
	}

	f := &pruneFilter{src: s.db, branch: s.writeBranch, drop: drop}
	stream := s.db.NewStream()
	stream.NumGo = runtime.NumCPU()
	stream.LogPrefix = "prune"
	stream.ChooseKey = f.keep
	stream.Send = func(buf *z.Buffer) error { return sw.Write(buf) }

	stop := make(chan struct{})
	if progress != nil {
		go func() {
			t := time.NewTicker(10 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-stop:
					return
				case <-t.C:
					progress(f.snapshot(res))
				}
			}
		}()
	}

	err = stream.Orchestrate(ctx)
	close(stop)
	if err == nil {
		err = sw.Flush()
	}
	if cerr := dst.Close(); err == nil && cerr != nil {
		err = fmt.Errorf("close %s: %w", dstPath, cerr)
	}
	return f.snapshot(res), err
}

// edgeEnds decodes only the two ends of a stored edge. The tags matter: the
// stored names are snake_case, which the decoder's case-insensitive matching
// does not reach from the Go field names.
type edgeEnds struct {
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
}

// pruneFilter decides, key by key, what a prune copies. Badger calls keep
// from several goroutines at once; drop is only read, and the counters are
// atomic.
type pruneFilter struct {
	src    *badger.DB
	branch string
	drop   map[string]struct{}

	edges, indexes, dangling, kept atomic.Int64
}

func (f *pruneFilter) snapshot(base PruneResult) PruneResult {
	base.Edges = f.edges.Load()
	base.Indexes = f.indexes.Load()
	base.Dangling = f.dangling.Load()
	base.Kept = f.kept.Load()
	return base
}

func (f *pruneFilter) keep(item *badger.Item) bool {
	if item.IsDeletedOrExpired() {
		return false
	}
	if f.keepKey(item) {
		f.kept.Add(1)
		return true
	}
	return false
}

func (f *pruneFilter) keepKey(item *badger.Item) bool {
	key := string(item.Key())
	b := f.branch + ":"

	if rest, ok := strings.CutPrefix(key, prefixNode+b); ok {
		return !f.dropped(rest)
	}
	if strings.HasPrefix(key, prefixEdge+b) {
		var e edgeEnds
		if err := item.Value(func(v []byte) error { return json.Unmarshal(v, &e) }); err != nil {
			return true // unreadable: not ours to judge
		}
		if f.dropped(e.SourceID) || f.dropped(e.TargetID) {
			f.edges.Add(1)
			return false
		}
		return true
	}
	// idx:edge:<branch>:<source>:<type>:<edge> and the reverse index, which
	// names the target first. The key holds only one end, so the edge is read
	// for the other.
	if rest, ok := strings.CutPrefix(key, prefixIdxEdge+b); ok {
		return f.keepEdgeIndex(rest, func(src, tgt string) string { return tgt })
	}
	if rest, ok := strings.CutPrefix(key, prefixIdxReverseEdge+b); ok {
		return f.keepEdgeIndex(rest, func(src, tgt string) string { return src })
	}
	for _, idx := range []string{prefixIdxType, prefixIdxFile, prefixIdxPkg, prefixIdxRole} {
		if strings.HasPrefix(key, idx+b) {
			if f.dropped(key[strings.LastIndex(key, ":")+1:]) {
				f.indexes.Add(1)
				return false
			}
			return true
		}
	}
	return true
}

// keepEdgeIndex decides an edge index entry, given the key after its branch
// and which end of the edge the key does not name.
func (f *pruneFilter) keepEdgeIndex(rest string, otherEnd func(src, tgt string) string) bool {
	first, last := strings.Index(rest, ":"), strings.LastIndex(rest, ":")
	if first < 0 || last == len(rest)-1 {
		return true
	}
	if f.dropped(rest[:first]) {
		f.indexes.Add(1)
		return false
	}
	eid := rest[last+1:]
	var e edgeEnds
	err := f.src.View(func(txn *badger.Txn) error {
		item, err := txn.Get(edgeKey(f.branch, eid))
		if err != nil {
			return err
		}
		return item.Value(func(v []byte) error { return json.Unmarshal(v, &e) })
	})
	if errors.Is(err, badger.ErrKeyNotFound) {
		f.dangling.Add(1)
		return false
	}
	if err != nil {
		return true
	}
	if f.dropped(otherEnd(e.SourceID, e.TargetID)) {
		f.indexes.Add(1)
		return false
	}
	return true
}

func (f *pruneFilter) dropped(id string) bool {
	_, ok := f.drop[id]
	return ok
}

// prunePrefix normalizes a graph path to the form file index keys are
// scanned with, refusing one that would match every file.
func prunePrefix(prefix string) (string, error) {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" || prefix == "." {
		return "", fmt.Errorf("refusing to prune an empty prefix, which would match every file")
	}
	return prefix + "/", nil
}

// filesUnder maps each file path in the write branch's file index that starts
// with prefix to the node IDs recorded against it.
func (s *BranchStore) filesUnder(prefix string) (map[string][]string, error) {
	scan := []byte(fmt.Sprintf("%s%s:%s", prefixIdxFile, s.writeBranch, prefix))
	head := len(prefixIdxFile) + len(s.writeBranch) + 1

	files := make(map[string][]string)
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = scan
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek(scan); it.Valid(); it.Next() {
			key := string(it.Item().Key())
			// idx:file:<branch>:<path>:<id>. A path may hold a colon; an ID
			// cannot, so the last colon separates them.
			cut := strings.LastIndex(key, ":")
			if cut <= head {
				continue
			}
			path, id := key[head:cut], key[cut+1:]
			files[path] = append(files[path], id)
		}
		return nil
	})
	return files, err
}
