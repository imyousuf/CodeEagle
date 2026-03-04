package docs

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dgraph-io/badger/v4"
)

const (
	// Key prefixes for docs cache BadgerDB.
	prefixHash   = "doc:hash:"   // doc:hash:<filePath> → content hash
	prefixResult = "doc:result:" // doc:result:<contentHash> → ExtractionResult JSON
	prefixSkip   = "doc:skip:"   // doc:skip:<contentHash> → "1" (extraction failed)
)

// Cache provides content-hash-based caching for docs extraction results.
// It uses a separate BadgerDB at .CodeEagle/docs.db/ to avoid coupling
// with the graph store or vector store.
type Cache struct {
	db *badger.DB
}

// OpenCache opens (or creates) the docs cache at the given directory path.
func OpenCache(dbPath string) (*Cache, error) {
	opts := badger.DefaultOptions(dbPath)
	opts.Logger = nil
	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("open docs cache: %w", err)
	}
	return &Cache{db: db}, nil
}

// Close closes the underlying BadgerDB.
func (c *Cache) Close() error {
	return c.db.Close()
}

// Check returns the cached ExtractionResult for a file if the content hash matches.
// Returns nil, nil if no cached result exists or if the hash has changed.
func (c *Cache) Check(filePath, contentHash string) (*ExtractionResult, error) {
	var storedHash string
	err := c.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(prefixHash + filePath))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			storedHash = string(val)
			return nil
		})
	})
	if err != nil {
		return nil, nil // no cached hash — cache miss
	}

	if storedHash != contentHash {
		return nil, nil // hash changed — cache miss
	}

	// Hash matches — look up the cached result.
	var result ExtractionResult
	err = c.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(prefixResult + contentHash))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &result)
		})
	})
	if err != nil {
		return nil, nil // no cached result — cache miss
	}

	return &result, nil
}

// Store saves the content hash and extraction result for a file.
func (c *Cache) Store(filePath, contentHash string, result *ExtractionResult) error {
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal extraction result: %w", err)
	}

	return c.db.Update(func(txn *badger.Txn) error {
		if err := txn.Set([]byte(prefixHash+filePath), []byte(contentHash)); err != nil {
			return err
		}
		if err := txn.Set([]byte(prefixResult+contentHash), data); err != nil {
			return err
		}
		// Clear any skip marker for this hash.
		_ = txn.Delete([]byte(prefixSkip + contentHash))
		return nil
	})
}

// MarkSkipped records that extraction failed for this content hash.
func (c *Cache) MarkSkipped(filePath, contentHash string) error {
	return c.db.Update(func(txn *badger.Txn) error {
		if err := txn.Set([]byte(prefixHash+filePath), []byte(contentHash)); err != nil {
			return err
		}
		return txn.Set([]byte(prefixSkip+contentHash), []byte("1"))
	})
}

// IsSkipped returns true if extraction was previously skipped for this content hash.
func (c *Cache) IsSkipped(contentHash string) bool {
	err := c.db.View(func(txn *badger.Txn) error {
		_, err := txn.Get([]byte(prefixSkip + contentHash))
		return err
	})
	return err == nil
}

// ListSkipped returns all file paths whose extraction was skipped.
func (c *Cache) ListSkipped() ([]string, error) {
	// First collect all content hashes that are skipped.
	skippedHashes := make(map[string]bool)
	err := c.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		prefix := []byte(prefixSkip)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			key := string(it.Item().Key())
			hash := strings.TrimPrefix(key, prefixSkip)
			skippedHashes[hash] = true
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list skipped hashes: %w", err)
	}

	if len(skippedHashes) == 0 {
		return nil, nil
	}

	// Then find file paths that map to skipped hashes.
	var paths []string
	err = c.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		prefix := []byte(prefixHash)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			filePath := strings.TrimPrefix(string(item.Key()), prefixHash)
			if err := item.Value(func(val []byte) error {
				if skippedHashes[string(val)] {
					paths = append(paths, filePath)
				}
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list skipped paths: %w", err)
	}

	return paths, nil
}
