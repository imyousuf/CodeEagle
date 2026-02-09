package agents

import (
	"context"
	"fmt"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/pkg/llm"
)

const plannerSystemPrompt = `You are a codebase planning agent. You analyze codebases to provide impact analysis, dependency mapping, scope estimation, and change risk assessment. You have access to a knowledge graph of the codebase. Answer based on the provided context. Be specific about files, functions, and services affected.`

// Planner is the planning agent for impact analysis, dependency mapping,
// scope estimation, and change risk assessment.
type Planner struct {
	BaseAgent
	repoPaths []string // optional repo paths for branch-aware context
}

// NewPlanner creates a new planning agent. Optional repoPaths enable branch-aware context.
func NewPlanner(client llm.Client, ctxBuilder *ContextBuilder, repoPaths ...string) *Planner {
	return &Planner{
		BaseAgent: BaseAgent{
			name:         "planner",
			llmClient:    client,
			ctxBuilder:   ctxBuilder,
			systemPrompt: plannerSystemPrompt,
		},
		repoPaths: repoPaths,
	}
}

// Ask analyzes the query to determine what context to fetch from the knowledge
// graph, then sends the enriched query to the LLM.
func (p *Planner) Ask(ctx context.Context, query string) (string, error) {
	lower := strings.ToLower(query)
	var contextText string
	var err error

	switch {
	case containsAny(lower, "branch", "changes", "current", "working"):
		contextText, err = p.buildBranchContextFromQuery(ctx)
	case containsAny(lower, "change", "affect", "impact", "modify"):
		contextText, err = p.buildImpactContextFromQuery(ctx, query)
	case containsAny(lower, "depend", "relies", "uses"):
		contextText, err = p.buildServiceContextFromQuery(ctx, query)
	case containsAny(lower, "scope", "touch", "files"):
		contextText, err = p.buildScopeContextFromQuery(ctx, query)
	default:
		contextText, err = p.ctxBuilder.BuildOverviewContext(ctx)
	}
	if err != nil {
		return "", fmt.Errorf("build context: %w", err)
	}

	return p.ask(ctx, contextText, query)
}

// buildImpactContextFromQuery extracts a likely node name from the query
// and builds impact context for it. Falls back to overview context.
func (p *Planner) buildImpactContextFromQuery(ctx context.Context, query string) (string, error) {
	// Try to find a node matching words in the query.
	node, err := p.findNodeByQuery(ctx, query)
	if err == nil && node != nil {
		return p.ctxBuilder.BuildImpactContext(ctx, node.ID)
	}
	// Fallback to overview context.
	return p.ctxBuilder.BuildOverviewContext(ctx)
}

// buildServiceContextFromQuery extracts a service name from the query
// and builds service context for it.
func (p *Planner) buildServiceContextFromQuery(ctx context.Context, query string) (string, error) {
	// Try extracting a service/package name from the query.
	serviceName := extractEntityName(query)
	if serviceName != "" {
		svcCtx, err := p.ctxBuilder.BuildServiceContext(ctx, serviceName)
		if err == nil {
			return svcCtx, nil
		}
	}
	return p.ctxBuilder.BuildOverviewContext(ctx)
}

// buildBranchContextFromQuery builds context about the current branch state.
func (p *Planner) buildBranchContextFromQuery(ctx context.Context) (string, error) {
	var parts []string
	for _, repoPath := range p.repoPaths {
		branchCtx, err := p.ctxBuilder.BuildBranchContext(ctx, repoPath)
		if err == nil {
			parts = append(parts, branchCtx)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n\n"), nil
	}
	// Fallback to overview context if no repo paths configured.
	return p.ctxBuilder.BuildOverviewContext(ctx)
}

// buildScopeContextFromQuery tries to build diff or service context.
func (p *Planner) buildScopeContextFromQuery(ctx context.Context, query string) (string, error) {
	serviceName := extractEntityName(query)
	if serviceName != "" {
		svcCtx, err := p.ctxBuilder.BuildServiceContext(ctx, serviceName)
		if err == nil {
			return svcCtx, nil
		}
	}
	return p.ctxBuilder.BuildOverviewContext(ctx)
}

// findNodeByQuery searches for a node in the graph matching words in the query.
func (p *Planner) findNodeByQuery(ctx context.Context, query string) (*graph.Node, error) {
	words := strings.Fields(query)
	for _, word := range words {
		// Skip common stop words.
		if isStopWord(word) {
			continue
		}
		nodes, err := p.ctxBuilder.store.QueryNodes(ctx, graph.NodeFilter{
			NamePattern: word,
		})
		if err != nil {
			continue
		}
		if len(nodes) > 0 {
			return nodes[0], nil
		}
	}
	return nil, fmt.Errorf("no matching node found")
}

// containsAny returns true if s contains any of the given substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// extractEntityName tries to extract a likely entity name (service, package,
// function) from a query string. It returns the first capitalized word or
// word that looks like a package/path.
func extractEntityName(query string) string {
	words := strings.Fields(query)
	for _, word := range words {
		// Remove surrounding punctuation.
		word = strings.Trim(word, ".,;:?!\"'`")
		if word == "" {
			continue
		}
		// If it looks like a path or package name.
		if strings.Contains(word, "/") || strings.Contains(word, ".") {
			return word
		}
		// If it starts with uppercase and is not a common English word.
		if word[0] >= 'A' && word[0] <= 'Z' && !isStopWord(strings.ToLower(word)) {
			return word
		}
	}
	return ""
}

// isStopWord returns true for common English words that should be skipped
// when searching for entity names.
func isStopWord(w string) bool {
	stopWords := map[string]struct{}{
		"the": {}, "a": {}, "an": {}, "is": {}, "are": {}, "was": {}, "were": {},
		"be": {}, "been": {}, "being": {}, "have": {}, "has": {}, "had": {},
		"do": {}, "does": {}, "did": {}, "will": {}, "would": {}, "could": {},
		"should": {}, "may": {}, "might": {}, "shall": {}, "can": {},
		"what": {}, "which": {}, "who": {}, "whom": {}, "this": {}, "that": {},
		"these": {}, "those": {}, "i": {}, "you": {}, "he": {}, "she": {},
		"it": {}, "we": {}, "they": {}, "me": {}, "him": {}, "her": {},
		"us": {}, "them": {}, "my": {}, "your": {}, "his": {}, "its": {},
		"our": {}, "their": {}, "if": {}, "on": {}, "in": {}, "at": {},
		"to": {}, "for": {}, "of": {}, "with": {}, "by": {}, "from": {},
		"up": {}, "about": {}, "into": {}, "through": {}, "during": {},
		"before": {}, "after": {}, "above": {}, "below": {}, "between": {},
		"and": {}, "but": {}, "or": {}, "not": {}, "so": {}, "yet": {},
		"all": {}, "each": {}, "every": {}, "both": {}, "few": {}, "more": {},
		"most": {}, "other": {}, "some": {}, "such": {}, "no": {}, "only": {},
		"same": {}, "than": {}, "too": {}, "very": {}, "just": {},
		"tell": {}, "let": {}, "get": {}, "give": {}, "show": {}, "know": {},
		"change": {}, "affect": {}, "impact": {}, "modify": {},
		"depend": {}, "relies": {}, "uses": {}, "scope": {}, "touch": {},
		"files": {}, "how": {}, "where": {}, "when": {}, "why": {},
	}
	_, ok := stopWords[strings.ToLower(w)]
	return ok
}
