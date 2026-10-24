package relations

import (
	"context"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/parser/markdown"
)

func indexMarkdown(t *testing.T, store graph.Store, path, content string, props map[string]string) {
	t.Helper()
	res, err := markdown.NewParser().ParseFile(path, []byte(content))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, n := range res.Nodes {
		if n.Properties == nil {
			n.Properties = map[string]string{}
		}
		n.Properties[graph.PropContentHash] = "sha256:" + path
		for k, v := range props {
			n.Properties[k] = v
		}
		if err := store.AddNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range res.Edges {
		if err := store.AddEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
}

var longBody = strings.Repeat("Clients retry idempotent calls with exponential backoff and full jitter. ", 8)

func TestLoadSubjectsExcludesWhatCannotBeASubject(t *testing.T) {
	store, err := embedded.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	indexMarkdown(t, store, "docs/retry.md", "# Retry policy\n\n"+longBody+"\n\n## Limits\n\n"+longBody, nil)
	indexMarkdown(t, store, "docs/stub.md", "# Stub\n\nSee elsewhere.\n", nil)
	indexMarkdown(t, store, "notes/meeting.md", "# Standup\n\n"+longBody, map[string]string{graph.PropIsTranscript: "true"})
	indexMarkdown(t, store, "docs/copy.md", "# Retry policy copy\n\n"+longBody, nil)
	indexMarkdown(t, store, "private/plan.md", "# Plan\n\n"+longBody, nil)
	if err := store.AddEdge(context.Background(), &graph.Edge{
		ID: "dup", Type: graph.EdgeDuplicateOf,
		SourceID: graph.NewNodeID(string(graph.NodeDocument), "docs/copy.md", "docs/copy.md"),
		TargetID: graph.NewNodeID(string(graph.NodeDocument), "docs/retry.md", "docs/retry.md"),
	}); err != nil {
		t.Fatal(err)
	}

	subjects, err := LoadSubjects(context.Background(), store, func(p string) bool { return !strings.HasPrefix(p, "private/") })
	if err != nil {
		t.Fatal(err)
	}
	if len(subjects) != 1 || subjects[0].Path != "docs/retry.md" {
		var got []string
		for _, s := range subjects {
			got = append(got, s.Path)
		}
		t.Fatalf("subjects = %v, want only docs/retry.md (stub, transcript, duplicate and excluded path left out)", got)
	}
	s := subjects[0]
	if s.Title != "Retry policy" {
		t.Errorf("title = %q", s.Title)
	}
	if len(s.Sections) != 2 || s.Sections[1].HeadingPath != "Retry policy > Limits" || s.Sections[1].Hash == "" {
		t.Errorf("sections = %+v", s.Sections)
	}
}

func TestLoadMeetingsGathersSegmentsAndDecisions(t *testing.T) {
	store, err := embedded.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	add := func(n *graph.Node) {
		if err := store.AddNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	add(&graph.Node{ID: "m", Type: graph.NodeMeeting, Name: "Platform sync", QualifiedName: "s1", UpdatedAt: day,
		Properties: map[string]string{graph.PropMeetingID: "s1", graph.PropContentHash: "c1", "mentions": "PLAT-412"}})
	add(&graph.Node{ID: "half", Type: graph.NodeMeeting, Name: "Abandoned",
		Properties: map[string]string{graph.PropMeetingID: "s2", graph.PropIncomplete: "true"}})
	add(&graph.Node{ID: "g2", Type: graph.NodeTopicSegment, Name: "Later",
		Properties: map[string]string{graph.PropMeetingID: "s1", graph.PropStartTime: "300", graph.PropEndTime: "400"}})
	add(&graph.Node{ID: "g1", Type: graph.NodeTopicSegment, Name: "Retry cap",
		Properties: map[string]string{graph.PropMeetingID: "s1", graph.PropStartTime: "10", graph.PropEndTime: "90",
			graph.PropSummary: "raised the cap"}})
	add(&graph.Node{ID: "d", Type: graph.NodeDecision, Name: "cap is five",
		Properties: map[string]string{graph.PropMeetingID: "s1", graph.PropSummary: "cap is five", graph.PropQuote: "let's make it five"}})

	meetings, err := LoadMeetings(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := meetings["s2"]; ok {
		t.Error("a meeting written partway was loaded")
	}
	m := meetings["s1"]
	if m == nil || m.ContentHash != "c1" || m.Mentions != "PLAT-412" {
		t.Fatalf("meeting = %+v", m)
	}
	if len(m.Segments) != 2 || m.Segments[0].Name != "Retry cap" || m.Segments[0].Start != 10 {
		t.Errorf("segments not loaded in time order: %+v", m.Segments)
	}
	if m.Segments[0].Meeting() != m {
		t.Error("a segment does not know its meeting")
	}
	if len(m.Decisions) != 1 || m.Decisions[0].Quote != "let's make it five" {
		t.Errorf("decisions = %+v", m.Decisions)
	}
}
