package linker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/pkg/llm"
)

const llmAnalyzerPrompt = `You are a code dependency analyzer. You analyze source code to identify which API endpoints are being called, even when the URLs are dynamically constructed.

You will be given:
1. A function's source code that makes HTTP calls with dynamic URLs
2. A list of available API endpoints in the codebase

Your task: determine which endpoints the function is likely calling based on code context (variable names, comments, surrounding code patterns).

Respond with a JSON array of matches. Each match should have:
- "endpoint_path": the path of the matched endpoint
- "confidence": "high", "medium", or "low"
- "reason": brief explanation

Only include matches with medium or high confidence. If no matches are likely, return an empty array [].`

const eventBusPrompt = `You are a code dependency analyzer specializing in event-driven architectures.

You will be given:
1. A list of functions that publish events (event producers)
2. A list of functions that subscribe to events (event consumers)
3. Available event names/topics in the codebase

Your task: match producers to consumers based on event names, topic patterns, and code context.

Respond with a JSON array of matches. Each match should have:
- "producer": the producer function identifier
- "consumer": the consumer function identifier
- "event": the event name/topic
- "confidence": "high", "medium", or "low"

Only include matches with medium or high confidence. If no matches are likely, return an empty array [].`

// llmMatch represents a single LLM-inferred endpoint match.
type llmMatch struct {
	EndpointPath string `json:"endpoint_path"`
	Confidence   string `json:"confidence"`
	Reason       string `json:"reason"`
}

// eventMatch represents a single LLM-inferred event bus match.
type eventMatch struct {
	Producer   string `json:"producer"`
	Consumer   string `json:"consumer"`
	Event      string `json:"event"`
	Confidence string `json:"confidence"`
}

