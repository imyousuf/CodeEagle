package agents

import (
	"context"
	"fmt"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// NewPlannerTools creates the set of tools available to the planning agent.
func NewPlannerTools(ctxBuilder *ContextBuilder) []Tool {
	return []Tool{
		&graphOverviewTool{ctxBuilder: ctxBuilder},
		&architectureOverviewTool{ctxBuilder: ctxBuilder},
		&serviceInfoTool{ctxBuilder: ctxBuilder},
		&fileInfoTool{ctxBuilder: ctxBuilder},
		&impactAnalysisTool{ctxBuilder: ctxBuilder},
		&searchNodesTool{store: ctxBuilder.store},
		&modelInfoTool{ctxBuilder: ctxBuilder},
		&projectGuidelinesTool{ctxBuilder: ctxBuilder},
		&queryFileSymbolsTool{store: ctxBuilder.store},
		&queryInterfaceImplTool{store: ctxBuilder.store},
		&queryNodeEdgesTool{store: ctxBuilder.store},
	}
}

// --- get_graph_overview ---

type graphOverviewTool struct {
	ctxBuilder *ContextBuilder
}

func (t *graphOverviewTool) Name() string { return "get_graph_overview" }

func (t *graphOverviewTool) Description() string {
	return "Get a high-level overview of the knowledge graph: total node/edge counts, breakdown by type, language distribution, and top packages."
}

func (t *graphOverviewTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *graphOverviewTool) Execute(ctx context.Context, _ map[string]any) (string, bool) {
	result, err := t.ctxBuilder.BuildOverviewContext(ctx)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), false
	}
	return result, true
}

// --- get_architecture_overview ---

type architectureOverviewTool struct {
	ctxBuilder *ContextBuilder
}

func (t *architectureOverviewTool) Name() string { return "get_architecture_overview" }

func (t *architectureOverviewTool) Description() string {
	return "Get the architecture overview: detected design patterns, layer distribution, and architectural roles across the codebase."
}

func (t *architectureOverviewTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *architectureOverviewTool) Execute(ctx context.Context, _ map[string]any) (string, bool) {
	result, err := t.ctxBuilder.BuildArchitectureContext(ctx)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), false
	}
	return result, true
}

// --- get_service_info ---

type serviceInfoTool struct {
	ctxBuilder *ContextBuilder
}

func (t *serviceInfoTool) Name() string { return "get_service_info" }

func (t *serviceInfoTool) Description() string {
	return "Get detailed information about a service or module: file count, symbols, type breakdown, API endpoints, and key types."
}

func (t *serviceInfoTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"service_name": map[string]any{
				"type":        "string",
				"description": "The service or package name to query (e.g., 'auth', 'indexer', 'graph').",
			},
		},
		"required": []string{"service_name"},
	}
}

func (t *serviceInfoTool) Execute(ctx context.Context, args map[string]any) (string, bool) {
	serviceName, _ := args["service_name"].(string)
	if serviceName == "" {
		return "Error: service_name is required", false
	}
	result, err := t.ctxBuilder.BuildServiceContext(ctx, serviceName)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), false
	}
	return result, true
}

// --- get_file_info ---

type fileInfoTool struct {
	ctxBuilder *ContextBuilder
}

func (t *fileInfoTool) Name() string { return "get_file_info" }

func (t *fileInfoTool) Description() string {
	return "Get detailed information about a specific file: symbols, dependencies, and dependents."
}

func (t *fileInfoTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "The file path to query (e.g., 'internal/graph/graph.go', 'cmd/main.go').",
			},
		},
		"required": []string{"file_path"},
	}
}

func (t *fileInfoTool) Execute(ctx context.Context, args map[string]any) (string, bool) {
	filePath, _ := args["file_path"].(string)
	if filePath == "" {
		return "Error: file_path is required", false
	}
	result, err := t.ctxBuilder.BuildFileContext(ctx, filePath)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), false
	}
	return result, true
}

// --- get_impact_analysis ---

type impactAnalysisTool struct {
	ctxBuilder *ContextBuilder
}

func (t *impactAnalysisTool) Name() string { return "get_impact_analysis" }

func (t *impactAnalysisTool) Description() string {
	return "Perform impact analysis on an entity: trace dependencies up to 3 levels deep to find direct, indirect, and transitive impact."
}

func (t *impactAnalysisTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"entity_name": map[string]any{
				"type":        "string",
				"description": "The name of the entity to analyze (e.g., 'HandleRequest', 'Store', 'AuthService'). Will search the graph by name pattern.",
			},
		},
		"required": []string{"entity_name"},
	}
}

