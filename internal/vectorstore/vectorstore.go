package vectorstore

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/hnsw"
	"github.com/dgraph-io/badger/v4"
	"github.com/imyousuf/CodeEagle/internal/badgerutil"
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
	// progress, when set, is called as a rebuild advances. A long rebuild
	// that prints nothing gives no way to tell slow from stuck.
	progress func(done, total int)
	// stale counts the vectors the last search skipped because their
	// nodes had left the graph.
	stale int
}

// Embedder returns the provider this store embeds with.
//
// A federated index must embed its queries with the same model, or the scores
// it returns cannot be compared with anything — which is the one thing
// federation checks before opening it.
func (vs *VectorStore) Embedder() embedding.Provider { return vs.embedder }

// WithProgress reports how far a rebuild has got.
func (vs *VectorStore) WithProgress(fn func(done, total int)) *VectorStore {
	vs.progress = fn
	return vs
}

// New creates a new VectorStore. It does not load the index; call Load() separately.
//
// Parameters:
//   - graphDB: the main graph store (for node lookups during search)
//   - embedder: the embedding provider
//   - branch: the graph branch to index
//   - idxPath: path to the HNSW index file (e.g., ".CodeEagle/vec.idx")
//   - dbPath: path to the vector BadgerDB directory (e.g., ".CodeEagle/vec.db")
//
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

func New(graphDB graph.Store, embedder embedding.Provider, branch, idxPath, dbPath string) (*VectorStore, error) {
	return newVectorStore(graphDB, embedder, branch, idxPath, dbPath, false)
}

// NewReadOnly creates a VectorStore in read-only mode, allowing concurrent
// access from multiple processes. If the WAL needs recovery, it briefly opens
// in write mode to repair, then re-opens as read-only.
func NewReadOnly(graphDB graph.Store, embedder embedding.Provider, branch, idxPath, dbPath string) (*VectorStore, error) {
	return newVectorStore(graphDB, embedder, branch, idxPath, dbPath, true)
}

func newVectorStore(graphDB graph.Store, embedder embedding.Provider, branch, idxPath, dbPath string, readOnly bool) (*VectorStore, error) {
	var db *badger.DB
	var err error

	if readOnly {
		db, err = openBadgerReadOnly(dbPath)
	} else {
		opts := badgerutil.TunedOptions(dbPath, badgerutil.DBRoleSecondary)
		db, err = badger.Open(opts)
	}
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
	empty := vs.idx.Len() == 0
	vs.mu.RUnlock()
	if empty {
		return nil, nil
	}

	// The network round trip happens outside the lock, so concurrent
	// searchers wait on the index and not on each other's embedding calls.
	queryVec, err := vs.embedder.EmbedQuery(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	vs.mu.Lock()
	defer vs.mu.Unlock()

	// A vector whose node has since left the graph is skipped, and an index
	// that has fallen behind the graph can have a third of its neighbours
	// in that state. Skipping them silently shrank every top-k, so the walk
	// widens until it has the number asked for or has looked far enough.
	var results []SearchResult
	for k := topK; ; k *= 2 {
		results = results[:0]
		vs.stale = 0
		for _, n := range vs.searchLocked(queryVec, k) {
			nodeID, chunkIdx := parseChunkKey(n.Key)
			if nodeID == "" {
				continue
			}
			node, err := vs.graphDB.GetNode(ctx, nodeID)
			if err != nil {
				vs.stale++
				continue
			}
			// CosineDistance returns distance (0 = identical), convert to similarity score.
			score := 1.0 - float64(hnsw.CosineDistance(queryVec, n.Value))
			results = append(results, SearchResult{
				Node:       node,
				Score:      score,
				ChunkText:  vs.getChunkText(nodeID, chunkIdx),
				ChunkIndex: chunkIdx,
			})
		}
		if len(results) >= topK || vs.stale == 0 || k >= vs.idx.Len() || k >= maxStaleWiden*topK {
			break
		}
	}
	if len(results) > topK {
		results = results[:topK]
	}
	return results, nil
}

// maxStaleWiden bounds how far past topK a search reaches to make up for
// stale vectors. Beyond it the index is not slightly behind but wrong, and
// the remedy is a rebuild, not a wider search.
const maxStaleWiden = 4

// exactSearchLimit is the index size up to which every vector is compared
// with the query instead of walking the graph.
//
// Measured on a 51,000-vector index: the walk returned none of the ten
// nearest segments for any of five queries — its top-200 sat at cosine
// 0.28-0.40 while the true nearest were at 0.61-0.68 — and it finished in
// 1.4ms, which is not the cost of visiting a connected graph of that size.
// Comparing every vector takes tens of milliseconds at this scale and is
// exact. The walk is kept for indices where that would no longer be true.
const exactSearchLimit = 250_000

// searchLocked returns the k nearest chunks to the query.
func (vs *VectorStore) searchLocked(queryVec []float32, k int) []hnsw.Node[string] {
	if vs.idx.Len() <= exactSearchLimit {
		if out, err := vs.exactSearch(queryVec, k); err == nil {
			return out
		}
	}
	// The candidate heap is bounded by ef, so asking for more results than
	// that returns the tail in whatever order the walk happened to visit
	// it: a caller who wants 200 candidates to rerank has to be given 200
	// real ones. The setting is restored afterwards.
	if k > vs.idx.EfSearch {
		prev := vs.idx.EfSearch
		vs.idx.EfSearch = k
		defer func() { vs.idx.EfSearch = prev }()
	}
	return vs.idx.Search(queryVec, k)
}

// exactSearch compares the query with every indexed vector. The chunk
// entries name every key the index holds; the vectors themselves are read
// from the graph, which keeps them in memory.
func (vs *VectorStore) exactSearch(queryVec []float32, k int) ([]hnsw.Node[string], error) {
	type candidate struct {
		key  string
		vec  []float32
		dist float32
	}
	var qn float64
	for _, x := range queryVec {
		qn += float64(x) * float64(x)
	}
	qn = math.Sqrt(qn)

	var cands []candidate
	err := vs.vecDB.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = []byte(prefixChunk)
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek(opts.Prefix); it.Valid(); it.Next() {
			key := strings.TrimPrefix(string(it.Item().Key()), prefixChunk)
			vec, ok := vs.idx.Lookup(key)
			if !ok || len(vec) != len(queryVec) {
				continue
			}
			// One pass for the dot product and the vector's norm; the
			// query's norm is fixed.
			var dot, vn float64
			for i, x := range vec {
				dot += float64(x) * float64(queryVec[i])
				vn += float64(x) * float64(x)
			}
			dist := float32(1)
			if vn > 0 && qn > 0 {
				dist = float32(1 - dot/(math.Sqrt(vn)*qn))
			}
			cands = append(cands, candidate{key: key, vec: vec, dist: dist})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].dist < cands[j].dist })
	if len(cands) > k {
		cands = cands[:k]
	}
	out := make([]hnsw.Node[string], len(cands))
	for i, c := range cands {
		out[i] = hnsw.MakeNode(c.key, c.vec)
	}
	return out, nil
}

