package vectorstore

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/hnsw"
	"github.com/dgraph-io/badger/v4"
	"github.com/imyousuf/CodeEagle/internal/embedding"
	"github.com/imyousuf/CodeEagle/internal/graph"
)

const (
	// Key prefixes for vector BadgerDB.
	prefixChunk = "chunk:" // chunk:<nodeID>:<chunkIndex> → ChunkEntry JSON
	prefixMeta  = "meta:"  // meta:<branch> → VectorIndexMeta JSON

	// HNSW tuning parameters.
	hnswM  = 16 // max neighbors per node
	hnswEf = 40 // search candidates
)

// ChunkEntry stores the text and metadata for an indexed chunk.
type ChunkEntry struct {
	NodeID     string `json:"node_id"`
	ChunkIndex int    `json:"chunk_index"`
	ChunkText  string `json:"chunk_text"`
}

// SearchResult represents a single vector search result.
type SearchResult struct {
	Node       *graph.Node
	Score      float64
	ChunkText  string
	ChunkIndex int
}

// VectorStore provides vector search over the knowledge graph using HNSW.
// It stores vectors in an HNSW index file and chunk metadata in its own BadgerDB.
type VectorStore struct {
	mu       sync.RWMutex
	idx      *hnsw.Graph[string] // in-memory HNSW, labels are "nodeID:chunkIdx"
	vecDB    *badger.DB          // separate BadgerDB for chunk text + metadata
	graphDB  graph.Store         // the main knowledge graph store
	embedder embedding.Provider
	branch   string
	idxPath  string // path to vec.idx file
	dbPath   string // path to vec.db directory
	meta     *VectorIndexMeta
	chunk    ChunkConfig
}

// New creates a new VectorStore. It does not load the index; call Load() separately.
//
// Parameters:
//   - graphDB: the main graph store (for node lookups during search)
//   - embedder: the embedding provider
//   - branch: the graph branch to index
//   - idxPath: path to the HNSW index file (e.g., ".CodeEagle/vec.idx")
//   - dbPath: path to the vector BadgerDB directory (e.g., ".CodeEagle/vec.db")
func New(graphDB graph.Store, embedder embedding.Provider, branch, idxPath, dbPath string) (*VectorStore, error) {
	opts := badger.DefaultOptions(dbPath)
	opts.Logger = nil
	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("open vector db: %w", err)
	}

	g := hnsw.NewGraph[string]()
	g.M = hnswM
	g.EfSearch = hnswEf
	g.Distance = hnsw.CosineDistance

	return &VectorStore{
		idx:      g,
		vecDB:    db,
		graphDB:  graphDB,
		embedder: embedder,
		branch:   branch,
		idxPath:  idxPath,
		dbPath:   dbPath,
		chunk:    DefaultChunkConfig(),
	}, nil
}

// Available returns true if vector search is usable.
func (vs *VectorStore) Available() bool {
	return vs != nil && vs.embedder != nil && vs.idx != nil
}

