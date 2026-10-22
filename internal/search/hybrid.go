package search

import (
	"context"
	"fmt"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/vectorstore"
)

// VectorSearcher is the part of the vector store a hybrid search needs.
type VectorSearcher interface {
	Search(ctx context.Context, query string, topK int) ([]vectorstore.SearchResult, error)
	StaleInLastSearch() int
}

// HybridOptions narrows and sizes a hybrid search.
type HybridOptions struct {
	// Limit is how many results to return.
	Limit int
	// Types, when set, keeps only nodes of these types.
	Types map[graph.NodeType]bool
	// NoDocs drops Document and AIGuideline nodes.
	NoDocs bool
	// Package keeps nodes whose package contains this, case-insensitively.
	Package string
	// Language keeps nodes of this language, case-insensitively.
	Language string
	// MinScore drops results scoring below it after reranking.
	MinScore float64
}

// HybridResult is a ranked search.
type HybridResult struct {
	// Results are the ranked results, at most Limit of them.
	Results []vectorstore.SearchResult
	// Ranked is how many results there were before the limit, so a caller
	// can say the list is not complete.
	Ranked int
	// Fetched is how many nearest vectors were asked for, Examined how many
	// were looked at, and Stale how many of those belonged to nodes no longer
	// in the graph. A thin result from a stale index reads exactly like a thin
	// result from a poor query.
	Fetched, Examined, Stale int
}

// Hybrid ranks graph nodes for a query by vector similarity, keyword matches
// from the graph and graph centrality together. Nodes keyword search finds
// and vector search missed are added, so a node named exactly what was asked
// for is never lost to a weak embedding.
//
// It is what `rag` prints and what other commands build on, so every caller
// ranks the same way.
func Hybrid(ctx context.Context, store graph.Store, vs VectorSearcher, query string, opts HybridOptions) (*HybridResult, error) {
	if opts.Limit <= 0 {
		opts.Limit = 15
	}
	// Fetch extra to allow for filtering, deduplication and reranking.
	fetchK := opts.Limit * 5
	if len(opts.Types) > 0 || opts.NoDocs {
		fetchK = opts.Limit * 10
	}
	if fetchK > 200 {
		fetchK = 200
	}

	results, err := vs.Search(ctx, query, fetchK)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}
	stale := vs.StaleInLastSearch()
	res := &HybridResult{Fetched: fetchK, Examined: stale + len(results), Stale: stale}

	results = DeduplicateResults(results)
	results = filterResults(results, func(n *graph.Node) bool {
		if opts.NoDocs && DocNodeTypes[n.Type] {
			return false
		}
		if len(opts.Types) > 0 && !opts.Types[n.Type] {
			return false
		}
		if opts.Package != "" && !strings.Contains(strings.ToLower(n.Package), strings.ToLower(opts.Package)) {
			return false
		}
		if opts.Language != "" && !strings.EqualFold(n.Language, opts.Language) {
			return false
		}
		return true
	})

	keywords := KeywordSearch(ctx, store, query)
	results, shares := InjectKeywordResults(results, keywords, opts.Types, opts.NoDocs, opts.Package, opts.Language)
	results = RerankResults(ctx, store, results, shares)

	if opts.MinScore > 0 {
		kept := results[:0]
		for _, r := range results {
			if r.Score >= opts.MinScore {
				kept = append(kept, r)
			}
		}
		results = kept
	}

	res.Ranked = len(results)
	if len(results) > opts.Limit {
		results = results[:opts.Limit]
	}
	res.Results = results
	return res, nil
}

// filterResults keeps results whose node passes keep, dropping those with no
// node at all.
func filterResults(results []vectorstore.SearchResult, keep func(*graph.Node) bool) []vectorstore.SearchResult {
	out := results[:0]
	for _, r := range results {
		if r.Node != nil && keep(r.Node) {
			out = append(out, r)
		}
	}
	return out
}