// Vector returns the stored embedding of a node's first chunk, or false
// when the node is not indexed.
//
// Comparing two indexed nodes with each other — rather than with a query —
// needs the vectors as they were stored: a query is embedded under a
// different prefix and sits in a different place, so re-embedding a node's
// text as a query would measure the wrong distance.
func (vs *VectorStore) Vector(nodeID string) ([]float32, bool) {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	if vs.idx == nil {
		return nil, false
	}
	return vs.idx.Lookup(chunkKey(nodeID, 0))
}

// StaleInLastSearch reports how many of the vectors the last search
// visited belonged to nodes no longer in the graph. A high count means the
// index has fallen behind and a rebuild is due; a search alone cannot say
// so, because it cannot tell a deleted node from one that was never there.
func (vs *VectorStore) StaleInLastSearch() int {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return vs.stale
}

// Staleness counts the indexed nodes the graph no longer has, and the
// embeddable nodes the graph has that the index does not. Both are why a
// search comes back thin, and neither is visible from a search.
func (vs *VectorStore) Staleness(ctx context.Context) (stale, missing int, err error) {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	indexed := make(map[string]bool)
	err = vs.vecDB.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = []byte(prefixChunk)
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek(opts.Prefix); it.Valid(); it.Next() {
			nodeID, _ := parseChunkKey(strings.TrimPrefix(string(it.Item().Key()), prefixChunk))
			indexed[nodeID] = true
		}
		return nil
	})
	if err != nil {
		return 0, 0, fmt.Errorf("scan chunks: %w", err)
	}

	present := make(map[string]bool)
	for _, typ := range EmbeddableTypes {
		nodes, err := vs.graphDB.QueryNodes(ctx, graph.NodeFilter{Type: typ})
		if err != nil {
			return 0, 0, err
		}
		for _, n := range nodes {
			present[n.ID] = true
			if EmbeddableText(n) != "" && !indexed[n.ID] {
				missing++
			}
		}
	}
	for id := range indexed {
		if !present[id] {
			stale++
		}
	}
	return stale, missing, nil
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