// llmAnalyzeUnresolvedCalls uses the LLM to resolve API calls that couldn't
// be matched by static analysis. It batches calls per service and sends
// them to the LLM with available endpoint context.
func (l *Linker) llmAnalyzeUnresolvedCalls(ctx context.Context) (int, error) {
	if l.llmClient == nil {
		return 0, nil
	}

	// Query unresolved API call dependencies (those without EdgeConsumes).
	apiCalls, err := l.store.QueryNodes(ctx, graph.NodeFilter{
		Type:       graph.NodeDependency,
		Properties: map[string]string{"kind": "api_call"},
	})
	if err != nil {
		return 0, err
	}

	// Query all endpoints for the available endpoint list.
	endpoints, err := l.store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeAPIEndpoint})
	if err != nil {
		return 0, err
	}
	if len(endpoints) == 0 {
		return 0, nil
	}

	// Filter to only unresolved calls (those without a resolved EdgeConsumes).
	unresolved := l.filterUnresolvedCalls(ctx, apiCalls)
	if len(unresolved) == 0 {
		return 0, nil
	}

	// Build endpoint list for the prompt.
	var epList strings.Builder
	for _, ep := range endpoints {
		method := ep.Properties["http_method"]
		path := ep.Properties["full_path"]
		if path == "" {
			path = ep.Properties["path"]
		}
		framework := ep.Properties["framework"]
		svc := topDir(ep.FilePath)
		fmt.Fprintf(&epList, "- %s %s (service: %s, framework: %s)\n", method, path, svc, framework)
	}

	// Group unresolved calls by service for batched LLM requests.
	byService := make(map[string][]*graph.Node)
	for _, call := range unresolved {
		svc := topDir(call.FilePath)
		byService[svc] = append(byService[svc], call)
	}

	// Query services for edge creation.
	services, err := l.store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeService})
	if err != nil {
		return 0, err
	}
	serviceByGroup := make(map[string]*graph.Node)
	for _, svc := range services {
		group := topDir(svc.FilePath)
		if group == "" {
			group = svc.Name
		}
		serviceByGroup[group] = svc
	}

	// Build endpoint index for creating edges.
	endpointByPath := make(map[string]*graph.Node)
	for _, ep := range endpoints {
		path := ep.Properties["full_path"]
		if path == "" {
			path = ep.Properties["path"]
		}
		if path != "" {
			endpointByPath[normalizeURLPath(path)] = ep
		}
	}

	resolved := 0
	for svc, calls := range byService {
		// Build the call descriptions for this service batch.
		var callDesc strings.Builder
		for _, call := range calls {
			method := call.Properties["http_method"]
			if method == "" {
				method = "UNKNOWN"
			}
			path := call.Properties["path"]
			fmt.Fprintf(&callDesc, "- %s call to %q in file %s (function context: %s)\n",
				method, path, call.FilePath, call.Name)
		}

		userMsg := fmt.Sprintf(
			"Service: %s\n\nUnresolved HTTP calls:\n%s\nAvailable API endpoints:\n%s\nWhich endpoints are these calls targeting?",
			svc, callDesc.String(), epList.String(),
		)

		resp, err := l.llmClient.Chat(ctx, llmAnalyzerPrompt, []llm.Message{
			{Role: llm.RoleUser, Content: userMsg},
		})
		if err != nil {
			if l.verbose {
				l.log("  LLM analyzer error for service %s: %v", svc, err)
			}
			continue
		}

		// Parse LLM response.
		matches := parseLLMMatches(resp.Content)
		for _, m := range matches {
			if m.Confidence == "low" {
				continue
			}

			normalizedPath := normalizeURLPath(m.EndpointPath)
			ep := endpointByPath[normalizedPath]
			if ep == nil {
				continue
			}

			// Find the calling node that best matches.
			var caller *graph.Node
			for _, call := range calls {
				callPath := normalizeURLPath(call.Properties["path"])
				if matchSegments(strings.Split(callPath, "/"), strings.Split(normalizedPath, "/")) {
					caller = call
					break
				}
			}
			if caller == nil && len(calls) > 0 {
				caller = calls[0]
			}
			if caller == nil {
				continue
			}

			// Create EdgeConsumes with LLM inference metadata.
			edge := &graph.Edge{
				ID:       graph.NewNodeID("llm_"+string(graph.EdgeConsumes), caller.ID, ep.ID),
				Type:     graph.EdgeConsumes,
				SourceID: caller.ID,
				TargetID: ep.ID,
				Properties: map[string]string{
					"inferred":   "true",
					"confidence": m.Confidence,
					"method":     "llm_analysis",
					"reason":     m.Reason,
				},
			}
			if err := l.store.AddEdge(ctx, edge); err != nil {
				continue
			}

			// Create service-level edge.
			callerSvc := serviceByGroup[topDir(caller.FilePath)]
			epSvc := serviceByGroup[topDir(ep.FilePath)]
			if callerSvc != nil && epSvc != nil && callerSvc.ID != epSvc.ID {
				svcEdge := &graph.Edge{
					ID:       graph.NewNodeID("llm_"+string(graph.EdgeDependsOn), callerSvc.ID, epSvc.ID),
					Type:     graph.EdgeDependsOn,
					SourceID: callerSvc.ID,
					TargetID: epSvc.ID,
					Properties: map[string]string{
						"kind":       "api_dependency",
						"inferred":   "true",
						"confidence": m.Confidence,
						"method":     "llm_analysis",
					},
				}
				_ = l.store.AddEdge(ctx, svcEdge)
			}

			resolved++
		}
	}

	return resolved, nil
}

