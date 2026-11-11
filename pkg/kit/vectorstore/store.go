// Package vectorstore provides vector search over a graph's nodes: an HNSW
// index of their embedded text, searched exactly below a size limit, kept in
// a Persistence of the caller's choosing.
//
// Which node types are embedded, and what text stands for a node, are the
// caller's policy (WithEmbeddable, WithEmbedText); the defaults embed
// documents by their text.
package vectorstore

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/hnsw"

	"github.com/imyousuf/CodeEagle/pkg/kit/embedding"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
)

const (
	// HNSW tuning parameters.
	hnswM  = 16 // max neighbors per node
	hnswEf = 40 // search candidates
)

// DefaultNamespace keys the metadata when WithNamespace is not given.
const DefaultNamespace = "default"

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

// Store provides vector search over a graph using HNSW. It keeps vectors in
// an index and chunk text and metadata in its Persistence.
type Store struct {
	mu       sync.RWMutex
	idx      *hnsw.Graph[string] // in-memory HNSW, labels are "nodeID:chunkIdx"
	p        Persistence         // nil once detached
	graphDB  graph.Store         // the graph whose nodes are indexed
	embedder embedding.Provider
	opts     options
	meta     *VectorIndexMeta
	// progress, when set, is called as a rebuild advances. A long rebuild
	// that prints nothing gives no way to tell slow from stuck.
	progress func(done, total int)
	// stale counts the vectors the last search skipped because their
	// nodes had left the graph.
	stale int
	// detachedKeys lists the indexed chunk keys once the persistence has
	// been closed by Detach.
	detachedKeys []string
	// deleted counts the vectors removed from the graph since it was loaded
	// or rebuilt; a graph with deletions is rebuilt before it is saved.
	deleted int
}

// options is the caller's policy.
type options struct {
	types              []graph.NodeType
	embeddable         map[graph.NodeType]bool
	embedText          func(*graph.Node) string
	textVersion        int
	reembedWithoutHash func(*graph.Node) bool
	namespace          string
	chunk              ChunkConfig
}

// Option configures a Store.
type Option func(*options)

// WithEmbeddable sets the node types that are embedded. Their order is the
// order a rebuild reads them in, which decides how nodes updated at the same
// moment are ordered. The default is documents only.
func WithEmbeddable(types ...graph.NodeType) Option {
	return func(o *options) { o.types = append([]graph.NodeType(nil), types...) }
}

// WithEmbedText sets the text embedded for a node ("" means not embedded)
// and the version of that text. The version is recorded in the index, and an
// index built from another version is rebuilt (NeedsReindex): vectors from
// two versions of the text do not share a space. Bump it whenever fn's
// output changes. The default is DefaultEmbedText, version 1.
func WithEmbedText(fn func(*graph.Node) string, version int) Option {
	return func(o *options) { o.embedText, o.textVersion = fn, version }
}

// WithReembedWithoutHash says which nodes indexed before text hashes were
// recorded are embedded again once; any other is taken as current, rather
// than re-embedding a whole index on upgrade. The default re-embeds none.
func WithReembedWithoutHash(fn func(*graph.Node) bool) Option {
	return func(o *options) { o.reembedWithoutHash = fn }
}

// WithNamespace keys the index metadata. Only the metadata is per
// namespace: chunks, text hashes and the index are shared by every
// namespace in one Persistence.
func WithNamespace(ns string) Option {
	return func(o *options) { o.namespace = ns }
}

// WithChunkConfig sets how text is split into chunks.
func WithChunkConfig(c ChunkConfig) Option {
	return func(o *options) { o.chunk = c }
}

// DefaultEmbedText embeds a node's DocComment, after its QualifiedName when
// it has one; a node without a DocComment is not embedded.
func DefaultEmbedText(n *graph.Node) string {
	if n.DocComment == "" {
		return ""
	}
	if n.QualifiedName != "" {
		return n.QualifiedName + "\n" + n.DocComment
	}
	return n.DocComment
}

