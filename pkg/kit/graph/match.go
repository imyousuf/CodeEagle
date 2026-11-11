package graph

import (
	"path/filepath"
	"strings"
)

// Matches reports whether a node satisfies every non-zero field of a filter.
// Every Store applies it, so a filter means the same thing whatever holds
// the graph.
//
// Fields are checked in a fixed order, and NameMatch is called only for a
// node that passed Type, FilePath, Package, Language and NamePattern. That
// matters to callers that count NameMatch calls: search weights a query term
// by how rare it is among the nodes NameMatch saw.
func Matches(node *Node, filter NodeFilter) bool {
	if filter.Type != "" && node.Type != filter.Type {
		return false
	}
	if filter.FilePath != "" && node.FilePath != filter.FilePath {
		return false
	}
	if filter.Package != "" && node.Package != filter.Package {
		return false
	}
	if filter.Language != "" && node.Language != filter.Language {
		return false
	}
	if filter.NamePattern != "" {
		matched, err := filepath.Match(filter.NamePattern, node.Name)
		if err != nil || !matched {
			return false
		}
	}
	if filter.NameMatch != nil && !filter.NameMatch(node.Name) {
		return false
	}
	if filter.Exported != nil && node.Exported != *filter.Exported {
		return false
	}
	// Property-based filtering: all specified key-value pairs must match.
	for key, val := range filter.Properties {
		if node.Properties == nil {
			return false
		}
		nodeVal, ok := node.Properties[key]
		if !ok {
			return false
		}
		// Support substring matching for comma-separated values (e.g., "repository" matches "repository,singleton").
		if nodeVal != val && !strings.Contains(nodeVal, val) {
			return false
		}
	}
	return true
}