// llmAnalyzeEventDriven uses the LLM to detect publish/subscribe patterns
// and create dependency edges between event producers and consumers.
func (l *Linker) llmAnalyzeEventDriven(ctx context.Context) (int, error) {
	if l.llmClient == nil {
		return 0, nil
	}

	// Look for event-bus-related nodes by searching for known patterns.
	// Event producers: functions that call publish/emit/send methods.
	// Event consumers: functions with subscribe/on/handle patterns.
	allFuncs, err := l.store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeFunction})
	if err != nil {
		return 0, err
	}

	var producers, consumers []string
	for _, fn := range allFuncs {
		name := strings.ToLower(fn.Name)
		sig := strings.ToLower(fn.Signature)
		// Detect event-related patterns from function names and signatures.
		if containsAny(name, "publish", "emit", "send_event", "dispatch", "fire") ||
			containsAny(sig, "publish", "emit", "send_event", "dispatch") {
			producers = append(producers, fmt.Sprintf("- %s in %s (service: %s)", fn.QualifiedName, fn.FilePath, topDir(fn.FilePath)))
		}
		if containsAny(name, "subscribe", "on_event", "handle_event", "consume", "listener") ||
			containsAny(sig, "subscribe", "on_event", "handle_event", "consumer") {
			consumers = append(consumers, fmt.Sprintf("- %s in %s (service: %s)", fn.QualifiedName, fn.FilePath, topDir(fn.FilePath)))
		}
	}

	if len(producers) == 0 || len(consumers) == 0 {
		return 0, nil
	}

	userMsg := fmt.Sprintf(
		"Event producers:\n%s\n\nEvent consumers:\n%s\n\nMatch producers to consumers based on event patterns.",
		strings.Join(producers, "\n"), strings.Join(consumers, "\n"),
	)

	resp, err := l.llmClient.Chat(ctx, eventBusPrompt, []llm.Message{
		{Role: llm.RoleUser, Content: userMsg},
	})
	if err != nil {
		if l.verbose {
			l.log("  LLM event analysis error: %v", err)
		}
		return 0, nil
	}

	matches := parseEventMatches(resp.Content)
	resolved := 0

	// Build function index for looking up by qualified name.
	funcByQName := make(map[string]*graph.Node)
	for _, fn := range allFuncs {
		funcByQName[fn.QualifiedName] = fn
	}

	services, err := l.store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeService})
	if err != nil {
		return 0, err
	}
	serviceByGroup := make(map[string]*graph.Node)
	for _, svc := range services {
		group := topDir(svc.FilePath)
		if group == "" {
			group = svc.Name
		}
		serviceByGroup[group] = svc
	}

	for _, m := range matches {
		if m.Confidence == "low" {
			continue
		}

		producerNode := funcByQName[m.Producer]
		consumerNode := funcByQName[m.Consumer]
		if producerNode == nil || consumerNode == nil {
			continue
		}

		// Create EdgeCalls from producer → consumer (event-driven).
		edge := &graph.Edge{
			ID:       graph.NewNodeID("event_"+string(graph.EdgeCalls), producerNode.ID, consumerNode.ID),
			Type:     graph.EdgeCalls,
			SourceID: producerNode.ID,
			TargetID: consumerNode.ID,
			Properties: map[string]string{
				"kind":       "event_driven",
				"event":      m.Event,
				"inferred":   "true",
				"confidence": m.Confidence,
				"method":     "llm_analysis",
			},
		}
		if err := l.store.AddEdge(ctx, edge); err != nil {
			continue
		}

		// Create service-level edge.
		prodSvc := serviceByGroup[topDir(producerNode.FilePath)]
		consSvc := serviceByGroup[topDir(consumerNode.FilePath)]
		if prodSvc != nil && consSvc != nil && prodSvc.ID != consSvc.ID {
			svcEdge := &graph.Edge{
				ID:       graph.NewNodeID("event_"+string(graph.EdgeDependsOn), consSvc.ID, prodSvc.ID),
				Type:     graph.EdgeDependsOn,
				SourceID: consSvc.ID,
				TargetID: prodSvc.ID,
				Properties: map[string]string{
					"kind":       "event_dependency",
					"event":      m.Event,
					"inferred":   "true",
					"confidence": m.Confidence,
					"method":     "llm_analysis",
				},
			}
			_ = l.store.AddEdge(ctx, svcEdge)
		}

		resolved++
	}

	return resolved, nil
}

// filterUnresolvedCalls returns API call nodes that don't have a
// resolved EdgeConsumes edge yet.
func (l *Linker) filterUnresolvedCalls(ctx context.Context, calls []*graph.Node) []*graph.Node {
	var unresolved []*graph.Node
	for _, call := range calls {
		edges, err := l.store.GetEdges(ctx, call.ID, graph.EdgeConsumes)
		if err != nil || len(edges) == 0 {
			unresolved = append(unresolved, call)
		}
	}
	return unresolved
}

// parseLLMMatches extracts the JSON array of matches from the LLM response.
func parseLLMMatches(content string) []llmMatch {
	// Try to find JSON array in the response.
	jsonStr := extractJSON(content)
	if jsonStr == "" {
		return nil
	}
	var matches []llmMatch
	if err := json.Unmarshal([]byte(jsonStr), &matches); err != nil {
		return nil
	}
	return matches
}

// parseEventMatches extracts event match results from LLM response.
func parseEventMatches(content string) []eventMatch {
	jsonStr := extractJSON(content)
	if jsonStr == "" {
		return nil
	}
	var matches []eventMatch
	if err := json.Unmarshal([]byte(jsonStr), &matches); err != nil {
		return nil
	}
	return matches
}

// extractJSON finds the first JSON array in a string.
func extractJSON(s string) string {
	start := strings.Index(s, "[")
	if start == -1 {
		return ""
	}
	// Find matching closing bracket.
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// containsAny checks if s contains any of the given substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
