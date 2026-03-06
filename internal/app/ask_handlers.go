//go:build app

package app

import (
	"context"
	"fmt"

	"github.com/imyousuf/CodeEagle/internal/agents"
	"github.com/imyousuf/CodeEagle/internal/vectorstore"
	"github.com/imyousuf/CodeEagle/pkg/llm"
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
// Opens all required resources, runs the agent, then closes everything.
// Emits Wails events: "agent:thinking", "agent:response", "agent:error".
func (a *App) AskAgent(agentType, query string) error {
	if query == "" {
		return fmt.Errorf("query cannot be empty")
	}

	go func() {
		// Emit thinking event.
		a.emit("agent:thinking", map[string]string{
			"agent": agentType,
		})

		// Serialize agent access.
		a.agentMu.Lock()
		defer a.agentMu.Unlock()

		// Open LLM.
		llmClient, closeLLM, err := a.openLLM()
		if err != nil {
			a.emit("agent:error", map[string]string{
				"agent": agentType,
				"error": fmt.Sprintf("LLM not available: %v", err),
			})
			return
		}
		defer closeLLM()

		// Open graph.
		gr, closeGraph, err := a.openGraph()
		if err != nil {
			a.emit("agent:error", map[string]string{
				"agent": agentType,
				"error": fmt.Sprintf("knowledge graph not available: %v — run 'codeeagle sync' first", err),
			})
			return
		}
		defer closeGraph()

		// Open vector (optional).
		vs, closeVector := a.openVector(gr.store, gr.branch)
		defer closeVector()

		agent, err := createAgent(agentType, llmClient, gr.ctxBuilder, vs, a.repoPaths)
		if err != nil {
			a.emit("agent:error", map[string]string{
				"agent": agentType,
				"error": err.Error(),
			})
			return
		}

		resp, err := agent.Ask(context.Background(), query)
		if err != nil {
			a.emit("agent:error", map[string]string{
				"agent": agentType,
				"error": err.Error(),
			})
			return
		}

		a.emit("agent:response", map[string]string{
			"agent":   agentType,
			"content": resp,
		})
	}()

	return nil
}

// createAgent instantiates the requested agent type with the given resources.
func createAgent(agentType string, llmClient llm.Client, ctxBuilder *agents.ContextBuilder, vs *vectorstore.VectorStore, repoPaths []string) (agents.Agent, error) {
	switch agentType {
	case "planner":
		var opts []agents.PlannerOption
		if vs != nil {
			opts = append(opts, agents.WithVectorStore(vs))
		}
		return agents.NewPlanner(llmClient, ctxBuilder, repoPaths, opts...), nil
	case "designer":
		return agents.NewDesigner(llmClient, ctxBuilder, vs), nil
	case "reviewer":
		return agents.NewReviewer(llmClient, ctxBuilder, vs), nil
	case "asker":
		return agents.NewAsker(llmClient, ctxBuilder, vs, repoPaths...), nil
	default:
		return nil, fmt.Errorf("unknown agent type: %s", agentType)
	}
}
