// Package search is CodeEagle's hybrid search: pkg/kit/search with
// CodeEagle's ranking. Keyword search returns only the node types the vector
// index embeds, and code entities earn the code-type bonus, unless a caller
// says otherwise.
//
// The kit's API is re-exported here, so the rest of CodeEagle keeps one
// import for searching and ranking.
package search

import (
	"context"

	"github.com/imyousuf/CodeEagle/internal/vectorstore"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
	kitsearch "github.com/imyousuf/CodeEagle/pkg/kit/search"
)

// The kit's types, under the names the rest of CodeEagle uses.
type (
	Hit            = kitsearch.Hit
	VectorSearcher = kitsearch.VectorSearcher
	HybridOptions  = kitsearch.HybridOptions
	HybridResult   = kitsearch.HybridResult
	KeywordResult  = kitsearch.KeywordResult
	Keywords       = kitsearch.Keywords
	EdgeInfo       = kitsearch.EdgeInfo
	Weights        = kitsearch.Weights
)

// PrefixCredit is what a keyword earns for matching the start of a word.
const PrefixCredit = kitsearch.PrefixCredit

var (
	// DocNodeTypes are the node types --no-docs drops.
	DocNodeTypes = kitsearch.DocNodeTypes
	// CodeNodeTypes are the code entities that earn the code-type bonus.
	CodeNodeTypes = kitsearch.CodeNodeTypes

	DeduplicateResults   = kitsearch.DeduplicateResults
	QueryTerms           = kitsearch.QueryTerms
	Tokenize             = kitsearch.Tokenize
	TermGroups           = kitsearch.TermGroups
	TermCredit           = kitsearch.TermCredit
	ShareOf              = kitsearch.ShareOf
	InjectKeywordResults = kitsearch.InjectKeywordResults
	RerankResults        = kitsearch.RerankResults
	ChunkSnippet         = kitsearch.ChunkSnippet
	RelativePath         = kitsearch.RelativePath
	OneHopEdges          = kitsearch.OneHopEdges
	OneHopEdgesText      = kitsearch.OneHopEdgesText
)

// KeywordSearch finds nodes whose names carry the query's words, among the
// node types the vector index embeds.
func KeywordSearch(ctx context.Context, store graph.Store, query string) *Keywords {
	return kitsearch.KeywordSearch(ctx, store, query, vectorstore.IsEmbeddable)
}

// Hybrid ranks graph nodes for a query as `rag` does: by vector similarity,
// keyword matches among embeddable nodes and graph centrality, with code
// entities boosted. Options a caller sets are kept.
func Hybrid(ctx context.Context, store graph.Store, vs VectorSearcher, query string, opts HybridOptions) (*HybridResult, error) {
	if opts.Embeddable == nil {
		opts.Embeddable = vectorstore.IsEmbeddable
	}
	if opts.Boost == nil {
		opts.Boost = CodeNodeTypes
	}
	return kitsearch.Hybrid(ctx, store, vs, query, opts)
}
