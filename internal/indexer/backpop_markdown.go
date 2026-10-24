package indexer

import (
	"context"
	"os"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// MarkdownSectionsVersion is the markdown section format the backfill brings
// existing nodes up to. Bump it when the parser starts storing something that
// nodes indexed before cannot have.
const MarkdownSectionsVersion = 1

// markdownLanguage is parser.LangMarkdown.
const markdownLanguage = "markdown"

// sectionTextParser is implemented by the markdown parser.
type sectionTextParser interface {
	StoresSectionText() bool
}

// storesMarkdownText reports whether the registered markdown parser stores
// section text. With it turned off there is nothing to backfill.
func (idx *Indexer) storesMarkdownText() bool {
	p, ok := idx.registry.ParserForFile("x.md")
	if !ok {
		return false
	}
	st, ok := p.(sectionTextParser)
	return ok && st.StoresSectionText()
}

// BackpopMarkdownSections re-parses markdown already in the graph, so files
// indexed before sections carried their text gain it without waiting for an
// edit. It returns how many files were re-parsed.
//
// It rewrites only nodes from the scope being written. On a feature branch the
// default branch's documents are visible through the fallback read, and
// re-parsing them would copy every one into the feature branch's scope.
func BackpopMarkdownSections(ctx context.Context, idx *Indexer, repoRoots []string) (int, error) {
	store := idx.Store()
	writeScope := ""
	if ws, ok := store.(interface{ WriteBranch() string }); ok {
		writeScope = ws.WriteBranch()
	}

	seen := make(map[string]bool)
	count := 0
	for _, t := range []graph.NodeType{graph.NodeDocument, graph.NodeAIGuideline} {
		nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: t, Language: markdownLanguage})
		if err != nil {
			return count, err
		}
		for _, n := range nodes {
			// The file's own node, not its sections, code blocks or TODOs.
			if n.Name != n.FilePath || seen[n.FilePath] {
				continue
			}
			if src := n.Properties[graph.PropGraphSource]; writeScope != "" && src != "" && src != writeScope {
				continue
			}
			seen[n.FilePath] = true

			abs := resolveAbsPath(n.FilePath, repoRoots)
			if abs == "" {
				continue
			}
			updatedAt := n.UpdatedAt
			if updatedAt.IsZero() {
				info, err := os.Stat(abs)
				if err != nil {
					continue
				}
				updatedAt = info.ModTime()
			}
			if err := idx.reindexQuietly(ctx, abs, updatedAt); err != nil {
				idx.log("Warning: backfill %s: %v", n.FilePath, err)
				continue
			}
			count++
		}
	}
	return count, nil
}
