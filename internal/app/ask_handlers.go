//go:build app

package app

import (
	"context"
	"fmt"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/imyousuf/CodeEagle/internal/agents"
)

// GetAgentTypes returns the list of available AI agents.
func (a *App) GetAgentTypes() []AgentInfo {
	return []AgentInfo{
		{
			ID:          "planner",
			Name:        "Planner",
			Description: "Impact analysis, dependency mapping, scope estimation, and change risk assessment",
		},
		{
			ID:          "designer",
			Name:        "Designer",
			Description: "Architecture pattern recognition, API design review, and cross-service consistency",
		},
		{
			ID:          "reviewer",
			Name:        "Reviewer",
			Description: "Code review against conventions, flag deviations, identify missing tests",
		},
		{
			ID:          "asker",
			Name:        "Asker",
			Description: "General-purpose Q&A about the indexed codebase",
		},
	}
}

// AskAgent sends a query to the specified agent asynchronously.
// It emits Wails events: "agent:thinking", "agent:response", "agent:error".
func (a *App) AskAgent(agentType, query string) error {
	if a.llmClient == nil {
		return fmt.Errorf("LLM provider not available; set ANTHROPIC_API_KEY or configure vertex-ai")
	}

	if query == "" {
		return fmt.Errorf("query cannot be empty")
	}

	go func() {
		// Emit thinking event.
		runtime.EventsEmit(a.ctx, "agent:thinking", map[string]string{
			"agent": agentType,
		})

		// Serialize agent access.
		a.agentMu.Lock()
		defer a.agentMu.Unlock()

		agent, err := a.createAgent(agentType)
		if err != nil {
			runtime.EventsEmit(a.ctx, "agent:error", map[string]string{
				"agent": agentType,
				"error": err.Error(),
			})
			return
		}

		resp, err := agent.Ask(context.Background(), query)
		if err != nil {
			runtime.EventsEmit(a.ctx, "agent:error", map[string]string{
				"agent": agentType,
				"error": err.Error(),
			})
			return
		}

		runtime.EventsEmit(a.ctx, "agent:response", map[string]string{
			"agent":   agentType,
			"content": resp,
		})
	}()

	return nil
}

// createAgent instantiates the requested agent type.
func (a *App) createAgent(agentType string) (agents.Agent, error) {
	switch agentType {
	case "planner":
		var opts []agents.PlannerOption
		if a.vectorStore != nil {
			opts = append(opts, agents.WithVectorStore(a.vectorStore))
		}
		return agents.NewPlanner(a.llmClient, a.ctxBuilder, a.repoPaths, opts...), nil
	case "designer":
		return agents.NewDesigner(a.llmClient, a.ctxBuilder, a.vectorStore), nil
	case "reviewer":
		return agents.NewReviewer(a.llmClient, a.ctxBuilder, a.vectorStore), nil
	case "asker":
		return agents.NewAsker(a.llmClient, a.ctxBuilder, a.vectorStore, a.repoPaths...), nil
	default:
		return nil, fmt.Errorf("unknown agent type: %s", agentType)
	}
}