// New returns a store over graphDB that embeds with embedder and keeps its
// state in p. It does not load the index; call Load. A nil embedder makes a
// store that can read metadata but not search or embed.
func New(graphDB graph.Store, embedder embedding.Provider, p Persistence, opts ...Option) *Store {
	o := options{
		types:       []graph.NodeType{graph.NodeDocument},
		embedText:   DefaultEmbedText,
		textVersion: 1,
		namespace:   DefaultNamespace,
		chunk:       DefaultChunkConfig(),
	}
	for _, opt := range opts {
		opt(&o)
	}
	o.embeddable = make(map[graph.NodeType]bool, len(o.types))
	for _, t := range o.types {
		o.embeddable[t] = true
	}
	return &Store{
		idx:      newGraph(),
		p:        p,
		graphDB:  graphDB,
		embedder: embedder,
		opts:     o,
	}
}

// Embedder returns the provider this store embeds with.
//
// A federated index must embed its queries with the same model, or the scores
// it returns cannot be compared with anything — which is the one thing
// federation checks before opening it.
func (vs *Store) Embedder() embedding.Provider { return vs.embedder }

// WithProgress reports how far a rebuild has got.
func (vs *Store) WithProgress(fn func(done, total int)) *Store {
	vs.progress = fn
	return vs
}

// IsEmbeddable reports whether nodes of a type are embedded.
func (vs *Store) IsEmbeddable(t graph.NodeType) bool { return vs.opts.embeddable[t] }

// Available returns true if vector search is usable.
func (vs *Store) Available() bool {
	return vs != nil && vs.embedder != nil && vs.idx != nil
}

