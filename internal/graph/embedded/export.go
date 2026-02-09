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
	Kind   string          `json:"kind"`             // "node" or "edge"
	Branch string          `json:"branch,omitempty"` // branch name (empty for legacy exports)
	Data   json.RawMessage `json:"data"`
}

// Export writes all nodes and edges from the write branch to w in JSON-lines format.
func (s *BranchStore) Export(_ context.Context, w io.Writer) error {
	return s.ExportBranch(context.Background(), w, s.writeBranch)
}

// ExportBranch writes all nodes and edges for the given branch to w in JSON-lines format.
func (s *BranchStore) ExportBranch(_ context.Context, w io.Writer, branch string) error {
	enc := json.NewEncoder(w)
	return s.db.View(func(txn *badger.Txn) error {
		// Export nodes.
		if err := scanBranchNodes(txn, branch, func(node *graph.Node) bool {
			data, err := json.Marshal(node)
			if err != nil {
				return true // skip bad nodes
			}
			_ = enc.Encode(exportRecord{Kind: "node", Branch: branch, Data: data})
			return true
		}); err != nil {
			return fmt.Errorf("export nodes: %w", err)
		}

		// Export edges.
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
			data, err := json.Marshal(&edge)
			if err != nil {
				continue
			}
			if err := enc.Encode(exportRecord{Kind: "edge", Branch: branch, Data: data}); err != nil {
				return fmt.Errorf("encode edge: %w", err)
			}
		}
		return nil
	})
}

// Import reads JSON-lines from r and imports into the store.
// If records have a Branch field (new format), imports into that branch via ImportIntoBranch.
// If no Branch field (legacy format), clears the entire DB and imports into writeBranch.
func (s *BranchStore) Import(ctx context.Context, r io.Reader) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	// Peek at the first record to determine if it's branch-aware.
	if !scanner.Scan() {
		return scanner.Err() // empty file
	}
	firstLine := scanner.Bytes()
	if len(firstLine) == 0 {
		return nil
	}

	var firstRec exportRecord
	if err := json.Unmarshal(firstLine, &firstRec); err != nil {
		return fmt.Errorf("unmarshal first record: %w", err)
	}

	if firstRec.Branch != "" {
		// Branch-aware format: clear the target branch and import all records.
		if err := s.DeleteByBranch(firstRec.Branch); err != nil {
			return fmt.Errorf("clear branch %s: %w", firstRec.Branch, err)
		}
		// Process first record.
		if err := s.importRecord(ctx, firstRec, firstRec.Branch); err != nil {
			return err
		}
		// Process remaining records.
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var rec exportRecord
			if err := json.Unmarshal(line, &rec); err != nil {
				return fmt.Errorf("unmarshal record: %w", err)
			}
			targetBranch := rec.Branch
			if targetBranch == "" {
				targetBranch = firstRec.Branch
			}
			if err := s.importRecord(ctx, rec, targetBranch); err != nil {
				return err
			}
		}
	} else {
		// Legacy format: clear entire DB and import into writeBranch.
		if err := s.db.DropAll(); err != nil {
			return fmt.Errorf("clear store: %w", err)
		}
		if err := s.importRecord(ctx, firstRec, s.writeBranch); err != nil {
			return err
		}
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var rec exportRecord
			if err := json.Unmarshal(line, &rec); err != nil {
				return fmt.Errorf("unmarshal record: %w", err)
			}
			if err := s.importRecord(ctx, rec, s.writeBranch); err != nil {
				return err
			}
		}
	}

	return scanner.Err()
}

// ImportIntoBranch clears the target branch and imports records from r into it.
// Returns the source branch name from the export data.
func (s *BranchStore) ImportIntoBranch(ctx context.Context, r io.Reader, targetBranch string) (sourceBranch string, err error) {
	if err := s.DeleteByBranch(targetBranch); err != nil {
		return "", fmt.Errorf("clear target branch %s: %w", targetBranch, err)
	}

	// Temporarily set writeBranch to targetBranch for import.
	origBranch := s.writeBranch
	s.writeBranch = targetBranch
	defer func() { s.writeBranch = origBranch }()

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec exportRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return sourceBranch, fmt.Errorf("unmarshal record: %w", err)
		}
		if rec.Branch != "" && sourceBranch == "" {
			sourceBranch = rec.Branch
		}
		if err := s.importRecord(ctx, rec, targetBranch); err != nil {
			return sourceBranch, err
		}
	}

	return sourceBranch, scanner.Err()
}

// ReadExportBranch reads the first record from an export file to extract the branch name.
func ReadExportBranch(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec exportRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return "", fmt.Errorf("unmarshal record: %w", err)
		}
		return rec.Branch, nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", nil // empty file
}

// importRecord adds a single export record to the store under the given branch.
func (s *BranchStore) importRecord(ctx context.Context, rec exportRecord, branch string) error {
	origBranch := s.writeBranch
	s.writeBranch = branch
	defer func() { s.writeBranch = origBranch }()

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
	return nil
}
