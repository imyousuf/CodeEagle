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
	client llm.Client
	store  graph.Store
}

// NewSummarizer creates a Summarizer backed by the given LLM client and graph store.
func NewSummarizer(client llm.Client, store graph.Store) *Summarizer {
	return &Summarizer{client: client, store: store}
}

// SummarizeService builds a prompt from the given nodes for a service/directory,
// sends it to the LLM for a 2-3 sentence summary, and stores the result as a
// Document node with properties generated=true, kind=summary, service=serviceName.
func (s *Summarizer) SummarizeService(ctx context.Context, serviceName string, nodes []*graph.Node) error {
	if len(nodes) == 0 {
		return nil
	}

	prompt := buildServicePrompt(serviceName, nodes)

	resp, err := s.client.Chat(ctx,
		"You are a code analysis assistant. Summarize codebases concisely.",
		[]llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
	)
	if err != nil {
		return fmt.Errorf("LLM chat for service %s: %w", serviceName, err)
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

	resp, err := s.client.Chat(ctx,
		"You are a code analysis assistant. Identify architectural patterns and conventions.",
		[]llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
	)
	if err != nil {
		return fmt.Errorf("LLM chat for patterns: %w", err)
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

// buildServicePrompt creates the LLM prompt for summarizing a service.
func buildServicePrompt(serviceName string, nodes []*graph.Node) string {
	byType := make(map[graph.NodeType]int)
	languages := make(map[string]int)
	var exportedNames []string
	for _, n := range nodes {
		byType[n.Type]++
		if n.Language != "" {
			languages[n.Language]++
		}
		if n.Exported && n.Type != graph.NodeFile && n.Type != graph.NodePackage {
			exportedNames = append(exportedNames, fmt.Sprintf("%s (%s)", n.Name, n.Type))
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

	b.WriteString("\nBased on these code entities, describe the architectural patterns, tech stack, and conventions used.")
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
