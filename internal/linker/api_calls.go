package linker

import (
	"context"
	"regexp"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// linkAPICalls matches NodeDependency nodes with kind=api_call to
// NodeAPIEndpoint nodes, creating EdgeConsumes edges and service-level
// EdgeDependsOn edges.
func (l *Linker) linkAPICalls(ctx context.Context) (int, error) {
	// Query all API call dependency nodes.
	apiCalls, err := l.store.QueryNodes(ctx, graph.NodeFilter{
		Type:       graph.NodeDependency,
		Properties: map[string]string{"kind": "api_call"},
	})
	if err != nil {
		return 0, err
	}
	if len(apiCalls) == 0 {
		return 0, nil
	}

	// Query all API endpoint nodes.
	endpoints, err := l.store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeAPIEndpoint})
	if err != nil {
		return 0, err
	}
	if len(endpoints) == 0 {
		return 0, nil
	}

	// Build endpoint index: normalized path → endpoint node.
	// An endpoint might have a full_path (resolved with prefix) or just path.
	endpointIndex := make(map[string]*graph.Node)
	for _, ep := range endpoints {
		fullPath := ep.Properties["full_path"]
		if fullPath == "" {
			fullPath = ep.Properties["path"]
		}
		if fullPath == "" {
			continue
		}
		normalized := normalizeURLPath(fullPath)
		endpointIndex[normalized] = ep
	}

	// Query services for service-level edge creation.
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

	// Track service-level edges to avoid duplicates.
	serviceDeps := make(map[string]bool)
	resolved := 0

	for _, call := range apiCalls {
		callPath := call.Properties["path"]
		if callPath == "" {
			continue
		}

		normalized := normalizeURLPath(callPath)
		ep := matchEndpoint(normalized, endpointIndex)
		if ep == nil {
			continue
		}

		// Create EdgeConsumes from the calling dependency → endpoint.
		consumeEdge := &graph.Edge{
			ID:       graph.NewNodeID(string(graph.EdgeConsumes), call.ID, ep.ID),
			Type:     graph.EdgeConsumes,
			SourceID: call.ID,
			TargetID: ep.ID,
			Properties: map[string]string{
				"resolved": "true",
			},
		}
		if err := l.store.AddEdge(ctx, consumeEdge); err != nil {
			continue
		}

		// Create service-level EdgeDependsOn if both sides have services.
		callerGroup := topDir(call.FilePath)
		callerSvc := serviceByGroup[callerGroup]
		endpointGroup := topDir(ep.FilePath)
		endpointSvc := serviceByGroup[endpointGroup]

		if callerSvc != nil && endpointSvc != nil && callerSvc.ID != endpointSvc.ID {
			depKey := callerSvc.ID + "→" + endpointSvc.ID
			if !serviceDeps[depKey] {
				depEdge := &graph.Edge{
					ID:       graph.NewNodeID(string(graph.EdgeDependsOn), callerSvc.ID, endpointSvc.ID),
					Type:     graph.EdgeDependsOn,
					SourceID: callerSvc.ID,
					TargetID: endpointSvc.ID,
					Properties: map[string]string{
						"kind": "api_dependency",
					},
				}
				if err := l.store.AddEdge(ctx, depEdge); err == nil {
					serviceDeps[depKey] = true
				}
			}
		}

		resolved++
	}

	return resolved, nil
}

// paramPattern matches URL path parameters like {id}, :id, <id>.
var paramPattern = regexp.MustCompile(`\{[^}]+\}|:[a-zA-Z_][a-zA-Z0-9_]*|<[^>]+>`)

// normalizeURLPath normalizes a URL path for matching:
// - Lowercase
// - Strip trailing slash
// - Replace path parameters ({id}, :id, <id>) with *
func normalizeURLPath(p string) string {
	p = strings.ToLower(p)
	p = strings.TrimRight(p, "/")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	// Replace path parameters with wildcard.
	p = paramPattern.ReplaceAllString(p, "*")
	return p
}

// matchEndpoint tries to match a normalized call path to an endpoint.
// First tries exact match, then suffix matching (for API gateway prefixes).
func matchEndpoint(callPath string, index map[string]*graph.Node) *graph.Node {
	// Exact match.
	if ep, ok := index[callPath]; ok {
		return ep
	}

	// Suffix match: the call path might have an extra prefix (e.g., /backend/api/v1/...)
	// while the endpoint is /api/v1/...
	for epPath, ep := range index {
		if strings.HasSuffix(callPath, epPath) {
			return ep
		}
		if strings.HasSuffix(epPath, callPath) {
			return ep
		}
	}

	// Wildcard-aware match: compare segments, treating * as wildcard.
	callSegments := strings.Split(callPath, "/")
	for epPath, ep := range index {
		epSegments := strings.Split(epPath, "/")
		if matchSegments(callSegments, epSegments) {
			return ep
		}
	}

	return nil
}

// matchSegments checks whether two URL segment slices match, treating *
// in either side as a wildcard that matches any single segment.
func matchSegments(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] == "*" || b[i] == "*" {
			continue
		}
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
