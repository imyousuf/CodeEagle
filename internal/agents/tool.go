package agents

import (
	"context"
	"fmt"
	"sync"

	"github.com/imyousuf/CodeEagle/pkg/llm"
)

// Tool is the interface for tools that can be invoked by LLM agents.
type Tool interface {
	// Name returns the tool's unique name.
	Name() string
	// Description returns a human-readable description of the tool.
	Description() string
	// Parameters returns the JSON Schema describing the tool's input.
	Parameters() map[string]any
	// Execute runs the tool with the given arguments.
	// Returns the result text and whether the tool call was successful.
	Execute(ctx context.Context, args map[string]any) (string, bool)
}

// Registry manages a collection of tools.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
	order []string
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry.
func (r *Registry) Register(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := tool.Name()
	if _, exists := r.tools[name]; !exists {
		r.order = append(r.order, name)
	}
	r.tools[name] = tool
}

// Get retrieves a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Definitions returns LLM tool definitions for all registered tools,
// in registration order.
func (r *Registry) Definitions() []llm.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]llm.Tool, 0, len(r.order))
	for _, name := range r.order {
		t := r.tools[name]
		defs = append(defs, ToLLMTool(t))
	}
	return defs
}

// Execute runs the named tool with the given arguments.
// Returns the result text, success flag, and an error if the tool was not found.
func (r *Registry) Execute(ctx context.Context, name string, args map[string]any) (string, bool, error) {
	r.mu.RLock()
	t, ok := r.tools[name]
	r.mu.RUnlock()
	if !ok {
		return "", false, fmt.Errorf("unknown tool: %s", name)
	}
	result, success := t.Execute(ctx, args)
	return result, success, nil
}

// ToLLMTool converts an agents.Tool to an llm.Tool definition.
func ToLLMTool(t Tool) llm.Tool {
	return llm.Tool{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters:  t.Parameters(),
	}
}

// ToLLMTools converts a slice of agents.Tool to llm.Tool definitions.
func ToLLMTools(tools []Tool) []llm.Tool {
	defs := make([]llm.Tool, len(tools))
	for i, t := range tools {
		defs[i] = ToLLMTool(t)
	}
	return defs
}
