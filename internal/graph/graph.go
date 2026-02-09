package graph

import "context"

// Direction specifies the traversal direction for edge queries.
type Direction int

const (
	Outgoing Direction = iota
	Incoming
	Both
)

// NodeFilter specifies criteria for querying nodes.
type NodeFilter struct {
	Type        NodeType
	FilePath    string
	Package     string
	Language    string
	NamePattern string // glob pattern matched against Name
	Exported    *bool
}

// Store is the interface for knowledge graph persistence.
type Store interface {
	// AddNode inserts a new node into the graph.
	AddNode(ctx context.Context, node *Node) error

	// UpdateNode replaces an existing node (matched by ID).
	UpdateNode(ctx context.Context, node *Node) error

	// DeleteNode removes a node by ID along with its connected edges.
	DeleteNode(ctx context.Context, id string) error

	// GetNode retrieves a single node by ID.
	GetNode(ctx context.Context, id string) (*Node, error)

	// QueryNodes returns all nodes matching the given filter.
	QueryNodes(ctx context.Context, filter NodeFilter) ([]*Node, error)

	// AddEdge inserts a new edge into the graph.
	AddEdge(ctx context.Context, edge *Edge) error

	// DeleteEdge removes an edge by ID.
	DeleteEdge(ctx context.Context, id string) error

	// GetEdges returns edges connected to nodeID with the given type.
	// If edgeType is empty, all edge types are returned.
	GetEdges(ctx context.Context, nodeID string, edgeType EdgeType) ([]*Edge, error)

	// GetNeighbors returns nodes connected to nodeID via edges of the given type
	// in the specified direction. If edgeType is empty, all edge types are traversed.
	GetNeighbors(ctx context.Context, nodeID string, edgeType EdgeType, direction Direction) ([]*Node, error)

	// DeleteByFile removes all nodes (and their edges) associated with the given file path.
	// This supports incremental updates: delete everything from a file before re-indexing it.
	DeleteByFile(ctx context.Context, filePath string) error

	// Stats returns aggregate statistics about the graph.
	Stats(ctx context.Context) (*GraphStats, error)

	// Close releases resources held by the store.
	Close() error
}
