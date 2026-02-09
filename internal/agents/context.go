package agents

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/gitutil"
	"github.com/imyousuf/CodeEagle/internal/graph"
)

// ContextBuilder translates knowledge graph data into formatted text
// suitable for inclusion in LLM prompts. Each method queries the graph
// and returns a structured, human-readable summary.
type ContextBuilder struct {
	store     graph.Store
	repoRoots []string
}

// NewContextBuilder creates a ContextBuilder backed by the given graph store.
// Optional repoRoots are used to resolve relative file paths (stored in the graph)
// back to absolute paths for reading file contents from disk.
func NewContextBuilder(store graph.Store, repoRoots ...string) *ContextBuilder {
	return &ContextBuilder{store: store, repoRoots: repoRoots}
}

// resolveFilePath resolves a potentially relative file path to an absolute path
// by checking against each repo root. Returns the original path if no match is found
// or if the path is already absolute and exists.
func (cb *ContextBuilder) resolveFilePath(relPath string) string {
	for _, root := range cb.repoRoots {
		abs := filepath.Join(root, relPath)
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
	}
	return relPath
}

// BuildGuidelineContext queries the graph for all AIGuideline nodes (e.g.,
// CLAUDE.md, AGENTS.md, GEMINI.md) and returns their full content read from disk.
// This is used for automatic context injection into agent system prompts.
func (cb *ContextBuilder) BuildGuidelineContext(ctx context.Context) (string, error) {
	nodes, err := cb.store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeAIGuideline})
	if err != nil {
		return "", fmt.Errorf("query AI guideline nodes: %w", err)
	}
	if len(nodes) == 0 {
		return "", nil
	}

	var b strings.Builder
	b.WriteString("## Project AI Guidelines\n\n")
	for _, n := range nodes {
		content, err := os.ReadFile(cb.resolveFilePath(n.FilePath))
		if err != nil {
			fmt.Fprintf(&b, "### %s\n\n(Could not read file: %v)\n\n", n.Name, err)
			continue
		}
		fmt.Fprintf(&b, "### %s\n\n%s\n\n", n.FilePath, strings.TrimSpace(string(content)))
	}
	return b.String(), nil
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

		// List symbols in this file, highlighting architectural nodes.
		b.WriteString("**Symbols:**\n")
		var archNotes []string
		for _, n := range nodes {
			if n.Type == graph.NodeFile {
				continue
			}
			fmt.Fprintf(&b, "- [%s] %s\n", n.Type, n.Name)
			// Flag model/architectural changes.
			switch n.Type {
			case graph.NodeDBModel, graph.NodeDomainModel:
				archNotes = append(archNotes, fmt.Sprintf("WARNING: %s %s is a data model - changes may have wide impact", n.Type, n.Name))
			}
			if n.Properties != nil {
				if role := n.Properties[graph.PropArchRole]; role != "" {
					archNotes = append(archNotes, fmt.Sprintf("Architectural role affected: %s (%s)", role, n.Name))
				}
				if layer := n.Properties[graph.PropLayerTag]; layer != "" {
					archNotes = append(archNotes, fmt.Sprintf("Layer affected: %s (%s)", layer, n.Name))
				}
			}
		}
		if len(archNotes) > 0 {
			b.WriteString("**Architectural impact:**\n")
			for _, note := range archNotes {
				fmt.Fprintf(&b, "- %s\n", note)
			}
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

		// Architectural patterns and layer distribution from node properties.
		patternCounts := make(map[string]int)
		layerCounts := make(map[string]int)
		for _, n := range allNodes {
			if n.Properties == nil {
				continue
			}
			if patterns := n.Properties[graph.PropDesignPattern]; patterns != "" {
				for _, p := range strings.Split(patterns, ",") {
					p = strings.TrimSpace(p)
					if p != "" {
						patternCounts[p]++
					}
				}
			}
			if layer := n.Properties[graph.PropLayerTag]; layer != "" {
				layerCounts[layer]++
			}
		}
		if len(patternCounts) > 0 {
			b.WriteString("\n### Architectural Patterns\n")
			pNames := make([]string, 0, len(patternCounts))
			for p := range patternCounts {
				pNames = append(pNames, p)
			}
			sort.Strings(pNames)
			for _, p := range pNames {
				fmt.Fprintf(&b, "- %s: %d instances\n", p, patternCounts[p])
			}
		}
		if len(layerCounts) > 0 {
			b.WriteString("\n### Layer Distribution\n")
			layers := make([]string, 0, len(layerCounts))
			for l := range layerCounts {
				layers = append(layers, l)
			}
			sort.Strings(layers)
			for _, l := range layers {
				fmt.Fprintf(&b, "- %s: %d nodes\n", l, layerCounts[l])
			}
		}
	}

	return b.String(), nil
}

