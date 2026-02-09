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
	prefixNode         = "n:"
	prefixEdge         = "e:"
	prefixIdxType      = "idx:type:"
	prefixIdxFile      = "idx:file:"
	prefixIdxPkg       = "idx:pkg:"
	prefixIdxEdge      = "idx:edge:"
	prefixIdxReverseEdge = "idx:redge:"
)

// Store implements graph.Store using BadgerDB.
type Store struct {
	db *badger.DB
}

// NewStore opens (or creates) a BadgerDB-backed graph store at dbPath.
func NewStore(dbPath string) (*Store, error) {
	opts := badger.DefaultOptions(dbPath)
	opts.Logger = nil // suppress badger logs
	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("open badger db: %w", err)
	}
	return &Store{db: db}, nil
}

// nodeKey returns the primary key for a node.
func nodeKey(id string) []byte { return []byte(prefixNode + id) }

// edgeKey returns the primary key for an edge.
func edgeKey(id string) []byte { return []byte(prefixEdge + id) }

// indexTypeKey returns a secondary index key for node type lookup.
func indexTypeKey(nodeType graph.NodeType, id string) []byte {
	return []byte(fmt.Sprintf("%s%s:%s", prefixIdxType, nodeType, id))
}

// indexFileKey returns a secondary index key for file path lookup.
func indexFileKey(filePath, id string) []byte {
	return []byte(fmt.Sprintf("%s%s:%s", prefixIdxFile, filePath, id))
}

// indexPkgKey returns a secondary index key for package lookup.
func indexPkgKey(pkg, id string) []byte {
	return []byte(fmt.Sprintf("%s%s:%s", prefixIdxPkg, pkg, id))
}

// indexEdgeKey returns a secondary index key for forward edge lookup.
func indexEdgeKey(sourceID string, edgeType graph.EdgeType, edgeID string) []byte {
	return []byte(fmt.Sprintf("%s%s:%s:%s", prefixIdxEdge, sourceID, edgeType, edgeID))
}

// indexReverseEdgeKey returns a secondary index key for reverse edge lookup.
func indexReverseEdgeKey(targetID string, edgeType graph.EdgeType, edgeID string) []byte {
	return []byte(fmt.Sprintf("%s%s:%s:%s", prefixIdxReverseEdge, targetID, edgeType, edgeID))
}

