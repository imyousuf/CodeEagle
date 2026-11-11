package vectorstore

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"sort"
	"strings"
	"sync"
)

// ErrDetached is returned by a store that has been detached from its
// persistence (Store.Detach) and asked to change or save anything.
var ErrDetached = errors.New("vectorstore: detached from its persistence")

// Persistence holds what a Store cannot rebuild from the graph: each chunk's
// text, a hash of the text each node was embedded from, the index metadata,
// and the vector index itself. Calls are serialised by the Store's lock.
//
// Chunk keys are "<nodeID>:<n>", the same labels the vector index uses.
type Persistence interface {
	// PutChunk stores a chunk under its key.
	PutChunk(key string, e ChunkEntry) error
	// ChunkText returns a chunk's text. A chunk that cannot be read is
	// shown without text, so any error means "".
	ChunkText(key string) (string, error)
	// EachChunkKey calls fn with every chunk key, in ascending byte order:
	// which of two equally near chunks a search returns depends on it.
	EachChunkKey(fn func(key string)) error
	// DeleteNode removes a node's chunks ("<nodeID>:*") and its text hash.
	// The Store goes on even if it fails, as a search tolerates a chunk
	// whose vector is gone.
	DeleteNode(nodeID string) error
	// PutTextHashes records, by node ID, a hash of the text each node's
	// vectors were made from.
	PutTextHashes(hashes map[string]string) error
	// TextHashes returns every recorded hash, by node ID.
	TextHashes() (map[string]string, error)
	// Clear removes every chunk and text hash, but not the metadata. The
	// Store goes on even if it fails.
	Clear() error
	// LoadMeta returns a namespace's metadata, or nil and no error when
	// there is none.
	LoadMeta(namespace string) (*VectorIndexMeta, error)
	// SaveMeta stores a namespace's metadata.
	SaveMeta(namespace string, m *VectorIndexMeta) error
	// OpenIndex reads the saved vector index. When none was saved its
	// error satisfies errors.Is(err, fs.ErrNotExist).
	OpenIndex() (io.ReadCloser, error)
	// CreateIndex replaces the saved vector index with what is written
	// before Close.
	CreateIndex() (io.WriteCloser, error)
	// Close releases the persistence.
	Close() error
}

// NewMemoryPersistence returns a Persistence held in memory, for tests and
// for an index that is rebuilt rather than kept.
func NewMemoryPersistence() Persistence {
	return &memory{chunks: make(map[string]ChunkEntry), hashes: make(map[string]string), meta: make(map[string]VectorIndexMeta)}
}

type memory struct {
	mu     sync.Mutex
	chunks map[string]ChunkEntry
	hashes map[string]string
	meta   map[string]VectorIndexMeta
	index  []byte
	saved  bool
}

func (m *memory) PutChunk(key string, e ChunkEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chunks[key] = e
	return nil
}

func (m *memory) ChunkText(key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.chunks[key]
	if !ok {
		return "", fs.ErrNotExist
	}
	return e.ChunkText, nil
}

func (m *memory) EachChunkKey(fn func(key string)) error {
	m.mu.Lock()
	keys := make([]string, 0, len(m.chunks))
	for k := range m.chunks {
		keys = append(keys, k)
	}
	m.mu.Unlock()
	sort.Strings(keys)
	for _, k := range keys {
		fn(k)
	}
	return nil
}

func (m *memory) DeleteNode(nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.chunks {
		if strings.HasPrefix(k, nodeID+":") {
			delete(m.chunks, k)
		}
	}
	delete(m.hashes, nodeID)
	return nil
}

func (m *memory) PutTextHashes(hashes map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, h := range hashes {
		m.hashes[id] = h
	}
	return nil
}

func (m *memory) TextHashes() (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]string, len(m.hashes))
	for id, h := range m.hashes {
		out[id] = h
	}
	return out, nil
}

func (m *memory) Clear() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chunks = make(map[string]ChunkEntry)
	m.hashes = make(map[string]string)
	return nil
}

func (m *memory) LoadMeta(namespace string) (*VectorIndexMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	meta, ok := m.meta[namespace]
	if !ok {
		return nil, nil
	}
	return &meta, nil
}

func (m *memory) SaveMeta(namespace string, meta *VectorIndexMeta) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.meta[namespace] = *meta
	return nil
}

func (m *memory) OpenIndex() (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.saved {
		return nil, &fs.PathError{Op: "open", Path: "vec.idx", Err: fs.ErrNotExist}
	}
	return io.NopCloser(bytes.NewReader(m.index)), nil
}

func (m *memory) CreateIndex() (io.WriteCloser, error) {
	return &memoryIndex{m: m}, nil
}

func (m *memory) Close() error { return nil }

// memoryIndex buffers a saved index, replacing the old one only on Close, as
// a file written to completion would.
type memoryIndex struct {
	m   *memory
	buf bytes.Buffer
}

func (w *memoryIndex) Write(p []byte) (int, error) { return w.buf.Write(p) }

func (w *memoryIndex) Close() error {
	w.m.mu.Lock()
	defer w.m.mu.Unlock()
	w.m.index, w.m.saved = append([]byte(nil), w.buf.Bytes()...), true
	return nil
}