// BuildSummaryContext queries the graph for LLM-generated summary Document nodes
// (where properties has generated=true) and returns their content. This provides
// high-level codebase understanding from the auto-summarization feature.
func (cb *ContextBuilder) BuildSummaryContext(ctx context.Context) (string, error) {
	nodes, err := cb.store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeDocument})
	if err != nil {
		return "", fmt.Errorf("query generated summary nodes: %w", err)
	}

	var summaries []*graph.Node
	for _, n := range nodes {
		if n.Properties != nil && n.Properties["generated"] == "true" {
			summaries = append(summaries, n)
		}
	}
	if len(summaries) == 0 {
		return "", nil
	}

	// Sort: patterns summary last, service summaries alphabetically.
	sort.Slice(summaries, func(i, j int) bool {
		ki := summaries[i].Properties["kind"]
		kj := summaries[j].Properties["kind"]
		if ki != kj {
			if ki == "patterns" {
				return false
			}
			if kj == "patterns" {
				return true
			}
		}
		return summaries[i].Name < summaries[j].Name
	})

	var b strings.Builder
	b.WriteString("## Auto-Generated Codebase Summaries\n\n")
	for _, n := range summaries {
		fmt.Fprintf(&b, "### %s\n\n%s\n\n", n.Name, strings.TrimSpace(n.DocComment))
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

// BuildModelContext returns all DB models, domain models, view models, and DTOs
// for a service, grouped by type. Includes field information from Properties["fields"].
func (cb *ContextBuilder) BuildModelContext(ctx context.Context, serviceName string) (string, error) {
	modelTypes := []graph.NodeType{
		graph.NodeDBModel,
		graph.NodeDomainModel,
		graph.NodeViewModel,
		graph.NodeDTO,
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## Data Models: %s\n", serviceName)

	found := false
	groupLabels := map[graph.NodeType]string{
		graph.NodeDBModel:      "DB Models",
		graph.NodeDomainModel:  "Domain Models",
		graph.NodeViewModel:    "View Models / DTOs",
		graph.NodeDTO:          "View Models / DTOs",
	}

	// Group ViewModel and DTO together.
	type group struct {
		label string
		nodes []*graph.Node
	}
	groups := []group{
		{label: "DB Models"},
		{label: "Domain Models"},
		{label: "View Models / DTOs"},
	}
	groupIndex := map[graph.NodeType]int{
		graph.NodeDBModel:     0,
		graph.NodeDomainModel: 1,
		graph.NodeViewModel:   2,
		graph.NodeDTO:         2,
	}
	_ = groupLabels // used implicitly via groupIndex

	for _, mt := range modelTypes {
		filter := graph.NodeFilter{Type: mt}
		if serviceName != "" {
			filter.Package = serviceName
		}
		nodes, err := cb.store.QueryNodes(ctx, filter)
		if err != nil {
			continue
		}
		for _, n := range nodes {
			idx := groupIndex[mt]
			groups[idx].nodes = append(groups[idx].nodes, n)
			found = true
		}
	}

	if !found {
		fmt.Fprintf(&b, "\nNo data models found for service %s.\n", serviceName)
		return b.String(), nil
	}

	for _, g := range groups {
		if len(g.nodes) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n### %s\n", g.label)
		for _, n := range g.nodes {
			fields := ""
			if n.Properties != nil && n.Properties["fields"] != "" {
				fields = fmt.Sprintf(" (fields: %s)", n.Properties["fields"])
			}
			fmt.Fprintf(&b, "- %s%s\n", n.Name, fields)
		}
	}

	return b.String(), nil
}

// BuildArchitectureContext returns a high-level architecture overview showing
// detected design patterns, layer distribution, and architectural role summary.
func (cb *ContextBuilder) BuildArchitectureContext(ctx context.Context) (string, error) {
	// Query all nodes to gather architectural metadata.
	allNodes, err := cb.store.QueryNodes(ctx, graph.NodeFilter{})
	if err != nil {
		return "", fmt.Errorf("query all nodes: %w", err)
	}

	var b strings.Builder
	b.WriteString("## Architecture Overview\n")

	// Collect design patterns with example nodes.
	patternExamples := make(map[string][]string) // pattern -> list of node names
	// Collect layer distribution.
	layerCounts := make(map[string]int)
	// Collect architectural roles.
	roleCounts := make(map[string]int)

	for _, n := range allNodes {
		if n.Properties == nil {
			continue
		}
		if patterns := n.Properties[graph.PropDesignPattern]; patterns != "" {
			for _, p := range strings.Split(patterns, ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					patternExamples[p] = append(patternExamples[p], n.Name)
				}
			}
		}
		if layer := n.Properties[graph.PropLayerTag]; layer != "" {
			layerCounts[layer]++
		}
		if role := n.Properties[graph.PropArchRole]; role != "" {
			roleCounts[role]++
		}
	}

	if len(patternExamples) > 0 {
		b.WriteString("\n### Detected Design Patterns\n")
		patternNames := make([]string, 0, len(patternExamples))
		for p := range patternExamples {
			patternNames = append(patternNames, p)
		}
		sort.Strings(patternNames)
		for _, p := range patternNames {
			examples := patternExamples[p]
			limit := 3
			if len(examples) < limit {
				limit = len(examples)
			}
			fmt.Fprintf(&b, "- **%s** (%d instances): %s\n", p, len(examples), strings.Join(examples[:limit], ", "))
		}
	}

	if len(layerCounts) > 0 {
		b.WriteString("\n### Layer Distribution\n")
		layers := make([]string, 0, len(layerCounts))
		for l := range layerCounts {
			layers = append(layers, l)
		}
		sort.Strings(layers)
		for _, l := range layers {
			fmt.Fprintf(&b, "- %s: %d nodes\n", l, layerCounts[l])
		}
	}

	if len(roleCounts) > 0 {
		b.WriteString("\n### Architectural Roles\n")
		roles := make([]string, 0, len(roleCounts))
		for r := range roleCounts {
			roles = append(roles, r)
		}
		sort.Strings(roles)
		for _, r := range roles {
			fmt.Fprintf(&b, "- %s: %d nodes\n", r, roleCounts[r])
		}
	}

	if len(patternExamples) == 0 && len(layerCounts) == 0 && len(roleCounts) == 0 {
		b.WriteString("\nNo architectural metadata detected.\n")
	}

	return b.String(), nil
}

// BuildModelImpactContext traces all consumers of a DB model or domain model
// using BFS, showing which services, controllers, DTOs/ViewModels, and
// migrations reference it.
func (cb *ContextBuilder) BuildModelImpactContext(ctx context.Context, modelNodeID string) (string, error) {
	root, err := cb.store.GetNode(ctx, modelNodeID)
	if err != nil {
		return "", fmt.Errorf("get model node %s: %w", modelNodeID, err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## Model Impact Analysis: %s (%s)\n\n", root.Name, root.Type)

	// BFS up to 3 levels following incoming edges (who depends on this model).
	impactEdges := []graph.EdgeType{
		graph.EdgeImports,
		graph.EdgeDependsOn,
		graph.EdgeCalls,
		graph.EdgeContains,
		graph.EdgeExposes,
		graph.EdgeMigrates,
	}

	type levelEntry struct {
		node  *graph.Node
		level int
	}

	visited := map[string]struct{}{modelNodeID: {}}
	queue := []levelEntry{{node: root, level: 0}}

	// Categorize consumers.
	var services []*graph.Node
	var controllers []*graph.Node
	var dtoViewModels []*graph.Node
	var migrations []*graph.Node
	var others []*graph.Node

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current.level >= 3 {
			continue
		}

		for _, et := range impactEdges {
			neighbors, err := cb.store.GetNeighbors(ctx, current.node.ID, et, graph.Incoming)
			if err != nil {
				continue
			}
			for _, n := range neighbors {
				if _, seen := visited[n.ID]; seen {
					continue
				}
				visited[n.ID] = struct{}{}
				queue = append(queue, levelEntry{node: n, level: current.level + 1})

				// Categorize by type or architectural role.
				role := ""
				if n.Properties != nil {
					role = n.Properties[graph.PropArchRole]
				}
				switch {
				case n.Type == graph.NodeService || role == "service":
					services = append(services, n)
				case role == "controller":
					controllers = append(controllers, n)
				case n.Type == graph.NodeDTO || n.Type == graph.NodeViewModel:
					dtoViewModels = append(dtoViewModels, n)
				case n.Type == graph.NodeMigration:
					migrations = append(migrations, n)
				default:
					others = append(others, n)
				}
			}
		}
	}

	writeSection := func(title string, nodes []*graph.Node) {
		if len(nodes) == 0 {
			return
		}
		fmt.Fprintf(&b, "### %s\n", title)
		for _, n := range nodes {
			loc := ""
			if n.FilePath != "" {
				loc = fmt.Sprintf(" in %s", n.FilePath)
			}
			fmt.Fprintf(&b, "- [%s] %s%s\n", n.Type, n.Name, loc)
		}
		b.WriteString("\n")
	}

	if len(services) == 0 && len(controllers) == 0 && len(dtoViewModels) == 0 && len(migrations) == 0 && len(others) == 0 {
		b.WriteString("No consumers found for this model.\n")
		return b.String(), nil
	}

	writeSection("Services", services)
	writeSection("Controllers", controllers)
	writeSection("DTOs / View Models", dtoViewModels)
	writeSection("Migrations", migrations)
	writeSection("Other Consumers", others)

	return b.String(), nil
}

// BuildBranchContext returns a formatted summary of the current git branch state
// relative to the default branch, including changed files and their symbols.
func (cb *ContextBuilder) BuildBranchContext(ctx context.Context, repoPath string) (string, error) {
	diff, err := gitutil.GetBranchDiff(repoPath)
	if err != nil {
		return "", fmt.Errorf("get branch diff for %s: %w", repoPath, err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## Git Branch Context\n\n")
	fmt.Fprintf(&b, "Current branch: %s\n", diff.CurrentBranch)
	fmt.Fprintf(&b, "Default branch: %s\n", diff.DefaultBranch)

	if diff.IsFeatureBranch {
		fmt.Fprintf(&b, "Ahead: %d commits\n", diff.Ahead)
		fmt.Fprintf(&b, "Behind: %d commits\n", diff.Behind)
	} else {
		b.WriteString("Status: on default branch\n")
	}

	if len(diff.ChangedFiles) == 0 {
		if diff.IsFeatureBranch {
			b.WriteString("\nNo changed files compared to default branch.\n")
		}
		return b.String(), nil
	}

	fmt.Fprintf(&b, "\n### Changed Files (%d)\n", len(diff.ChangedFiles))
	for _, cf := range diff.ChangedFiles {
		fmt.Fprintf(&b, "- [%s] %s (+%d/-%d)\n", cf.Status, cf.Path, cf.Additions, cf.Deletions)
	}

	// For each changed file, query graph for its symbols.
	b.WriteString("\n### Symbols in Changed Files\n")
	for _, cf := range diff.ChangedFiles {
		if cf.Status == "deleted" {
			continue
		}
		nodes, err := cb.store.QueryNodes(ctx, graph.NodeFilter{FilePath: cf.Path})
		if err != nil || len(nodes) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n**%s:**\n", cf.Path)
		for _, n := range nodes {
			if n.Type == graph.NodeFile {
				continue
			}
			fmt.Fprintf(&b, "- [%s] %s\n", n.Type, n.Name)
		}
	}

	// Include commits on the branch for additional context.
	if diff.IsFeatureBranch {
		commits, err := gitutil.GetCommitsBetween(repoPath, diff.DefaultBranch)
		if err == nil && len(commits) > 0 {
			fmt.Fprintf(&b, "\n### Commits on Branch (%d)\n", len(commits))
			for _, c := range commits {
				fmt.Fprintf(&b, "- %s %s (%s)\n", c.Hash[:8], c.Message, c.Author)
			}
		}
	}

	return b.String(), nil
}
