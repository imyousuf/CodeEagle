package linker

import (
	"context"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// linkDependencies resolves manifest dependencies between services.
// When service A depends on package "llm-framework" and service B declares
// its package name as "llm-framework", we create EdgeDependsOn from A → B.
func (l *Linker) linkDependencies(ctx context.Context) (int, error) {
	// Query all manifest dependency nodes.
	deps, err := l.store.QueryNodes(ctx, graph.NodeFilter{
		Type:       graph.NodeDependency,
		Properties: map[string]string{"kind": "manifest_dep"},
	})
	if err != nil {
		return 0, err
	}
	if len(deps) == 0 {
		return 0, nil
	}

	// Query all services and build a map: package_name → service.
	services, err := l.store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeService})
	if err != nil {
		return 0, err
	}

	// Map service names/package names to service nodes.
	serviceByName := make(map[string]*graph.Node)
	serviceByGroup := make(map[string]*graph.Node)
	for _, svc := range services {
		serviceByName[svc.Name] = svc
		group := topDir(svc.FilePath)
		if group == "" {
			group = svc.Name
		}
		serviceByGroup[group] = svc
		// Also index by properties like go_module.
		if mod, ok := svc.Properties["go_module"]; ok {
			serviceByName[mod] = svc
		}
	}

	// Track service-level edges to avoid duplicates.
	serviceDeps := make(map[string]bool)
	resolved := 0

	for _, dep := range deps {
		depName := dep.Name
		if depName == "" {
			continue
		}

		// Find the consuming service (where this dependency is declared).
		consumerGroup := topDir(dep.FilePath)
		consumerSvc := serviceByGroup[consumerGroup]
		if consumerSvc == nil {
			continue
		}

		// Check if the dependency name matches any local service.
		providerSvc := serviceByName[depName]
		if providerSvc == nil {
			continue
		}

		// Don't create self-dependency edges.
		if consumerSvc.ID == providerSvc.ID {
			continue
		}

		depKey := consumerSvc.ID + "→" + providerSvc.ID
		if serviceDeps[depKey] {
			continue
		}

		edge := &graph.Edge{
			ID:       graph.NewNodeID(string(graph.EdgeDependsOn), consumerSvc.ID, providerSvc.ID),
			Type:     graph.EdgeDependsOn,
			SourceID: consumerSvc.ID,
			TargetID: providerSvc.ID,
			Properties: map[string]string{
				"kind":    "library_dependency",
				"dep":     depName,
				"version": dep.Properties["version"],
			},
		}
		if err := l.store.AddEdge(ctx, edge); err != nil {
			continue
		}

		serviceDeps[depKey] = true
		resolved++
	}

	// Version conflict detection.
	l.detectVersionConflicts(deps)

	return resolved, nil
}

// detectVersionConflicts checks for the same dependency used by
// different services with different versions and logs warnings.
func (l *Linker) detectVersionConflicts(deps []*graph.Node) {
	if !l.verbose {
		return
	}

	// Group by dep name → list of (service group, version).
	type depVersion struct {
		group   string
		version string
	}
	byName := make(map[string][]depVersion)
	for _, dep := range deps {
		version := dep.Properties["version"]
		if version == "" {
			continue
		}
		group := topDir(dep.FilePath)
		byName[dep.Name] = append(byName[dep.Name], depVersion{group, version})
	}

	for name, versions := range byName {
		if len(versions) <= 1 {
			continue
		}
		// Check for conflicting versions.
		seen := make(map[string]string) // version → group
		for _, dv := range versions {
			if prevGroup, ok := seen[dv.version]; ok {
				_ = prevGroup // same version across services, fine
				continue
			}
			seen[dv.version] = dv.group
		}
		if len(seen) > 1 {
			l.log("  Version conflict: %s has different versions across services:", name)
			for version, group := range seen {
				l.log("    %s: %s", group, version)
			}
		}
	}
}
