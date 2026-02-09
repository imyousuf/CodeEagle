package graph

import (
	"context"
	"fmt"
)

// LayeredStore implements Store with two layers: a read-only main store
// (typically CI-built and committed) and a read-write local store for local changes.
// Reads merge results from both stores; writes go to the local store only.
type LayeredStore struct {
	main  Store
	local Store
}

// NewLayeredStore creates a LayeredStore backed by the given main and local stores.
// If main and local are the same Store instance, it behaves as a single-store pass-through.
func NewLayeredStore(main, local Store) *LayeredStore {
	return &LayeredStore{main: main, local: local}
}

func (ls *LayeredStore) AddNode(ctx context.Context, node *Node) error {
	return ls.local.AddNode(ctx, node)
}

func (ls *LayeredStore) UpdateNode(ctx context.Context, node *Node) error {
	return ls.local.UpdateNode(ctx, node)
}

func (ls *LayeredStore) DeleteNode(ctx context.Context, id string) error {
	return ls.local.DeleteNode(ctx, id)
}

func (ls *LayeredStore) GetNode(ctx context.Context, id string) (*Node, error) {
	// Try local first; if found, local overrides main.
	node, err := ls.local.GetNode(ctx, id)
	if err == nil {
		tagNodeSource(node, "local")
		return node, nil
	}
	node, err = ls.main.GetNode(ctx, id)
	if err == nil {
		tagNodeSource(node, "main")
	}
	return node, err
}

func (ls *LayeredStore) QueryNodes(ctx context.Context, filter NodeFilter) ([]*Node, error) {
	mainNodes, err := ls.main.QueryNodes(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("query main store: %w", err)
	}
	localNodes, err := ls.local.QueryNodes(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("query local store: %w", err)
	}

	// Merge: local overrides main for duplicate IDs.
	// Tag each node with its source layer so the LLM can distinguish
	// committed (main) data from local uncommitted changes.
	seen := make(map[string]struct{}, len(localNodes))
	var result []*Node
	for _, n := range localNodes {
		seen[n.ID] = struct{}{}
		tagNodeSource(n, "local")
		result = append(result, n)
	}
	for _, n := range mainNodes {
		if _, ok := seen[n.ID]; !ok {
			tagNodeSource(n, "main")
			result = append(result, n)
		}
	}
	return result, nil
}

func (ls *LayeredStore) AddEdge(ctx context.Context, edge *Edge) error {
	return ls.local.AddEdge(ctx, edge)
}

func (ls *LayeredStore) DeleteEdge(ctx context.Context, id string) error {
	return ls.local.DeleteEdge(ctx, id)
}

func (ls *LayeredStore) GetEdges(ctx context.Context, nodeID string, edgeType EdgeType) ([]*Edge, error) {
	mainEdges, err := ls.main.GetEdges(ctx, nodeID, edgeType)
	if err != nil {
		return nil, fmt.Errorf("get edges from main: %w", err)
	}
	localEdges, err := ls.local.GetEdges(ctx, nodeID, edgeType)
	if err != nil {
		return nil, fmt.Errorf("get edges from local: %w", err)
	}

	seen := make(map[string]struct{}, len(localEdges))
	var result []*Edge
	for _, e := range localEdges {
		seen[e.ID] = struct{}{}
		tagEdgeSource(e, "local")
		result = append(result, e)
	}
	for _, e := range mainEdges {
		if _, ok := seen[e.ID]; !ok {
			tagEdgeSource(e, "main")
			result = append(result, e)
		}
	}
	return result, nil
}

func (ls *LayeredStore) GetNeighbors(ctx context.Context, nodeID string, edgeType EdgeType, direction Direction) ([]*Node, error) {
	mainNeighbors, err := ls.main.GetNeighbors(ctx, nodeID, edgeType, direction)
	if err != nil {
		return nil, fmt.Errorf("get neighbors from main: %w", err)
	}
	localNeighbors, err := ls.local.GetNeighbors(ctx, nodeID, edgeType, direction)
	if err != nil {
		return nil, fmt.Errorf("get neighbors from local: %w", err)
	}

	seen := make(map[string]struct{}, len(localNeighbors))
	var result []*Node
	for _, n := range localNeighbors {
		seen[n.ID] = struct{}{}
		tagNodeSource(n, "local")
		result = append(result, n)
	}
	for _, n := range mainNeighbors {
		if _, ok := seen[n.ID]; !ok {
			tagNodeSource(n, "main")
			result = append(result, n)
		}
	}
	return result, nil
}

func (ls *LayeredStore) DeleteByFile(ctx context.Context, filePath string) error {
	return ls.local.DeleteByFile(ctx, filePath)
}

func (ls *LayeredStore) Stats(ctx context.Context) (*GraphStats, error) {
	mainStats, err := ls.main.Stats(ctx)
	if err != nil {
		return nil, fmt.Errorf("main stats: %w", err)
	}
	localStats, err := ls.local.Stats(ctx)
	if err != nil {
		return nil, fmt.Errorf("local stats: %w", err)
	}

	merged := &GraphStats{
		NodeCount:   mainStats.NodeCount + localStats.NodeCount,
		EdgeCount:   mainStats.EdgeCount + localStats.EdgeCount,
		NodesByType: make(map[NodeType]int64),
		EdgesByType: make(map[EdgeType]int64),
	}
	for k, v := range mainStats.NodesByType {
		merged.NodesByType[k] += v
	}
	for k, v := range localStats.NodesByType {
		merged.NodesByType[k] += v
	}
	for k, v := range mainStats.EdgesByType {
		merged.EdgesByType[k] += v
	}
	for k, v := range localStats.EdgesByType {
		merged.EdgesByType[k] += v
	}
	return merged, nil
}

func (ls *LayeredStore) Close() error {
	mainErr := ls.main.Close()
	localErr := ls.local.Close()
	if mainErr != nil {
		return mainErr
	}
	return localErr
}

// tagNodeSource sets the PropGraphSource property on a node to indicate
// whether it came from the main (shared/committed) or local (uncommitted) store.
func tagNodeSource(n *Node, source string) {
	if n.Properties == nil {
		n.Properties = make(map[string]string)
	}
	n.Properties[PropGraphSource] = source
}

// tagEdgeSource sets the PropGraphSource property on an edge to indicate
// whether it came from the main (shared/committed) or local (uncommitted) store.
func tagEdgeSource(e *Edge, source string) {
	if e.Properties == nil {
		e.Properties = make(map[string]string)
	}
	e.Properties[PropGraphSource] = source
}
