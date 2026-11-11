package graph_test

import (
	"slices"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// TestNewNodeIDIsPinned fixes the IDs NewNodeID produces. Every stored node,
// edge endpoint and vector key is addressed by one, so a change to how they
// are derived orphans every graph already indexed. The expected values were
// computed outside Go, as the first 24 hex digits of
// sha256("<type>:<path>:<name>").
func TestNewNodeIDIsPinned(t *testing.T) {
	tests := []struct {
		nodeType, filePath, name string
		want                     string
	}{
		{"Function", "main.go", "main", "f65bd298334c213eb3df8251"},
		{"Method", "internal/graph/embedded/store.go", "BranchStore.AddNode", "5b4e09d2c027c60ed7363659"},
		{"Document", "docs/café.md", "Café — notes", "838e72d7bd113d8230f33d8f"},
		{"", "", "", "71546855d6279ef70d20909b"},
		{"Topic", "", "retry policy", "8c4f43525237d2b86e27571a"},
		{"Meeting", "/home/u/sessions/2026-01-02/session.json", "Weekly sync", "a21dbcff0d25d9240ccb939c"},
	}
	for _, tc := range tests {
		got := graph.NewNodeID(tc.nodeType, tc.filePath, tc.name)
		if got != tc.want {
			t.Errorf("NewNodeID(%q, %q, %q) = %q, want %q", tc.nodeType, tc.filePath, tc.name, got, tc.want)
		}
	}
}

// TestNewNodeIDSeparatesFields: the fields are joined with a colon and not
// escaped, so moving a colon between two fields yields the same ID. This is
// pinned so that a change to it is deliberate.
func TestNewNodeIDSeparatesFields(t *testing.T) {
	a := graph.NewNodeID("Function", "a:b", "c")
	b := graph.NewNodeID("Function", "a", "b:c")
	if a != b {
		t.Errorf("NewNodeID joined fields differently: %q vs %q", a, b)
	}
	if got := graph.NewNodeID(string(graph.NodeFunction), "main.go", "main"); len(got) != 24 {
		t.Errorf("NewNodeID length = %d, want 24 hex digits", len(got))
	}
}

// TestMeetingNodeTypesIsPinned fixes the meeting types and their order;
// meeting migration and the meetings scope read this list.
func TestMeetingNodeTypesIsPinned(t *testing.T) {
	want := []graph.NodeType{"Meeting", "Speaker", "TopicSegment", "Decision", "ActionItem"}
	got := graph.MeetingNodeTypes()
	if !slices.Equal(got, want) {
		t.Errorf("MeetingNodeTypes() = %v, want %v", got, want)
	}

	// Each call returns a fresh slice, so a caller may modify it.
	got[0] = "Mutated"
	if again := graph.MeetingNodeTypes(); again[0] != graph.NodeMeeting {
		t.Errorf("MeetingNodeTypes()[0] = %q after a caller changed its copy, want %q", again[0], graph.NodeMeeting)
	}
}
