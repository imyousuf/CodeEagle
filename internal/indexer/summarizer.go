package indexer

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/pkg/llm"
)

// Summarizer uses an LLM to generate high-level summaries of indexed code
// and stores the results as Document nodes in the knowledge graph.
type Summarizer struct {
	client  llm.Client
	store   graph.Store
	log     func(format string, args ...any)
	verbose bool
}

// NewSummarizer creates a Summarizer backed by the given LLM client and graph store.
// If logger is nil, a no-op logger is used. When verbose is true, the summarizer
// logs details about each LLM call.
func NewSummarizer(client llm.Client, store graph.Store, logger func(format string, args ...any), verbose bool) *Summarizer {
	if logger == nil {
		logger = func(format string, args ...any) {}
	}
	return &Summarizer{client: client, store: store, log: logger, verbose: verbose}
}

// SummarizeService builds a prompt from the given nodes for a service/directory,
// sends it to the LLM for a 2-3 sentence summary, and stores the result as a
// Document node with properties generated=true, kind=summary, service=serviceName.
func (s *Summarizer) SummarizeService(ctx context.Context, serviceName string, nodes []*graph.Node) error {
	if len(nodes) == 0 {
		return nil
	}

	prompt := buildServicePrompt(serviceName, nodes)

	if s.verbose {
		s.log("  LLM call: summarizing service %s...", serviceName)
	}

	resp, err := s.client.Chat(ctx,
		"You are a code analysis assistant. Summarize codebases concisely.",
		[]llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
	)
	if err != nil {
		return fmt.Errorf("LLM chat for service %s: %w", serviceName, err)
	}

	if s.verbose {
		preview := resp.Content
		if len(preview) > 100 {
			preview = preview[:100]
		}
		s.log("  LLM response: %s... (%d chars total)", preview, len(resp.Content))
	}

	nodeID := graph.NewNodeID("Document", "generated", "summary:"+serviceName)
	node := &graph.Node{
		ID:         nodeID,
		Type:       graph.NodeDocument,
		Name:       fmt.Sprintf("Summary: %s", serviceName),
		DocComment: resp.Content,
		Properties: map[string]string{
			"generated": "true",
			"kind":      "summary",
			"service":   serviceName,
		},
	}

	// Delete any existing summary node for this service before storing.
	_ = s.store.DeleteNode(ctx, nodeID)
	if err := s.store.AddNode(ctx, node); err != nil {
		return fmt.Errorf("store summary for %s: %w", serviceName, err)
	}
	return nil
}

// SummarizePatterns groups all nodes by language and type, sends them to the LLM
// to identify architectural patterns, and stores the result as a Document node
// with properties generated=true, kind=patterns.
func (s *Summarizer) SummarizePatterns(ctx context.Context, allNodes []*graph.Node) error {
	if len(allNodes) == 0 {
		return nil
	}

	prompt := buildPatternsPrompt(allNodes)

	if s.verbose {
		s.log("  LLM call: summarizing codebase patterns...")
	}

	resp, err := s.client.Chat(ctx,
		"You are a code analysis assistant. Identify architectural patterns and conventions.",
		[]llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
	)
	if err != nil {
		return fmt.Errorf("LLM chat for patterns: %w", err)
	}

	if s.verbose {
		preview := resp.Content
		if len(preview) > 100 {
			preview = preview[:100]
		}
		s.log("  LLM response: %s... (%d chars total)", preview, len(resp.Content))
	}

	nodeID := graph.NewNodeID("Document", "generated", "patterns")
	node := &graph.Node{
		ID:         nodeID,
		Type:       graph.NodeDocument,
		Name:       "Codebase Patterns",
		DocComment: resp.Content,
		Properties: map[string]string{
			"generated": "true",
			"kind":      "patterns",
		},
	}

	_ = s.store.DeleteNode(ctx, nodeID)
	if err := s.store.AddNode(ctx, node); err != nil {
		return fmt.Errorf("store patterns summary: %w", err)
	}
	return nil
}

