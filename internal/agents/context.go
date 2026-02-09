package agents

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// ContextBuilder translates knowledge graph data into formatted text
// suitable for inclusion in LLM prompts. Each method queries the graph
// and returns a structured, human-readable summary.
type ContextBuilder struct {
	store graph.Store
}

// NewContextBuilder creates a ContextBuilder backed by the given graph store.
func NewContextBuilder(store graph.Store) *ContextBuilder {
	return &ContextBuilder{store: store}
}

// BuildFileContext returns a formatted summary of a single file: its symbols,
// dependencies (outgoing imports), and dependents (incoming imports/calls).
func (cb *ContextBuilder) BuildFileContext(ctx context.Context, filePath string) (string, error) {
	nodes, err := cb.store.QueryNodes(ctx, graph.NodeFilter{FilePath: filePath})
	if err != nil {
		return "", fmt.Errorf("query nodes for file %s: %w", filePath, err)
	}
	if len(nodes) == 0 {
		return fmt.Sprintf("## File: %s\n\nNo indexed symbols found.\n", filePath), nil
	}

	// Determine language from the first node that has one.
	lang := ""
	for _, n := range nodes {
		if n.Language != "" {
			lang = n.Language
			break
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## File: %s\n", filePath)
	if lang != "" {
		fmt.Fprintf(&b, "Language: %s\n", lang)
	}

	// Symbols section.
	b.WriteString("\n### Symbols\n")
	for _, n := range nodes {
		if n.Type == graph.NodeFile {
			continue // skip the file-level node itself
		}
		exported := "unexported"
		if n.Exported {
			exported = "exported"
		}
		lineRange := ""
		if n.Line > 0 {
			lineRange = fmt.Sprintf(" (line %d", n.Line)
			if n.EndLine > 0 && n.EndLine != n.Line {
				lineRange += fmt.Sprintf("-%d)", n.EndLine)
			} else {
				lineRange += ")"
			}
		}
		fmt.Fprintf(&b, "- [%s] %s%s - %s\n", n.Type, n.Name, lineRange, exported)
	}

	// Collect edges for all nodes in this file to find dependencies and dependents.
	deps := make(map[string]struct{})
	dependents := make(map[string]struct{})
	for _, n := range nodes {
		edges, err := cb.store.GetEdges(ctx, n.ID, "")
		if err != nil {
			continue
		}
		for _, e := range edges {
			switch e.Type {
			case graph.EdgeImports, graph.EdgeDependsOn:
				if e.SourceID == n.ID {
					target, err := cb.store.GetNode(ctx, e.TargetID)
					if err == nil {
						deps[target.QualifiedName] = struct{}{}
					}
				}
				if e.TargetID == n.ID {
					source, err := cb.store.GetNode(ctx, e.SourceID)
					if err == nil {
						dependents[source.FilePath] = struct{}{}
					}
				}
			case graph.EdgeCalls:
				if e.TargetID == n.ID {
					source, err := cb.store.GetNode(ctx, e.SourceID)
					if err == nil && source.FilePath != filePath {
						dependents[source.FilePath] = struct{}{}
					}
				}
			}
		}
	}

	if len(deps) > 0 {
		b.WriteString("\n### Dependencies\n")
		for dep := range deps {
			fmt.Fprintf(&b, "- imports %s\n", dep)
		}
	}
	if len(dependents) > 0 {
		b.WriteString("\n### Dependents\n")
		for dep := range dependents {
			fmt.Fprintf(&b, "- Used by %s\n", dep)
		}
	}

	return b.String(), nil
}

// BuildServiceContext returns a formatted overview of a service or module,
// aggregating all nodes whose package matches the given service name pattern.
func (cb *ContextBuilder) BuildServiceContext(ctx context.Context, serviceName string) (string, error) {
	nodes, err := cb.store.QueryNodes(ctx, graph.NodeFilter{Package: serviceName})
	if err != nil {
		return "", fmt.Errorf("query nodes for service %s: %w", serviceName, err)
	}
	if len(nodes) == 0 {
		return fmt.Sprintf("## Service: %s\n\nNo indexed nodes found.\n", serviceName), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## Service: %s\n\n", serviceName)

	// Group by type.
	byType := make(map[graph.NodeType][]*graph.Node)
	files := make(map[string]struct{})
	var endpoints []*graph.Node
	depCount := 0
	for _, n := range nodes {
		byType[n.Type] = append(byType[n.Type], n)
		if n.FilePath != "" {
			files[n.FilePath] = struct{}{}
		}
		if n.Type == graph.NodeAPIEndpoint {
			endpoints = append(endpoints, n)
		}
		// Count outgoing dependency edges.
		edges, err := cb.store.GetEdges(ctx, n.ID, graph.EdgeDependsOn)
		if err == nil {
			for _, e := range edges {
				if e.SourceID == n.ID {
					depCount++
				}
			}
		}
	}

	fmt.Fprintf(&b, "Files: %d\n", len(files))
	fmt.Fprintf(&b, "Total symbols: %d\n", len(nodes))
	fmt.Fprintf(&b, "External dependencies: %d\n\n", depCount)

	// Node type breakdown.
	b.WriteString("### Breakdown by type\n")
	for nt, ns := range byType {
		fmt.Fprintf(&b, "- %s: %d\n", nt, len(ns))
	}

	if len(endpoints) > 0 {
		b.WriteString("\n### API Endpoints\n")
		for _, ep := range endpoints {
			fmt.Fprintf(&b, "- %s\n", ep.Name)
		}
	}

	// Key types (structs and interfaces).
	keyTypes := append(byType[graph.NodeStruct], byType[graph.NodeInterface]...)
	if len(keyTypes) > 0 {
		b.WriteString("\n### Key Types\n")
		for _, t := range keyTypes {
			fmt.Fprintf(&b, "- [%s] %s\n", t.Type, t.Name)
		}
	}

	return b.String(), nil
}

// BuildImpactContext performs a BFS traversal from the given node, following
// dependency-related edges up to 3 levels deep. It returns a formatted list
// of affected nodes grouped by depth of impact.
func (cb *ContextBuilder) BuildImpactContext(ctx context.Context, nodeID string) (string, error) {
	root, err := cb.store.GetNode(ctx, nodeID)
	if err != nil {
		return "", fmt.Errorf("get root node %s: %w", nodeID, err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## Impact Analysis: %s (%s)\n\n", root.Name, root.Type)

	// BFS up to 3 levels. We follow incoming edges for Imports, DependsOn, Calls,
	// Implements because we want to find "what depends on this node" (who would
	// be affected if this node changes).
	impactEdges := []graph.EdgeType{
		graph.EdgeImports,
		graph.EdgeDependsOn,
		graph.EdgeCalls,
		graph.EdgeImplements,
	}

	type levelEntry struct {
		node  *graph.Node
		level int
	}

	visited := map[string]struct{}{nodeID: {}}
	queue := []levelEntry{{node: root, level: 0}}
	levels := make(map[int][]*graph.Node) // level -> nodes at that level

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current.level >= 3 {
			continue
		}

		for _, et := range impactEdges {
			// Get nodes that depend on the current node (incoming edges).
			neighbors, err := cb.store.GetNeighbors(ctx, current.node.ID, et, graph.Incoming)
			if err != nil {
				continue
			}
			for _, n := range neighbors {
				if _, seen := visited[n.ID]; seen {
					continue
				}
				visited[n.ID] = struct{}{}
				nextLevel := current.level + 1
				levels[nextLevel] = append(levels[nextLevel], n)
				queue = append(queue, levelEntry{node: n, level: nextLevel})
			}
		}
	}

	if len(levels) == 0 {
		b.WriteString("No dependent nodes found.\n")
		return b.String(), nil
	}

	levelLabels := map[int]string{
		1: "Direct",
		2: "Indirect",
		3: "Transitive",
	}
	for lvl := 1; lvl <= 3; lvl++ {
		nodes := levels[lvl]
		if len(nodes) == 0 {
			continue
		}
		fmt.Fprintf(&b, "### %s impact (level %d)\n", levelLabels[lvl], lvl)
		for _, n := range nodes {
			loc := ""
			if n.FilePath != "" {
				loc = fmt.Sprintf(" in %s", n.FilePath)
			}
			fmt.Fprintf(&b, "- [%s] %s%s\n", n.Type, n.Name, loc)
		}
		b.WriteString("\n")
	}

	return b.String(), nil
}

// BuildDiffContext takes a list of changed files and returns a per-file summary
// of the symbols that changed and what other files/nodes might be affected.
func (cb *ContextBuilder) BuildDiffContext(ctx context.Context, changedFiles []string) (string, error) {
	if len(changedFiles) == 0 {
		return "No changed files.\n", nil
	}

	var b strings.Builder
	b.WriteString("## Changed Files Impact\n\n")

	for _, fp := range changedFiles {
		nodes, err := cb.store.QueryNodes(ctx, graph.NodeFilter{FilePath: fp})
		if err != nil {
			fmt.Fprintf(&b, "### %s\n\nError querying: %v\n\n", fp, err)
			continue
		}
		if len(nodes) == 0 {
			fmt.Fprintf(&b, "### %s\n\nNo indexed symbols (new or untracked file).\n\n", fp)
			continue
		}

		fmt.Fprintf(&b, "### %s\n", fp)

		// List symbols in this file.
		b.WriteString("**Symbols:**\n")
		for _, n := range nodes {
			if n.Type == graph.NodeFile {
				continue
			}
			fmt.Fprintf(&b, "- [%s] %s\n", n.Type, n.Name)
		}

		// Find what depends on these symbols.
		affected := make(map[string]string) // filePath -> node name for dedup
		for _, n := range nodes {
			for _, et := range []graph.EdgeType{graph.EdgeImports, graph.EdgeDependsOn, graph.EdgeCalls} {
				neighbors, err := cb.store.GetNeighbors(ctx, n.ID, et, graph.Incoming)
				if err != nil {
					continue
				}
				for _, dep := range neighbors {
					if dep.FilePath != fp && dep.FilePath != "" {
						affected[dep.FilePath] = dep.Name
					}
				}
			}
		}

		if len(affected) > 0 {
			b.WriteString("**Potentially affected files:**\n")
			for afp := range affected {
				fmt.Fprintf(&b, "- %s\n", afp)
			}
		}
		b.WriteString("\n")
	}

	return b.String(), nil
}

// BuildOverviewContext returns a high-level summary of the entire knowledge graph:
// total node/edge counts, breakdown by type, and language distribution.
func (cb *ContextBuilder) BuildOverviewContext(ctx context.Context) (string, error) {
	stats, err := cb.store.Stats(ctx)
	if err != nil {
		return "", fmt.Errorf("get graph stats: %w", err)
	}

	var b strings.Builder
	b.WriteString("## Knowledge Graph Overview\n\n")
	fmt.Fprintf(&b, "Total nodes: %d\n", stats.NodeCount)
	fmt.Fprintf(&b, "Total edges: %d\n\n", stats.EdgeCount)

	if len(stats.NodesByType) > 0 {
		b.WriteString("### Nodes by type\n")
		// Sort keys for deterministic output.
		nodeTypes := make([]string, 0, len(stats.NodesByType))
		for nt := range stats.NodesByType {
			nodeTypes = append(nodeTypes, string(nt))
		}
		sort.Strings(nodeTypes)
		for _, nt := range nodeTypes {
			fmt.Fprintf(&b, "- %s: %d\n", nt, stats.NodesByType[graph.NodeType(nt)])
		}
	}

	if len(stats.EdgesByType) > 0 {
		b.WriteString("\n### Edges by type\n")
		edgeTypes := make([]string, 0, len(stats.EdgesByType))
		for et := range stats.EdgesByType {
			edgeTypes = append(edgeTypes, string(et))
		}
		sort.Strings(edgeTypes)
		for _, et := range edgeTypes {
			fmt.Fprintf(&b, "- %s: %d\n", et, stats.EdgesByType[graph.EdgeType(et)])
		}
	}

	// Language distribution: query all nodes and count languages.
	allNodes, err := cb.store.QueryNodes(ctx, graph.NodeFilter{})
	if err == nil && len(allNodes) > 0 {
		langCount := make(map[string]int)
		pkgCount := make(map[string]int)
		for _, n := range allNodes {
			if n.Language != "" {
				langCount[n.Language]++
			}
			if n.Package != "" {
				pkgCount[n.Package]++
			}
		}
		if len(langCount) > 0 {
			b.WriteString("\n### Language distribution\n")
			langs := make([]string, 0, len(langCount))
			for l := range langCount {
				langs = append(langs, l)
			}
			sort.Strings(langs)
			for _, l := range langs {
				fmt.Fprintf(&b, "- %s: %d nodes\n", l, langCount[l])
			}
		}
		if len(pkgCount) > 0 {
			b.WriteString("\n### Top packages by node count\n")
			type pkgEntry struct {
				name  string
				count int
			}
			pkgs := make([]pkgEntry, 0, len(pkgCount))
			for name, count := range pkgCount {
				pkgs = append(pkgs, pkgEntry{name, count})
			}
			sort.Slice(pkgs, func(i, j int) bool {
				return pkgs[i].count > pkgs[j].count
			})
			limit := 10
			if len(pkgs) < limit {
				limit = len(pkgs)
			}
			for _, p := range pkgs[:limit] {
				fmt.Fprintf(&b, "- %s: %d nodes\n", p.name, p.count)
			}
		}
	}

	return b.String(), nil
}

// BuildMetricsContext returns a formatted table of code quality metrics
// for all symbols in the given file.
func (cb *ContextBuilder) BuildMetricsContext(ctx context.Context, filePath string) (string, error) {
	nodes, err := cb.store.QueryNodes(ctx, graph.NodeFilter{FilePath: filePath})
	if err != nil {
		return "", fmt.Errorf("query nodes for file %s: %w", filePath, err)
	}
	if len(nodes) == 0 {
		return fmt.Sprintf("## Metrics: %s\n\nNo indexed symbols found.\n", filePath), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## Metrics: %s\n\n", filePath)

	hasMetrics := false
	for _, n := range nodes {
		if n.Type == graph.NodeFile {
			continue
		}
		if len(n.Metrics) == 0 {
			continue
		}
		hasMetrics = true
		fmt.Fprintf(&b, "### %s %s\n", n.Type, n.Name)
		// Sort metric keys for deterministic output.
		keys := make([]string, 0, len(n.Metrics))
		for k := range n.Metrics {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "- %s: %.2f\n", k, n.Metrics[k])
		}
		b.WriteString("\n")
	}

	if !hasMetrics {
		b.WriteString("No metrics data available for symbols in this file.\n")
	}

	return b.String(), nil
}
