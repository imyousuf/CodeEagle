package embedded

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dgraph-io/badger/v4"
	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/gitutil"
	"github.com/imyousuf/CodeEagle/internal/graph"
)

// MeetingScope is the key scope meeting data is stored under.
//
// The store partitions keys by git branch so that indexing a feature branch
// does not disturb main's view of the code. That is right for code and wrong
// for meetings: a meeting happened, and it does not belong to a branch.
// Filing meetings under whichever branch happened to be checked out when they
// were indexed means switching branches hides the entire history, and a sync
// on the new branch sees an empty graph and re-indexes everything from
// scratch.
//
// Meetings are therefore written under a fixed scope. It is deliberately not a
// legal git branch name, so it can never collide with a real one.
const MeetingScope = "@meetings"

// ReservedScopePrefix marks a scope that is ours rather than a git branch.
//
// The leading "@" is what makes such a name illegal as a git branch, which is
// the property the meeting scope relies on to never collide with one.
const ReservedScopePrefix = "@"

// IsReservedScope reports whether a scope holds data of ours rather than a
// branch's view of the code.
//
// Branch cleanup deletes every scope in the database that git no longer lists
// as a branch. A reserved scope is by construction never listed, so without
// this check it looks permanently stale and is deleted on the next sync —
// taking the whole meeting corpus with it, silently, for a command the user
// ran to index code.
func IsReservedScope(name string) bool {
	return strings.HasPrefix(name, ReservedScopePrefix)
}

// OpenMeetings opens the meeting graph, which is shared across branches.
func OpenMeetings(cfg *config.Config, dbPathOverride string) (*BranchStore, error) {
	path := cfg.ResolveDBPath(dbPathOverride)
	if path == "" {
		return nil, fmt.Errorf("no graph database path; run 'codeeagle init' or use --db-path")
	}
	store, err := NewBranchStore(path, MeetingScope, []string{MeetingScope})
	if err != nil {
		return nil, fmt.Errorf("open meeting store: %w", err)
	}
	return store, nil
}

// OpenMeetingsReadOnly opens the meeting graph for reading.
func OpenMeetingsReadOnly(cfg *config.Config, dbPathOverride string) (*BranchStore, error) {
	path := cfg.ResolveDBPath(dbPathOverride)
	if path == "" {
		return nil, fmt.Errorf("no graph database path configured")
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("graph database not found at %s; run 'codeeagle meetings sync' first", path)
	}
	store, err := NewReadOnlyBranchStore(path, MeetingScope, []string{MeetingScope})
	if err != nil {
		return nil, fmt.Errorf("open meeting store (read-only): %w", err)
	}
	return store, nil
}

// RescopeResult reports what moving a scope's keys did.
type RescopeResult struct {
	From string
	To   string
	Keys int
}

// keyScopePrefixes are the key families that embed a scope, each written as
// "<prefix><scope>:<rest>".
var keyScopePrefixes = []string{
	prefixNode, prefixEdge, prefixIdxType, prefixIdxFile,
	prefixIdxPkg, prefixIdxEdge, prefixIdxReverseEdge, prefixIdxRole,
}

// meetingScopeTypes are the node types a meeting corpus may consist of.
//
// Person, Topic and the date hierarchy are shared with code indexing rather
// than exclusive to meetings, so finding them proves nothing on its own — but
// finding anything outside this set proves the scope holds a codebase.
func meetingScopeTypes() map[graph.NodeType]bool {
	allowed := map[graph.NodeType]bool{
		graph.NodePerson: true,
		graph.NodeTopic:  true,
		graph.NodeYear:   true,
		graph.NodeMonth:  true,
		graph.NodeDate:   true,
	}
	for _, t := range graph.MeetingNodeTypes() {
		allowed[t] = true
	}
	return allowed
}

// ScopeNodeTypes counts the node types filed under a scope.
func (s *BranchStore) ScopeNodeTypes(scope string) (map[graph.NodeType]int, error) {
	counts := make(map[graph.NodeType]int)
	prefix := []byte(prefixNode + scope + ":")

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			val, err := it.Item().ValueCopy(nil)
			if err != nil {
				return err
			}
			var n graph.Node
			if err := json.Unmarshal(val, &n); err != nil {
				continue
			}
			counts[n.Type]++
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", scope, err)
	}
	return counts, nil
}

