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

// MigrateResult holds statistics about a path migration.
type MigrateResult struct {
	NodesScanned   int
	NodesMigrated  int
	EdgesScanned   int
	EdgesRemapped  int
	BranchesFound  []string
}

// MigrateAbsToRelPaths converts absolute file paths to relative paths
// in all nodes and edges across all branches. It rebuilds node IDs (since
// IDs are derived from file paths) and remaps edge source/target references.
//
// repoRoots are the repository root paths to strip from absolute paths.
// If dryRun is true, no writes are performed and only the result stats are returned.
func (s *BranchStore) MigrateAbsToRelPaths(ctx context.Context, repoRoots []string, dryRun bool) (*MigrateResult, error) {
	result := &MigrateResult{}

	branches, err := s.ListBranches()
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}
	result.BranchesFound = branches

	for _, branch := range branches {
		if err := s.migrateBranch(ctx, branch, repoRoots, dryRun, result); err != nil {
			return result, fmt.Errorf("migrate branch %s: %w", branch, err)
		}
	}

	return result, nil
}

func (s *BranchStore) migrateBranch(_ context.Context, branch string, repoRoots []string, dryRun bool, result *MigrateResult) error {
	// Pass 1: Collect all nodes and build ID mapping.
	type nodeEntry struct {
		oldID string
		node  *graph.Node
		newID string
	}
	var nodesToMigrate []nodeEntry
	idMapping := make(map[string]string) // oldID -> newID

	err := s.db.View(func(txn *badger.Txn) error {
		return scanBranchNodes(txn, branch, func(node *graph.Node) bool {
			result.NodesScanned++
			nodeCopy := *node
			oldPath := nodeCopy.FilePath

			if oldPath == "" || !filepath.IsAbs(oldPath) {
				// Already relative or no path — no migration needed for path,
				// but still track the ID in case edges reference it.
				idMapping[nodeCopy.ID] = nodeCopy.ID
				return true
			}

			// Convert to relative path.
			relPath := toRelPath(oldPath, repoRoots)
			if relPath == oldPath {
				// No matching repo root — keep as-is.
				idMapping[nodeCopy.ID] = nodeCopy.ID
				return true
			}

			nodeCopy.FilePath = relPath

			// Update Name for NodeFile type (set to absolute path by parsers).
			if nodeCopy.Type == graph.NodeFile {
				nodeCopy.Name = relPath
			}

			// Compute new node ID.
			newID := graph.NewNodeID(string(nodeCopy.Type), relPath, nodeCopy.Name)
			idMapping[nodeCopy.ID] = newID

			nodesToMigrate = append(nodesToMigrate, nodeEntry{
				oldID: nodeCopy.ID,
				node:  &nodeCopy,
				newID: newID,
			})
			result.NodesMigrated++
			return true
		})
	})
	if err != nil {
		return fmt.Errorf("scan nodes: %w", err)
	}

	// Pass 2: Collect all edges and remap IDs.
	type edgeEntry struct {
		oldID string
		edge  *graph.Edge
		newID string
	}
	var edgesToMigrate []edgeEntry

	err = s.db.View(func(txn *badger.Txn) error {
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
			result.EdgesScanned++

			edgeCopy := edge
			oldEdgeID := edgeCopy.ID

			newSourceID, srcOk := idMapping[edgeCopy.SourceID]
			newTargetID, tgtOk := idMapping[edgeCopy.TargetID]

			changed := false
			if srcOk && newSourceID != edgeCopy.SourceID {
				edgeCopy.SourceID = newSourceID
				changed = true
			}
			if tgtOk && newTargetID != edgeCopy.TargetID {
				edgeCopy.TargetID = newTargetID
				changed = true
			}

			if changed {
				// Recompute edge ID from its components.
				newEdgeID := fmt.Sprintf("%s-%s-%s", edgeCopy.SourceID, edgeCopy.Type, edgeCopy.TargetID)
				edgeCopy.ID = newEdgeID

				edgesToMigrate = append(edgesToMigrate, edgeEntry{
					oldID: oldEdgeID,
					edge:  &edgeCopy,
					newID: newEdgeID,
				})
				result.EdgesRemapped++
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("scan edges: %w", err)
	}

	if dryRun {
		return nil
	}

	// Pass 3: Delete old nodes and write new ones.
	// Process in batches to avoid transaction size limits.
	origBranch := s.writeBranch
	s.writeBranch = branch
	defer func() { s.writeBranch = origBranch }()

	for _, ne := range nodesToMigrate {
		err := s.db.Update(func(txn *badger.Txn) error {
			// Delete old node and its indexes.
			if err := deleteNodeInTxn(txn, branch, ne.oldID); err != nil {
				// Node may have already been deleted if it shared an ID; skip.
				return nil
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("delete old node %s: %w", ne.oldID, err)
		}

		// Write new node with updated ID and path.
		ne.node.ID = ne.newID
		data, err := json.Marshal(ne.node)
		if err != nil {
			return fmt.Errorf("marshal node %s: %w", ne.newID, err)
		}
		err = s.db.Update(func(txn *badger.Txn) error {
			if err := txn.Set(nodeKey(branch, ne.newID), data); err != nil {
				return err
			}
			if err := txn.Set(indexTypeKey(branch, ne.node.Type, ne.newID), nil); err != nil {
				return err
			}
			if ne.node.FilePath != "" {
				if err := txn.Set(indexFileKey(branch, ne.node.FilePath, ne.newID), nil); err != nil {
					return err
				}
			}
			if ne.node.Package != "" {
				if err := txn.Set(indexPkgKey(branch, ne.node.Package, ne.newID), nil); err != nil {
					return err
				}
			}
			if role := nodeArchRole(ne.node); role != "" {
				if err := txn.Set(indexRoleKey(branch, role, ne.newID), nil); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("write new node %s: %w", ne.newID, err)
		}
	}

	// Pass 4: Delete old edges and write new ones.
	for _, ee := range edgesToMigrate {
		err := s.db.Update(func(txn *badger.Txn) error {
			// Try to delete old edge (may fail if node deletion already cascaded).
			_ = deleteEdgeInTxn(txn, branch, ee.oldID)
			return nil
		})
		if err != nil {
			return fmt.Errorf("delete old edge %s: %w", ee.oldID, err)
		}

		data, err := json.Marshal(ee.edge)
		if err != nil {
			return fmt.Errorf("marshal edge %s: %w", ee.newID, err)
		}
		err = s.db.Update(func(txn *badger.Txn) error {
			if err := txn.Set(edgeKey(branch, ee.newID), data); err != nil {
				return err
			}
			if err := txn.Set(indexEdgeKey(branch, ee.edge.SourceID, ee.edge.Type, ee.newID), nil); err != nil {
				return err
			}
			return txn.Set(indexReverseEdgeKey(branch, ee.edge.TargetID, ee.edge.Type, ee.newID), nil)
		})
		if err != nil {
			return fmt.Errorf("write new edge %s: %w", ee.newID, err)
		}
	}

	return nil
}

// toRelPath converts an absolute path to a relative path using the given roots.
func toRelPath(absPath string, roots []string) string {
	for _, root := range roots {
		rel, err := filepath.Rel(root, absPath)
		if err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return absPath
}
