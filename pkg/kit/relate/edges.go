package relate

import (
	"context"
	"fmt"

	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
)

// linkEdge is the edge each kind of link is written as.
var linkEdge = map[string]graph.EdgeType{
	Changes:   graph.EdgeChanges,
	Replaces:  graph.EdgeSupersedes,
	Records:   graph.EdgeRecords,
	Companion: graph.EdgeCompanionOf,
}

// EdgeType returns the edge a kind of link is written as, or "" for a kind
// this package does not decide.
func EdgeType(kind string) graph.EdgeType { return linkEdge[kind] }

// WriteEdges rewrites the edges between the nodes of the pair a, b to match
// its verdict: one edge per accepted link, carrying its probability and
// judge, and none left over from links no longer accepted. nodeID maps a
// document's ID to the ID of the node that stands for it.
func WriteEdges(ctx context.Context, store graph.Store, a, b string, v *Verdict, nodeID func(doc string) string) error {
	id, otherID := nodeID(a), nodeID(b)
	want := make(map[string]bool, len(v.Links))
	for _, l := range v.Links {
		want[graph.NewNodeID("edge", nodeID(l.From), nodeID(l.To)+":"+string(linkEdge[l.Kind]))] = true
	}
	for _, t := range linkEdge {
		current, err := store.GetEdges(ctx, id, t)
		if err != nil {
			return err
		}
		for _, e := range current {
			pairEdge := (e.SourceID == id && e.TargetID == otherID) || (e.SourceID == otherID && e.TargetID == id)
			if pairEdge && !want[e.ID] {
				if err := store.DeleteEdge(ctx, e.ID); err != nil {
					return err
				}
			}
		}
	}
	for _, l := range v.Links {
		t := linkEdge[l.Kind]
		from, to := nodeID(l.From), nodeID(l.To)
		if err := store.AddEdge(ctx, &graph.Edge{
			ID:       graph.NewNodeID("edge", from, to+":"+string(t)),
			Type:     t,
			SourceID: from,
			TargetID: to,
			Properties: map[string]string{
				"probability": fmt.Sprintf("%.3f", l.P),
				"judge":       v.Judge,
			},
		}); err != nil {
			return err
		}
	}
	return nil
}
