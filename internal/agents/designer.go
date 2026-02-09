package agents

import (
	"context"
	"fmt"
	"strings"

	"github.com/imyousuf/CodeEagle/pkg/llm"
)

const designerSystemPrompt = `You are a codebase design agent. You analyze architecture patterns, API consistency, cross-service patterns, and suggest designs based on existing codebase conventions. Answer based on the provided context.`

// Designer is the design agent for architecture review and pattern recognition.
type Designer struct {
	BaseAgent
}

// NewDesigner creates a new design agent.
func NewDesigner(client llm.Client, ctxBuilder *ContextBuilder) *Designer {
	return &Designer{
		BaseAgent: BaseAgent{
			name:         "designer",
			llmClient:    client,
			ctxBuilder:   ctxBuilder,
			systemPrompt: designerSystemPrompt,
		},
	}
}

// Ask builds relevant context from the knowledge graph and sends the query
// to the LLM. It always includes the graph overview and adds service or file
// context when a specific entity is mentioned.
func (d *Designer) Ask(ctx context.Context, query string) (string, error) {
	var parts []string

	// Always include overview context for architectural questions.
	overview, err := d.ctxBuilder.BuildOverviewContext(ctx)
	if err != nil {
		return "", fmt.Errorf("build overview context: %w", err)
	}
	parts = append(parts, overview)

	// Try to add more specific context based on entities mentioned in the query.
	entityName := extractEntityName(query)
	if entityName != "" {
		// Try as service/package first.
		svcCtx, err := d.ctxBuilder.BuildServiceContext(ctx, entityName)
		if err == nil && !strings.Contains(svcCtx, "No indexed nodes found") {
			parts = append(parts, svcCtx)
		} else {
			// Try as file path.
			fileCtx, err := d.ctxBuilder.BuildFileContext(ctx, entityName)
			if err == nil && !strings.Contains(fileCtx, "No indexed symbols found") {
				parts = append(parts, fileCtx)
			}
		}
	}

	contextText := strings.Join(parts, "\n\n")
	return d.ask(ctx, contextText, query)
}