func (s *Store) AddNode(_ context.Context, node *graph.Node) error {
	data, err := json.Marshal(node)
	if err != nil {
		return fmt.Errorf("marshal node: %w", err)
	}
	return s.db.Update(func(txn *badger.Txn) error {
		if err := txn.Set(nodeKey(node.ID), data); err != nil {
			return err
		}
		if err := txn.Set(indexTypeKey(node.Type, node.ID), nil); err != nil {
			return err
		}
		if node.FilePath != "" {
			if err := txn.Set(indexFileKey(node.FilePath, node.ID), nil); err != nil {
				return err
			}
		}
		if node.Package != "" {
			if err := txn.Set(indexPkgKey(node.Package, node.ID), nil); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) UpdateNode(_ context.Context, node *graph.Node) error {
	data, err := json.Marshal(node)
	if err != nil {
		return fmt.Errorf("marshal node: %w", err)
	}
	return s.db.Update(func(txn *badger.Txn) error {
		// Read existing node to clean up old indexes if fields changed.
		old, err := getNodeInTxn(txn, node.ID)
		if err != nil {
			return fmt.Errorf("get existing node for update: %w", err)
		}
		// Remove stale indexes.
		if old.Type != node.Type {
			_ = txn.Delete(indexTypeKey(old.Type, old.ID))
		}
		if old.FilePath != node.FilePath && old.FilePath != "" {
			_ = txn.Delete(indexFileKey(old.FilePath, old.ID))
		}
		if old.Package != node.Package && old.Package != "" {
			_ = txn.Delete(indexPkgKey(old.Package, old.ID))
		}
		// Write new data and indexes.
		if err := txn.Set(nodeKey(node.ID), data); err != nil {
			return err
		}
		if err := txn.Set(indexTypeKey(node.Type, node.ID), nil); err != nil {
			return err
		}
		if node.FilePath != "" {
			if err := txn.Set(indexFileKey(node.FilePath, node.ID), nil); err != nil {
				return err
			}
		}
		if node.Package != "" {
			if err := txn.Set(indexPkgKey(node.Package, node.ID), nil); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) DeleteNode(_ context.Context, id string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		return deleteNodeInTxn(txn, id)
	})
}

// deleteNodeInTxn removes a node and all its edges within a transaction.
func deleteNodeInTxn(txn *badger.Txn, id string) error {
	node, err := getNodeInTxn(txn, id)
	if err != nil {
		return err
	}
	// Delete forward edges (this node as source).
	edgeIDs, err := scanIndexPrefix(txn, []byte(fmt.Sprintf("%s%s:", prefixIdxEdge, id)))
	if err != nil {
		return err
	}
	for _, eid := range edgeIDs {
		if err := deleteEdgeInTxn(txn, eid); err != nil {
			return err
		}
	}
	// Delete reverse edges (this node as target).
	redgeIDs, err := scanIndexPrefix(txn, []byte(fmt.Sprintf("%s%s:", prefixIdxReverseEdge, id)))
	if err != nil {
		return err
	}
	for _, eid := range redgeIDs {
		if err := deleteEdgeInTxn(txn, eid); err != nil {
			return err
		}
	}
	// Delete indexes.
	_ = txn.Delete(indexTypeKey(node.Type, id))
	if node.FilePath != "" {
		_ = txn.Delete(indexFileKey(node.FilePath, id))
	}
	if node.Package != "" {
		_ = txn.Delete(indexPkgKey(node.Package, id))
	}
	// Delete the node itself.
	return txn.Delete(nodeKey(id))
}

func (s *Store) GetNode(_ context.Context, id string) (*graph.Node, error) {
	var node *graph.Node
	err := s.db.View(func(txn *badger.Txn) error {
		var e error
		node, e = getNodeInTxn(txn, id)
		return e
	})
	return node, err
}

func getNodeInTxn(txn *badger.Txn, id string) (*graph.Node, error) {
	item, err := txn.Get(nodeKey(id))
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

func (s *Store) QueryNodes(_ context.Context, filter graph.NodeFilter) ([]*graph.Node, error) {
	var nodeIDs []string
	var useFullScan bool

	err := s.db.View(func(txn *badger.Txn) error {
		// Pick the most selective index.
		switch {
		case filter.FilePath != "":
			ids, err := scanIndexPrefix(txn, []byte(prefixIdxFile+filter.FilePath+":"))
			if err != nil {
				return err
			}
			nodeIDs = ids
		case filter.Type != "":
			ids, err := scanIndexPrefix(txn, []byte(prefixIdxType+string(filter.Type)+":"))
			if err != nil {
				return err
			}
			nodeIDs = ids
		case filter.Package != "":
			ids, err := scanIndexPrefix(txn, []byte(prefixIdxPkg+filter.Package+":"))
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

	var results []*graph.Node
	err = s.db.View(func(txn *badger.Txn) error {
		if useFullScan {
			return scanAllNodes(txn, func(node *graph.Node) bool {
				if matchesFilter(node, filter) {
					results = append(results, node)
				}
				return true
			})
		}
		for _, id := range nodeIDs {
			node, err := getNodeInTxn(txn, id)
			if err != nil {
				continue // index entry for deleted node; skip
			}
			if matchesFilter(node, filter) {
				results = append(results, node)
			}
		}
		return nil
	})
	return results, err
}

func (s *Store) AddEdge(_ context.Context, edge *graph.Edge) error {
	data, err := json.Marshal(edge)
	if err != nil {
		return fmt.Errorf("marshal edge: %w", err)
	}
	return s.db.Update(func(txn *badger.Txn) error {
		if err := txn.Set(edgeKey(edge.ID), data); err != nil {
			return err
		}
		if err := txn.Set(indexEdgeKey(edge.SourceID, edge.Type, edge.ID), nil); err != nil {
			return err
		}
		return txn.Set(indexReverseEdgeKey(edge.TargetID, edge.Type, edge.ID), nil)
	})
}

func (s *Store) DeleteEdge(_ context.Context, id string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		return deleteEdgeInTxn(txn, id)
	})
}

func deleteEdgeInTxn(txn *badger.Txn, id string) error {
	item, err := txn.Get(edgeKey(id))
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
	_ = txn.Delete(indexEdgeKey(edge.SourceID, edge.Type, edge.ID))
	_ = txn.Delete(indexReverseEdgeKey(edge.TargetID, edge.Type, edge.ID))
	return txn.Delete(edgeKey(id))
}

func (s *Store) GetEdges(_ context.Context, nodeID string, edgeType graph.EdgeType) ([]*graph.Edge, error) {
	var results []*graph.Edge
	err := s.db.View(func(txn *badger.Txn) error {
		// Forward edges.
		fwdPrefix := buildEdgeIndexPrefix(prefixIdxEdge, nodeID, edgeType)
		fwdIDs, err := scanIndexPrefix(txn, fwdPrefix)
		if err != nil {
			return err
		}
		// Reverse edges.
		revPrefix := buildEdgeIndexPrefix(prefixIdxReverseEdge, nodeID, edgeType)
		revIDs, err := scanIndexPrefix(txn, revPrefix)
		if err != nil {
			return err
		}
		seen := make(map[string]struct{})
		for _, eid := range append(fwdIDs, revIDs...) {
			if _, ok := seen[eid]; ok {
				continue
			}
			seen[eid] = struct{}{}
			e, err := getEdgeInTxn(txn, eid)
			if err != nil {
				continue
			}
			results = append(results, e)
		}
		return nil
	})
	return results, err
}

func (s *Store) GetNeighbors(_ context.Context, nodeID string, edgeType graph.EdgeType, direction graph.Direction) ([]*graph.Node, error) {
	var results []*graph.Node
	err := s.db.View(func(txn *badger.Txn) error {
		seen := make(map[string]struct{})

		// Outgoing: nodeID is source -> follow forward index -> neighbor is target.
		if direction == graph.Outgoing || direction == graph.Both {
			prefix := buildEdgeIndexPrefix(prefixIdxEdge, nodeID, edgeType)
			edgeIDs, err := scanIndexPrefix(txn, prefix)
			if err != nil {
				return err
			}
			for _, eid := range edgeIDs {
				e, err := getEdgeInTxn(txn, eid)
				if err != nil {
					continue
				}
				if _, ok := seen[e.TargetID]; ok {
					continue
				}
				seen[e.TargetID] = struct{}{}
				n, err := getNodeInTxn(txn, e.TargetID)
				if err != nil {
					continue
				}
				results = append(results, n)
			}
		}

		// Incoming: nodeID is target -> follow reverse index -> neighbor is source.
		if direction == graph.Incoming || direction == graph.Both {
			prefix := buildEdgeIndexPrefix(prefixIdxReverseEdge, nodeID, edgeType)
			edgeIDs, err := scanIndexPrefix(txn, prefix)
			if err != nil {
				return err
			}
			for _, eid := range edgeIDs {
				e, err := getEdgeInTxn(txn, eid)
				if err != nil {
					continue
				}
				if _, ok := seen[e.SourceID]; ok {
					continue
				}
				seen[e.SourceID] = struct{}{}
				n, err := getNodeInTxn(txn, e.SourceID)
				if err != nil {
					continue
				}
				results = append(results, n)
			}
		}

		return nil
	})
	return results, err
}

func (s *Store) DeleteByFile(_ context.Context, filePath string) error {
	// Collect all node IDs for the file first, then delete in batches.
	var nodeIDs []string
	err := s.db.View(func(txn *badger.Txn) error {
		ids, err := scanIndexPrefix(txn, []byte(prefixIdxFile+filePath+":"))
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
			return deleteNodeInTxn(txn, id)
		})
		if err != nil {
			return fmt.Errorf("delete node %s for file %s: %w", id, filePath, err)
		}
	}
	return nil
}

func (s *Store) Stats(_ context.Context) (*graph.GraphStats, error) {
	stats := &graph.GraphStats{
		NodesByType: make(map[graph.NodeType]int64),
		EdgesByType: make(map[graph.EdgeType]int64),
	}
	err := s.db.View(func(txn *badger.Txn) error {
		// Count nodes.
		err := scanAllNodes(txn, func(node *graph.Node) bool {
			stats.NodeCount++
			stats.NodesByType[node.Type]++
			return true
		})
		if err != nil {
			return err
		}
		// Count edges.
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = true
		opts.Prefix = []byte(prefixEdge)
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek(opts.Prefix); it.Valid(); it.Next() {
			item := it.Item()
			var edge graph.Edge
			err := item.Value(func(val []byte) error {
				return json.Unmarshal(val, &edge)
			})
			if err != nil {
				continue
			}
			stats.EdgeCount++
			stats.EdgesByType[edge.Type]++
		}
		return nil
	})
	return stats, err
}

func (s *Store) Close() error {
	return s.db.Close()
}

// --- helpers ---

// buildEdgeIndexPrefix constructs the prefix for scanning edge indexes.
// If edgeType is empty, it scans all edge types for the given nodeID.
func buildEdgeIndexPrefix(prefix, nodeID string, edgeType graph.EdgeType) []byte {
	if edgeType == "" {
		return []byte(fmt.Sprintf("%s%s:", prefix, nodeID))
	}
	return []byte(fmt.Sprintf("%s%s:%s:", prefix, nodeID, edgeType))
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

// scanAllNodes iterates over all node entries and calls fn for each.
// Return false from fn to stop iteration.
func scanAllNodes(txn *badger.Txn, fn func(*graph.Node) bool) error {
	opts := badger.DefaultIteratorOptions
	opts.PrefetchValues = true
	opts.Prefix = []byte(prefixNode)
	it := txn.NewIterator(opts)
	defer it.Close()
	for it.Seek(opts.Prefix); it.Valid(); it.Next() {
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

func getEdgeInTxn(txn *badger.Txn, id string) (*graph.Edge, error) {
	item, err := txn.Get(edgeKey(id))
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
	return true
}
