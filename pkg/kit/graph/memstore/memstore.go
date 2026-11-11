// Package memstore is an in-memory graph.Store: the reference implementation
// of the contract graphtest pins, and a store for tests.
//
// It holds one namespace and keeps everything in maps guarded by one lock.
// Nothing is persisted, so it suits tests, short-lived tools, and as a model
// for a store with persistence of its own; it is not built for shared or
// network storage.
//
// Results come back in the order CodeEagle's Badger store returns them within
// one scope, because callers depend on it: nodes sorted by ID, and a node's
// edges outgoing before incoming, each sorted by edge type and then ID.
package memstore

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
)

// Store is an in-memory graph.Store. The zero value is not usable; call New.
type Store struct {
	mu    sync.RWMutex
	nodes map[string]*graph.Node
	edges map[string]*graph.Edge
	// out and in index edge IDs by the node at each end, so a node's edges
	// are found without scanning every edge.
	out map[string]map[string]struct{}
	in  map[string]map[string]struct{}
}

var _ graph.Store = (*Store)(nil)

// New returns an empty store.
func New() *Store {
	return &Store{
		nodes: make(map[string]*graph.Node),
		edges: make(map[string]*graph.Edge),
		out:   make(map[string]map[string]struct{}),
		in:    make(map[string]map[string]struct{}),
	}
}

// AddNode stores a node, replacing any node with the same ID entirely.
func (s *Store) AddNode(_ context.Context, node *graph.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodes[node.ID] = copyNode(node)
	return nil
}

// UpdateNode replaces an existing node; a node that is not stored is an
// error.
func (s *Store) UpdateNode(_ context.Context, node *graph.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.nodes[node.ID]; !ok {
		return fmt.Errorf("update node %s: not found", node.ID)
	}
	s.nodes[node.ID] = copyNode(node)
	return nil
}

// DeleteNode removes a node and every edge that starts or ends at it.
func (s *Store) DeleteNode(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.nodes[id]; !ok {
		return fmt.Errorf("delete node %s: not found", id)
	}
	s.deleteNodeLocked(id)
	return nil
}

func (s *Store) deleteNodeLocked(id string) {
	for eid := range s.out[id] {
		s.deleteEdgeLocked(eid)
	}
	for eid := range s.in[id] {
		s.deleteEdgeLocked(eid)
	}
	delete(s.nodes, id)
}

// GetNode returns a copy of a node; a node that is not stored is an error.
func (s *Store) GetNode(_ context.Context, id string) (*graph.Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n, ok := s.nodes[id]
	if !ok {
		return nil, fmt.Errorf("get node %s: not found", id)
	}
	return copyNode(n), nil
}

// QueryNodes returns copies of the nodes graph.Matches accepts, sorted by ID.
// NameMatch is therefore called at most once per stored node.
func (s *Store) QueryNodes(_ context.Context, filter graph.NodeFilter) ([]*graph.Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*graph.Node
	for _, id := range sortedKeys(s.nodes) {
		if n := s.nodes[id]; graph.Matches(n, filter) {
			out = append(out, copyNode(n))
		}
	}
	return out, nil
}

// AddEdge stores an edge, replacing any edge with the same ID. Its endpoints
// need not exist.
func (s *Store) AddEdge(_ context.Context, edge *graph.Edge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.edges[edge.ID]; ok {
		s.deleteEdgeLocked(edge.ID)
	}
	e := copyEdge(edge)
	s.edges[e.ID] = e
	link(s.out, e.SourceID, e.ID)
	link(s.in, e.TargetID, e.ID)
	return nil
}

// DeleteEdge removes an edge; an edge that is not stored is an error.
func (s *Store) DeleteEdge(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.edges[id]; !ok {
		return fmt.Errorf("delete edge %s: not found", id)
	}
	s.deleteEdgeLocked(id)
	return nil
}

func (s *Store) deleteEdgeLocked(id string) {
	e, ok := s.edges[id]
	if !ok {
		return
	}
	unlink(s.out, e.SourceID, id)
	unlink(s.in, e.TargetID, id)
	delete(s.edges, id)
}

