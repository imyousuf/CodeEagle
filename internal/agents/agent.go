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
	verbose      bool
	log          func(format string, args ...any)
}

// Name returns the agent's name.
func (a *BaseAgent) Name() string { return a.name }

// SetVerbose enables or disables verbose logging on the agent.
// If logger is nil, a no-op logger is used.
func (a *BaseAgent) SetVerbose(verbose bool, logger func(format string, args ...any)) {
	a.verbose = verbose
	if logger != nil {
		a.log = logger
	} else {
		a.log = func(format string, args ...any) {}
	}
}

// ask builds messages from the system prompt, AI guidelines, context text,
// and user query, sends them to the LLM, and returns the response content.
// AI guideline files (CLAUDE.md, AGENTS.md, etc.) are automatically injected
// as context before the codebase context.
func (a *BaseAgent) ask(ctx context.Context, contextText, query string) (string, error) {
	if a.verbose && a.log != nil {
		a.log("Sending query to LLM (provider: %s)...", a.llmClient.Provider())
	}
	var messages []llm.Message

	// Auto-inject AI guidelines if available.
	if a.ctxBuilder != nil {
		guidelines, err := a.ctxBuilder.BuildGuidelineContext(ctx)
		if err == nil && guidelines != "" {
			messages = append(messages, llm.Message{
				Role:    llm.RoleUser,
				Content: "Here are the project's AI guidelines and conventions:\n\n" + guidelines,
			})
			messages = append(messages, llm.Message{
				Role:    llm.RoleAssistant,
				Content: "I've noted the project guidelines and will follow them in my analysis.",
			})
		}
	}

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
