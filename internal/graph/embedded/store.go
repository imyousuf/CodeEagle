package embedded

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dgraph-io/badger/v4"
	"github.com/imyousuf/CodeEagle/internal/graph"
)

// Key prefixes for the BadgerDB key scheme.
const (
	prefixNode           = "n:"
	prefixEdge           = "e:"
	prefixIdxType        = "idx:type:"
	prefixIdxFile        = "idx:file:"
	prefixIdxPkg         = "idx:pkg:"
	prefixIdxEdge        = "idx:edge:"
	prefixIdxReverseEdge = "idx:redge:"
	prefixIdxRole        = "idx:role:"
)

// BranchStore implements graph.Store using BadgerDB with branch-aware key prefixes.
// All keys are prefixed with the branch name, enabling N-branch support in a single DB.
type BranchStore struct {
	db           *badger.DB
	writeBranch  string
	readBranches []string // ordered by priority; first branch wins for duplicate IDs
}

// NewBranchStore opens (or creates) a BadgerDB-backed graph store at dbPath with
// the given write branch and read branches (ordered by priority).
func NewBranchStore(dbPath, writeBranch string, readBranches []string) (*BranchStore, error) {
	opts := badger.DefaultOptions(dbPath)
	opts.Logger = nil // suppress badger logs
	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("open badger db: %w", err)
	}
	return &BranchStore{db: db, writeBranch: writeBranch, readBranches: readBranches}, nil
}

// NewStore opens (or creates) a BadgerDB-backed graph store at dbPath.
// Backward-compatible wrapper that uses branch "default" for all operations.
func NewStore(dbPath string) (*BranchStore, error) {
	return NewBranchStore(dbPath, "default", []string{"default"})
}

// WriteBranch returns the branch used for write operations.
func (s *BranchStore) WriteBranch() string { return s.writeBranch }

// ReadBranches returns the ordered list of branches used for read operations.
func (s *BranchStore) ReadBranches() []string { return s.readBranches }

// --- branch-aware key functions ---

// nodeKey returns the primary key for a node in the given branch.
func nodeKey(branch, id string) []byte { return []byte(prefixNode + branch + ":" + id) }

// edgeKey returns the primary key for an edge in the given branch.
func edgeKey(branch, id string) []byte { return []byte(prefixEdge + branch + ":" + id) }

// indexTypeKey returns a secondary index key for node type lookup.
func indexTypeKey(branch string, nodeType graph.NodeType, id string) []byte {
	return []byte(fmt.Sprintf("%s%s:%s:%s", prefixIdxType, branch, nodeType, id))
}

// indexFileKey returns a secondary index key for file path lookup.
func indexFileKey(branch, filePath, id string) []byte {
	return []byte(fmt.Sprintf("%s%s:%s:%s", prefixIdxFile, branch, filePath, id))
}

// indexPkgKey returns a secondary index key for package lookup.
func indexPkgKey(branch, pkg, id string) []byte {
	return []byte(fmt.Sprintf("%s%s:%s:%s", prefixIdxPkg, branch, pkg, id))
}

// indexEdgeKey returns a secondary index key for forward edge lookup.
func indexEdgeKey(branch, sourceID string, edgeType graph.EdgeType, edgeID string) []byte {
	return []byte(fmt.Sprintf("%s%s:%s:%s:%s", prefixIdxEdge, branch, sourceID, edgeType, edgeID))
}

// indexReverseEdgeKey returns a secondary index key for reverse edge lookup.
func indexReverseEdgeKey(branch, targetID string, edgeType graph.EdgeType, edgeID string) []byte {
	return []byte(fmt.Sprintf("%s%s:%s:%s:%s", prefixIdxReverseEdge, branch, targetID, edgeType, edgeID))
}

// indexRoleKey returns a secondary index key for architectural role lookup.
func indexRoleKey(branch, role, id string) []byte {
	return []byte(fmt.Sprintf("%s%s:%s:%s", prefixIdxRole, branch, role, id))
}

// nodeArchRole extracts the architectural role from a node's properties.
func nodeArchRole(n *graph.Node) string {
	if n.Properties == nil {
		return ""
	}
	return n.Properties[graph.PropArchRole]
}

