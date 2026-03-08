package indexer

import (
	"context"
	"fmt"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// isFileTypeNode returns true for node types that represent files on disk
// and should have date hierarchy nodes attached.
func isFileTypeNode(t graph.NodeType) bool {
	switch t {
	case graph.NodeFile, graph.NodeTestFile, graph.NodeDocument,
		graph.NodeDirectory, graph.NodeAIGuideline:
		return true
	}
	return false
}

// EnsureDateNodes creates Year, Month, and Date nodes for the given time
// and links the file node to the Date node via an UpdatedOn edge.
// Date nodes are shared across files — calling this for two files on the same
// date reuses the same Year/Month/Date nodes (deterministic IDs + upsert).
func EnsureDateNodes(ctx context.Context, store graph.Store, t time.Time, fileNodeID string) error {
	t = t.UTC()
	yearStr := fmt.Sprintf("%d", t.Year())
	monthStr := fmt.Sprintf("%d-%02d", t.Year(), t.Month())
	dateStr := fmt.Sprintf("%d-%02d-%02d", t.Year(), t.Month(), t.Day())

	yearID := graph.NewNodeID(string(graph.NodeYear), "", yearStr)
	monthID := graph.NewNodeID(string(graph.NodeMonth), "", monthStr)
	dateID := graph.NewNodeID(string(graph.NodeDate), "", dateStr)

	// Create date hierarchy nodes (no FilePath — like Topic nodes).
	yearNode := &graph.Node{ID: yearID, Type: graph.NodeYear, Name: yearStr}
	monthNode := &graph.Node{ID: monthID, Type: graph.NodeMonth, Name: monthStr}
	dateNode := &graph.Node{ID: dateID, Type: graph.NodeDate, Name: dateStr}

	for _, n := range []*graph.Node{yearNode, monthNode, dateNode} {
		if err := store.AddNode(ctx, n); err != nil {
			return fmt.Errorf("add date node %s: %w", n.Name, err)
		}
	}

	// Create hierarchy edges: Year -Contains-> Month -Contains-> Date.
	yearMonthEdge := &graph.Edge{
		ID:       graph.NewNodeID("edge", yearID, monthID+":Contains"),
		Type:     graph.EdgeContains,
		SourceID: yearID,
		TargetID: monthID,
	}
	monthDateEdge := &graph.Edge{
		ID:       graph.NewNodeID("edge", monthID, dateID+":Contains"),
		Type:     graph.EdgeContains,
		SourceID: monthID,
		TargetID: dateID,
	}

	for _, e := range []*graph.Edge{yearMonthEdge, monthDateEdge} {
		if err := store.AddEdge(ctx, e); err != nil {
			return fmt.Errorf("add date hierarchy edge %s: %w", e.ID, err)
		}
	}

	// Create file -> date edge.
	updatedOnEdge := &graph.Edge{
		ID:       graph.NewNodeID("edge", fileNodeID, dateID+":UpdatedOn"),
		Type:     graph.EdgeUpdatedOn,
		SourceID: fileNodeID,
		TargetID: dateID,
	}
	if err := store.AddEdge(ctx, updatedOnEdge); err != nil {
		return fmt.Errorf("add UpdatedOn edge: %w", err)
	}

	return nil
}

// DeleteUpdatedOnEdges removes all UpdatedOn edges from a file node.
// Called before EnsureDateNodes to handle date changes on re-index.
func DeleteUpdatedOnEdges(ctx context.Context, store graph.Store, fileNodeID string) error {
	edges, err := store.GetEdges(ctx, fileNodeID, graph.EdgeUpdatedOn)
	if err != nil {
		return fmt.Errorf("get UpdatedOn edges: %w", err)
	}
	for _, e := range edges {
		if e.SourceID == fileNodeID {
			if err := store.DeleteEdge(ctx, e.ID); err != nil {
				return fmt.Errorf("delete UpdatedOn edge %s: %w", e.ID, err)
			}
		}
	}
	return nil
}
