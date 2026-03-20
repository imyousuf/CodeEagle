//go:build app

package app

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/search"
)

// Search performs a hybrid semantic + keyword search over the knowledge graph.
// Opens graph and vector stores for the request and closes them when done.
func (a *App) Search(query string, filters SearchFilters) (*SearchResults, error) {
	var sr *SearchResults
	err := a.withGraph(func(gr *graphResources) error {
		vs, closeVector := a.openVector(gr.store, gr.branch)
		defer closeVector()
		if vs == nil || !vs.Available() {
			return fmt.Errorf("search not available — run 'codeeagle sync' and 'codeeagle vectorindex' to build the index")
		}

		if query == "" {
			sr = &SearchResults{}
			return nil
		}

		limit := filters.Limit
		if limit <= 0 {
			limit = 15
		}
		if limit > 30 {
			limit = 30
		}

		// Parse type filter.
		var typeFilter map[graph.NodeType]bool
		if filters.NodeType != "" {
			parts := strings.Split(filters.NodeType, ",")
			typeFilter = make(map[graph.NodeType]bool, len(parts))
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					typeFilter[graph.NodeType(p)] = true
				}
			}
		}

		// Fetch extra results to account for filtering, dedup, and reranking.
		fetchK := limit * 5
		if len(typeFilter) > 0 || filters.NoDocs {
			fetchK = limit * 10
		}
		if fetchK > 200 {
			fetchK = 200
		}

		ctx := context.Background()

		results, err := vs.Search(ctx, query, fetchK)
		if err != nil {
			return fmt.Errorf("search failed: %w", err)
		}

		// Deduplicate by node ID.
		results = search.DeduplicateResults(results)

		// Apply filters.
		if filters.NoDocs {
			filtered := results[:0]
			for _, r := range results {
				if r.Node != nil && !search.DocNodeTypes[r.Node.Type] {
					filtered = append(filtered, r)
				}
			}
			results = filtered
		}

		if len(typeFilter) > 0 {
			filtered := results[:0]
			for _, r := range results {
				if r.Node != nil && typeFilter[r.Node.Type] {
					filtered = append(filtered, r)
				}
			}
			results = filtered
		}

		if filters.Package != "" {
			filtered := results[:0]
			for _, r := range results {
				if r.Node != nil && strings.Contains(strings.ToLower(r.Node.Package), strings.ToLower(filters.Package)) {
					filtered = append(filtered, r)
				}
			}
			results = filtered
		}

		if filters.Language != "" {
			filtered := results[:0]
			for _, r := range results {
				if r.Node != nil && strings.EqualFold(r.Node.Language, filters.Language) {
					filtered = append(filtered, r)
				}
			}
			results = filtered
		}

		// Hybrid keyword search + reranking.
		keywordNodes, totalKeywords := search.KeywordSearch(ctx, gr.store, query)
		results, keywordCounts := search.InjectKeywordResults(results, keywordNodes, typeFilter, filters.NoDocs, filters.Package, filters.Language)
		results = search.RerankResults(ctx, gr.store, results, keywordCounts, totalKeywords)

		// Apply min score filter.
		if filters.MinScore > 0 {
			filtered := results[:0]
			for _, r := range results {
				if r.Score >= filters.MinScore {
					filtered = append(filtered, r)
				}
			}
			results = filtered
		}

		// Limit results.
		if len(results) > limit {
			results = results[:limit]
		}

		// Split into code vs doc results.
		sr = &SearchResults{
			Query: query,
			Total: len(results),
		}

		for _, r := range results {
			if r.Node == nil {
				continue
			}
			n := r.Node
			relevance := int(math.Round(r.Score * 100))
			relPath := search.RelativePath(n.FilePath, a.repoPaths)

			if search.DocNodeTypes[n.Type] {
				sr.Docs = append(sr.Docs, DocResult{
					Name:      n.Name,
					Type:      n.Type,
					FilePath:  relPath,
					Snippet:   search.ChunkSnippet(r.ChunkText, 3),
					Relevance: relevance,
					Score:     r.Score,
				})
			} else {
				sr.Code = append(sr.Code, CodeResult{
					Name:      n.Name,
					Type:      n.Type,
					FilePath:  relPath,
					Line:      n.Line,
					Package:   n.Package,
					Language:  n.Language,
					Signature: n.Signature,
					Snippet:   search.ChunkSnippet(r.ChunkText, 3),
					Relevance: relevance,
					Score:     r.Score,
				})
			}
		}

		if meta := vs.Meta(); meta != nil {
			sr.Provider = meta.Provider + "/" + meta.Model
		}

		return nil
	})
	return sr, err
}