// Search performs a semantic search and returns the top-K results.
func (vs *VectorStore) Search(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	if vs.idx.Len() == 0 {
		return nil, nil
	}

	queryVec, err := vs.embedder.EmbedQuery(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	neighbors := vs.idx.Search(queryVec, topK)

	var results []SearchResult
	for _, n := range neighbors {
		nodeID, chunkIdx := parseChunkKey(n.Key)
		if nodeID == "" {
			continue
		}

		node, err := vs.graphDB.GetNode(ctx, nodeID)
		if err != nil {
			continue // node may have been deleted
		}

		chunkText := vs.getChunkText(nodeID, chunkIdx)

		// CosineDistance returns distance (0 = identical), convert to similarity score.
		score := 1.0 - float64(hnsw.CosineDistance(queryVec, n.Value))

		results = append(results, SearchResult{
			Node:       node,
			Score:      score,
			ChunkText:  chunkText,
			ChunkIndex: chunkIdx,
		})
	}

	return results, nil
}

// IndexNode indexes a single node's embeddable text.
func (vs *VectorStore) IndexNode(ctx context.Context, node *graph.Node) error {
	if !IsEmbeddable(node.Type) {
		return nil
	}

	text := EmbeddableText(node)
	if text == "" {
		return nil
	}

	vs.mu.Lock()
	defer vs.mu.Unlock()

	// Remove old vectors for this node.
	vs.removeNodeVectors(node.ID)

	// Chunk the text.
	chunks := Chunk(text, vs.chunk)

	// Embed all chunks.
	embeddings, err := vs.embedder.Embed(ctx, chunks)
	if err != nil {
		return fmt.Errorf("embed node %s: %w", node.ID, err)
	}

	// Store each chunk.
	for i, vec := range embeddings {
		key := chunkKey(node.ID, i)

		// Add to HNSW.
		vs.idx.Add(hnsw.MakeNode(key, vec))

		// Store chunk text in BadgerDB.
		entry := ChunkEntry{
			NodeID:     node.ID,
			ChunkIndex: i,
			ChunkText:  chunks[i],
		}
		if err := vs.putChunkEntry(key, entry); err != nil {
			return fmt.Errorf("store chunk %s: %w", key, err)
		}
	}

	return nil
}

// IndexNodes indexes multiple nodes in batch.
func (vs *VectorStore) IndexNodes(ctx context.Context, nodes []*graph.Node) error {
	for _, node := range nodes {
		if err := vs.IndexNode(ctx, node); err != nil {
			return err
		}
	}
	return nil
}

// RemoveNode removes all vectors for a node.
func (vs *VectorStore) RemoveNode(_ context.Context, nodeID string) error {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	vs.removeNodeVectors(nodeID)
	return nil
}

// Rebuild performs a full reindex from the graph store.
func (vs *VectorStore) Rebuild(ctx context.Context) error {
	vs.mu.Lock()
	// Create a fresh HNSW graph.
	vs.idx = hnsw.NewGraph[string]()
	vs.idx.M = hnswM
	vs.idx.EfSearch = hnswEf
	vs.idx.Distance = hnsw.CosineDistance
	vs.mu.Unlock()

	// Clear all chunk entries.
	if err := vs.clearAllChunks(); err != nil {
		return fmt.Errorf("clear chunks: %w", err)
	}

	// Query all nodes and index embeddable ones.
	nodeCount := 0
	for _, nodeType := range EmbeddableTypes {
		nodes, err := vs.graphDB.QueryNodes(ctx, graph.NodeFilter{Type: nodeType})
		if err != nil {
			return fmt.Errorf("query %s nodes: %w", nodeType, err)
		}
		for _, node := range nodes {
			if err := vs.IndexNode(ctx, node); err != nil {
				return fmt.Errorf("index node %s: %w", node.ID, err)
			}
			nodeCount++
		}
	}

	// Update metadata.
	now := time.Now()
	vs.mu.Lock()
	if vs.meta == nil {
		vs.meta = &VectorIndexMeta{
			CreatedAt: now,
			Version:   1,
		}
	} else {
		vs.meta.Version++
	}
	vs.meta.Provider = vs.embedder.Name()
	vs.meta.Model = vs.embedder.ModelName()
	vs.meta.Dimensions = vs.embedder.Dimensions()
	vs.meta.ChunkSize = vs.chunk.ChunkSize
	vs.meta.Overlap = vs.chunk.Overlap
	vs.meta.UpdatedAt = now
	vs.meta.NodeCount = nodeCount
	vs.mu.Unlock()

	return nil
}

// Load loads the HNSW index from disk and metadata from BadgerDB.
// Returns false if no index exists on disk.
func (vs *VectorStore) Load() (bool, error) {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	// Load metadata.
	meta, err := vs.loadMeta()
	if err != nil {
		return false, fmt.Errorf("load meta: %w", err)
	}
	vs.meta = meta

	// Load HNSW index.
	f, err := os.Open(vs.idxPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("open vec.idx: %w", err)
	}
	defer f.Close()

	g := hnsw.NewGraph[string]()
	g.M = hnswM
	g.EfSearch = hnswEf
	g.Distance = hnsw.CosineDistance
	if err := g.Import(bufio.NewReader(f)); err != nil {
		return false, fmt.Errorf("import vec.idx: %w", err)
	}
	vs.idx = g

	return true, nil
}

// Save persists the HNSW index to disk and metadata to BadgerDB.
func (vs *VectorStore) Save() error {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	// Save HNSW index.
	f, err := os.Create(vs.idxPath)
	if err != nil {
		return fmt.Errorf("create vec.idx: %w", err)
	}
	if err := vs.idx.Export(f); err != nil {
		f.Close()
		return fmt.Errorf("export vec.idx: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close vec.idx: %w", err)
	}

	// Save metadata.
	if vs.meta != nil {
		vs.meta.UpdatedAt = time.Now()
		if err := vs.saveMeta(vs.meta); err != nil {
			return fmt.Errorf("save meta: %w", err)
		}
	}

	return nil
}

// Close releases resources.
func (vs *VectorStore) Close() error {
	if vs.vecDB != nil {
		return vs.vecDB.Close()
	}
	return nil
}

// Meta returns the current index metadata (may be nil if not yet loaded/built).
func (vs *VectorStore) Meta() *VectorIndexMeta {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return vs.meta
}

// LoadMetaOnly loads just the metadata from BadgerDB without loading the HNSW index.
// Useful for status display when we only need metadata info.
func (vs *VectorStore) LoadMetaOnly() (*VectorIndexMeta, error) {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	meta, err := vs.loadMeta()
	if err != nil {
		return nil, err
	}
	vs.meta = meta
	return meta, nil
}

// Len returns the number of vectors in the HNSW index.
func (vs *VectorStore) Len() int {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return vs.idx.Len()
}

// NeedsReindex checks if the current index was built with a different provider/model.
func (vs *VectorStore) NeedsReindex() bool {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	if vs.meta == nil {
		return true
	}
	return vs.meta.Provider != vs.embedder.Name() || vs.meta.Model != vs.embedder.ModelName()
}

// --- internal helpers ---

func chunkKey(nodeID string, chunkIdx int) string {
	return nodeID + ":" + strconv.Itoa(chunkIdx)
}

func parseChunkKey(key string) (nodeID string, chunkIdx int) {
	idx := strings.LastIndex(key, ":")
	if idx < 0 {
		return key, 0
	}
	ci, err := strconv.Atoi(key[idx+1:])
	if err != nil {
		return key, 0
	}
	return key[:idx], ci
}

func (vs *VectorStore) removeNodeVectors(nodeID string) {
	// Remove from HNSW — try chunk indices 0..99 (generous upper bound).
	for i := range 100 {
		key := chunkKey(nodeID, i)
		if !vs.idx.Delete(key) {
			break // no more chunks for this node
		}
	}

	// Remove chunk entries from BadgerDB.
	prefix := []byte(prefixChunk + nodeID + ":")
	vs.deleteByPrefix(prefix)
}

func (vs *VectorStore) getChunkText(nodeID string, chunkIdx int) string {
	key := []byte(prefixChunk + chunkKey(nodeID, chunkIdx))
	var text string
	_ = vs.vecDB.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
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
	return text
}

func (vs *VectorStore) putChunkEntry(key string, entry ChunkEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return vs.vecDB.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(prefixChunk+key), data)
	})
}

func (vs *VectorStore) clearAllChunks() error {
	vs.deleteByPrefix([]byte(prefixChunk))
	return nil
}

func (vs *VectorStore) deleteByPrefix(prefix []byte) {
	var keys [][]byte
	_ = vs.vecDB.View(func(txn *badger.Txn) error {
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
		_ = vs.vecDB.Update(func(txn *badger.Txn) error {
			for _, k := range keys[i:end] {
				_ = txn.Delete(k)
			}
			return nil
		})
	}
}

func (vs *VectorStore) loadMeta() (*VectorIndexMeta, error) {
	key := []byte(prefixMeta + vs.branch)
	var meta VectorIndexMeta
	err := vs.vecDB.View(func(txn *badger.Txn) error {
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

func (vs *VectorStore) saveMeta(meta *VectorIndexMeta) error {
	key := []byte(prefixMeta + vs.branch)
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return vs.vecDB.Update(func(txn *badger.Txn) error {
		return txn.Set(key, data)
	})
}
