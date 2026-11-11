// Package vectorstore is CodeEagle's vector index: pkg/kit/vectorstore with
// CodeEagle's policy (which node types are embedded and what text stands for
// each, policy.go) and its Badger persistence (badger.go), at .CodeEagle's
// vec.db and vec.idx.
package vectorstore

import (
	"fmt"

	"github.com/imyousuf/CodeEagle/internal/embedding"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
	kitvectorstore "github.com/imyousuf/CodeEagle/pkg/kit/vectorstore"
)

// The kit's types, under the names the rest of CodeEagle uses.
type (
	VectorStore     = kitvectorstore.Store
	SearchResult    = kitvectorstore.SearchResult
	VectorIndexMeta = kitvectorstore.VectorIndexMeta
	ChunkEntry      = kitvectorstore.ChunkEntry
	ChunkConfig     = kitvectorstore.ChunkConfig
)

// EmbedBatchSize is how many chunks are sent for embedding in one request.
const EmbedBatchSize = kitvectorstore.EmbedBatchSize

var (
	// DefaultChunkConfig returns the default chunking parameters.
	DefaultChunkConfig = kitvectorstore.DefaultChunkConfig
	// Chunk splits text into overlapping chunks at semantic boundaries.
	Chunk = kitvectorstore.Chunk
)

// New opens the vector index for writing. It does not load the index; call
// Load. branch keys the index metadata; the chunks and vec.idx are shared by
// every branch.
func New(graphDB graph.Store, embedder embedding.Provider, branch, idxPath, dbPath string) (*VectorStore, error) {
	return open(graphDB, embedder, branch, idxPath, dbPath, false)
}

// NewReadOnly opens the vector index read-only, allowing concurrent access
// from multiple processes. If the WAL needs recovery, it briefly opens in
// write mode to repair, then re-opens as read-only.
func NewReadOnly(graphDB graph.Store, embedder embedding.Provider, branch, idxPath, dbPath string) (*VectorStore, error) {
	return open(graphDB, embedder, branch, idxPath, dbPath, true)
}

func open(graphDB graph.Store, embedder embedding.Provider, branch, idxPath, dbPath string, readOnly bool) (*VectorStore, error) {
	p, err := OpenBadger(dbPath, idxPath, readOnly)
	if err != nil {
		return nil, fmt.Errorf("open vector db: %w", err)
	}
	return kitvectorstore.New(graphDB, embedder, p, Options(branch)...), nil
}

// Options is CodeEagle's policy for a kit vector store keyed by namespace.
func Options(namespace string) []kitvectorstore.Option {
	return []kitvectorstore.Option{
		kitvectorstore.WithEmbeddable(EmbeddableTypes...),
		kitvectorstore.WithEmbedText(EmbeddableText, EmbeddableTextVersion),
		// A relation's brief is rewritten in place, and the vectors search
		// matched it by were its first brief's until something re-embedded
		// it; a run that stopped between writing briefs and re-embedding them
		// left them so for good. Relations indexed before hashes were
		// recorded are embedded again once, since they are few and the ones
		// rewritten; anything else is taken as current.
		kitvectorstore.WithReembedWithoutHash(func(n *graph.Node) bool { return n.Type == graph.NodeRelation }),
		kitvectorstore.WithNamespace(namespace),
	}
}

// TextCurrent reports whether an index was built from the current
// embeddable text.
func TextCurrent(m *VectorIndexMeta) bool { return m.TextCurrentAt(EmbeddableTextVersion) }
