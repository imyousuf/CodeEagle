package linker

import (
	"context"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// linkImports connects import NodeDependency nodes to their corresponding
// manifest NodeDependency nodes via EdgeDependsOn. This bridges the gap
// between `import foo` statements and `foo==1.2.3` in a manifest file.
func (l *Linker) linkImports(ctx context.Context) (int, error) {
	// Query all import dependency nodes.
	imports, err := l.store.QueryNodes(ctx, graph.NodeFilter{
		Type:       graph.NodeDependency,
		Properties: map[string]string{"kind": "import"},
	})
	if err != nil {
		return 0, err
	}
	if len(imports) == 0 {
		return 0, nil
	}

	// Query all manifest dependency nodes.
	manifests, err := l.store.QueryNodes(ctx, graph.NodeFilter{
		Type:       graph.NodeDependency,
		Properties: map[string]string{"kind": "manifest_dep"},
	})
	if err != nil {
		return 0, err
	}
	if len(manifests) == 0 {
		return 0, nil
	}

	// Build index: manifest dep name → list of manifest nodes.
	// Also build a normalized name index for Python (hyphens ↔ underscores).
	manifestByName := make(map[string][]*graph.Node)
	for _, m := range manifests {
		manifestByName[m.Name] = append(manifestByName[m.Name], m)
		// Python packages often use hyphens in PyPI but underscores in import.
		normalized := normalizePythonPkg(m.Name)
		if normalized != m.Name {
			manifestByName[normalized] = append(manifestByName[normalized], m)
		}
	}

	linked := 0
	seen := make(map[string]bool) // avoid duplicate edges

	for _, imp := range imports {
		matches := l.findManifestMatches(imp, manifestByName)
		for _, manifest := range matches {
			edgeKey := imp.ID + "→" + manifest.ID
			if seen[edgeKey] {
				continue
			}

			edge := &graph.Edge{
				ID:       graph.NewNodeID(string(graph.EdgeDependsOn), imp.ID, manifest.ID),
				Type:     graph.EdgeDependsOn,
				SourceID: imp.ID,
				TargetID: manifest.ID,
				Properties: map[string]string{
					"kind": "import_to_manifest",
				},
			}
			if err := l.store.AddEdge(ctx, edge); err != nil {
				continue
			}
			seen[edgeKey] = true
			linked++
		}
	}

	return linked, nil
}

// findManifestMatches returns manifest nodes that match the given import node.
func (l *Linker) findManifestMatches(imp *graph.Node, manifestByName map[string][]*graph.Node) []*graph.Node {
	name := imp.Name

	// 1. Exact match: import name == manifest dep name (e.g., "axios" == "axios").
	if matches, ok := manifestByName[name]; ok {
		return sameServiceFilter(imp, matches)
	}

	// 2. Go subpackage match: import "github.com/foo/bar/pkg/util" matches
	//    manifest dep "github.com/foo/bar" (longest prefix match).
	if strings.Contains(name, "/") {
		var bestMatch []*graph.Node
		bestLen := 0
		for mName, mNodes := range manifestByName {
			if strings.HasPrefix(name, mName+"/") || name == mName {
				if len(mName) > bestLen {
					bestLen = len(mName)
					bestMatch = mNodes
				}
			}
		}
		if len(bestMatch) > 0 {
			return sameServiceFilter(imp, bestMatch)
		}
	}

	// 3. Python dotted module: import "llm_framework.core" matches manifest
	//    dep "llm-framework" (first component, normalize underscores/hyphens).
	if strings.Contains(name, ".") {
		firstComponent := strings.SplitN(name, ".", 2)[0]
		normalized := normalizePythonPkg(firstComponent)
		if matches, ok := manifestByName[normalized]; ok {
			return sameServiceFilter(imp, matches)
		}
		// Also try the unnormalized first component.
		if normalized != firstComponent {
			if matches, ok := manifestByName[firstComponent]; ok {
				return sameServiceFilter(imp, matches)
			}
		}
	}

	// 4. Java qualified import: import "com.example.spring.web.RestTemplate"
	//    Match against manifest dep names like "spring-web" using the package
	//    segments (skip the common prefixes like com/org/io).
	if strings.Count(name, ".") >= 2 {
		parts := strings.Split(name, ".")
		// Try matching middle segments (e.g., "spring" from "org.springframework...")
		for mName := range manifestByName {
			mNorm := strings.ToLower(strings.ReplaceAll(mName, "-", "."))
			nameNorm := strings.ToLower(name)
			if strings.Contains(nameNorm, mNorm) {
				return sameServiceFilter(imp, manifestByName[mName])
			}
		}
		// Try second segment (group ID-like) for shorter matches.
		_ = parts
	}

	return nil
}

// sameServiceFilter returns only manifest nodes that are in the same service
// group as the import node (same top-level directory).
func sameServiceFilter(imp *graph.Node, manifests []*graph.Node) []*graph.Node {
	impGroup := topDir(imp.FilePath)
	var filtered []*graph.Node
	for _, m := range manifests {
		if topDir(m.FilePath) == impGroup {
			filtered = append(filtered, m)
		}
	}
	// If no same-service match, return all (cross-service link is still useful).
	if len(filtered) == 0 {
		return manifests
	}
	return filtered
}

// normalizePythonPkg normalizes a Python package name by converting
// hyphens to underscores and lowercasing (PEP 503 normalization).
func normalizePythonPkg(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, "-", "_"))
}