// SummarizeArchitecture builds a rich prompt including architectural roles,
// design patterns, layer tags, and interface-implementation pairs, sends it
// to the LLM to identify deeper architectural patterns, and stores the result
// as a Document node with kind=architecture_analysis.
func (s *Summarizer) SummarizeArchitecture(ctx context.Context, serviceName string, nodes []*graph.Node) error {
	if len(nodes) == 0 {
		return nil
	}

	prompt := buildArchitecturePrompt(serviceName, nodes)

	if s.verbose {
		s.log("  LLM call: summarizing architecture for %s...", serviceName)
	}

	resp, err := s.client.Chat(ctx,
		"You are a software architecture analyst. Identify architectural and design patterns in codebases.",
		[]llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
	)
	if err != nil {
		return fmt.Errorf("LLM chat for architecture %s: %w", serviceName, err)
	}

	if s.verbose {
		preview := resp.Content
		if len(preview) > 100 {
			preview = preview[:100]
		}
		s.log("  LLM response: %s... (%d chars total)", preview, len(resp.Content))
	}

	nodeID := graph.NewNodeID("Document", "generated", "architecture:"+serviceName)
	node := &graph.Node{
		ID:         nodeID,
		Type:       graph.NodeDocument,
		Name:       fmt.Sprintf("Architecture Analysis: %s", serviceName),
		DocComment: resp.Content,
		Properties: map[string]string{
			"generated": "true",
			"kind":      "architecture_analysis",
			"service":   serviceName,
		},
	}

	_ = s.store.DeleteNode(ctx, nodeID)
	if err := s.store.AddNode(ctx, node); err != nil {
		return fmt.Errorf("store architecture analysis for %s: %w", serviceName, err)
	}
	return nil
}