// GetEdges returns copies of a node's edges of one type, or of every type
// when edgeType is empty: outgoing first, then incoming, each sorted by type
// and ID. An edge appears once even when it loops back to the same node.
func (s *Store) GetEdges(_ context.Context, nodeID string, edgeType graph.EdgeType) ([]*graph.Edge, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]struct{})
	var out []*graph.Edge
	for _, e := range append(s.edgesLocked(s.out[nodeID], edgeType), s.edgesLocked(s.in[nodeID], edgeType)...) {
		if _, ok := seen[e.ID]; ok {
			continue
		}
		seen[e.ID] = struct{}{}
		out = append(out, copyEdge(e))
	}
	return out, nil
}

// GetNeighbors returns copies of the nodes at the other end of a node's
// edges, once each, in edge order. A neighbour whose node is not stored is
// skipped.
func (s *Store) GetNeighbors(_ context.Context, nodeID string, edgeType graph.EdgeType, direction graph.Direction) ([]*graph.Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]struct{})
	var out []*graph.Node
	visit := func(peer string) {
		if _, ok := seen[peer]; ok {
			return
		}
		seen[peer] = struct{}{}
		if n, ok := s.nodes[peer]; ok {
			out = append(out, copyNode(n))
		}
	}
	if direction == graph.Outgoing || direction == graph.Both {
		for _, e := range s.edgesLocked(s.out[nodeID], edgeType) {
			visit(e.TargetID)
		}
	}
	if direction == graph.Incoming || direction == graph.Both {
		for _, e := range s.edgesLocked(s.in[nodeID], edgeType) {
			visit(e.SourceID)
		}
	}
	return out, nil
}

// DeleteByFile removes every node recorded under filePath, with its edges.
func (s *Store) DeleteByFile(_ context.Context, filePath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if filePath == "" {
		return nil
	}
	for _, id := range sortedKeys(s.nodes) {
		if s.nodes[id].FilePath == filePath {
			s.deleteNodeLocked(id)
		}
	}
	return nil
}

// Stats counts nodes and edges, in total and by type.
func (s *Store) Stats(_ context.Context) (*graph.GraphStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := &graph.GraphStats{
		NodeCount:   int64(len(s.nodes)),
		EdgeCount:   int64(len(s.edges)),
		NodesByType: make(map[graph.NodeType]int64),
		EdgesByType: make(map[graph.EdgeType]int64),
	}
	for _, n := range s.nodes {
		st.NodesByType[n.Type]++
	}
	for _, e := range s.edges {
		st.EdgesByType[e.Type]++
	}
	return st, nil
}

// Close does nothing; the store holds no resources.
func (s *Store) Close() error { return nil }

// edgesLocked returns the edges named in ids, of one type or every type,
// sorted by type and then ID.
func (s *Store) edgesLocked(ids map[string]struct{}, edgeType graph.EdgeType) []*graph.Edge {
	out := make([]*graph.Edge, 0, len(ids))
	for id := range ids {
		if e := s.edges[id]; edgeType == "" || e.Type == edgeType {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func link(index map[string]map[string]struct{}, nodeID, edgeID string) {
	if index[nodeID] == nil {
		index[nodeID] = make(map[string]struct{})
	}
	index[nodeID][edgeID] = struct{}{}
}

func unlink(index map[string]map[string]struct{}, nodeID, edgeID string) {
	delete(index[nodeID], edgeID)
	if len(index[nodeID]) == 0 {
		delete(index, nodeID)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func copyNode(n *graph.Node) *graph.Node {
	c := *n
	if n.Properties != nil {
		c.Properties = make(map[string]string, len(n.Properties))
		for k, v := range n.Properties {
			c.Properties[k] = v
		}
	}
	if n.Metrics != nil {
		c.Metrics = make(map[string]float64, len(n.Metrics))
		for k, v := range n.Metrics {
			c.Metrics[k] = v
		}
	}
	return &c
}

func copyEdge(e *graph.Edge) *graph.Edge {
	c := *e
	if e.Properties != nil {
		c.Properties = make(map[string]string, len(e.Properties))
		for k, v := range e.Properties {
			c.Properties[k] = v
		}
	}
	return &c
}
