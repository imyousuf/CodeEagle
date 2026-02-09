package agents

import (
	"context"
	"fmt"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/pkg/llm"
)

const askerSystemPrompt = `You are a knowledgeable codebase assistant. You answer freeform questions about the codebase using a knowledge graph that captures source code entities, relationships, documentation, and code quality metrics. Provide specific, grounded answers referencing files, functions, services, and patterns found in the provided context. If the context does not contain enough information to answer fully, say so clearly.`

// Asker is the general-purpose Q&A agent that answers freeform questions
// about the indexed codebase. It uses a richer context selection strategy
// than other agents since it handles any type of question.
type Asker struct {
	BaseAgent
	repoPaths []string
}

// NewAsker creates a new ask agent. Optional repoPaths enable branch-aware context.
func NewAsker(client llm.Client, ctxBuilder *ContextBuilder, repoPaths ...string) *Asker {
	return &Asker{
		BaseAgent: BaseAgent{
			name:         "asker",
			llmClient:    client,
			ctxBuilder:   ctxBuilder,
			systemPrompt: askerSystemPrompt,
		},
		repoPaths: repoPaths,
	}
}

// Ask analyzes the query to determine what context to fetch from the knowledge
// graph, combines multiple context sources, then sends the enriched query to
// the LLM. Unlike other agents that use a switch/case, the asker accumulates
// context from all matching categories.
func (a *Asker) Ask(ctx context.Context, query string) (string, error) {
	if a.verbose && a.log != nil {
		a.log("Starting asker query...")
	}
	lower := strings.ToLower(query)
	var parts []string

	// Always include overview context for baseline awareness.
	overview, err := a.ctxBuilder.BuildOverviewContext(ctx)
	if err != nil {
		return "", fmt.Errorf("build overview context: %w", err)
	}
	parts = append(parts, overview)

	// Always include LLM-generated summaries if available.
	summaryCtx, err := a.ctxBuilder.BuildSummaryContext(ctx)
	if err == nil && summaryCtx != "" {
		parts = append(parts, summaryCtx)
	}

	// If query mentions a file path or extension, add file context.
	if filePath := extractFilePath(query); filePath != "" {
		fileCtx, err := a.ctxBuilder.BuildFileContext(ctx, filePath)
		if err == nil && !strings.Contains(fileCtx, "No indexed symbols found") {
			parts = append(parts, fileCtx)
		}
	}

	// If query mentions a service/module name, add service context.
	if entityName := extractEntityName(query); entityName != "" {
		svcCtx, err := a.ctxBuilder.BuildServiceContext(ctx, entityName)
		if err == nil && !strings.Contains(svcCtx, "No indexed nodes found") {
			parts = append(parts, svcCtx)
		}
	}

	// Model/schema/database/entity queries.
	if containsAny(lower, "model", "schema", "database", "entity") {
		entityName := extractEntityName(query)
		modelCtx, err := a.ctxBuilder.BuildModelContext(ctx, entityName)
		if err == nil && !strings.Contains(modelCtx, "No data models found") {
			parts = append(parts, modelCtx)
		}
	}

	// Architecture/pattern/design/structure queries.
	if containsAny(lower, "architecture", "pattern", "design", "structure") {
		archCtx, err := a.ctxBuilder.BuildArchitectureContext(ctx)
		if err == nil && !strings.Contains(archCtx, "No architectural metadata") {
			parts = append(parts, archCtx)
		}
	}

	// Branch/changes/diff/PR queries.
	if containsAny(lower, "branch", "changes", "diff", "pr") {
		for _, repoPath := range a.repoPaths {
			branchCtx, err := a.ctxBuilder.BuildBranchContext(ctx, repoPath)
			if err == nil {
				parts = append(parts, branchCtx)
			}
		}
	}

	// Metrics/complexity/coverage/quality queries.
	if containsAny(lower, "metric", "complexity", "coverage", "quality") {
		if filePath := extractFilePath(query); filePath != "" {
			metricsCtx, err := a.ctxBuilder.BuildMetricsContext(ctx, filePath)
			if err == nil && !strings.Contains(metricsCtx, "No indexed symbols found") {
				parts = append(parts, metricsCtx)
			}
		}
	}

	// Impact/affect/change/depend queries.
	if containsAny(lower, "impact", "affect", "depend") {
		node, err := a.findNodeByQuery(ctx, query)
		if err == nil && node != nil {
			impactCtx, err := a.ctxBuilder.BuildImpactContext(ctx, node.ID)
			if err == nil {
				parts = append(parts, impactCtx)
			}
		}
	}

	// Guideline/convention/rule queries (guidelines are auto-injected by BaseAgent,
	// but explicitly adding them as context makes the content visible in the prompt).
	if containsAny(lower, "guideline", "convention", "rule") {
		guideCtx, err := a.ctxBuilder.BuildGuidelineContext(ctx)
		if err == nil && guideCtx != "" {
			parts = append(parts, guideCtx)
		}
	}

	contextText := strings.Join(parts, "\n\n")
	return a.ask(ctx, contextText, query)
}

// findNodeByQuery searches for a node in the graph matching words in the query.
func (a *Asker) findNodeByQuery(ctx context.Context, query string) (*graph.Node, error) {
	words := strings.Fields(query)
	for _, word := range words {
		if isStopWord(word) {
			continue
		}
		nodes, err := a.ctxBuilder.store.QueryNodes(ctx, graph.NodeFilter{
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

// extractFilePath looks for tokens in the query that look like file paths
// (contain a / or end with a known file extension).
func extractFilePath(query string) string {
	words := strings.Fields(query)
	for _, word := range words {
		word = strings.Trim(word, ".,;:?!\"'`")
		if word == "" {
			continue
		}
		if strings.Contains(word, "/") {
			return word
		}
		for _, ext := range []string{".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".java", ".html", ".md", ".yaml", ".yml", ".json"} {
			if strings.HasSuffix(word, ext) {
				return word
			}
		}
	}
	return ""
}