func (t *impactAnalysisTool) Execute(ctx context.Context, args map[string]any) (string, bool) {
	entityName, _ := args["entity_name"].(string)
	if entityName == "" {
		return "Error: entity_name is required", false
	}

	// Search for the entity by name pattern.
	nodes, err := t.ctxBuilder.store.QueryNodes(ctx, graph.NodeFilter{
		NamePattern: entityName,
	})
	if err != nil {
		return fmt.Sprintf("Error querying nodes: %v", err), false
	}
	if len(nodes) == 0 {
		return fmt.Sprintf("No entity found matching %q. Try using search_nodes to find the exact name.", entityName), false
	}

	// Use the first match.
	result, err := t.ctxBuilder.BuildImpactContext(ctx, nodes[0].ID)
	if err != nil {
		return fmt.Sprintf("Error building impact context: %v", err), false
	}
	return result, true
}

// --- search_nodes ---

type searchNodesTool struct {
	store graph.Store
}

func (t *searchNodesTool) Name() string { return "search_nodes" }

func (t *searchNodesTool) Description() string {
	return "Search the knowledge graph for nodes matching the given criteria. Returns a table of matching nodes (limited to 50 results)."
}

func (t *searchNodesTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name_pattern": map[string]any{
				"type":        "string",
				"description": "Pattern to match against node names (glob-style, e.g., 'Handle*', 'Store').",
			},
			"node_type": map[string]any{
				"type":        "string",
				"description": "Filter by node type (e.g., 'Function', 'Struct', 'Interface', 'File', 'Service', 'Package').",
			},
			"package": map[string]any{
				"type":        "string",
				"description": "Filter by package name.",
			},
			"language": map[string]any{
				"type":        "string",
				"description": "Filter by programming language (e.g., 'go', 'python', 'typescript').",
			},
			"file_path": map[string]any{
				"type":        "string",
				"description": "Filter by file path.",
			},
		},
	}
}

func (t *searchNodesTool) Execute(ctx context.Context, args map[string]any) (string, bool) {
	filter := graph.NodeFilter{}
	if v, ok := args["name_pattern"].(string); ok && v != "" {
		filter.NamePattern = v
	}
	if v, ok := args["node_type"].(string); ok && v != "" {
		filter.Type = graph.NodeType(v)
	}
	if v, ok := args["package"].(string); ok && v != "" {
		filter.Package = v
	}
	if v, ok := args["language"].(string); ok && v != "" {
		filter.Language = v
	}
	if v, ok := args["file_path"].(string); ok && v != "" {
		filter.FilePath = v
	}

	nodes, err := t.store.QueryNodes(ctx, filter)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), false
	}
	if len(nodes) == 0 {
		return "No nodes found matching the given criteria.", false
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d nodes", len(nodes))

	limit := 50
	if len(nodes) > limit {
		fmt.Fprintf(&b, " (showing first %d)", limit)
		nodes = nodes[:limit]
	}
	b.WriteString(":\n\n")

	b.WriteString("| ID | Type | Name | File | Package |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, n := range nodes {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
			truncate(n.ID, 12), n.Type, n.Name, n.FilePath, n.Package)
	}

	return b.String(), true
}

// --- get_model_info ---

type modelInfoTool struct {
	ctxBuilder *ContextBuilder
}

func (t *modelInfoTool) Name() string { return "get_model_info" }

func (t *modelInfoTool) Description() string {
	return "Get information about data models (DB models, domain models, DTOs, view models) for a service or the entire codebase."
}

func (t *modelInfoTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"service_name": map[string]any{
				"type":        "string",
				"description": "The service name to filter models by. Leave empty to get all models across the codebase.",
			},
		},
	}
}

func (t *modelInfoTool) Execute(ctx context.Context, args map[string]any) (string, bool) {
	serviceName, _ := args["service_name"].(string)
	result, err := t.ctxBuilder.BuildModelContext(ctx, serviceName)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), false
	}
	return result, true
}

// --- get_project_guidelines ---

type projectGuidelinesTool struct {
	ctxBuilder *ContextBuilder
}

func (t *projectGuidelinesTool) Name() string { return "get_project_guidelines" }

func (t *projectGuidelinesTool) Description() string {
	return "Get the project's AI guidelines and conventions from CLAUDE.md, AGENTS.md, GEMINI.md, and similar guideline files. Use this to understand project conventions before making recommendations."
}

func (t *projectGuidelinesTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *projectGuidelinesTool) Execute(ctx context.Context, _ map[string]any) (string, bool) {
	result, err := t.ctxBuilder.BuildGuidelineContext(ctx)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), false
	}
	if result == "" {
		return "No project guidelines found in the knowledge graph.", true
	}
	return result, true
}

// truncate shortens a string to the given max length, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
