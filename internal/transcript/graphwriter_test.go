package transcript

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

func testStore(t *testing.T) graph.Store {
	t.Helper()
	store, err := embedded.NewStore(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// sampleResult builds an enriched session covering every node kind the writer
// produces.
func sampleResult(path string) *Result {
	s := build(
		[3]string{"You", SourceMic, "Let's start with the migration status please everyone."},
		[3]string{"Person 1", SourceMonitor, "The schema migration is done and we verified it overnight."},
		[3]string{"Person 2", SourceMonitor, "I still need to update the client before we can ship."},
	)
	s.Path = path
	s.ID = "session-abc"
	for i := range s.Segments {
		s.Segments[i].EndTime = s.Segments[i].StartTime + 30
	}

	return &Result{
		Session: s,
		Identities: []SpeakerIdentity{
			{Label: "You", Name: "Imran Yousuf", Confidence: 1.0, Evidence: "microphone", Method: MethodOwnerAnchor},
			{Label: "Person 1", Name: "Mona", Confidence: 0.95, Evidence: "Thanks, Mona.", Method: MethodLLM},
			{Label: "Person 2", Name: "Kevin", Confidence: 0.92, Evidence: "Kevin, can you?", Method: MethodLLM},
		},
		Analysis: &Analysis{
			Title:   "Schema migration status",
			Summary: "The team confirmed the schema migration completed and agreed to ship once the client is updated.",
			Topics: []Topic{{
				Name:         "schema migration",
				Summary:      "The migration ran overnight and was verified.",
				StartTime:    0,
				EndTime:      60,
				Participants: []string{"Mona", "Imran Yousuf"},
				Keywords:     []string{"migration", "schema"},
			}},
			Decisions: []Decision{{
				Text:      "Ship once the client is updated",
				Rationale: "Migration is verified",
				DecidedBy: []string{"Imran Yousuf"},
				Topic:     "schema migration",
				Quote:     "The schema migration is done and we verified it overnight.",
			}},
			ActionItems: []ActionItem{{
				Text:     "Update the client",
				Assignee: "Kevin",
				DueDate:  "2026-02-01",
				Topic:    "schema migration",
				Quote:    "I still need to update the client before we can ship.",
			}},
			Mentions: []string{"schema-service"},
		},
	}
}

func TestWriterProjectsMeeting(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)

	people, err := LoadPersonRegistry(ctx, store)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	w := NewWriter(store, people, WriterOptions{MinConfidence: 0.7, Owner: "Imran Yousuf"})

	res := sampleResult("/tmp/sessions/abc/session.json")
	st, err := w.Write(ctx, res)
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	if st.Meetings != 1 {
		t.Errorf("meetings = %d, want 1", st.Meetings)
	}
	if st.Speakers != 3 {
		t.Errorf("speakers = %d, want 3", st.Speakers)
	}
	if st.Identified != 3 {
		t.Errorf("identified = %d, want 3", st.Identified)
	}
	if st.Decisions != 1 || st.ActionItems != 1 || st.Topics != 1 || st.Segments != 1 {
		t.Errorf("content counts wrong: %+v", st)
	}

	// The meeting should carry the model's title, not the recorder's placeholder.
	meetings, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeMeeting})
	if err != nil {
		t.Fatalf("query meetings: %v", err)
	}
	if len(meetings) != 1 {
		t.Fatalf("got %d meetings, want 1", len(meetings))
	}
	if meetings[0].Name != "Schema migration status" {
		t.Errorf("meeting name = %q, want the analysed title", meetings[0].Name)
	}

	// Quotes taken from the transcript should be marked verified.
	decisions, _ := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeDecision})
	if len(decisions) != 1 {
		t.Fatalf("got %d decisions, want 1", len(decisions))
	}
	if decisions[0].Properties["quote_verified"] != "true" {
		t.Errorf("quote_verified = %q, want true", decisions[0].Properties["quote_verified"])
	}

	// The action item must be linked to the person who committed to it.
	actions, _ := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeActionItem})
	if len(actions) != 1 {
		t.Fatalf("got %d action items, want 1", len(actions))
	}
	assignees, err := store.GetNeighbors(ctx, actions[0].ID, graph.EdgeAssignedTo, graph.Outgoing)
	if err != nil {
		t.Fatalf("get assignee: %v", err)
	}
	if len(assignees) != 1 || assignees[0].Name != "Kevin" {
		t.Errorf("assignee = %+v, want Kevin", assignees)
	}
}

func TestWriterIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	people, _ := LoadPersonRegistry(ctx, store)
	w := NewWriter(store, people, WriterOptions{MinConfidence: 0.7})

	path := "/tmp/sessions/abc/session.json"
	for i := 0; i < 3; i++ {
		if _, err := w.Write(ctx, sampleResult(path)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	// Re-indexing the same recording must update it, not accumulate copies.
	for _, typ := range []graph.NodeType{
		graph.NodeMeeting, graph.NodeDecision, graph.NodeActionItem, graph.NodeTopicSegment,
	} {
		nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: typ})
		if err != nil {
			t.Fatalf("query %s: %v", typ, err)
		}
		if len(nodes) != 1 {
			t.Errorf("%s count = %d after 3 writes, want 1", typ, len(nodes))
		}
	}
	// People are global and must not be duplicated either.
	persons, _ := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodePerson})
	if len(persons) != 3 {
		t.Errorf("person count = %d, want 3", len(persons))
	}
}

func TestWriterSkipsLowConfidenceIdentities(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	people, _ := LoadPersonRegistry(ctx, store)
	w := NewWriter(store, people, WriterOptions{MinConfidence: 0.9})

	res := sampleResult("/tmp/sessions/abc/session.json")
	// Drop one speaker below the bar; the graph should decline to name them.
	for i := range res.Identities {
		if res.Identities[i].Label == "Person 2" {
			res.Identities[i].Confidence = 0.4
		}
	}
	st, err := w.Write(ctx, res)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if st.Identified != 2 || st.Unidentified != 1 {
		t.Errorf("identified=%d unidentified=%d, want 2/1", st.Identified, st.Unidentified)
	}

	// The speaker node still exists — an unidentified participant is a fact
	// worth recording, and it is what the review command lists.
	speakers, _ := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeSpeaker})
	if len(speakers) != 3 {
		t.Errorf("speaker count = %d, want 3", len(speakers))
	}

	// With no confident identity, the action item must not be assigned.
	actions, _ := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeActionItem})
	assignees, _ := store.GetNeighbors(ctx, actions[0].ID, graph.EdgeAssignedTo, graph.Outgoing)
	if len(assignees) != 0 {
		t.Errorf("got %d assignees, want none for an unidentified person", len(assignees))
	}
}

func TestPersonRegistryMergesTranscriptionVariants(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	reg, err := LoadPersonRegistry(ctx, store)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}

	first, err := reg.Resolve(ctx, "Imran")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// A later meeting hears the same person's name differently.
	second, err := reg.Resolve(ctx, "Imron")
	if err != nil {
		t.Fatalf("resolve variant: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("Imran and Imron resolved to different people (%s vs %s)", first.ID, second.ID)
	}

	// The variant is recorded, so the next meeting resolves it without fuzzy
	// matching at all.
	reloaded, _ := store.GetNode(ctx, first.ID)
	if got := reloaded.Properties[graph.PropAliases]; got != "Imron" {
		t.Errorf("aliases = %q, want Imron", got)
	}

	persons, _ := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodePerson})
	if len(persons) != 1 {
		t.Errorf("person count = %d, want 1", len(persons))
	}
}

func TestPersonRegistryKeepsDistinctPeopleApart(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	reg, _ := LoadPersonRegistry(ctx, store)

	a, _ := reg.Resolve(ctx, "Jon")
	b, _ := reg.Resolve(ctx, "Ron")
	if a.ID == b.ID {
		t.Error("Jon and Ron were merged; distinct people must stay distinct")
	}
}

func TestPersonRegistryReusesExistingPeople(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)

	// A person created elsewhere — by face recognition, for instance — must be
	// reused rather than duplicated.
	existing := &graph.Node{
		ID:            graph.NewNodeID(string(graph.NodePerson), "", "Mona"),
		Type:          graph.NodePerson,
		Name:          "Mona",
		QualifiedName: "Mona",
	}
	if err := store.AddNode(ctx, existing); err != nil {
		t.Fatalf("seed person: %v", err)
	}

	reg, _ := LoadPersonRegistry(ctx, store)
	got, err := reg.Resolve(ctx, "Mona")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.ID != existing.ID {
		t.Error("did not reuse the person already in the graph")
	}
	if reg.Created() != 0 {
		t.Errorf("created = %d, want 0", reg.Created())
	}
}

func TestPersonRegistryRejectsNonNames(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	reg, _ := LoadPersonRegistry(ctx, store)

	for _, bad := range []string{"", "   ", "Thanks", "Everyone", "Bhai"} {
		if _, err := reg.Resolve(ctx, bad); err == nil {
			t.Errorf("Resolve(%q) succeeded; want an error", bad)
		}
	}
}
