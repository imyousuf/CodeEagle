package cli

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

// TestRecomputeAttendanceSumsEveryLabel covers labelling a second diarization
// fragment for somebody already identified.
//
// Attendance is keyed on the person and the meeting alone, and writing an edge
// is an upsert, so writing one label's figures replaces the total rather than
// adding to it. Labelling a second fragment used to shrink the first one's
// credit — the speaking time of a person who talked for two and a half minutes
// would drop to the thirty seconds of whichever fragment was named last.
func TestRecomputeAttendanceSumsEveryLabel(t *testing.T) {
	ctx := context.Background()
	store, err := embedded.NewBranchStore(filepath.Join(t.TempDir(), "db"), "test", []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const meetingID = "session-42"
	meetingNodeID := graph.NewNodeID(string(graph.NodeMeeting), "rec.json", meetingID)
	if err := store.AddNode(ctx, &graph.Node{
		ID: meetingNodeID, Type: graph.NodeMeeting, Name: "Standup",
		QualifiedName: meetingID, FilePath: "rec.json",
	}); err != nil {
		t.Fatal(err)
	}

	person := &graph.Node{
		ID:   graph.NewNodeID(string(graph.NodePerson), "", "Bob Stone"),
		Type: graph.NodePerson, Name: "Bob Stone", QualifiedName: "Bob Stone",
	}
	if err := store.AddNode(ctx, person); err != nil {
		t.Fatal(err)
	}

	// Two labels, both Bob: one found automatically, one assigned by hand.
	var speakers []*graph.Node
	for _, sp := range []struct {
		label   string
		seconds string
	}{{"Person 2", "120"}, {"Person 5", "30"}} {
		node := &graph.Node{
			ID:   graph.NewNodeID(string(graph.NodeSpeaker), "rec.json", sp.label),
			Type: graph.NodeSpeaker, Name: sp.label, FilePath: "rec.json",
			Properties: map[string]string{
				graph.PropMeetingID:       meetingID,
				graph.PropSpeakingSeconds: sp.seconds,
			},
		}
		if err := store.AddNode(ctx, node); err != nil {
			t.Fatal(err)
		}
		if err := store.AddEdge(ctx, &graph.Edge{
			ID:   graph.NewNodeID("edge", node.ID, person.ID+":"+string(graph.EdgeIdentifiedAs)),
			Type: graph.EdgeIdentifiedAs, SourceID: node.ID, TargetID: person.ID,
		}); err != nil {
			t.Fatal(err)
		}
		speakers = append(speakers, node)
	}

	// Labelling either fragment must produce the same total.
	for _, speaker := range speakers {
		if err := recomputeAttendance(ctx, store, person, speaker, meetingNodeID); err != nil {
			t.Fatalf("recomputeAttendance: %v", err)
		}
	}

	edges, err := store.GetEdges(ctx, person.ID, graph.EdgeAttended)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Fatalf("got %d attendance edges, want exactly 1", len(edges))
	}

	seconds, err := strconv.ParseFloat(edges[0].Properties[graph.PropSpeakingSeconds], 64)
	if err != nil {
		t.Fatalf("speaking seconds unparseable: %v", err)
	}
	if seconds != 150 {
		t.Errorf("attendance records %.0fs, want 150 — both fragments together", seconds)
	}
	if got := edges[0].Properties[graph.PropSpeakerLabel]; got != "Person 2, Person 5" {
		t.Errorf("labels = %q, want both listed", got)
	}
}

// TestRecomputeAttendanceIgnoresOtherPeople covers not sweeping up speaking
// time that belongs to somebody else in the same meeting.
func TestRecomputeAttendanceIgnoresOtherPeople(t *testing.T) {
	ctx := context.Background()
	store, err := embedded.NewBranchStore(filepath.Join(t.TempDir(), "db"), "test", []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const meetingID = "session-7"
	meetingNodeID := graph.NewNodeID(string(graph.NodeMeeting), "rec.json", meetingID)

	bob := &graph.Node{ID: "person-bob", Type: graph.NodePerson, Name: "Bob Stone"}
	ann := &graph.Node{ID: "person-ann", Type: graph.NodePerson, Name: "Ann Reed"}
	for _, p := range []*graph.Node{bob, ann} {
		if err := store.AddNode(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	var bobSpeaker *graph.Node
	for _, sp := range []struct {
		label   string
		seconds string
		person  *graph.Node
	}{{"Person 2", "120", bob}, {"Person 3", "400", ann}} {
		node := &graph.Node{
			ID:   graph.NewNodeID(string(graph.NodeSpeaker), "rec.json", sp.label),
			Type: graph.NodeSpeaker, Name: sp.label, FilePath: "rec.json",
			Properties: map[string]string{
				graph.PropMeetingID:       meetingID,
				graph.PropSpeakingSeconds: sp.seconds,
			},
		}
		if err := store.AddNode(ctx, node); err != nil {
			t.Fatal(err)
		}
		if err := store.AddEdge(ctx, &graph.Edge{
			ID:   graph.NewNodeID("edge", node.ID, sp.person.ID+":"+string(graph.EdgeIdentifiedAs)),
			Type: graph.EdgeIdentifiedAs, SourceID: node.ID, TargetID: sp.person.ID,
		}); err != nil {
			t.Fatal(err)
		}
		if sp.person == bob {
			bobSpeaker = node
		}
	}

	if err := recomputeAttendance(ctx, store, bob, bobSpeaker, meetingNodeID); err != nil {
		t.Fatal(err)
	}

	edges, err := store.GetEdges(ctx, bob.ID, graph.EdgeAttended)
	if err != nil || len(edges) != 1 {
		t.Fatalf("got %d edges, %v", len(edges), err)
	}
	seconds, _ := strconv.ParseFloat(edges[0].Properties[graph.PropSpeakingSeconds], 64)
	if seconds != 120 {
		t.Errorf("Bob credited with %.0fs, want 120 — Ann's time is not his", seconds)
	}
}