func (s *BranchStore) AddNode(_ context.Context, node *graph.Node) error {
	b := s.writeBranch
	data, err := json.Marshal(node)
	if err != nil {
		return fmt.Errorf("marshal node: %w", err)
	}
	return s.db.Update(func(txn *badger.Txn) error {
		if err := txn.Set(nodeKey(b, node.ID), data); err != nil {
			return err
		}
		if err := txn.Set(indexTypeKey(b, node.Type, node.ID), nil); err != nil {
			return err
		}
		if node.FilePath != "" {
			if err := txn.Set(indexFileKey(b, node.FilePath, node.ID), nil); err != nil {
				return err
			}
		}
		if node.Package != "" {
			if err := txn.Set(indexPkgKey(b, node.Package, node.ID), nil); err != nil {
				return err
			}
		}
		if role := nodeArchRole(node); role != "" {
			if err := txn.Set(indexRoleKey(b, role, node.ID), nil); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *BranchStore) UpdateNode(_ context.Context, node *graph.Node) error {
	b := s.writeBranch
	data, err := json.Marshal(node)
	if err != nil {
		return fmt.Errorf("marshal node: %w", err)
	}
	return s.db.Update(func(txn *badger.Txn) error {
		// Read existing node to clean up old indexes if fields changed.
		old, err := getNodeInTxn(txn, b, node.ID)
		if err != nil {
			return fmt.Errorf("get existing node for update: %w", err)
		}
		// Remove stale indexes.
		if old.Type != node.Type {
			_ = txn.Delete(indexTypeKey(b, old.Type, old.ID))
		}
		if old.FilePath != node.FilePath && old.FilePath != "" {
			_ = txn.Delete(indexFileKey(b, old.FilePath, old.ID))
		}
		if old.Package != node.Package && old.Package != "" {
			_ = txn.Delete(indexPkgKey(b, old.Package, old.ID))
		}
		if oldRole := nodeArchRole(old); oldRole != "" && oldRole != nodeArchRole(node) {
			_ = txn.Delete(indexRoleKey(b, oldRole, old.ID))
		}
		// Write new data and indexes.
		if err := txn.Set(nodeKey(b, node.ID), data); err != nil {
			return err
		}
		if err := txn.Set(indexTypeKey(b, node.Type, node.ID), nil); err != nil {
			return err
		}
		if node.FilePath != "" {
			if err := txn.Set(indexFileKey(b, node.FilePath, node.ID), nil); err != nil {
				return err
			}
		}
		if node.Package != "" {
			if err := txn.Set(indexPkgKey(b, node.Package, node.ID), nil); err != nil {
				return err
			}
		}
		if role := nodeArchRole(node); role != "" {
			if err := txn.Set(indexRoleKey(b, role, node.ID), nil); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *BranchStore) DeleteNode(_ context.Context, id string) error {
	b := s.writeBranch
	return s.db.Update(func(txn *badger.Txn) error {
		return deleteNodeInTxn(txn, b, id)
	})
}

// deleteNodeInTxn removes a node and all its edges within a transaction.
func deleteNodeInTxn(txn *badger.Txn, branch, id string) error {
	node, err := getNodeInTxn(txn, branch, id)
	if err != nil {
		return err
	}
	// Delete forward edges (this node as source).
	edgeIDs, err := scanIndexPrefix(txn, []byte(fmt.Sprintf("%s%s:%s:", prefixIdxEdge, branch, id)))
	if err != nil {
		return err
	}
	for _, eid := range edgeIDs {
		if err := deleteEdgeInTxn(txn, branch, eid); err != nil {
			return err
		}
	}
	// Delete reverse edges (this node as target).
	redgeIDs, err := scanIndexPrefix(txn, []byte(fmt.Sprintf("%s%s:%s:", prefixIdxReverseEdge, branch, id)))
	if err != nil {
		return err
	}
	for _, eid := range redgeIDs {
		if err := deleteEdgeInTxn(txn, branch, eid); err != nil {
			return err
		}
	}
	// Delete indexes.
	_ = txn.Delete(indexTypeKey(branch, node.Type, id))
	if node.FilePath != "" {
		_ = txn.Delete(indexFileKey(branch, node.FilePath, id))
	}
	if node.Package != "" {
		_ = txn.Delete(indexPkgKey(branch, node.Package, id))
	}
	if role := nodeArchRole(node); role != "" {
		_ = txn.Delete(indexRoleKey(branch, role, id))
	}
	// Delete the node itself.
	return txn.Delete(nodeKey(branch, id))
}

func (s *BranchStore) GetNode(_ context.Context, id string) (*graph.Node, error) {
	var node *graph.Node
	err := s.db.View(func(txn *badger.Txn) error {
		for _, branch := range s.readBranches {
			n, err := getNodeInTxn(txn, branch, id)
			if err == nil {
				tagNodeSource(n, branch)
				node = n
				return nil
			}
		}
		return fmt.Errorf("get node %s: not found in any branch", id)
	})
	return node, err
}

func getNodeInTxn(txn *badger.Txn, branch, id string) (*graph.Node, error) {
	item, err := txn.Get(nodeKey(branch, id))
	if err != nil {
		return nil, fmt.Errorf("get node %s: %w", id, err)
	}
	var node graph.Node
	err = item.Value(func(val []byte) error {
		return json.Unmarshal(val, &node)
	})
	if err != nil {
		return nil, fmt.Errorf("unmarshal node %s: %w", id, err)
	}
	return &node, nil
}

func (s *BranchStore) QueryNodes(_ context.Context, filter graph.NodeFilter) ([]*graph.Node, error) {
	seen := make(map[string]struct{})
	var results []*graph.Node

	for _, branch := range s.readBranches {
		var nodeIDs []string
		var useFullScan bool

		err := s.db.View(func(txn *badger.Txn) error {
			switch {
			case filter.FilePath != "":
				ids, err := scanIndexPrefix(txn, []byte(fmt.Sprintf("%s%s:%s:", prefixIdxFile, branch, filter.FilePath)))
				if err != nil {
					return err
				}
				nodeIDs = ids
			case filter.Type != "":
				ids, err := scanIndexPrefix(txn, []byte(fmt.Sprintf("%s%s:%s:", prefixIdxType, branch, filter.Type)))
				if err != nil {
					return err
				}
				nodeIDs = ids
			case filter.Package != "":
				ids, err := scanIndexPrefix(txn, []byte(fmt.Sprintf("%s%s:%s:", prefixIdxPkg, branch, filter.Package)))
				if err != nil {
					return err
				}
				nodeIDs = ids
			default:
				useFullScan = true
			}
			return nil
		})
		if err != nil {
			return nil, err
		}

		err = s.db.View(func(txn *badger.Txn) error {
			if useFullScan {
				return scanBranchNodes(txn, branch, func(node *graph.Node) bool {
					if _, ok := seen[node.ID]; ok {
						return true // skip, earlier branch already has this ID
					}
					if matchesFilter(node, filter) {
						seen[node.ID] = struct{}{}
						tagNodeSource(node, branch)
						results = append(results, node)
					}
					return true
				})
			}
			for _, id := range nodeIDs {
				if _, ok := seen[id]; ok {
					continue
				}
				node, err := getNodeInTxn(txn, branch, id)
				if err != nil {
					continue // index entry for deleted node; skip
				}
				if matchesFilter(node, filter) {
					seen[id] = struct{}{}
					tagNodeSource(node, branch)
					results = append(results, node)
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return results, nil
}

func (s *BranchStore) AddEdge(_ context.Context, edge *graph.Edge) error {
	b := s.writeBranch
	data, err := json.Marshal(edge)
	if err != nil {
		return fmt.Errorf("marshal edge: %w", err)
	}
	return s.db.Update(func(txn *badger.Txn) error {
		if err := txn.Set(edgeKey(b, edge.ID), data); err != nil {
			return err
		}
		if err := txn.Set(indexEdgeKey(b, edge.SourceID, edge.Type, edge.ID), nil); err != nil {
			return err
		}
		return txn.Set(indexReverseEdgeKey(b, edge.TargetID, edge.Type, edge.ID), nil)
	})
}

func (s *BranchStore) DeleteEdge(_ context.Context, id string) error {
	b := s.writeBranch
	return s.db.Update(func(txn *badger.Txn) error {
		return deleteEdgeInTxn(txn, b, id)
	})
}

func deleteEdgeInTxn(txn *badger.Txn, branch, id string) error {
	item, err := txn.Get(edgeKey(branch, id))
	if err != nil {
		return fmt.Errorf("get edge %s: %w", id, err)
	}
	var edge graph.Edge
	err = item.Value(func(val []byte) error {
		return json.Unmarshal(val, &edge)
	})
	if err != nil {
		return fmt.Errorf("unmarshal edge %s: %w", id, err)
	}
	_ = txn.Delete(indexEdgeKey(branch, edge.SourceID, edge.Type, edge.ID))
	_ = txn.Delete(indexReverseEdgeKey(branch, edge.TargetID, edge.Type, edge.ID))
	return txn.Delete(edgeKey(branch, id))
}

func (s *BranchStore) GetEdges(_ context.Context, nodeID string, edgeType graph.EdgeType) ([]*graph.Edge, error) {
	seen := make(map[string]struct{})
	var results []*graph.Edge

	err := s.db.View(func(txn *badger.Txn) error {
		for _, branch := range s.readBranches {
			fwdPrefix := buildEdgeIndexPrefix(prefixIdxEdge, branch, nodeID, edgeType)
			fwdIDs, err := scanIndexPrefix(txn, fwdPrefix)
			if err != nil {
				return err
			}
			revPrefix := buildEdgeIndexPrefix(prefixIdxReverseEdge, branch, nodeID, edgeType)
			revIDs, err := scanIndexPrefix(txn, revPrefix)
			if err != nil {
				return err
			}
			for _, eid := range append(fwdIDs, revIDs...) {
				if _, ok := seen[eid]; ok {
					continue
				}
				seen[eid] = struct{}{}
				e, err := getEdgeInTxn(txn, branch, eid)
				if err != nil {
					continue
				}
				tagEdgeSource(e, branch)
				results = append(results, e)
			}
		}
		return nil
	})
	return results, err
}

func (s *BranchStore) GetNeighbors(_ context.Context, nodeID string, edgeType graph.EdgeType, direction graph.Direction) ([]*graph.Node, error) {
	var results []*graph.Node
	err := s.db.View(func(txn *badger.Txn) error {
		seen := make(map[string]struct{})

		for _, branch := range s.readBranches {
			// Outgoing: nodeID is source -> follow forward index -> neighbor is target.
			if direction == graph.Outgoing || direction == graph.Both {
				prefix := buildEdgeIndexPrefix(prefixIdxEdge, branch, nodeID, edgeType)
				edgeIDs, err := scanIndexPrefix(txn, prefix)
				if err != nil {
					return err
				}
				for _, eid := range edgeIDs {
					e, err := getEdgeInTxn(txn, branch, eid)
					if err != nil {
						continue
					}
					if _, ok := seen[e.TargetID]; ok {
						continue
					}
					seen[e.TargetID] = struct{}{}
					n, err := getNodeFromBranches(txn, s.readBranches, e.TargetID)
					if err != nil {
						continue
					}
					results = append(results, n)
				}
			}

			// Incoming: nodeID is target -> follow reverse index -> neighbor is source.
			if direction == graph.Incoming || direction == graph.Both {
				prefix := buildEdgeIndexPrefix(prefixIdxReverseEdge, branch, nodeID, edgeType)
				edgeIDs, err := scanIndexPrefix(txn, prefix)
				if err != nil {
					return err
				}
				for _, eid := range edgeIDs {
					e, err := getEdgeInTxn(txn, branch, eid)
					if err != nil {
						continue
					}
					if _, ok := seen[e.SourceID]; ok {
						continue
					}
					seen[e.SourceID] = struct{}{}
					n, err := getNodeFromBranches(txn, s.readBranches, e.SourceID)
					if err != nil {
						continue
					}
					results = append(results, n)
				}
			}
		}

		return nil
	})
	return results, err
}

// getNodeFromBranches tries to get a node from the first available branch.
func getNodeFromBranches(txn *badger.Txn, branches []string, id string) (*graph.Node, error) {
	for _, b := range branches {
		n, err := getNodeInTxn(txn, b, id)
		if err == nil {
			tagNodeSource(n, b)
			return n, nil
		}
	}
	return nil, fmt.Errorf("node %s not found in any branch", id)
}

func (s *BranchStore) DeleteByFile(_ context.Context, filePath string) error {
	b := s.writeBranch
	// Collect all node IDs for the file in the write branch, then delete in batches.
	var nodeIDs []string
	err := s.db.View(func(txn *badger.Txn) error {
		ids, err := scanIndexPrefix(txn, []byte(fmt.Sprintf("%s%s:%s:", prefixIdxFile, b, filePath)))
		if err != nil {
			return err
		}
		nodeIDs = ids
		return nil
	})
	if err != nil {
		return err
	}
	for _, id := range nodeIDs {
		err := s.db.Update(func(txn *badger.Txn) error {
			return deleteNodeInTxn(txn, b, id)
		})
		if err != nil {
			return fmt.Errorf("delete node %s for file %s: %w", id, filePath, err)
		}
	}
	return nil
}

func (s *BranchStore) Stats(_ context.Context) (*graph.GraphStats, error) {
	stats := &graph.GraphStats{
		NodesByType: make(map[graph.NodeType]int64),
		EdgesByType: make(map[graph.EdgeType]int64),
	}
	seenNodes := make(map[string]struct{})
	seenEdges := make(map[string]struct{})

	err := s.db.View(func(txn *badger.Txn) error {
		for _, branch := range s.readBranches {
			// Count nodes.
			if err := scanBranchNodes(txn, branch, func(node *graph.Node) bool {
				if _, ok := seenNodes[node.ID]; ok {
					return true
				}
				seenNodes[node.ID] = struct{}{}
				stats.NodeCount++
				stats.NodesByType[node.Type]++
				return true
			}); err != nil {
				return err
			}
			// Count edges.
			opts := badger.DefaultIteratorOptions
			opts.PrefetchValues = true
			edgePrefix := []byte(prefixEdge + branch + ":")
			opts.Prefix = edgePrefix
			it := txn.NewIterator(opts)
			defer it.Close()
			for it.Seek(edgePrefix); it.Valid(); it.Next() {
				item := it.Item()
				var edge graph.Edge
				err := item.Value(func(val []byte) error {
					return json.Unmarshal(val, &edge)
				})
				if err != nil {
					continue
				}
				if _, ok := seenEdges[edge.ID]; ok {
					continue
				}
				seenEdges[edge.ID] = struct{}{}
				stats.EdgeCount++
				stats.EdgesByType[edge.Type]++
			}
		}
		return nil
	})
	return stats, err
}

func (s *BranchStore) Close() error {
	return s.db.Close()
}

// DeleteByBranch removes all keys belonging to the given branch from the DB.
func (s *BranchStore) DeleteByBranch(branch string) error {
	// All key prefixes that contain branch data.
	prefixes := []string{
		prefixNode + branch + ":",
		prefixEdge + branch + ":",
		prefixIdxType + branch + ":",
		prefixIdxFile + branch + ":",
		prefixIdxPkg + branch + ":",
		prefixIdxEdge + branch + ":",
		prefixIdxReverseEdge + branch + ":",
		prefixIdxRole + branch + ":",
	}
	for _, prefix := range prefixes {
		if err := s.deleteKeysByPrefix([]byte(prefix)); err != nil {
			return fmt.Errorf("delete branch %s prefix %s: %w", branch, prefix, err)
		}
	}
	return nil
}

// deleteKeysByPrefix removes all keys with the given prefix.
func (s *BranchStore) deleteKeysByPrefix(prefix []byte) error {
	// Collect keys first, then delete in batches.
	var keys [][]byte
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek(prefix); it.Valid(); it.Next() {
			key := it.Item().KeyCopy(nil)
			keys = append(keys, key)
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Delete in batches to avoid transaction size limits.
	const batchSize = 1000
	for i := 0; i < len(keys); i += batchSize {
		end := i + batchSize
		if end > len(keys) {
			end = len(keys)
		}
		batch := keys[i:end]
		err := s.db.Update(func(txn *badger.Txn) error {
			for _, key := range batch {
				if err := txn.Delete(key); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// ListBranches discovers unique branch names present in the DB by scanning node key prefixes.
func (s *BranchStore) ListBranches() ([]string, error) {
	branchSet := make(map[string]struct{})
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = []byte(prefixNode)
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek(opts.Prefix); it.Valid(); it.Next() {
			key := string(it.Item().Key())
			// Key format: n:<branch>:<nodeID>
			rest := key[len(prefixNode):]
			if idx := strings.Index(rest, ":"); idx > 0 {
				branchSet[rest[:idx]] = struct{}{}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	branches := make([]string, 0, len(branchSet))
	for b := range branchSet {
		branches = append(branches, b)
	}
	return branches, nil
}

// --- helpers ---

// buildEdgeIndexPrefix constructs the prefix for scanning edge indexes.
// If edgeType is empty, it scans all edge types for the given nodeID.
func buildEdgeIndexPrefix(prefix, branch, nodeID string, edgeType graph.EdgeType) []byte {
	if edgeType == "" {
		return []byte(fmt.Sprintf("%s%s:%s:", prefix, branch, nodeID))
	}
	return []byte(fmt.Sprintf("%s%s:%s:%s:", prefix, branch, nodeID, edgeType))
}

// scanIndexPrefix scans all keys with the given prefix and extracts the trailing
// ID segment (the last colon-separated part).
func scanIndexPrefix(txn *badger.Txn, prefix []byte) ([]string, error) {
	var ids []string
	opts := badger.DefaultIteratorOptions
	opts.PrefetchValues = false
	opts.Prefix = prefix
	it := txn.NewIterator(opts)
	defer it.Close()
	for it.Seek(prefix); it.Valid(); it.Next() {
		key := string(it.Item().Key())
		// The ID is the last segment after the final colon.
		if idx := strings.LastIndex(key, ":"); idx >= 0 && idx < len(key)-1 {
			ids = append(ids, key[idx+1:])
		}
	}
	return ids, nil
}

// scanBranchNodes iterates over all node entries for a specific branch and calls fn for each.
// Return false from fn to stop iteration.
func scanBranchNodes(txn *badger.Txn, branch string, fn func(*graph.Node) bool) error {
	opts := badger.DefaultIteratorOptions
	opts.PrefetchValues = true
	branchPrefix := []byte(prefixNode + branch + ":")
	opts.Prefix = branchPrefix
	it := txn.NewIterator(opts)
	defer it.Close()
	for it.Seek(branchPrefix); it.Valid(); it.Next() {
		item := it.Item()
		var node graph.Node
		err := item.Value(func(val []byte) error {
			return json.Unmarshal(val, &node)
		})
		if err != nil {
			continue
		}
		if !fn(&node) {
			break
		}
	}
	return nil
}

func getEdgeInTxn(txn *badger.Txn, branch, id string) (*graph.Edge, error) {
	item, err := txn.Get(edgeKey(branch, id))
	if err != nil {
		return nil, fmt.Errorf("get edge %s: %w", id, err)
	}
	var edge graph.Edge
	err = item.Value(func(val []byte) error {
		return json.Unmarshal(val, &edge)
	})
	if err != nil {
		return nil, fmt.Errorf("unmarshal edge %s: %w", id, err)
	}
	return &edge, nil
}

// matchesFilter checks whether a node matches all non-zero fields in the filter.
func matchesFilter(node *graph.Node, filter graph.NodeFilter) bool {
	if filter.Type != "" && node.Type != filter.Type {
		return false
	}
	if filter.FilePath != "" && node.FilePath != filter.FilePath {
		return false
	}
	if filter.Package != "" && node.Package != filter.Package {
		return false
	}
	if filter.Language != "" && node.Language != filter.Language {
		return false
	}
	if filter.NamePattern != "" {
		matched, err := filepath.Match(filter.NamePattern, node.Name)
		if err != nil || !matched {
			return false
		}
	}
	if filter.Exported != nil && node.Exported != *filter.Exported {
		return false
	}
	// Property-based filtering: all specified key-value pairs must match.
	for key, val := range filter.Properties {
		if node.Properties == nil {
			return false
		}
		nodeVal, ok := node.Properties[key]
		if !ok {
			return false
		}
		// Support substring matching for comma-separated values (e.g., "repository" matches "repository,singleton").
		if nodeVal != val && !strings.Contains(nodeVal, val) {
			return false
		}
	}
	return true
}

// tagNodeSource sets the PropGraphSource property on a node to indicate
// which branch it came from. Set on reads only, never persisted.
func tagNodeSource(n *graph.Node, source string) {
	if n.Properties == nil {
		n.Properties = make(map[string]string)
	}
	n.Properties[graph.PropGraphSource] = source
}

// tagEdgeSource sets the PropGraphSource property on an edge to indicate
// which branch it came from. Set on reads only, never persisted.
func tagEdgeSource(e *graph.Edge, source string) {
	if e.Properties == nil {
		e.Properties = make(map[string]string)
	}
	e.Properties[graph.PropGraphSource] = source
}
