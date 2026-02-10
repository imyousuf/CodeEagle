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
