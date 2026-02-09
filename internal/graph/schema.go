package graph

import (
	"crypto/sha256"
	"fmt"
)

// NodeType represents the kind of entity in the knowledge graph.
type NodeType string

const (
	NodeRepository   NodeType = "Repository"
	NodeService      NodeType = "Service"
	NodeModule       NodeType = "Module"
	NodePackage      NodeType = "Package"
	NodeFile         NodeType = "File"
	NodeFunction     NodeType = "Function"
	NodeMethod       NodeType = "Method"
	NodeClass        NodeType = "Class"
	NodeStruct       NodeType = "Struct"
	NodeInterface    NodeType = "Interface"
	NodeEnum         NodeType = "Enum"
	NodeType_        NodeType = "Type"
	NodeConstant     NodeType = "Constant"
	NodeVariable     NodeType = "Variable"
	NodeAPIEndpoint  NodeType = "APIEndpoint"
	NodeDBModel      NodeType = "DBModel"
	NodeMigration    NodeType = "Migration"
	NodeDependency   NodeType = "Dependency"
	NodeDocument     NodeType = "Document"
	NodeAIGuideline  NodeType = "AIGuideline"
	NodeTestFunction NodeType = "TestFunction"
	NodeTestFile     NodeType = "TestFile"
)

// EdgeType represents a relationship between two nodes.
type EdgeType string

const (
	EdgeContains   EdgeType = "Contains"
	EdgeImports    EdgeType = "Imports"
	EdgeDependsOn  EdgeType = "DependsOn"
	EdgeCalls      EdgeType = "Calls"
	EdgeImplements EdgeType = "Implements"
	EdgeExposes    EdgeType = "Exposes"
	EdgeConsumes   EdgeType = "Consumes"
	EdgeDocuments  EdgeType = "Documents"
	EdgeTests      EdgeType = "Tests"
	EdgeMigrates   EdgeType = "Migrates"
	EdgeConfigures EdgeType = "Configures"
)

// Node represents a source code or documentation entity in the knowledge graph.
type Node struct {
	ID            string             `json:"id"`
	Type          NodeType           `json:"type"`
	Name          string             `json:"name"`
	QualifiedName string             `json:"qualified_name"`
	FilePath      string             `json:"file_path"`
	Line          int                `json:"line"`
	EndLine       int                `json:"end_line"`
	Package       string             `json:"package"`
	Language      string             `json:"language"`
	Exported      bool               `json:"exported"`
	Signature     string             `json:"signature,omitempty"`
	DocComment    string             `json:"doc_comment,omitempty"`
	Properties    map[string]string  `json:"properties,omitempty"`
	Metrics       map[string]float64 `json:"metrics,omitempty"`
}

// Edge represents a relationship between two nodes in the knowledge graph.
type Edge struct {
	ID         string            `json:"id"`
	Type       EdgeType          `json:"type"`
	SourceID   string            `json:"source_id"`
	TargetID   string            `json:"target_id"`
	Properties map[string]string `json:"properties,omitempty"`
}

// GraphStats holds aggregate statistics about the knowledge graph.
type GraphStats struct {
	NodeCount   int64              `json:"node_count"`
	EdgeCount   int64              `json:"edge_count"`
	NodesByType map[NodeType]int64 `json:"nodes_by_type"`
	EdgesByType map[EdgeType]int64 `json:"edges_by_type"`
}

// NewNodeID generates a deterministic node ID from the type, file path, and name.
// The ID is a hex-encoded SHA-256 hash prefix to keep keys compact and collision-resistant.
func NewNodeID(nodeType, filePath, name string) string {
	raw := fmt.Sprintf("%s:%s:%s", nodeType, filePath, name)
	h := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", h[:12])
}
