package transcript

import (
	"context"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// TestClearTaxonomyKeepsPromotedSubjects is the guard on `meetings taxonomy
// --rebuild`.
//
// Grouping is shown the topic list and asked to name concepts over it, and
// placing a meeting's topics promotes whatever the model named as a parent.
// Both routes mean a topic meetings already point at can end up marked as a
// concept. Deleting every concept on a rebuild then takes those meetings'
// HasTopic edges with it: on the reference corpus that was 2,422 edges across
// 340 of 614 meetings, because promoted subjects outnumbered invented
// concepts roughly four to one.
func TestClearTaxonomyKeepsPromotedSubjects(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)

	// A topic a meeting named, which grouping later promoted. The registry is
	// the only thing that writes a usage count.
	promoted := &graph.Node{
		ID:   graph.NewNodeID(string(graph.NodeTopic), "", "Authentication"),
		Type: graph.NodeTopic, Name: "Authentication",
		Properties: map[string]string{
			propTopicUses:  "14",
			PropTopicLevel: LevelTheme,
			PropTopicDepth: "1",
		},
	}
	// A concept grouping invented: nothing ever resolved it by name.
	invented := &graph.Node{
		ID:   graph.NewNodeID(string(graph.NodeTopic), "", "Platform"),
		Type: graph.NodeTopic, Name: "Platform",
		Properties: map[string]string{
			PropTopicLevel:   LevelTheme,
			PropTopicDepth:   "1",
			PropTopicInduced: "true",
		},
	}
	leaf := &graph.Node{
		ID:   graph.NewNodeID(string(graph.NodeTopic), "", "Token lifetimes"),
		Type: graph.NodeTopic, Name: "Token lifetimes",
		Properties: map[string]string{PropTopicLevel: LevelSubject},
	}
	meeting := &graph.Node{
		ID: "meeting-1", Type: graph.NodeMeeting, Name: "Auth sync",
	}
	for _, n := range []*graph.Node{promoted, invented, leaf, meeting} {
		if err := store.AddNode(ctx, n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Name, err)
		}
	}

	// The meeting points at the promoted subject: this is what must survive.
	link := &graph.Edge{
		ID:       graph.NewNodeID("edge", meeting.ID, promoted.ID+":"+string(graph.EdgeHasTopic)),
		Type:     graph.EdgeHasTopic,
		SourceID: meeting.ID, TargetID: promoted.ID,
	}
	// Grouping gave the promoted subject a child; that placement is discarded.
	child := &graph.Edge{
		ID:       graph.NewNodeID("edge", promoted.ID, leaf.ID+":"+string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: promoted.ID, TargetID: leaf.ID,
	}
	for _, e := range []*graph.Edge{link, child} {
		if err := store.AddEdge(ctx, e); err != nil {
			t.Fatalf("AddEdge: %v", err)
		}
	}

	removed, err := ClearTaxonomy(ctx, store)
	if err != nil {
		t.Fatalf("ClearTaxonomy: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed %d concepts, want 1 (only the invented one)", removed)
	}

	survivor, err := store.GetNode(ctx, promoted.ID)
	if err != nil || survivor == nil {
		t.Fatal("a topic meetings point at was deleted by --rebuild")
	}
	if survivor.Properties[propTopicUses] != "14" {
		t.Errorf("usage count lost: %q", survivor.Properties[propTopicUses])
	}
	if survivor.Properties[PropTopicLevel] != LevelSubject {
		t.Errorf("promoted subject not demoted: %q", survivor.Properties[PropTopicLevel])
	}

	topics, err := store.GetNeighbors(ctx, meeting.ID, graph.EdgeHasTopic, graph.Outgoing)
	if err != nil {
		t.Fatalf("GetNeighbors: %v", err)
	}
	if len(topics) != 1 {
		t.Errorf("the meeting lost its topic link: %d remain", len(topics))
	}

	// The invented concept really is gone.
	if n, err := store.GetNode(ctx, invented.ID); err == nil && n != nil {
		t.Error("an invented concept survived the rebuild")
	}

	// And its grouping of the leaf was undone.
	kids, err := store.GetNeighbors(ctx, promoted.ID, graph.EdgeContains, graph.Outgoing)
	if err == nil && len(kids) != 0 {
		t.Errorf("demoted topic kept %d children", len(kids))
	}
}

// TestInducedConceptDiscriminatesLegacyNodes covers concepts written before
// the marker existed, which is every concept already in a user's database.
func TestInducedConceptDiscriminatesLegacyNodes(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]string
		want  bool
	}{
		{"marked outright", map[string]string{PropTopicInduced: "true"}, true},
		{"legacy, no registry bookkeeping", map[string]string{PropTopicLevel: LevelTheme}, true},
		{"legacy, has a usage count", map[string]string{propTopicUses: "3"}, false},
		{"legacy, has alternate wordings", map[string]string{graph.PropAliases: "authn"}, false},
	}
	for _, tt := range cases {
		got := inducedConcept(&graph.Node{Properties: tt.props})
		if got != tt.want {
			t.Errorf("%s: inducedConcept = %v, want %v", tt.name, got, tt.want)
		}
	}
}
