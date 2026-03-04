package linker

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// linkCalls resolves cross-file intra-package function calls.
//
// The Go parser stores unresolved direct calls (functions not found in the same
// file) as Properties["unresolved_calls"] on function/method nodes. This phase
// looks up those names across all functions in the same package and creates
// Calls edges for matches.
func (l *Linker) linkCalls(ctx context.Context) (int, error) {
	// Query all Go callable nodes (functions, test functions, methods).
	funcs, err := l.store.QueryNodes(ctx, graph.NodeFilter{
		Type:     graph.NodeFunction,
		Language: "go",
	})
	if err != nil {
		return 0, err
	}
	testFuncs, err := l.store.QueryNodes(ctx, graph.NodeFilter{
		Type:     graph.NodeTestFunction,
		Language: "go",
	})
	if err != nil {
		return 0, err
	}
	methods, err := l.store.QueryNodes(ctx, graph.NodeFilter{
		Type:     graph.NodeMethod,
		Language: "go",
	})
	if err != nil {
		return 0, err
	}

	// Merge all callable nodes.
	allCallable := make([]*graph.Node, 0, len(funcs)+len(testFuncs)+len(methods))
	allCallable = append(allCallable, funcs...)
	allCallable = append(allCallable, testFuncs...)
	allCallable = append(allCallable, methods...)

	// Build package → name → []*Node lookup for resolution targets.
	// Functions and test functions are indexed by their Name.
	pkgFuncMap := make(map[string]map[string][]*graph.Node) // package → funcName → nodes
	for _, n := range allCallable {
		if n.Package == "" {
			continue
		}
		if pkgFuncMap[n.Package] == nil {
			pkgFuncMap[n.Package] = make(map[string][]*graph.Node)
		}
		// Index functions/test functions by Name.
		if n.Type == graph.NodeFunction || n.Type == graph.NodeTestFunction {
			pkgFuncMap[n.Package][n.Name] = append(pkgFuncMap[n.Package][n.Name], n)
		}
	}

	// Find nodes with unresolved calls and resolve them.
	linked := 0
	for _, caller := range allCallable {
		unresolvedStr, ok := caller.Properties["unresolved_calls"]
		if !ok || unresolvedStr == "" {
			continue
		}

		names := strings.Split(unresolvedStr, ",")
		funcMap := pkgFuncMap[caller.Package]
		if funcMap == nil {
			continue
		}

		resolved := false
		for _, name := range names {
			candidates := funcMap[name]
			if len(candidates) == 0 {
				continue
			}

			// Pick the best match: prefer different file (that's the whole point),
			// then prefer same directory.
			target := pickCallTarget(caller, candidates)
			if target == nil || target.ID == caller.ID {
				continue
			}

			edge := &graph.Edge{
				ID:       graph.NewNodeID(string(graph.EdgeCalls), caller.ID, target.ID),
				Type:     graph.EdgeCalls,
				SourceID: caller.ID,
				TargetID: target.ID,
				Properties: map[string]string{
					"kind": "cross_file",
				},
			}
			if err := l.store.AddEdge(ctx, edge); err != nil {
				continue
			}
			linked++
			resolved = true
		}

		// Clear the unresolved_calls property after resolution.
		if resolved {
			delete(caller.Properties, "unresolved_calls")
			_ = l.store.UpdateNode(ctx, caller)
		}
	}

	return linked, nil
}

// pickCallTarget selects the best function node from candidates for a cross-file call.
// Prefers candidates in a different file (same-file would already be resolved by parser),
// then prefers same directory.
func pickCallTarget(caller *graph.Node, candidates []*graph.Node) *graph.Node {
	callerDir := filepath.Dir(caller.FilePath)

	// Filter to different-file candidates.
	var diffFile []*graph.Node
	for _, c := range candidates {
		if c.FilePath != caller.FilePath {
			diffFile = append(diffFile, c)
		}
	}
	if len(diffFile) == 0 {
		return nil
	}
	if len(diffFile) == 1 {
		return diffFile[0]
	}

	// Prefer same directory.
	for _, c := range diffFile {
		if filepath.Dir(c.FilePath) == callerDir {
			return c
		}
	}
	return diffFile[0]
}
