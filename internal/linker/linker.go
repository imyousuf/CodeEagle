// Package linker resolves cross-service relationships after indexing.
// It runs as a post-index phase, analyzing the whole graph to create
// edges between services, endpoints, and API calls.
package linker

import (
	"context"
	"fmt"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/pkg/llm"
)

// Linker resolves cross-service relationships in the knowledge graph.
type Linker struct {
	store     graph.Store
	llmClient llm.Client
	log       func(format string, args ...any)
	verbose   bool
}

// NewLinker creates a new Linker.
// The llmClient is optional; if nil, LLM-assisted analysis is skipped.
func NewLinker(store graph.Store, llmClient llm.Client, logFn func(format string, args ...any), verbose bool) *Linker {
	if logFn == nil {
		logFn = func(string, ...any) {}
	}
	return &Linker{
		store:     store,
		llmClient: llmClient,
		log:       logFn,
		verbose:   verbose,
	}
}

// Phase represents a named linker phase.
type Phase struct {
	Name string
	Fn   func(ctx context.Context) (int, error)
}

// Phases returns all linker phases in execution order (excluding LLM phases).
func (l *Linker) Phases() []Phase {
	return []Phase{
		{Name: "services", Fn: l.linkServices},
		{Name: "endpoints", Fn: l.linkEndpoints},
		{Name: "api_calls", Fn: l.linkAPICalls},
		{Name: "dependencies", Fn: l.linkDependencies},
		{Name: "imports", Fn: l.linkImports},
		{Name: "implements", Fn: l.linkImplements},
		{Name: "tests", Fn: l.linkTests},
	}
}

// NewPhases returns only the newly added phases (implements + tests).
func (l *Linker) NewPhases() []Phase {
	return []Phase{
		{Name: "implements", Fn: l.linkImplements},
		{Name: "tests", Fn: l.linkTests},
	}
}

// RunPhases executes the given phases in order and returns per-phase counts.
func (l *Linker) RunPhases(ctx context.Context, phases []Phase) (map[string]int, error) {
	results := make(map[string]int, len(phases))
	for _, phase := range phases {
		count, err := phase.Fn(ctx)
		if err != nil {
			return results, fmt.Errorf("phase %s: %w", phase.Name, err)
		}
		results[phase.Name] = count
		if l.verbose {
			l.log("  Phase %s: linked %d", phase.Name, count)
		}
	}
	return results, nil
}

// RunAll executes all linking phases in order.
func (l *Linker) RunAll(ctx context.Context) error {
	if l.verbose {
		l.log("Running cross-service linker...")
	}

	// 1. Detect services and create service → file edges.
	serviceCount, err := l.linkServices(ctx)
	if err != nil {
		return fmt.Errorf("link services: %w", err)
	}
	if l.verbose {
		l.log("  Linked %d services", serviceCount)
	}

	// 2. Link endpoints to their containing services.
	endpointCount, err := l.linkEndpoints(ctx)
	if err != nil {
		return fmt.Errorf("link endpoints: %w", err)
	}
	if l.verbose {
		l.log("  Linked %d endpoints to services", endpointCount)
	}

	// 3. Resolve API calls to endpoints.
	callCount, err := l.linkAPICalls(ctx)
	if err != nil {
		return fmt.Errorf("link API calls: %w", err)
	}
	if l.verbose {
		l.log("  Resolved %d API calls to endpoints", callCount)
	}

	// 4. Resolve library dependencies between services.
	depCount, err := l.linkDependencies(ctx)
	if err != nil {
		return fmt.Errorf("link dependencies: %w", err)
	}
	if l.verbose {
		l.log("  Resolved %d cross-service dependencies", depCount)
	}

	// 4.5. Link import statements to manifest dependencies.
	importCount, err := l.linkImports(ctx)
	if err != nil {
		return fmt.Errorf("link imports: %w", err)
	}
	if l.verbose {
		l.log("  Linked %d imports to manifest dependencies", importCount)
	}

	// 4.6. Resolve cross-file implements relationships.
	implCount, err := l.linkImplements(ctx)
	if err != nil {
		return fmt.Errorf("link implements: %w", err)
	}
	if l.verbose {
		l.log("  Linked %d cross-file implements", implCount)
	}

	// 4.7. Link test files/functions to source entities.
	testCount, err := l.linkTests(ctx)
	if err != nil {
		return fmt.Errorf("link tests: %w", err)
	}
	if l.verbose {
		l.log("  Linked %d test coverage edges", testCount)
	}

	// 5. LLM-assisted analysis for unresolved calls (optional).
	if l.llmClient != nil {
		llmCount, err := l.llmAnalyzeUnresolvedCalls(ctx)
		if err != nil {
			if l.verbose {
				l.log("  Warning: LLM call analysis: %v", err)
			}
		} else if l.verbose {
			l.log("  LLM resolved %d additional API calls", llmCount)
		}

		eventCount, err := l.llmAnalyzeEventDriven(ctx)
		if err != nil {
			if l.verbose {
				l.log("  Warning: LLM event analysis: %v", err)
			}
		} else if l.verbose {
			l.log("  LLM resolved %d event-driven dependencies", eventCount)
		}
	}

	if l.verbose {
		l.log("Cross-service linker complete.")
	}

	return nil
}
