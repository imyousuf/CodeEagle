package linker

import (
	"context"
	"path/filepath"
	"testing"

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
