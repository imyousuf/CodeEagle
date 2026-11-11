package topics_test

import (
	"context"
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph/memstore"
	"github.com/imyousuf/CodeEagle/pkg/kit/topics"
)

func TestRegistryMergesRewordings(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	r, err := topics.LoadRegistry(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.Resolve(ctx, "MCP authentication")
	if err != nil {
		t.Fatal(err)
	}
	// Each of these names the same subject: other case and punctuation, the
	// words in another order and number, a word-for-word abbreviation, and
	// a descriptive label whose subject comes before the colon.
	for _, wording := range []string{
		"mcp-authentication", "authentication MCP", "MCP authentications",
		"MCP auth", "MCP authentication: who owns the token",
	} {
		got, err := r.Resolve(ctx, wording)
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != first.ID {
			t.Errorf("%q resolved to %q, want the existing topic %q", wording, got.Name, first.Name)
		}
	}
	if r.Created() != 1 {
		t.Errorf("created %d topics, want 1", r.Created())
	}
	n, err := store.GetNode(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n.Properties[topics.PropTopicUses] != "6" {
		t.Errorf("uses = %q, want 6", n.Properties[topics.PropTopicUses])
	}
	// Only a wording that differs after cleaning and normalization is
	// recorded: the hyphenated one and the descriptive one are the label.
	if got := topics.Aliases(n); len(got) != 3 || got[0] != "MCP auth" {
		t.Errorf("wordings = %q, want the three that normalize differently, sorted", got)
	}

	// A registry loaded later resolves a recorded wording directly.
	again, err := topics.LoadRegistry(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := again.Resolve(ctx, "MCP auth"); got.ID != first.ID || again.Created() != 0 {
		t.Errorf("reloaded registry resolved %q to %q", "MCP auth", got.Name)
	}
}

func TestRegistryRefusesNearMisses(t *testing.T) {
	ctx := context.Background()
	r, err := topics.LoadRegistry(ctx, memstore.New())
	if err != nil {
		t.Fatal(err)
	}
	// Close in spelling, different in subject: merging any of these would
	// file one subject's material under another.
	labels := []string{"schema migration", "schema validation", "data migration", "client rollout", "client rollback"}
	seen := map[string]string{}
	for _, l := range labels {
		n, err := r.Resolve(ctx, l)
		if err != nil {
			t.Fatal(err)
		}
		if other, dup := seen[n.ID]; dup {
			t.Errorf("%q merged into %q", l, other)
		}
		seen[n.ID] = l
	}
	if r.Created() != len(labels) {
		t.Errorf("created %d, want %d", r.Created(), len(labels))
	}
	for _, bad := range []string{"", "   ", " - "} {
		if _, err := r.Resolve(ctx, bad); err == nil {
			t.Errorf("Resolve(%q) accepted an empty label", bad)
		}
	}
}

func TestRegistryVocabularyLeadsWithUse(t *testing.T) {
	ctx := context.Background()
	r, _ := topics.LoadRegistry(ctx, memstore.New())
	for _, l := range []string{"billing", "auth", "auth", "auth", "search", "search"} {
		if _, err := r.Resolve(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	got := r.Vocabulary(2)
	if len(got) != 2 || got[0] != "auth" || got[1] != "search" {
		t.Errorf("vocabulary = %v", got)
	}
}

// TestAttachFilesAnyNode pins that topics attach to whatever node names
// them, with the HasTopic edge every other writer uses.
func TestAttachFilesAnyNode(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	r, _ := topics.LoadRegistry(ctx, store)
	for _, id := range []string{"fact-1", "section-1"} {
		topic, err := r.Attach(ctx, id, "Email etiquette")
		if err != nil {
			t.Fatal(err)
		}
		edges, err := store.GetEdges(ctx, id, graph.EdgeHasTopic)
		if err != nil {
			t.Fatal(err)
		}
		want := graph.NewNodeID("edge", id, topic.ID+":"+string(graph.EdgeHasTopic))
		if len(edges) != 1 || edges[0].ID != want || edges[0].TargetID != topic.ID {
			t.Errorf("edges of %s = %+v", id, edges)
		}
	}
	if r.Created() != 1 {
		t.Errorf("created %d topics, want one shared by both nodes", r.Created())
	}
}

func TestLevels(t *testing.T) {
	r, _ := topics.LoadRegistry(context.Background(), memstore.New())
	n := &graph.Node{ID: "t", Type: graph.NodeTopic, Name: "t"}
	if snap, changed := r.DefaultToSubject(n); !changed || snap.Properties[topics.PropTopicLevel] != topics.LevelSubject || snap.Properties[topics.PropTopicDepth] != "0" {
		t.Errorf("DefaultToSubject = %+v, %v", snap, changed)
	}
	if _, changed := r.DefaultToSubject(n); changed {
		t.Error("a subject was defaulted again")
	}
	snap, changed := r.PromoteToTheme(n)
	if !changed || snap.Properties[topics.PropTopicLevel] != topics.LevelTheme || snap.Properties[topics.PropTopicDepth] != "1" {
		t.Errorf("PromoteToTheme = %+v, %v", snap, changed)
	}
	snap.Properties["x"] = "y"
	if n.Properties["x"] != "" {
		t.Error("the snapshot shares the live node's properties")
	}
}

func TestSameTopicAndCleanTopic(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want bool
	}{
		{"MCP auth", "MCP authentication", true},
		{"OAuth server vs MCP server", "MCP server and OAuth server", true},
		{"schema migration", "schema validation", false},
		{"", "", false},
	} {
		if got := topics.SameTopic(tt.a, tt.b); got != tt.want {
			t.Errorf("SameTopic(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
	for in, want := range map[string]string{
		"Standup: Kamur updates on tools":                             "Standup",
		"  Roadmap — Q3 ":                                             "Roadmap",
		"one two three four five six seven eight":                     "one two three four five six",
		"auth, billing, search, onboarding, hiring, infra and others": "auth",
	} {
		if got := topics.CleanTopic(in); got != want {
			t.Errorf("CleanTopic(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewNodes(t *testing.T) {
	nodes, edges := topics.NewNodes([]string{"Auth", " auth ", "Data   Model", ""}, "doc-1")
	if len(nodes) != 2 || nodes[0].Name != "auth" || nodes[1].Name != "data model" {
		t.Fatalf("nodes = %+v", nodes)
	}
	if nodes[0].ID != graph.NewNodeID(string(graph.NodeTopic), "", "auth") {
		t.Errorf("topic id = %s", nodes[0].ID)
	}
	if len(edges) != 2 || edges[1].SourceID != "doc-1" || edges[1].TargetID != nodes[1].ID ||
		edges[1].ID != graph.NewNodeID("edge", "doc-1", nodes[1].ID+":HasTopic") {
		t.Errorf("edges = %+v", edges)
	}
	if topics.Normalize("  Mixed   CASE ") != "mixed case" {
		t.Error("Normalize")
	}
}

func TestNamesHelpers(t *testing.T) {
	if got := topics.NormalizeName("  Jean-Luc_O'Brien "); got != "jean luc obrien" {
		t.Errorf("NormalizeName = %q", got)
	}
	if topics.JaroWinkler("imran", "imran") != 1 || topics.JaroWinkler("", "x") != 0 {
		t.Error("JaroWinkler bounds")
	}
	if s := topics.JaroWinkler("imran", "imron"); s < 0.9 || s >= 1 {
		t.Errorf("JaroWinkler(imran, imron) = %v", s)
	}
}
