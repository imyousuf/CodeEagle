package linker

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

func meetingTestStore(t *testing.T) graph.Store {
	t.Helper()
	store, err := embedded.NewStore(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func addNode(t *testing.T, store graph.Store, typ graph.NodeType, name string, props map[string]string) *graph.Node {
	t.Helper()
	n := &graph.Node{
		ID:            graph.NewNodeID(string(typ), "", name),
		Type:          typ,
		Name:          name,
		QualifiedName: name,
		Properties:    props,
	}
	if err := store.AddNode(context.Background(), n); err != nil {
		t.Fatalf("add %s %q: %v", typ, name, err)
	}
	return n
}

func mentionTargets(t *testing.T, store graph.Store, nodeID string) []string {
	t.Helper()
	nodes, err := store.GetNeighbors(context.Background(), nodeID, graph.EdgeMentions, graph.Outgoing)
	if err != nil {
		t.Fatalf("get mentions: %v", err)
	}
	names := make([]string, 0, len(nodes))
	for _, n := range nodes {
		names = append(names, n.Name)
	}
	return names
}

func TestLinkMeetingMentions(t *testing.T) {
	ctx := context.Background()
	store := meetingTestStore(t)

	addNode(t, store, graph.NodeService, "schema-service", nil)
	addNode(t, store, graph.NodePackage, "billing", nil)
	addNode(t, store, graph.NodeService, "notifications", nil)

	meeting := addNode(t, store, graph.NodeMeeting, "Migration planning", map[string]string{
		// As spoken, not as spelled in the repository.
		"mentions": "schema service, Billing, something-we-do-not-have",
	})

	l := NewLinker(store, nil, nil, false)
	n, err := l.linkMeetingMentions(ctx)
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if n != 2 {
		t.Errorf("linked %d, want 2", n)
	}

	got := mentionTargets(t, store, meeting.ID)
	want := map[string]bool{"schema-service": true, "billing": true}
	if len(got) != 2 {
		t.Fatalf("mentions = %v, want 2 entries", got)
	}
	for _, name := range got {
		if !want[name] {
			t.Errorf("unexpected mention target %q", name)
		}
	}
}

func TestLinkMeetingMentionsIgnoresShortAndAmbiguousNames(t *testing.T) {
	ctx := context.Background()
	store := meetingTestStore(t)

	// Too short to be distinctive: "api" is said in every meeting.
	addNode(t, store, graph.NodeService, "api", nil)

	// A name shared by many entities identifies none of them.
	for _, pkg := range []string{"a/common", "b/common", "c/common", "d/common"} {
		n := &graph.Node{
			ID:            graph.NewNodeID(string(graph.NodePackage), pkg, "common"),
			Type:          graph.NodePackage,
			Name:          "common",
			QualifiedName: pkg,
		}
		if err := store.AddNode(ctx, n); err != nil {
			t.Fatalf("add package: %v", err)
		}
	}

	meeting := addNode(t, store, graph.NodeMeeting, "Standup", map[string]string{
		"mentions": "api, common",
	})

	l := NewLinker(store, nil, nil, false)
	if _, err := l.linkMeetingMentions(ctx); err != nil {
		t.Fatalf("link: %v", err)
	}
	if got := mentionTargets(t, store, meeting.ID); len(got) != 0 {
		t.Errorf("mentions = %v, want none", got)
	}
}

func TestLinkTopicSegmentMentions(t *testing.T) {
	ctx := context.Background()
	store := meetingTestStore(t)

	addNode(t, store, graph.NodeService, "auth-service", nil)

	// A segment links through its keywords and through its own name.
	segment := addNode(t, store, graph.NodeTopicSegment, "authentication", map[string]string{
		"keywords": "auth service, tokens",
	})
	addNode(t, store, graph.NodeMeeting, "Design review", nil)

	l := NewLinker(store, nil, nil, false)
	if _, err := l.linkMeetingMentions(ctx); err != nil {
		t.Fatalf("link: %v", err)
	}

	got := mentionTargets(t, store, segment.ID)
	if len(got) != 1 || got[0] != "auth-service" {
		t.Errorf("segment mentions = %v, want [auth-service]", got)
	}
}

func TestMentionKeys(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		// A role suffix people drop in conversation yields a second key.
		{"schema-service", []string{"schema service", "schema"}},
		{"billing_api", []string{"billing api", "billing"}},
		{"notifications", []string{"notifications"}},
		// Too short to be distinctive.
		{"db", nil},
		{"api", nil},
		// Trimming must not produce a stub key.
		{"a-service", []string{"a service"}},
	}
	for _, tt := range tests {
		got := mentionKeys(tt.in)
		if len(got) != len(tt.want) {
			t.Errorf("mentionKeys(%q) = %v, want %v", tt.in, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("mentionKeys(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
			}
		}
	}
}

func TestNormalizeMention(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Schema-Service", "schema service"},
		{"internal/graph", "internal graph"},
		{"  MCP_Proxy  ", "mcp proxy"},
		{"v2.api", "v2 api"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := normalizeMention(tt.in); got != tt.want {
			t.Errorf("normalizeMention(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestLinkMeetingAttendanceSumsSplitLabels(t *testing.T) {
	ctx := context.Background()
	store := meetingTestStore(t)

	meeting := addNode(t, store, graph.NodeMeeting, "Planning", nil)
	person := addNode(t, store, graph.NodePerson, "Mona", nil)

	// One person, heard on two labels — which is what diarization produces
	// when a voice is split, and what a later hand assignment adds.
	for _, sp := range []struct {
		label string
		secs  string
	}{{"Person 1", "600.0"}, {"Person 7", "240.0"}} {
		n := &graph.Node{
			ID:            graph.NewNodeID(string(graph.NodeSpeaker), "/s/session.json", sp.label),
			Type:          graph.NodeSpeaker,
			Name:          sp.label,
			QualifiedName: "sess:" + sp.label,
			FilePath:      "/s/session.json",
			Properties:    map[string]string{graph.PropSpeakingSeconds: sp.secs},
		}
		if err := store.AddNode(ctx, n); err != nil {
			t.Fatalf("add speaker: %v", err)
		}
		addEdge(t, store, graph.EdgeContains, meeting.ID, n.ID)
		addEdge(t, store, graph.EdgeIdentifiedAs, n.ID, person.ID)
	}

	l := NewLinker(store, nil, nil, false)
	n, err := l.linkMeetingAttendance(ctx)
	if err != nil {
		t.Fatalf("link attendance: %v", err)
	}
	if n != 1 {
		t.Errorf("wrote %d attendance edges, want 1", n)
	}

	edges, err := store.GetEdges(ctx, person.ID, graph.EdgeAttended)
	if err != nil {
		t.Fatalf("get edges: %v", err)
	}
	var found *graph.Edge
	for _, e := range edges {
		if e.SourceID == person.ID && e.TargetID == meeting.ID {
			found = e
		}
	}
	if found == nil {
		t.Fatal("no attendance edge written")
	}
	// Both labels must be credited, not just one.
	if got := found.Properties[graph.PropSpeakingSeconds]; got != "840.0" {
		t.Errorf("speaking seconds = %q, want 840.0 (600 + 240)", got)
	}
	if got := found.Properties[graph.PropSpeakerLabel]; got != "Person 1, Person 7" {
		t.Errorf("labels = %q, want both", got)
	}
}

func TestLinkMeetingAttendanceIgnoresUnidentifiedSpeakers(t *testing.T) {
	ctx := context.Background()
	store := meetingTestStore(t)

	meeting := addNode(t, store, graph.NodeMeeting, "Standup", nil)
	anon := &graph.Node{
		ID:         graph.NewNodeID(string(graph.NodeSpeaker), "/s/session.json", "Person 4"),
		Type:       graph.NodeSpeaker,
		Name:       "Person 4",
		FilePath:   "/s/session.json",
		Properties: map[string]string{graph.PropSpeakingSeconds: "300.0"},
	}
	if err := store.AddNode(ctx, anon); err != nil {
		t.Fatalf("add speaker: %v", err)
	}
	addEdge(t, store, graph.EdgeContains, meeting.ID, anon.ID)

	l := NewLinker(store, nil, nil, false)
	n, err := l.linkMeetingAttendance(ctx)
	if err != nil {
		t.Fatalf("link attendance: %v", err)
	}
	// An unidentified speaker attended, but there is no person to credit.
	if n != 0 {
		t.Errorf("wrote %d attendance edges, want 0", n)
	}
}

// addEdge links two nodes for test setup.
func addEdge(t *testing.T, store graph.Store, typ graph.EdgeType, from, to string) {
	t.Helper()
	e := &graph.Edge{
		ID:       graph.NewNodeID("edge", from, to+":"+string(typ)),
		Type:     typ,
		SourceID: from,
		TargetID: to,
	}
	if err := store.AddEdge(context.Background(), e); err != nil {
		t.Fatalf("add edge %s: %v", typ, err)
	}
}

// addMeetingWith creates a meeting attended by the given people.
func addMeetingWith(t *testing.T, store graph.Store, name string, when time.Time, people ...*graph.Node) *graph.Node {
	t.Helper()
	m := &graph.Node{
		ID:            graph.NewNodeID(string(graph.NodeMeeting), "/s/"+name, name),
		Type:          graph.NodeMeeting,
		Name:          name,
		QualifiedName: name,
		UpdatedAt:     when,
	}
	if err := store.AddNode(context.Background(), m); err != nil {
		t.Fatalf("add meeting: %v", err)
	}
	for _, p := range people {
		addEdge(t, store, graph.EdgeAttended, p.ID, m.ID)
	}
	return m
}

func followsUpTarget(t *testing.T, store graph.Store, from string) string {
	t.Helper()
	nodes, err := store.GetNeighbors(context.Background(), from, graph.EdgeFollowsUp, graph.Outgoing)
	if err != nil {
		t.Fatalf("get follows-up: %v", err)
	}
	if len(nodes) == 0 {
		return ""
	}
	return nodes[0].Name
}

func TestLinkMeetingSeriesThreadsRecurringMeetings(t *testing.T) {
	ctx := context.Background()
	store := meetingTestStore(t)

	imran := addNode(t, store, graph.NodePerson, "Imran", nil)
	jeremiah := addNode(t, store, graph.NodePerson, "Jeremiah", nil)

	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	// The same one-to-one three weeks running. Titles differ between
	// instances, as a model's titles do; the people are what recurs.
	addMeetingWith(t, store, "1:1 retrieval status", base, imran, jeremiah)
	second := addMeetingWith(t, store, "1:1 Q2 planning", base.AddDate(0, 0, 7), imran, jeremiah)
	third := addMeetingWith(t, store, "1:1 hiring and RAG", base.AddDate(0, 0, 14), imran, jeremiah)

	l := NewLinker(store, nil, nil, false)
	n, err := l.linkMeetingSeries(ctx)
	if err != nil {
		t.Fatalf("link series: %v", err)
	}
	if n != 2 {
		t.Errorf("linked %d, want 2 (each instance to its predecessor)", n)
	}

	// Each instance points back to the one before it, so a thread can be walked.
	if got := followsUpTarget(t, store, third.ID); got != "1:1 Q2 planning" {
		t.Errorf("third follows %q, want the second instance", got)
	}
	if got := followsUpTarget(t, store, second.ID); got != "1:1 retrieval status" {
		t.Errorf("second follows %q, want the first instance", got)
	}
}

func TestLinkMeetingSeriesIgnoresDifferentGroups(t *testing.T) {
	ctx := context.Background()
	store := meetingTestStore(t)

	imran := addNode(t, store, graph.NodePerson, "Imran", nil)
	jeremiah := addNode(t, store, graph.NodePerson, "Jeremiah", nil)
	mona := addNode(t, store, graph.NodePerson, "Mona", nil)

	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	addMeetingWith(t, store, "1:1 with Jeremiah", base, imran, jeremiah)
	other := addMeetingWith(t, store, "1:1 with Mona", base.AddDate(0, 0, 1), imran, mona)

	l := NewLinker(store, nil, nil, false)
	if _, err := l.linkMeetingSeries(ctx); err != nil {
		t.Fatalf("link series: %v", err)
	}
	// Different people means a different standing meeting.
	if got := followsUpTarget(t, store, other.ID); got != "" {
		t.Errorf("unrelated meetings were threaded together: %q", got)
	}
}

func TestLinkMeetingSeriesRequiresTwoParticipants(t *testing.T) {
	ctx := context.Background()
	store := meetingTestStore(t)

	imran := addNode(t, store, graph.NodePerson, "Imran", nil)
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

	// Solo recordings share exactly one participant. Threading on that would
	// chain together every recording their owner ever made.
	addMeetingWith(t, store, "note to self 1", base, imran)
	second := addMeetingWith(t, store, "note to self 2", base.AddDate(0, 0, 1), imran)

	l := NewLinker(store, nil, nil, false)
	n, err := l.linkMeetingSeries(ctx)
	if err != nil {
		t.Fatalf("link series: %v", err)
	}
	if n != 0 {
		t.Errorf("linked %d, want 0 for single-participant recordings", n)
	}
	if got := followsUpTarget(t, store, second.ID); got != "" {
		t.Errorf("solo recordings were threaded: %q", got)
	}
}

func TestLinkMeetingSeriesBreaksOnLongGaps(t *testing.T) {
	ctx := context.Background()
	store := meetingTestStore(t)

	imran := addNode(t, store, graph.NodePerson, "Imran", nil)
	jeremiah := addNode(t, store, graph.NodePerson, "Jeremiah", nil)

	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	addMeetingWith(t, store, "old 1:1", base, imran, jeremiah)
	// Revived six months later: a different thread, not a continuation.
	revived := addMeetingWith(t, store, "revived 1:1", base.AddDate(0, 6, 0), imran, jeremiah)

	l := NewLinker(store, nil, nil, false)
	if _, err := l.linkMeetingSeries(ctx); err != nil {
		t.Fatalf("link series: %v", err)
	}
	if got := followsUpTarget(t, store, revived.ID); got != "" {
		t.Errorf("series was threaded across a six-month gap: %q", got)
	}
}
