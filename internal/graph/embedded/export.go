package embedded

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/dgraph-io/badger/v4"
	"github.com/imyousuf/CodeEagle/internal/graph"
)

// exportRecord is the JSON-lines format for export/import.
type exportRecord struct {
	Kind string          `json:"kind"` // "node" or "edge"
	Data json.RawMessage `json:"data"`
}

// Export writes all nodes and edges to w in JSON-lines format.
func (s *Store) Export(_ context.Context, w io.Writer) error {
	enc := json.NewEncoder(w)
	return s.db.View(func(txn *badger.Txn) error {
		// Export nodes.
		if err := scanAllNodes(txn, func(node *graph.Node) bool {
			data, err := json.Marshal(node)
			if err != nil {
				return true // skip bad nodes
			}
			_ = enc.Encode(exportRecord{Kind: "node", Data: data})
			return true
		}); err != nil {
			return fmt.Errorf("export nodes: %w", err)
		}

		// Export edges.
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
			data, err := json.Marshal(&edge)
			if err != nil {
				continue
			}
			if err := enc.Encode(exportRecord{Kind: "edge", Data: data}); err != nil {
				return fmt.Errorf("encode edge: %w", err)
			}
		}
		return nil
	})
}

// Import reads JSON-lines from r, clears the store, and inserts all records.
func (s *Store) Import(ctx context.Context, r io.Reader) error {
	// Clear all existing data.
	if err := s.db.DropAll(); err != nil {
		return fmt.Errorf("clear store: %w", err)
	}

	scanner := bufio.NewScanner(r)
	// Increase buffer for potentially large lines.
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec exportRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return fmt.Errorf("unmarshal record: %w", err)
		}

		switch rec.Kind {
		case "node":
			var node graph.Node
			if err := json.Unmarshal(rec.Data, &node); err != nil {
				return fmt.Errorf("unmarshal node: %w", err)
			}
			if err := s.AddNode(ctx, &node); err != nil {
				return fmt.Errorf("import node %s: %w", node.ID, err)
			}
		case "edge":
			var edge graph.Edge
			if err := json.Unmarshal(rec.Data, &edge); err != nil {
				return fmt.Errorf("unmarshal edge: %w", err)
			}
			if err := s.AddEdge(ctx, &edge); err != nil {
				return fmt.Errorf("import edge %s: %w", edge.ID, err)
			}
		default:
			return fmt.Errorf("unknown record kind: %q", rec.Kind)
		}
	}

	return scanner.Err()
}