// buildArchitecturePrompt creates the LLM prompt for architecture analysis of a service.
func buildArchitecturePrompt(serviceName string, nodes []*graph.Node) string {
	// Group nodes by architectural role.
	byRole := make(map[string][]*graph.Node)
	// Collect design patterns.
	patternCounts := make(map[string]int)
	// Collect layer distribution.
	layerCounts := make(map[string]int)
	// Collect interfaces and their implementations.
	var interfaces []string
	var implementations []string
	// Collect DB models.
	var dbModels []string

	for _, n := range nodes {
		if role, ok := n.Properties[graph.PropArchRole]; ok && role != "" {
			byRole[role] = append(byRole[role], n)
		}
		if patterns, ok := n.Properties[graph.PropDesignPattern]; ok && patterns != "" {
			for _, p := range strings.Split(patterns, ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					patternCounts[p]++
				}
			}
		}
		if layer, ok := n.Properties[graph.PropLayerTag]; ok && layer != "" {
			layerCounts[layer]++
		}
		if n.Type == graph.NodeInterface {
			interfaces = append(interfaces, n.Name)
		}
		if n.Type == graph.NodeStruct || n.Type == graph.NodeClass {
			implementations = append(implementations, fmt.Sprintf("%s (%s)", n.Name, n.Type))
		}
		if n.Type == graph.NodeDBModel {
			dbModels = append(dbModels, n.Name)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Service/directory: %s\n\n", serviceName)

	// Architectural role distribution.
	if len(byRole) > 0 {
		b.WriteString("Architectural role distribution:\n")
		sortedRoles := sortedMapKeys(byRole)
		for _, role := range sortedRoles {
			roleNodes := byRole[role]
			fmt.Fprintf(&b, "- %s: %d nodes\n", role, len(roleNodes))
			limit := 5
			if len(roleNodes) < limit {
				limit = len(roleNodes)
			}
			for _, rn := range roleNodes[:limit] {
				fmt.Fprintf(&b, "    - %s (%s)\n", rn.Name, rn.Type)
			}
			if len(roleNodes) > 5 {
				fmt.Fprintf(&b, "    - ... and %d more\n", len(roleNodes)-5)
			}
		}
		b.WriteString("\n")
	}

	// Design patterns detected by classifier.
	if len(patternCounts) > 0 {
		b.WriteString("Design patterns detected:\n")
		sortedPatterns := sortedStringMapKeys(patternCounts)
		for _, p := range sortedPatterns {
			fmt.Fprintf(&b, "- %s: %d occurrences\n", p, patternCounts[p])
		}
		b.WriteString("\n")
	}

	// Layer distribution.
	if len(layerCounts) > 0 {
		b.WriteString("Layer distribution:\n")
		sortedLayers := sortedStringMapKeys(layerCounts)
		for _, layer := range sortedLayers {
			fmt.Fprintf(&b, "- %s: %d nodes\n", layer, layerCounts[layer])
		}
		b.WriteString("\n")
	}

	// Interfaces and implementations.
	if len(interfaces) > 0 {
		sort.Strings(interfaces)
		b.WriteString("Interfaces:\n")
		for _, iface := range interfaces {
			fmt.Fprintf(&b, "- %s\n", iface)
		}
		b.WriteString("\n")
	}
	if len(implementations) > 0 {
		sort.Strings(implementations)
		limit := 15
		if len(implementations) < limit {
			limit = len(implementations)
		}
		b.WriteString("Structs/Classes (potential implementations):\n")
		for _, impl := range implementations[:limit] {
			fmt.Fprintf(&b, "- %s\n", impl)
		}
		if len(implementations) > 15 {
			fmt.Fprintf(&b, "- ... and %d more\n", len(implementations)-15)
		}
		b.WriteString("\n")
	}

	// DB models.
	if len(dbModels) > 0 {
		sort.Strings(dbModels)
		fmt.Fprintf(&b, "Database models (%d):\n", len(dbModels))
		for _, m := range dbModels {
			fmt.Fprintf(&b, "- %s\n", m)
		}
		b.WriteString("\n")
	}

	b.WriteString("Based on the above, identify:\n")
	b.WriteString("1. DDD patterns (bounded contexts, aggregates, value objects, domain events)\n")
	b.WriteString("2. AOP patterns (cross-cutting concerns via decorators/annotations)\n")
	b.WriteString("3. Observer/Event patterns (event buses, listeners, subscribers)\n")
	b.WriteString("4. Repository pattern usage\n")
	b.WriteString("5. Factory/Builder/Strategy patterns\n")
	b.WriteString("6. CQRS (Command Query Responsibility Segregation)\n")
	b.WriteString("7. Clean Architecture / Hexagonal Architecture layers\n")
	b.WriteString("8. Microservice communication patterns (sync REST, async messaging)\n")
	b.WriteString("9. Dependency injection patterns\n")
	return b.String()
}

// sortedMapKeys returns sorted keys from a map[string][]*graph.Node.
func sortedMapKeys(m map[string][]*graph.Node) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sortedStringMapKeys returns sorted keys from a map[string]int.
func sortedStringMapKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// buildServicePrompt creates the LLM prompt for summarizing a service.
func buildServicePrompt(serviceName string, nodes []*graph.Node) string {
	byType := make(map[graph.NodeType]int)
	languages := make(map[string]int)
	var exportedNames []string
	roleCounts := make(map[string]int)
	patternCounts := make(map[string]int)
	layerCounts := make(map[string]int)
	dbModelCount := 0

	for _, n := range nodes {
		byType[n.Type]++
		if n.Language != "" {
			languages[n.Language]++
		}
		if n.Exported && n.Type != graph.NodeFile && n.Type != graph.NodePackage {
			exportedNames = append(exportedNames, fmt.Sprintf("%s (%s)", n.Name, n.Type))
		}
		if role, ok := n.Properties[graph.PropArchRole]; ok && role != "" {
			roleCounts[role]++
		}
		if patterns, ok := n.Properties[graph.PropDesignPattern]; ok && patterns != "" {
			for _, p := range strings.Split(patterns, ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					patternCounts[p]++
				}
			}
		}
		if layer, ok := n.Properties[graph.PropLayerTag]; ok && layer != "" {
			layerCounts[layer]++
		}
		if n.Type == graph.NodeDBModel {
			dbModelCount++
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Service/directory: %s\n\n", serviceName)
	b.WriteString("Node breakdown:\n")
	for nt, count := range byType {
		fmt.Fprintf(&b, "- %s: %d\n", nt, count)
	}
	if len(languages) > 0 {
		b.WriteString("\nLanguages:\n")
		for lang, count := range languages {
			fmt.Fprintf(&b, "- %s: %d nodes\n", lang, count)
		}
	}
	if len(roleCounts) > 0 {
		b.WriteString("\nArchitectural roles:\n")
		for role, count := range roleCounts {
			fmt.Fprintf(&b, "- %s: %d\n", role, count)
		}
	}
	if len(patternCounts) > 0 {
		b.WriteString("\nDesign patterns:\n")
		for p, count := range patternCounts {
			fmt.Fprintf(&b, "- %s: %d\n", p, count)
		}
	}
	if len(layerCounts) > 0 {
		b.WriteString("\nLayers:\n")
		for layer, count := range layerCounts {
			fmt.Fprintf(&b, "- %s: %d\n", layer, count)
		}
	}
	if dbModelCount > 0 {
		fmt.Fprintf(&b, "\nDB models: %d\n", dbModelCount)
	}
	if len(exportedNames) > 0 {
		sort.Strings(exportedNames)
		limit := 20
		if len(exportedNames) < limit {
			limit = len(exportedNames)
		}
		b.WriteString("\nKey exported symbols:\n")
		for _, name := range exportedNames[:limit] {
			fmt.Fprintf(&b, "- %s\n", name)
		}
		if len(exportedNames) > 20 {
			fmt.Fprintf(&b, "- ... and %d more\n", len(exportedNames)-20)
		}
	}
	b.WriteString("\nSummarize this service in 2-3 sentences: what it does, key patterns, and technology.")
	return b.String()
}

// buildPatternsPrompt creates the LLM prompt for identifying codebase patterns.
func buildPatternsPrompt(allNodes []*graph.Node) string {
	byLang := make(map[string]map[graph.NodeType]int)
	totalByType := make(map[graph.NodeType]int)
	crossServicePatterns := make(map[string]int)
	crossServiceRoles := make(map[string]int)
	crossServiceLayers := make(map[string]int)

	for _, n := range allNodes {
		lang := n.Language
		if lang == "" {
			lang = "unknown"
		}
		if byLang[lang] == nil {
			byLang[lang] = make(map[graph.NodeType]int)
		}
		byLang[lang][n.Type]++
		totalByType[n.Type]++

		if patterns, ok := n.Properties[graph.PropDesignPattern]; ok && patterns != "" {
			for _, p := range strings.Split(patterns, ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					crossServicePatterns[p]++
				}
			}
		}
		if role, ok := n.Properties[graph.PropArchRole]; ok && role != "" {
			crossServiceRoles[role]++
		}
		if layer, ok := n.Properties[graph.PropLayerTag]; ok && layer != "" {
			crossServiceLayers[layer]++
		}
	}

	// Collect unique packages/directories.
	pkgs := make(map[string]struct{})
	for _, n := range allNodes {
		if n.Package != "" {
			pkgs[n.Package] = struct{}{}
		}
	}

	var b strings.Builder
	b.WriteString("Codebase entity summary:\n\n")

	// Sort languages for deterministic output.
	langs := make([]string, 0, len(byLang))
	for l := range byLang {
		langs = append(langs, l)
	}
	sort.Strings(langs)

	for _, lang := range langs {
		counts := byLang[lang]
		fmt.Fprintf(&b, "Language: %s\n", lang)
		for nt, count := range counts {
			fmt.Fprintf(&b, "  - %s: %d\n", nt, count)
		}
	}

	if len(pkgs) > 0 {
		sortedPkgs := make([]string, 0, len(pkgs))
		for p := range pkgs {
			sortedPkgs = append(sortedPkgs, p)
		}
		sort.Strings(sortedPkgs)
		fmt.Fprintf(&b, "\nPackages/modules (%d total):\n", len(sortedPkgs))
		limit := 30
		if len(sortedPkgs) < limit {
			limit = len(sortedPkgs)
		}
		for _, p := range sortedPkgs[:limit] {
			fmt.Fprintf(&b, "- %s\n", p)
		}
		if len(sortedPkgs) > 30 {
			fmt.Fprintf(&b, "- ... and %d more\n", len(sortedPkgs)-30)
		}
	}

	// Cross-service architectural patterns.
	if len(crossServicePatterns) > 0 {
		b.WriteString("\nCross-service design patterns:\n")
		sortedPatterns := sortedStringMapKeys(crossServicePatterns)
		for _, p := range sortedPatterns {
			fmt.Fprintf(&b, "- %s: %d occurrences\n", p, crossServicePatterns[p])
		}
	}

	if len(crossServiceRoles) > 0 {
		b.WriteString("\nArchitectural role distribution across codebase:\n")
		sortedRoles := sortedStringMapKeys(crossServiceRoles)
		for _, role := range sortedRoles {
			fmt.Fprintf(&b, "- %s: %d\n", role, crossServiceRoles[role])
		}
	}

	if len(crossServiceLayers) > 0 {
		b.WriteString("\nLayer consistency across codebase:\n")
		sortedLayers := sortedStringMapKeys(crossServiceLayers)
		for _, layer := range sortedLayers {
			fmt.Fprintf(&b, "- %s: %d nodes\n", layer, crossServiceLayers[layer])
		}
	}

	b.WriteString("\nBased on these code entities, describe the architectural patterns, tech stack, and conventions used.")
	b.WriteString(" Also analyze cross-service pattern consistency and layer adherence.")
	return b.String()
}

// GroupNodesByTopDir groups nodes by their top-level directory relative to the
// given base paths. Nodes without a file path are placed in a "(root)" group.
func GroupNodesByTopDir(nodes []*graph.Node, basePaths []string) map[string][]*graph.Node {
	groups := make(map[string][]*graph.Node)
	for _, n := range nodes {
		if n.FilePath == "" {
			groups["(root)"] = append(groups["(root)"], n)
			continue
		}

		group := "(root)"
		for _, base := range basePaths {
			rel, err := filepath.Rel(base, n.FilePath)
			if err != nil || strings.HasPrefix(rel, "..") {
				continue
			}
			parts := strings.SplitN(rel, string(filepath.Separator), 2)
			if len(parts) > 0 && parts[0] != "" {
				group = parts[0]
			}
			break
		}
		groups[group] = append(groups[group], n)
	}
	return groups
}
