package agents

import (
	"context"
	"fmt"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/pkg/llm"
)

const plannerSystemPrompt = `You are a codebase planning agent. You analyze codebases to provide impact analysis, dependency mapping, scope estimation, and change risk assessment. You have access to a knowledge graph of the codebase. Answer based on the provided context. Be specific about files, functions, and services affected.`

const agenticPlannerSystemPrompt = `You are a codebase planning agent with access to a knowledge graph of source code entities and their relationships. You help with impact analysis, dependency mapping, scope estimation, and change risk assessment.

You have tools to query the knowledge graph. Use them iteratively to gather the information you need before answering. Do NOT guess -- use the tools to verify.

Strategy:
1. Start with get_project_guidelines to understand project conventions
2. Use get_graph_overview to understand the codebase scope
3. Use search_nodes to find specific entities the user asks about
4. Use get_impact_analysis to trace dependencies and affected code
5. Use get_service_info or get_file_info for detailed context on specific areas

Provide your final answer with:
- Specific file paths, function names, and service names from the graph
- Clear categorization of direct vs indirect vs transitive impact
- Risk assessment based on the complexity and breadth of impact
- Concrete recommendations grounded in the project's conventions`

const defaultMaxIterations = 15

// Planner is the planning agent for impact analysis, dependency mapping,
// scope estimation, and change risk assessment.
type Planner struct {
	BaseAgent
	repoPaths     []string  // optional repo paths for branch-aware context
	registry      *Registry // tool registry for agentic mode
	maxIterations int
}

// NewPlanner creates a new planning agent with tool-use support.
// If the LLM client supports tools, the planner uses an agentic loop.
// Otherwise, it falls back to single-turn keyword-based context selection.
// Optional repoPaths enable branch-aware context.
func NewPlanner(client llm.Client, ctxBuilder *ContextBuilder, repoPaths ...string) *Planner {
	registry := NewRegistry()
	for _, tool := range NewPlannerTools(ctxBuilder) {
		registry.Register(tool)
	}

	return &Planner{
		BaseAgent: BaseAgent{
			name:         "planner",
			llmClient:    client,
			ctxBuilder:   ctxBuilder,
			systemPrompt: plannerSystemPrompt,
		},
		repoPaths:     repoPaths,
		registry:      registry,
		maxIterations: defaultMaxIterations,
	}
}

// SetMaxIterations sets the maximum number of agentic loop iterations.
func (p *Planner) SetMaxIterations(n int) {
	if n > 0 {
		p.maxIterations = n
	}
}

// Ask sends a query to the planning agent. If the LLM client supports tool
// calling, it uses an agentic loop where the LLM iteratively calls tools.
// Otherwise, it falls back to single-turn keyword-based context selection.
func (p *Planner) Ask(ctx context.Context, query string) (string, error) {
	toolClient, ok := p.llmClient.(llm.ToolCapableClient)
	if !ok {
		return p.askSingleTurn(ctx, query)
	}
	return p.askAgentic(ctx, query, toolClient)
}

// askAgentic runs an agentic loop where the LLM iteratively calls tools.
func (p *Planner) askAgentic(ctx context.Context, query string, toolClient llm.ToolCapableClient) (string, error) {
	var messages []llm.Message

	// Inject guidelines as initial context for API providers.
	// Claude CLI gets them via get_project_guidelines tool instead.
	if p.llmClient.Provider() != "claude-cli" {
		if guidelines, err := p.ctxBuilder.BuildGuidelineContext(ctx); err == nil && guidelines != "" {
			messages = append(messages,
				llm.Message{Role: llm.RoleUser, Content: "Project guidelines:\n\n" + guidelines},
				llm.Message{Role: llm.RoleAssistant, Content: "I've noted the project guidelines."},
			)
		}
	}

	messages = append(messages, llm.Message{Role: llm.RoleUser, Content: query})
	tools := p.registry.Definitions()

	for i := 0; i < p.maxIterations; i++ {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("planner timeout after %d iterations: %w", i, err)
		}

		resp, err := toolClient.ChatWithTools(ctx, agenticPlannerSystemPrompt, messages, tools)
		if err != nil {
			return "", fmt.Errorf("LLM chat failed at iteration %d: %w", i, err)
		}

		if !resp.HasToolCalls() {
			return resp.Content, nil
		}

		// Add assistant message with tool calls.
		messages = append(messages, llm.Message{
			Role:      llm.RoleAssistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		// Execute each tool call and add the results.
		for _, tc := range resp.ToolCalls {
			result, _, err := p.registry.Execute(ctx, tc.Name, tc.Arguments)
			if err != nil {
				result = fmt.Sprintf("Error: %v", err)
			}
			messages = append(messages, llm.Message{
				Role:       llm.RoleTool,
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}

	return "", fmt.Errorf("planner reached maximum iterations (%d)", p.maxIterations)
}

// askSingleTurn is the original single-turn implementation that uses
// keyword-based context selection. Used as fallback when the LLM client
// does not support tool calling.
func (p *Planner) askSingleTurn(ctx context.Context, query string) (string, error) {
	lower := strings.ToLower(query)
	var contextText string
	var err error

	switch {
	case containsAny(lower, "branch", "changes", "current", "working"):
		contextText, err = p.buildBranchContextFromQuery(ctx)
	case containsAny(lower, "model", "schema", "database", "entity", "domain"):
		contextText, err = p.buildModelContextFromQuery(ctx, query)
	case containsAny(lower, "architecture", "pattern", "layer", "structure"):
		contextText, err = p.ctxBuilder.BuildArchitectureContext(ctx)
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

// buildModelContextFromQuery extracts a service name from the query and builds
// model context for it. Falls back to overview context.
func (p *Planner) buildModelContextFromQuery(ctx context.Context, query string) (string, error) {
	serviceName := extractEntityName(query)
	if serviceName != "" {
		modelCtx, err := p.ctxBuilder.BuildModelContext(ctx, serviceName)
		if err == nil && !strings.Contains(modelCtx, "No data models found") {
			return modelCtx, nil
		}
	}
	// Fallback: try with empty service name to get all models.
	modelCtx, err := p.ctxBuilder.BuildModelContext(ctx, "")
	if err == nil && !strings.Contains(modelCtx, "No data models found") {
		return modelCtx, nil
	}
	return p.ctxBuilder.BuildOverviewContext(ctx)
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