// Rescope moves every key filed under one scope to another.
//
// The scope appears only in the key, never in the stored value, so this is a
// key rename rather than a rewrite of the data — which is what makes it safe
// to move a corpus indexed under a branch into the shared meeting scope
// without re-indexing it.
//
// It refuses to overwrite: a key already present under the target scope is
// left alone and the source copy is dropped, so running it twice is harmless.
//
// It also refuses to move a scope that holds an indexed codebase. A scope is
// moved whole, because a key carries its scope but not its type, and the
// meeting scope is a fallback read for every branch — so moving a branch that
// was also used for `codeeagle sync` would take that branch's entire code
// graph with it, delete it from the branch, and leak it into every other
// branch's reads. Moving only the meeting nodes would be no better: Person and
// Topic nodes are shared with code indexing, and removing them from the branch
// would strip a document of the topics attached to it.
func (s *BranchStore) Rescope(from, to string, dryRun bool) (*RescopeResult, error) {
	if from == "" || to == "" {
		return nil, fmt.Errorf("both scopes must be named")
	}
	if from == to {
		return &RescopeResult{From: from, To: to}, nil
	}

	counts, err := s.ScopeNodeTypes(from)
	if err != nil {
		return nil, err
	}
	allowed := meetingScopeTypes()
	var foreign []string
	for typ, n := range counts {
		if !allowed[typ] {
			foreign = append(foreign, fmt.Sprintf("%s=%d", typ, n))
		}
	}
	if len(foreign) > 0 {
		sort.Strings(foreign)
		return nil, fmt.Errorf(
			"scope %q holds an indexed codebase (%s), not just meetings: "+
				"moving it would take the code graph with it. Index meetings "+
				"into their own database with graph.db_path instead",
			from, strings.Join(foreign, " "))
	}

	result := &RescopeResult{From: from, To: to}

	for _, prefix := range keyScopePrefixes {
		srcPrefix := []byte(prefix + from + ":")

		// Collect first, then write: mutating while iterating a Badger
		// transaction is not safe, and the batches must stay small enough to
		// commit.
		type kv struct{ key, val []byte }
		var pending []kv

		err := s.db.View(func(txn *badger.Txn) error {
			opts := badger.DefaultIteratorOptions
			opts.Prefix = srcPrefix
			it := txn.NewIterator(opts)
			defer it.Close()

			for it.Seek(srcPrefix); it.ValidForPrefix(srcPrefix); it.Next() {
				item := it.Item()
				key := item.KeyCopy(nil)
				val, err := item.ValueCopy(nil)
				if err != nil {
					return err
				}
				rest := strings.TrimPrefix(string(key), prefix+from+":")
				pending = append(pending, kv{
					key: []byte(prefix + to + ":" + rest),
					val: val,
				})
			}
			return nil
		})
		if err != nil {
			return result, fmt.Errorf("scan %s: %w", prefix, err)
		}

		result.Keys += len(pending)
		if dryRun || len(pending) == 0 {
			continue
		}

		const batchSize = 500
		for start := 0; start < len(pending); start += batchSize {
			end := min(start+batchSize, len(pending))
			batch := pending[start:end]

			err := s.db.Update(func(txn *badger.Txn) error {
				for _, e := range batch {
					if _, err := txn.Get(e.key); err == nil {
						// Already present under the target scope; leave it.
						continue
					} else if err != badger.ErrKeyNotFound {
						return err
					}
					if err := txn.Set(e.key, e.val); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				return result, fmt.Errorf("write %s: %w", prefix, err)
			}
		}

		// Drop the originals once the copies are committed, so an interrupted
		// run leaves duplicates rather than losing data.
		for start := 0; start < len(pending); start += batchSize {
			end := min(start+batchSize, len(pending))
			err := s.db.Update(func(txn *badger.Txn) error {
				for i := start; i < end; i++ {
					rest := strings.TrimPrefix(string(pending[i].key), prefix+to+":")
					if err := txn.Delete([]byte(prefix + from + ":" + rest)); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				return result, fmt.Errorf("drop %s: %w", prefix, err)
			}
		}
	}

	return result, nil
}

// OpenReadWriteWithScopes opens the code graph for writing, with extra scopes
// added to the read path.
//
// Writes go to the current branch as usual; the extra scopes only widen what
// can be read, which is how branch-independent data such as meetings becomes
// visible from any branch without a code sync being able to overwrite it.
func OpenReadWriteWithScopes(cfg *config.Config, repoPaths []string, dbPathOverride string, extra ...string) (*BranchStore, string, error) {
	path := cfg.ResolveDBPath(dbPathOverride)
	if path == "" {
		return nil, "", fmt.Errorf("no graph database path; run 'codeeagle init' or use --db-path")
	}
	current, read := gitutil.BuildReadBranches(repoPaths)
	store, err := NewBranchStore(path, current, appendScopes(read, extra...))
	if err != nil {
		return nil, "", fmt.Errorf("open graph store: %w", err)
	}
	return store, current, nil
}

// OpenReadOnlyWithScopes opens the code graph for reading, with extra scopes
// added to the read path.
func OpenReadOnlyWithScopes(cfg *config.Config, repoPaths []string, dbPathOverride string, extra ...string) (*BranchStore, string, error) {
	path := cfg.ResolveDBPath(dbPathOverride)
	if path == "" {
		return nil, "", fmt.Errorf("no graph database path configured")
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, "", fmt.Errorf("graph database not found at %s; run 'codeeagle sync' first to build the index", path)
	}
	current, read := gitutil.BuildReadBranches(repoPaths)
	store, err := NewReadOnlyBranchStore(path, current, appendScopes(read, extra...))
	if err != nil {
		return nil, "", fmt.Errorf("open graph store (read-only): %w", err)
	}
	return store, current, nil
}

// appendScopes adds scopes to a read list, skipping any already present so the
// first match still wins for a duplicate id.
func appendScopes(read []string, extra ...string) []string {
	seen := make(map[string]bool, len(read))
	for _, r := range read {
		seen[r] = true
	}
	for _, e := range extra {
		if e != "" && !seen[e] {
			read = append(read, e)
			seen[e] = true
		}
	}
	return read
}

// ImportScopeFrom copies a scope out of another database into this one.
//
// Meetings are not tied to a repository, so a corpus indexed against one
// project's database often belongs somewhere more central — a home
// configuration reachable from any directory. The data is already enriched,
// and re-deriving it would mean paying a model to reproduce what is sitting on
// disk, so this moves it rather than rebuilding it.
//
// The target scope is merged into, not replaced, so several sources can be
// collected into one. The source is opened read-only and is never modified:
// the caller keeps their original until they have checked the result.
func (s *BranchStore) ImportScopeFrom(
	ctx context.Context, srcPath, srcScope, dstScope string, dryRun bool,
) (*RescopeResult, error) {
	if srcPath == "" || srcScope == "" || dstScope == "" {
		return nil, fmt.Errorf("source path, source scope and target scope are all required")
	}
	if abs, err := filepath.Abs(srcPath); err == nil {
		if same, err := filepath.Abs(s.path); err == nil && same == abs {
			return nil, fmt.Errorf("source and target are the same database; use --from without --from-db")
		}
	}

	src, err := NewReadOnlyBranchStore(srcPath, srcScope, []string{srcScope})
	if err != nil {
		return nil, fmt.Errorf("open source database %s: %w", srcPath, err)
	}
	defer src.Close()

	counts, err := src.ScopeNodeTypes(srcScope)
	if err != nil {
		return nil, err
	}
	if len(counts) == 0 {
		return &RescopeResult{From: srcScope, To: dstScope}, nil
	}
	allowed := meetingScopeTypes()
	var foreign []string
	for typ, n := range counts {
		if !allowed[typ] {
			foreign = append(foreign, fmt.Sprintf("%s=%d", typ, n))
		}
	}
	if len(foreign) > 0 {
		sort.Strings(foreign)
		return nil, fmt.Errorf(
			"scope %q in %s holds an indexed codebase (%s), not just meetings",
			srcScope, srcPath, strings.Join(foreign, " "))
	}

	result := &RescopeResult{From: srcScope, To: dstScope}
	for _, n := range counts {
		result.Keys += n
	}
	if dryRun {
		return result, nil
	}

	var buf bytes.Buffer
	if err := src.ExportBranch(ctx, &buf, srcScope); err != nil {
		return result, fmt.Errorf("read scope %q: %w", srcScope, err)
	}
	if _, err := s.MergeIntoBranch(ctx, &buf, dstScope); err != nil {
		return result, fmt.Errorf("write scope %q: %w", dstScope, err)
	}
	return result, nil
}
