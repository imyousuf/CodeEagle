package linker

import (
	"context"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// linkImplements resolves cross-file implements relationships.
// It handles three language families:
//   - Go (structural typing): checks if struct method sets satisfy interface method sets
//   - Java/TypeScript (nominal typing): resolves Properties["implements"] to Interface nodes
//   - Python Protocol: resolves classes that inherit from Protocol interfaces
func (l *Linker) linkImplements(ctx context.Context) (int, error) {
	// Build set of existing Implements edges to avoid duplicates.
	existing := make(map[string]bool) // "sourceID→targetID"

	linked := 0

	// --- Go structural typing ---
	goLinked, err := l.linkGoImplements(ctx, existing)
	if err != nil {
		return linked, err
	}
	linked += goLinked

	// --- Java / TypeScript nominal typing ---
	nomLinked, err := l.linkNominalImplements(ctx, existing)
	if err != nil {
		return linked, err
	}
	linked += nomLinked

	// --- Python Protocol ---
	pyLinked, err := l.linkPythonProtocol(ctx, existing)
	if err != nil {
		return linked, err
	}
	linked += pyLinked

	return linked, nil
}

// linkGoImplements checks if Go structs satisfy Go interfaces using structural typing.
func (l *Linker) linkGoImplements(ctx context.Context, existing map[string]bool) (int, error) {
	// Query all Go interfaces.
	interfaces, err := l.store.QueryNodes(ctx, graph.NodeFilter{
		Type:     graph.NodeInterface,
		Language: "go",
	})
	if err != nil {
		return 0, err
	}
	if len(interfaces) == 0 {
		return 0, nil
	}

	// Parse interface method sets: interfaceID -> set of method names.
	type ifaceInfo struct {
		node    *graph.Node
		methods map[string]bool
	}
	var ifaceInfos []ifaceInfo
	for _, iface := range interfaces {
		methods := make(map[string]bool)
		if iface.Properties != nil && iface.Properties["methods"] != "" {
			for _, m := range strings.Split(iface.Properties["methods"], ",") {
				m = strings.TrimSpace(m)
				if m != "" {
					methods[m] = true
				}
			}
		}
		if len(methods) == 0 {
			continue
		}
		ifaceInfos = append(ifaceInfos, ifaceInfo{node: iface, methods: methods})
	}
	if len(ifaceInfos) == 0 {
		return 0, nil
	}

	// Query all Go structs.
	structs, err := l.store.QueryNodes(ctx, graph.NodeFilter{
		Type:     graph.NodeStruct,
		Language: "go",
	})
	if err != nil {
		return 0, err
	}

	// For each struct, find its methods.
	linked := 0
	for _, s := range structs {
		// Get methods where receiver matches struct name.
		methods, err := l.store.QueryNodes(ctx, graph.NodeFilter{
			Type:       graph.NodeMethod,
			Language:   "go",
			Properties: map[string]string{"receiver": s.Name},
		})
		if err != nil {
			continue
		}

		structMethodNames := make(map[string]bool)
		for _, m := range methods {
			structMethodNames[m.Name] = true
		}

		// Check against each interface.
		for _, iface := range ifaceInfos {
			// Skip same-file matches (parser already handles those).
			if iface.node.FilePath == s.FilePath {
				continue
			}

			// Check structural compatibility: struct must have all interface methods.
			satisfied := true
			for methodName := range iface.methods {
				if !structMethodNames[methodName] {
					satisfied = false
					break
				}
			}
			if !satisfied {
				continue
			}

			edgeKey := s.ID + "→" + iface.node.ID
			if existing[edgeKey] {
				continue
			}

			edge := &graph.Edge{
				ID:       graph.NewNodeID(string(graph.EdgeImplements), s.ID, iface.node.ID),
				Type:     graph.EdgeImplements,
				SourceID: s.ID,
				TargetID: iface.node.ID,
				Properties: map[string]string{
					"kind": "structural",
				},
			}
			if err := l.store.AddEdge(ctx, edge); err != nil {
				continue
			}
			existing[edgeKey] = true
			linked++

			if l.verbose {
				l.log("    Go implements: %s -> %s", s.Name, iface.node.Name)
			}
		}
	}

	return linked, nil
}

// linkNominalImplements resolves nominal implements relationships for Java and TypeScript.
func (l *Linker) linkNominalImplements(ctx context.Context, existing map[string]bool) (int, error) {
	// Query all classes with "implements" property.
	classes, err := l.store.QueryNodes(ctx, graph.NodeFilter{
		Type: graph.NodeClass,
	})
	if err != nil {
		return 0, err
	}

	// Build interface index: name -> list of interface nodes.
	allInterfaces, err := l.store.QueryNodes(ctx, graph.NodeFilter{
		Type: graph.NodeInterface,
	})
	if err != nil {
		return 0, err
	}

	ifaceByName := make(map[string][]*graph.Node)
	for _, iface := range allInterfaces {
		ifaceByName[iface.Name] = append(ifaceByName[iface.Name], iface)
	}

	linked := 0
	for _, cls := range classes {
		if cls.Properties == nil {
			continue
		}
		implStr := cls.Properties["implements"]
		if implStr == "" {
			continue
		}

		for _, ifaceName := range strings.Split(implStr, ",") {
			ifaceName = strings.TrimSpace(ifaceName)
			if ifaceName == "" {
				continue
			}

			candidates := ifaceByName[ifaceName]
			if len(candidates) == 0 {
				continue
			}

			// Prefer same-package match.
			target := bestMatch(cls, candidates)
			if target == nil {
				continue
			}

			edgeKey := cls.ID + "→" + target.ID
			if existing[edgeKey] {
				continue
			}

			edge := &graph.Edge{
				ID:       graph.NewNodeID(string(graph.EdgeImplements), cls.ID, target.ID),
				Type:     graph.EdgeImplements,
				SourceID: cls.ID,
				TargetID: target.ID,
				Properties: map[string]string{
					"kind": "nominal",
				},
			}
			if err := l.store.AddEdge(ctx, edge); err != nil {
				continue
			}
			existing[edgeKey] = true
			linked++

			if l.verbose {
				l.log("    Nominal implements: %s -> %s", cls.Name, target.Name)
			}
		}
	}

	return linked, nil
}

// linkPythonProtocol resolves Python Protocol interface implementations.
func (l *Linker) linkPythonProtocol(ctx context.Context, existing map[string]bool) (int, error) {
	// Query Protocol interfaces (NodeInterface with protocol=true).
	protocols, err := l.store.QueryNodes(ctx, graph.NodeFilter{
		Type:       graph.NodeInterface,
		Language:   "python",
		Properties: map[string]string{"protocol": "true"},
	})
	if err != nil {
		return 0, err
	}
	if len(protocols) == 0 {
		return 0, nil
	}

	protoByName := make(map[string][]*graph.Node)
	for _, p := range protocols {
		protoByName[p.Name] = append(protoByName[p.Name], p)
	}

	// Query all Python classes.
	classes, err := l.store.QueryNodes(ctx, graph.NodeFilter{
		Type:     graph.NodeClass,
		Language: "python",
	})
	if err != nil {
		return 0, err
	}

	linked := 0
	for _, cls := range classes {
		if cls.Properties == nil || cls.Properties["bases"] == "" {
			continue
		}

		for _, base := range strings.Split(cls.Properties["bases"], ",") {
			base = strings.TrimSpace(base)
			candidates := protoByName[base]
			if len(candidates) == 0 {
				continue
			}

			target := bestMatch(cls, candidates)
			if target == nil {
				continue
			}

			edgeKey := cls.ID + "→" + target.ID
			if existing[edgeKey] {
				continue
			}

			edge := &graph.Edge{
				ID:       graph.NewNodeID(string(graph.EdgeImplements), cls.ID, target.ID),
				Type:     graph.EdgeImplements,
				SourceID: cls.ID,
				TargetID: target.ID,
				Properties: map[string]string{
					"kind": "protocol",
				},
			}
			if err := l.store.AddEdge(ctx, edge); err != nil {
				continue
			}
			existing[edgeKey] = true
			linked++

			if l.verbose {
				l.log("    Python Protocol: %s -> %s", cls.Name, target.Name)
			}
		}
	}

	return linked, nil
}

// bestMatch returns the best matching interface node for a class,
// preferring same-directory, then same-package matches.
func bestMatch(cls *graph.Node, candidates []*graph.Node) *graph.Node {
	if len(candidates) == 0 {
		return nil
	}
	if len(candidates) == 1 {
		return candidates[0]
	}

	// Prefer same top-level directory (same service).
	clsDir := topDir(cls.FilePath)
	for _, c := range candidates {
		if topDir(c.FilePath) == clsDir {
			return c
		}
	}

	// Prefer same package.
	for _, c := range candidates {
		if c.Package != "" && c.Package == cls.Package {
			return c
		}
	}

	// Fall back to first candidate.
	return candidates[0]
}