// EmbedBatchSize is how many chunks are sent for embedding in one request.
//
// One request per node made indexing network-bound to the point of absurdity:
// a full rebuild spent eighty-four minutes of wall time on ninety seconds of
// work, because almost all of it was round-trip overhead — a single-word probe
// takes about as long as a full batch. Grouping chunks turns hours into
// minutes and leaves the accelerator doing the work instead of waiting.
const EmbedBatchSize = 64

// IndexNodes indexes many nodes, embedding their chunks in batches.
//
// The network call happens outside the lock. Holding it across a round trip
// would serialize every other reader of the index behind whatever the
// embedding service is doing.
func (vs *VectorStore) IndexNodes(ctx context.Context, nodes []*graph.Node) error {
	return vs.indexNodes(ctx, nodes, nil)
}

// pendingChunk is one chunk waiting to be embedded, and where it belongs.
type pendingChunk struct {
	nodeID string
	index  int
	text   string
}

// indexNodes embeds and stores nodes in batches, reporting progress if asked.
func (vs *VectorStore) indexNodes(
	ctx context.Context, nodes []*graph.Node, progress func(done, total int),
) error {
	var batch []pendingChunk
	indexed := 0

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		texts := make([]string, len(batch))
		for i, c := range batch {
			texts[i] = c.text
		}

		// Outside the lock: this is the slow part.
		embeddings, err := vs.embedder.Embed(ctx, texts)
		if err != nil {
			return fmt.Errorf("embed batch of %d: %w", len(texts), err)
		}
		if len(embeddings) != len(batch) {
			return fmt.Errorf("embed returned %d vectors for %d chunks",
				len(embeddings), len(batch))
		}

		vs.mu.Lock()
		defer vs.mu.Unlock()
		for i, c := range batch {
			key := chunkKey(c.nodeID, c.index)
			vs.idx.Add(hnsw.MakeNode(key, embeddings[i]))
			entry := ChunkEntry{NodeID: c.nodeID, ChunkIndex: c.index, ChunkText: c.text}
			if err := vs.putChunkEntry(key, entry); err != nil {
				return fmt.Errorf("store chunk %s: %w", key, err)
			}
		}
		batch = batch[:0]
		return nil
	}

	for _, node := range nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !IsEmbeddable(node.Type) {
			continue
		}
		text := EmbeddableText(node)
		if text == "" {
			continue
		}

		vs.mu.Lock()
		vs.removeNodeVectors(node.ID)
		vs.mu.Unlock()

		for i, chunk := range Chunk(text, vs.chunk) {
			batch = append(batch, pendingChunk{nodeID: node.ID, index: i, text: chunk})
			if len(batch) >= EmbedBatchSize {
				if err := flush(); err != nil {
					return err
				}
			}
		}

		indexed++
		if progress != nil {
			progress(indexed, len(nodes))
		}
	}
	return flush()
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

	// Query every embeddable node first, so the work can be counted before
	// it starts — a rebuild that prints one line and then nothing for an hour
	// gives no way to tell slow from stuck.
	var all []*graph.Node
	for _, nodeType := range EmbeddableTypes {
		nodes, err := vs.graphDB.QueryNodes(ctx, graph.NodeFilter{Type: nodeType})
		if err != nil {
			return fmt.Errorf("query %s nodes: %w", nodeType, err)
		}
		all = append(all, nodes...)
	}

	if err := vs.indexNodes(ctx, all, vs.progress); err != nil {
		return err
	}
	nodeCount := len(all)

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
	vs.meta.TextVersion = EmbeddableTextVersion
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

// NeedsReindex reports whether the index must be rebuilt in full: it was
// built with a different provider or model, or from an earlier version of
// the embeddable text. Either way its vectors and new ones would not share a
// space, and an incremental update would mix them without anything failing.
func (vs *VectorStore) NeedsReindex() bool {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	if vs.meta == nil {
		return true
	}
	return vs.meta.Provider != vs.embedder.Name() ||
		vs.meta.Model != vs.embedder.ModelName() ||
		!vs.meta.TextCurrent()
}

// ReindexReason says why NeedsReindex is true, for telling the user what
// changed rather than only that something did.
func (vs *VectorStore) ReindexReason() string {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	switch {
	case vs.meta == nil:
		return "no index metadata"
	case vs.meta.Provider != vs.embedder.Name() || vs.meta.Model != vs.embedder.ModelName():
		return fmt.Sprintf("index built with %s/%s, current provider is %s/%s",
			vs.meta.Provider, vs.meta.Model, vs.embedder.Name(), vs.embedder.ModelName())
	case !vs.meta.TextCurrent():
		return fmt.Sprintf("index built from embeddable text version %d, current is %d",
			vs.meta.TextVersion, EmbeddableTextVersion)
	}
	return ""
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
