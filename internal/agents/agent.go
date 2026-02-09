package agents

import (
	"context"
	"fmt"

	"github.com/imyousuf/CodeEagle/pkg/llm"
)

// Agent is the interface for all CodeEagle AI agents.
type Agent interface {
	// Name returns the agent's name.
	Name() string
	// Ask sends a query to the agent and returns the response.
	Ask(ctx context.Context, query string) (string, error)
}

// BaseAgent provides shared functionality for all agents.
type BaseAgent struct {
	name         string
	llmClient    llm.Client
	ctxBuilder   *ContextBuilder
	systemPrompt string
}

// Name returns the agent's name.
func (a *BaseAgent) Name() string { return a.name }

// ask builds messages from the system prompt, context text, and user query,
// sends them to the LLM, and returns the response content.
func (a *BaseAgent) ask(ctx context.Context, contextText, query string) (string, error) {
	var messages []llm.Message

	if contextText != "" {
		messages = append(messages, llm.Message{
			Role:    llm.RoleUser,
			Content: "Here is the relevant codebase context:\n\n" + contextText,
		})
		messages = append(messages, llm.Message{
			Role:    llm.RoleAssistant,
			Content: "Thank you for providing the codebase context. I will use this to answer your question.",
		})
	}

	messages = append(messages, llm.Message{
		Role:    llm.RoleUser,
		Content: query,
	})

	resp, err := a.llmClient.Chat(ctx, a.systemPrompt, messages)
	if err != nil {
		return "", fmt.Errorf("LLM chat failed: %w", err)
	}

	return resp.Content, nil
}