// Search performs a semantic search and returns the top-K results.
func (vs *Store) Search(ctx context.Context, query string, topK int) ([]SearchResult, error) {
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
func (vs *Store) searchLocked(queryVec []float32, k int) []hnsw.Node[string] {
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
	return vs.safeSearchLocked(queryVec, k)
}

// safeSearchLocked searches the graph, rebuilding it once if the walk
// crashes on a link a Delete left behind.
func (vs *Store) safeSearchLocked(queryVec []float32, k int) (out []hnsw.Node[string]) {
	defer func() {
		if recover() != nil {
			vs.rebuildLocked("")
			out = vs.idx.Search(queryVec, k)
		}
	}()
	return vs.idx.Search(queryVec, k)
}

// addLocked adds one vector, rebuilding the graph once if the Add crashes
// on a link a Delete left behind.
func (vs *Store) addLocked(key string, vec []float32) {
	defer func() {
		if recover() != nil {
			vs.rebuildLocked(key)
			vs.idx.Add(hnsw.MakeNode(key, vec))
		}
	}()
	vs.idx.Add(hnsw.MakeNode(key, vec))
}

// rebuildLocked replaces the graph with a fresh one holding the same live
// vectors, all but skip.
//
// coder/hnsw v0.6.1's Delete leaves neighbour links to the nodes it removed.
// Saved and loaded again, those links come back nil, and the next Add or
// search that walks past one dereferences it: re-embedding rewritten briefs
// in one run crashed the next. Lookup still finds every live vector, so a
// clean graph is rebuilt from them without embedding anything again, and
// keys whose chunks are gone are left out.
func (vs *Store) rebuildLocked(skip string) {
	var keys []string
	_ = vs.eachChunkKey(func(key string) {
		if key != skip {
			keys = append(keys, key)
		}
	})
	fresh := newGraph()
	for _, key := range keys {
		if vec, ok := vs.idx.Lookup(key); ok {
			fresh.Add(hnsw.MakeNode(key, vec))
		}
	}
	vs.idx = fresh
	vs.deleted = 0
}

// exactSearch compares the query with every indexed vector. The chunk
// entries name every key the index holds; the vectors themselves are read
// from the graph, which keeps them in memory.
func (vs *Store) exactSearch(queryVec []float32, k int) ([]hnsw.Node[string], error) {
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
	err := vs.eachChunkKey(func(key string) {
		vec, ok := vs.idx.Lookup(key)
		if !ok || len(vec) != len(queryVec) {
			return
		}
		// One pass for the dot product and the vector's norm; the query's
		// norm is fixed.
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
func (vs *Store) Vector(nodeID string) ([]float32, bool) {
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
func (vs *Store) StaleInLastSearch() int {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return vs.stale
}

// Staleness counts the indexed nodes the graph no longer has, and the
// embeddable nodes the graph has that the index does not. Both are why a
// search comes back thin, and neither is visible from a search.
func (vs *Store) Staleness(ctx context.Context) (stale, missing int, err error) {
	staleIDs, missingNodes, err := vs.gaps(ctx, nil)
	return len(staleIDs), len(missingNodes), err
}

// Reconcile closes the gap between the index and the graph in place: it drops
// the vectors of nodes the graph no longer has and embeds up to limit
// embeddable nodes it lacks or whose text changed since they were embedded,
// most recently updated first. Zero means no limit.
//
// Until now only a full rebuild embedded anything, so a document indexed after
// the last rebuild was invisible to search by meaning. Reconciling on every
// sync keeps the index current without a rebuild, and the limit keeps one sync
// from embedding a whole corpus: a large gap closes over several runs.
//
// The graph store must read every scope whose nodes are in the index (the
// branch, meetings, relations), or their vectors look stale and are dropped.
func (vs *Store) Reconcile(ctx context.Context, limit int) (added, removed int, err error) {
	if vs.detached() {
		return 0, 0, ErrDetached
	}
	staleIDs, missing, err := vs.gaps(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	vs.mu.Lock()
	for _, id := range staleIDs {
		vs.removeNodeVectors(id)
	}
	vs.mu.Unlock()
	if limit > 0 && len(missing) > limit {
		missing = missing[:limit]
	}
	if err := vs.IndexNodes(ctx, missing); err != nil {
		return 0, len(staleIDs), err
	}
	return len(missing), len(staleIDs), nil
}

// EmbedMissing embeds up to limit nodes of the given types that the index
// lacks, and removes nothing. It is for a store that sees only part of the
// graph, such as the meetings scope alone: anything it cannot see would look
// stale to it, so it must not judge staleness.
func (vs *Store) EmbedMissing(ctx context.Context, limit int, types ...graph.NodeType) (int, error) {
	if vs.detached() {
		return 0, ErrDetached
	}
	_, missing, err := vs.gaps(ctx, types)
	if err != nil {
		return 0, err
	}
	if limit > 0 && len(missing) > limit {
		missing = missing[:limit]
	}
	if err := vs.IndexNodes(ctx, missing); err != nil {
		return 0, err
	}
	return len(missing), nil
}

// gaps lists the indexed node IDs the graph no longer has, and the embeddable
// nodes of the given types (all embeddable types when none are given) the
// index lacks, most recently updated first.
func (vs *Store) gaps(ctx context.Context, types []graph.NodeType) (stale []string, missing []*graph.Node, err error) {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	indexed := make(map[string]bool)
	err = vs.eachChunkKey(func(key string) {
		nodeID, _ := parseChunkKey(key)
		indexed[nodeID] = true
	})
	if err != nil {
		return nil, nil, fmt.Errorf("scan chunks: %w", err)
	}

	hashes, err := vs.textHashes()
	if err != nil {
		return nil, nil, fmt.Errorf("scan text hashes: %w", err)
	}

	if len(types) == 0 {
		types = vs.opts.types
	}
	present := make(map[string]bool)
	for _, typ := range types {
		nodes, err := vs.graphDB.QueryNodes(ctx, graph.NodeFilter{Type: typ})
		if err != nil {
			return nil, nil, err
		}
		for _, n := range nodes {
			present[n.ID] = true
			if !vs.IsEmbeddable(n.Type) {
				continue
			}
			text := vs.opts.embedText(n)
			if text == "" {
				continue
			}
			if !indexed[n.ID] || vs.changedText(n, hashes[n.ID], text) {
				missing = append(missing, n)
			}
		}
	}
	sort.SliceStable(missing, func(i, j int) bool {
		return missing[i].UpdatedAt.After(missing[j].UpdatedAt)
	})
	// Staleness is only judged over every embeddable type: with a subset,
	// nodes of the other types are absent from present, not from the graph.
	if len(types) == len(vs.opts.types) {
		for id := range indexed {
			if !present[id] {
				stale = append(stale, id)
			}
		}
		sort.Strings(stale)
	}
	return stale, missing, nil
}

// IndexNode indexes a single node's embeddable text.
func (vs *Store) IndexNode(ctx context.Context, node *graph.Node) error {
	if !vs.IsEmbeddable(node.Type) {
		return nil
	}

	text := vs.opts.embedText(node)
	if text == "" {
		return nil
	}
	if vs.detached() {
		return ErrDetached
	}

	vs.mu.Lock()
	defer vs.mu.Unlock()

	// Remove old vectors for this node.
	vs.removeNodeVectors(node.ID)

	// Chunk the text.
	chunks := Chunk(text, vs.opts.chunk)

	// Embed all chunks.
	embeddings, err := vs.embedder.Embed(ctx, chunks)
	if err != nil {
		return fmt.Errorf("embed node %s: %w", node.ID, err)
	}

	// Store each chunk.
	for i, vec := range embeddings {
		key := chunkKey(node.ID, i)

		// Add to HNSW.
		vs.addLocked(key, vec)

		// Store chunk text.
		entry := ChunkEntry{
			NodeID:     node.ID,
			ChunkIndex: i,
			ChunkText:  chunks[i],
		}
		if err := vs.p.PutChunk(key, entry); err != nil {
			return fmt.Errorf("store chunk %s: %w", key, err)
		}
	}
	return vs.putTextHashes(map[string]string{node.ID: textHash(text)})
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
func (vs *Store) IndexNodes(ctx context.Context, nodes []*graph.Node) error {
	if vs.detached() {
		return ErrDetached
	}
	return vs.indexNodes(ctx, nodes, nil)
}

// changedText reports whether an indexed node's text has changed since its
// vectors were made. A node indexed before hashes were recorded has none: it
// is embedded again only if WithReembedWithoutHash says so, and otherwise
// taken as current rather than re-embedding the whole index on upgrade.
func (vs *Store) changedText(n *graph.Node, stored, text string) bool {
	if stored == "" {
		return vs.opts.reembedWithoutHash != nil && vs.opts.reembedWithoutHash(n)
	}
	return stored != textHash(text)
}

// pendingChunk is one chunk waiting to be embedded, and where it belongs.
type pendingChunk struct {
	nodeID string
	index  int
	text   string
}

// indexNodes embeds and stores nodes in batches, reporting progress if asked.
func (vs *Store) indexNodes(
	ctx context.Context, nodes []*graph.Node, progress func(done, total int),
) error {
	var batch []pendingChunk
	indexed := 0
	hashes := make(map[string]string)

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
			vs.addLocked(key, embeddings[i])
			entry := ChunkEntry{NodeID: c.nodeID, ChunkIndex: c.index, ChunkText: c.text}
			if err := vs.p.PutChunk(key, entry); err != nil {
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
		if !vs.IsEmbeddable(node.Type) {
			continue
		}
		text := vs.opts.embedText(node)
		if text == "" {
			continue
		}

		vs.mu.Lock()
		vs.removeNodeVectors(node.ID)
		vs.mu.Unlock()
		hashes[node.ID] = textHash(text)

		for i, chunk := range Chunk(text, vs.opts.chunk) {
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
	if err := flush(); err != nil {
		return err
	}
	return vs.putTextHashes(hashes)
}

// RemoveNode removes all vectors for a node.
func (vs *Store) RemoveNode(_ context.Context, nodeID string) error {
	if vs.detached() {
		return ErrDetached
	}
	vs.mu.Lock()
	defer vs.mu.Unlock()
	vs.removeNodeVectors(nodeID)
	return nil
}

// Rebuild performs a full reindex from the graph store.
func (vs *Store) Rebuild(ctx context.Context) error {
	if vs.detached() {
		return ErrDetached
	}
	vs.mu.Lock()
	vs.idx = newGraph()
	vs.mu.Unlock()

	// Record that a rebuild started, before the first deletion and durably,
	// so that a failure between here and the end is visible afterwards. The
	// chunks are about to go while the graph of vectors on disk still
	// describes all of them; without this marker the two disagree silently
	// and every later search answers from whatever survived.
	if err := vs.markRebuilding(true); err != nil {
		return fmt.Errorf("mark rebuild started: %w", err)
	}

	// Clear all chunk entries. A failure leaves chunks the rebuild then
	// overwrites, so it is not a reason to stop.
	_ = vs.p.Clear()

	// Query every embeddable node first, so the work can be counted before
	// it starts — a rebuild that prints one line and then nothing for an hour
	// gives no way to tell slow from stuck.
	var all []*graph.Node
	for _, nodeType := range vs.opts.types {
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
	vs.meta.ChunkSize = vs.opts.chunk.ChunkSize
	vs.meta.Overlap = vs.opts.chunk.Overlap
	vs.meta.UpdatedAt = now
	vs.meta.NodeCount = nodeCount
	vs.meta.TextVersion = vs.opts.textVersion
	vs.meta.EmbedConfig = vs.embedConfig()
	vs.meta.Rebuilding = false
	vs.mu.Unlock()

	// Persisted here rather than left to the caller's Save: a caller that
	// returns early on an error would otherwise leave the marker set, which
	// is correct, but one that succeeds must clear it even if saving the
	// graph fails afterwards for an unrelated reason.
	if err := vs.markRebuilding(false); err != nil {
		return fmt.Errorf("mark rebuild finished: %w", err)
	}

	return nil
}

// markRebuilding persists whether a rebuild is in progress.
//
// Written straight through to the store rather than held in memory, because
// the failure it guards against is the process not reaching its own cleanup.
func (vs *Store) markRebuilding(active bool) error {
	vs.mu.Lock()
	if vs.meta == nil {
		vs.meta = &VectorIndexMeta{CreatedAt: time.Now(), Version: 1}
	}
	vs.meta.Rebuilding = active
	meta := *vs.meta
	vs.mu.Unlock()
	return vs.p.SaveMeta(vs.opts.namespace, &meta)
}

// Rebuilding reports whether a rebuild began and never finished, which makes
// the stored vectors an arbitrary fraction of the corpus.
func (vs *Store) Rebuilding() bool {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return vs.meta != nil && vs.meta.Rebuilding
}

// Load loads the HNSW index and the metadata from the persistence.
// Returns false if no index has been saved.
func (vs *Store) Load() (bool, error) {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	if vs.p == nil {
		return false, ErrDetached
	}

	// Load metadata.
	meta, err := vs.p.LoadMeta(vs.opts.namespace)
	if err != nil {
		return false, fmt.Errorf("load meta: %w", err)
	}
	vs.meta = meta

	// Load HNSW index.
	f, err := vs.p.OpenIndex()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("open vec.idx: %w", err)
	}
	defer f.Close()

	g := newGraph()
	if err := g.Import(bufio.NewReader(f)); err != nil {
		return false, fmt.Errorf("import vec.idx: %w", err)
	}
	vs.idx = g

	return true, nil
}

// Save persists the HNSW index and the metadata.
func (vs *Store) Save() error {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	if vs.p == nil {
		return ErrDetached
	}

	// A graph saved with links to deleted nodes loads with nil ones.
	if vs.deleted > 0 {
		vs.rebuildLocked("")
	}

	// Save HNSW index.
	f, err := vs.p.CreateIndex()
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
		if err := vs.p.SaveMeta(vs.opts.namespace, vs.meta); err != nil {
			return fmt.Errorf("save meta: %w", err)
		}
	}

	return nil
}

// Close releases the persistence; a detached store has nothing to release.
func (vs *Store) Close() error {
	if vs.p != nil {
		return vs.p.Close()
	}
	return nil
}

// detached reports whether Detach has closed the persistence.
func (vs *Store) detached() bool {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return vs.p == nil
}

// eachChunkKey calls fn with every key the index holds: from the
// persistence, or from the list kept when the store was detached.
func (vs *Store) eachChunkKey(fn func(key string)) error {
	if vs.p == nil {
		for _, key := range vs.detachedKeys {
			fn(key)
		}
		return nil
	}
	return vs.p.EachChunkKey(fn)
}

// Detach closes the persistence once the index is loaded, so a reader that
// keeps the index for a long session holds no lock on it: a read-only
// Badger handle takes a shared lock that keeps every writer out while it is
// open. A detached store still searches, without chunk text; anything that
// would change or save it returns ErrDetached.
func (vs *Store) Detach() error {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	if vs.p == nil {
		return nil
	}
	var keys []string
	if err := vs.eachChunkKey(func(key string) { keys = append(keys, key) }); err != nil {
		return fmt.Errorf("list indexed chunks: %w", err)
	}
	err := vs.p.Close()
	vs.p, vs.detachedKeys = nil, keys
	return err
}

// UseGraph changes the graph store that search resolves nodes against, for a
// reader that opens the graph for each request rather than holding it.
func (vs *Store) UseGraph(g graph.Store) {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	vs.graphDB = g
}

// Meta returns the current index metadata (may be nil if not yet loaded/built).
func (vs *Store) Meta() *VectorIndexMeta {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return vs.meta
}

// LoadMetaOnly loads just the metadata, without loading the HNSW index.
// Useful for status display when we only need metadata info.
func (vs *Store) LoadMetaOnly() (*VectorIndexMeta, error) {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	if vs.p == nil {
		return nil, ErrDetached
	}

	meta, err := vs.p.LoadMeta(vs.opts.namespace)
	if err != nil {
		return nil, err
	}
	vs.meta = meta
	return meta, nil
}

// Len returns the number of vectors in the HNSW index.
func (vs *Store) Len() int {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return vs.idx.Len()
}

// TextVersion is the version of the embedded text this store produces
// (WithEmbedText).
func (vs *Store) TextVersion() int { return vs.opts.textVersion }

// embedConfig is the embedder's fingerprint, when it has one.
func (vs *Store) embedConfig() string {
	if f, ok := vs.embedder.(embedding.Fingerprinter); ok {
		return f.EmbedConfig()
	}
	return ""
}

// NeedsReindex reports whether the index must be rebuilt in full: it was
// built with a different provider or model, from an earlier version of the
// embedded text, or under other embedding instructions. Either way its
// vectors and new ones would not share a space, and an incremental update
// would mix them without anything failing.
func (vs *Store) NeedsReindex() bool {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	if vs.meta == nil {
		return true
	}
	return vs.meta.Provider != vs.embedder.Name() ||
		vs.meta.Model != vs.embedder.ModelName() ||
		!vs.meta.TextCurrentAt(vs.opts.textVersion) ||
		vs.meta.EmbedConfig != vs.embedConfig()
}

// ReindexReason says why NeedsReindex is true, for telling the user what
// changed rather than only that something did.
func (vs *Store) ReindexReason() string {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	switch {
	case vs.meta == nil:
		return "no index metadata"
	case vs.meta.Provider != vs.embedder.Name() || vs.meta.Model != vs.embedder.ModelName():
		return fmt.Sprintf("index built with %s/%s, current provider is %s/%s",
			vs.meta.Provider, vs.meta.Model, vs.embedder.Name(), vs.embedder.ModelName())
	case !vs.meta.TextCurrentAt(vs.opts.textVersion):
		return fmt.Sprintf("index built from embeddable text version %d, current is %d",
			vs.meta.TextVersion, vs.opts.textVersion)
	case vs.meta.EmbedConfig != vs.embedConfig():
		return "index built under different embedding instructions"
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

func (vs *Store) removeNodeVectors(nodeID string) {
	// Remove from HNSW — try chunk indices 0..99 (generous upper bound).
	for i := range 100 {
		key := chunkKey(nodeID, i)
		if !vs.idx.Delete(key) {
			break // no more chunks for this node
		}
		vs.deleted++
	}

	// coder/hnsw v0.6.1 panics on the next Add once Delete has emptied the
	// graph, which happens whenever the only node is re-embedded, or a
	// reconcile drops every vector before adding new ones. An empty graph
	// holds nothing worth keeping, so it is replaced with a fresh one.
	if vs.idx.Len() == 0 {
		vs.idx = newGraph()
	}

	// Remove the node's chunks and text hash.
	if vs.p != nil {
		_ = vs.p.DeleteNode(nodeID)
	}
}

// newGraph returns an empty vector graph with the index's settings.
func newGraph() *hnsw.Graph[string] {
	g := hnsw.NewGraph[string]()
	g.M = hnswM
	g.EfSearch = hnswEf
	g.Distance = hnsw.CosineDistance
	return g
}

func (vs *Store) getChunkText(nodeID string, chunkIdx int) string {
	if vs.p == nil {
		return ""
	}
	text, err := vs.p.ChunkText(chunkKey(nodeID, chunkIdx))
	if err != nil {
		return ""
	}
	return text
}

// putTextHashes records the text each node's vectors were made from.
func (vs *Store) putTextHashes(hashes map[string]string) error {
	if len(hashes) == 0 || vs.p == nil {
		return nil
	}
	return vs.p.PutTextHashes(hashes)
}

// textHashes reads every recorded text hash, by node ID.
func (vs *Store) textHashes() (map[string]string, error) {
	if vs.p == nil {
		return nil, ErrDetached
	}
	return vs.p.TextHashes()
}

// textHash identifies the text a node's vectors were made from.
func textHash(text string) string {
	h := fnv.New64a()
	h.Write([]byte(text))
	return strconv.FormatUint(h.Sum64(), 16)
}
