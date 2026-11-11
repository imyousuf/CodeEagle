package vectorstore

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dgraph-io/badger/v4"

	"github.com/imyousuf/CodeEagle/internal/badgerutil"
	kitvectorstore "github.com/imyousuf/CodeEagle/pkg/kit/vectorstore"
)

const (
	// Key prefixes for vector BadgerDB.
	prefixChunk = "chunk:" // chunk:<nodeID>:<chunkIndex> → ChunkEntry JSON
	prefixMeta  = "meta:"  // meta:<branch> → VectorIndexMeta JSON
	// prefixText records, per node, a hash of the text its vectors were made
	// from, so a node whose text changed is embedded again.
	prefixText = "text:" // text:<nodeID> → hash of the embedded text
)

// BadgerPersistence keeps a vector index's chunks, text hashes and metadata
// in a BadgerDB of its own (vec.db) and the index itself in a file (vec.idx).
type BadgerPersistence struct {
	db      *badger.DB
	idxPath string
}

var _ kitvectorstore.Persistence = (*BadgerPersistence)(nil)

// OpenBadger opens the vector database at dbPath, with the index file at
// idxPath. Read-only allows concurrent access from several processes; if the
// write-ahead log needs recovery, it briefly opens for writing to repair it,
// then reopens read-only.
func OpenBadger(dbPath, idxPath string, readOnly bool) (*BadgerPersistence, error) {
	var db *badger.DB
	var err error
	if readOnly {
		db, err = openBadgerReadOnly(dbPath)
	} else {
		db, err = badger.Open(badgerutil.TunedOptions(dbPath, badgerutil.DBRoleSecondary))
	}
	if err != nil {
		return nil, err
	}
	return &BadgerPersistence{db: db, idxPath: idxPath}, nil
}

// openBadgerReadOnly tries to open BadgerDB in read-only mode. If the WAL needs
// recovery, it briefly opens in write mode to flush, closes, and retries read-only.
func openBadgerReadOnly(dbPath string) (*badger.DB, error) {
	opts := badgerutil.TunedOptionsReadOnly(dbPath, badgerutil.DBRoleSecondary)

	db, err := badger.Open(opts)
	if err == nil {
		return db, nil
	}

	if !strings.Contains(err.Error(), "truncate required") {
		return nil, fmt.Errorf("open badger db (read-only): %w", err)
	}

	// WAL needs recovery — briefly open in write mode.
	repairOpts := badgerutil.TunedOptions(dbPath, badgerutil.DBRoleSecondary)
	repairDB, repairErr := badger.Open(repairOpts)
	if repairErr != nil {
		return nil, fmt.Errorf("repair badger WAL: %w", repairErr)
	}
	repairDB.Close()

	db, err = badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("open badger db (read-only after repair): %w", err)
	}
	return db, nil
}

// PutChunk stores a chunk as JSON under chunk:<key>.
func (b *BadgerPersistence) PutChunk(key string, entry ChunkEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return b.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(prefixChunk+key), data)
	})
}

// ChunkText reads a chunk's text.
func (b *BadgerPersistence) ChunkText(key string) (string, error) {
	var text string
	err := b.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(prefixChunk + key))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			var entry ChunkEntry
			if err := json.Unmarshal(val, &entry); err != nil {
				return err
			}
			text = entry.ChunkText
			return nil
		})
	})
	return text, err
}

// EachChunkKey visits every chunk key in Badger's (byte) order.
func (b *BadgerPersistence) EachChunkKey(fn func(key string)) error {
	return b.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = []byte(prefixChunk)
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek(opts.Prefix); it.Valid(); it.Next() {
			fn(strings.TrimPrefix(string(it.Item().Key()), prefixChunk))
		}
		return nil
	})
}

// DeleteNode removes a node's chunks and its text hash.
func (b *BadgerPersistence) DeleteNode(nodeID string) error {
	b.deleteByPrefix([]byte(prefixChunk + nodeID + ":"))
	_ = b.db.Update(func(txn *badger.Txn) error { return txn.Delete([]byte(prefixText + nodeID)) })
	return nil
}

// PutTextHashes records the text each node's vectors were made from.
func (b *BadgerPersistence) PutTextHashes(hashes map[string]string) error {
	wb := b.db.NewWriteBatch()
	defer wb.Cancel()
	for id, h := range hashes {
		if err := wb.Set([]byte(prefixText+id), []byte(h)); err != nil {
			return fmt.Errorf("store text hash of %s: %w", id, err)
		}
	}
	return wb.Flush()
}

// TextHashes reads every recorded text hash, by node ID.
func (b *BadgerPersistence) TextHashes() (map[string]string, error) {
	out := make(map[string]string)
	err := b.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(prefixText)
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek(opts.Prefix); it.Valid(); it.Next() {
			item := it.Item()
			v, err := item.ValueCopy(nil)
			if err != nil {
				return err
			}
			out[strings.TrimPrefix(string(item.Key()), prefixText)] = string(v)
		}
		return nil
	})
	return out, err
}

// Clear removes every chunk and text hash.
func (b *BadgerPersistence) Clear() error {
	b.deleteByPrefix([]byte(prefixChunk))
	b.deleteByPrefix([]byte(prefixText))
	return nil
}

func (b *BadgerPersistence) deleteByPrefix(prefix []byte) {
	var keys [][]byte
	_ = b.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek(prefix); it.Valid(); it.Next() {
			keys = append(keys, it.Item().KeyCopy(nil))
		}
		return nil
	})

	const batchSize = 1000
	for i := 0; i < len(keys); i += batchSize {
		end := min(i+batchSize, len(keys))
		_ = b.db.Update(func(txn *badger.Txn) error {
			for _, k := range keys[i:end] {
				_ = txn.Delete(k)
			}
			return nil
		})
	}
}

// LoadMeta reads meta:<namespace>; none is (nil, nil).
func (b *BadgerPersistence) LoadMeta(namespace string) (*VectorIndexMeta, error) {
	key := []byte(prefixMeta + namespace)
	var meta VectorIndexMeta
	err := b.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &meta)
		})
	})
	if err != nil {
		if err == badger.ErrKeyNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &meta, nil
}

// SaveMeta writes meta:<namespace> as JSON.
func (b *BadgerPersistence) SaveMeta(namespace string, meta *VectorIndexMeta) error {
	key := []byte(prefixMeta + namespace)
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return b.db.Update(func(txn *badger.Txn) error {
		return txn.Set(key, data)
	})
}

// OpenIndex opens vec.idx.
func (b *BadgerPersistence) OpenIndex() (io.ReadCloser, error) { return os.Open(b.idxPath) }

// CreateIndex truncates vec.idx for writing.
func (b *BadgerPersistence) CreateIndex() (io.WriteCloser, error) { return os.Create(b.idxPath) }

// Close closes the database.
func (b *BadgerPersistence) Close() error { return b.db.Close() }
