package linker

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// linkEndpoints links API endpoint nodes to their containing services
// and resolves full paths using router mount prefixes.
func (l *Linker) linkEndpoints(ctx context.Context) (int, error) {
	// Query all APIEndpoint nodes.
	endpoints, err := l.store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeAPIEndpoint})
	if err != nil {
		return 0, err
	}
	if len(endpoints) == 0 {
		return 0, nil
	}

	// Query router mount nodes (from Python include_router detection).
	routerMounts, err := l.store.QueryNodes(ctx, graph.NodeFilter{
		Type:       graph.NodeVariable,
		Properties: map[string]string{"kind": "router_mount"},
	})
	if err != nil {
		return 0, err
	}

	// Build prefix map: file directory → prefix.
	// Router mounts are defined in the same file as the include_router call.
	prefixByDir := make(map[string]string)
	for _, rm := range routerMounts {
		dir := filepath.Dir(filepath.ToSlash(rm.FilePath))
		if p, ok := rm.Properties["prefix"]; ok {
			prefixByDir[dir] = p
		}
	}

	// Query all services for lookup by top-level directory.
	services, err := l.store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeService})
	if err != nil {
		return 0, err
	}
	serviceByGroup := make(map[string]*graph.Node)
	for _, svc := range services {
		group := topDir(svc.FilePath)
		if group == "" {
			// Auto-detected services have no FilePath; use Name as group.
			group = svc.Name
		}
		serviceByGroup[group] = svc
	}

	linked := 0
	for _, ep := range endpoints {
		// Resolve full path by checking for a prefix in the same directory tree.
		path := ep.Properties["path"]
		if path != "" {
			fullPath := resolveFullPath(ep.FilePath, path, prefixByDir)
			if fullPath != path {
				if ep.Properties == nil {
					ep.Properties = make(map[string]string)
				}
				ep.Properties["full_path"] = fullPath
				_ = l.store.UpdateNode(ctx, ep)
			}
		}

		// Find the containing service based on file path.
		group := topDir(ep.FilePath)
		svc, ok := serviceByGroup[group]
		if !ok {
			continue
		}

		// Create EdgeExposes from service → endpoint.
		edge := &graph.Edge{
			ID:       graph.NewNodeID(string(graph.EdgeExposes), svc.ID, ep.ID),
			Type:     graph.EdgeExposes,
			SourceID: svc.ID,
			TargetID: ep.ID,
		}
		if err := l.store.AddEdge(ctx, edge); err != nil {
			// Ignore duplicate edge errors.
			continue
		}
		linked++
	}

	return linked, nil
}

// resolveFullPath resolves a route path to its full form by prepending
// any router mount prefix from the same directory or an ancestor directory.
func resolveFullPath(filePath, routePath string, prefixByDir map[string]string) string {
	dir := filepath.Dir(filepath.ToSlash(filePath))

	// Walk up directory tree looking for a matching prefix.
	for dir != "" && dir != "." {
		if prefix, ok := prefixByDir[dir]; ok {
			return normalizePath(prefix + routePath)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return routePath
}

// normalizePath cleans a URL path by collapsing double slashes
// and ensuring a leading slash.
func normalizePath(p string) string {
	// Collapse double slashes.
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}
