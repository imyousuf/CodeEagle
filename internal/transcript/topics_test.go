package transcript

import (
	"context"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

func TestCleanTopic(t *testing.T) {
	tests := []struct{ in, want string }{
		{"MCP authentication", "MCP authentication"},
		// Models write a meeting summary into a label; the subject is the part
		// before the explanation.
		{"Standup: Kamur updates on remember/recall tools and code execution DNS issue", "Standup"},
		{"Rebrand — dates and enablement", "Rebrand"},
		{"Pipeline - Q2 status", "Pipeline"},
		// A long list of subjects is not one subject.
		{"OBO, PAT, service-to-service, and authorization code flow", "OBO"},
		// A short comma phrase is left intact.
		{"Retention, renewals", "Retention, renewals"},
		{"   ", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := CleanTopic(tt.in); got != tt.want {
			t.Errorf("CleanTopic(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCleanTopicTruncatesRunOnLabels(t *testing.T) {
	long := "authentication and authorization and tenancy and logging and billing and retries"
	got := CleanTopic(long)
	if len(got) >= len(long) {
		t.Errorf("CleanTopic did not shorten a run-on label: %q", got)
	}
	if got == "" {
		t.Error("CleanTopic discarded a usable label entirely")
	}
}

func TestSameTopic(t *testing.T) {
	same := [][2]string{
		{"MCP authentication", "mcp authentication"},
		// Word order and stop words carry no subject meaning.
		{"authentication for MCP", "MCP authentication"},
		{"Token lifetimes", "token lifetime"},
		{"Rebrand discussion", "Rebrand"},
		// An abbreviation of the same subject.
		{"MCP auth", "MCP authentication"},
	}
	for _, p := range same {
		if !SameTopic(p[0], p[1]) {
			t.Errorf("SameTopic(%q, %q) = false, want true", p[0], p[1])
		}
	}

	// Fusing two subjects is the worse error, so anything genuinely different
	// must stay apart.
	different := [][2]string{
		{"MCP authentication", "MCP logging"},
		{"OAuth token revocation", "OAuth token lifetimes"},
		{"Tenant isolation", "Data residency"},
		{"Canvas sharing", "Canvas extension points"},
		{"Billing", "Bidding"},
		{"", "MCP"},
	}
	for _, p := range different {
		if SameTopic(p[0], p[1]) {
			t.Errorf("SameTopic(%q, %q) = true, want false", p[0], p[1])
		}
	}
}

func TestTopicRegistryMergesWordings(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)

	reg, err := LoadTopicRegistry(ctx, store)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}

	first, err := reg.Resolve(ctx, "MCP authentication")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// A later meeting phrases the same subject differently.
	second, err := reg.Resolve(ctx, "authentication for MCP")
	if err != nil {
		t.Fatalf("resolve variant: %v", err)
	}
	if first.ID != second.ID {
		t.Error("two wordings of one subject produced two topics")
	}

	topics, _ := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeTopic})
	if len(topics) != 1 {
		t.Errorf("topic count = %d, want 1", len(topics))
	}
	// The alternate wording is recorded, so the next meeting resolves it
	// without needing to match again.
	reloaded, _ := store.GetNode(ctx, first.ID)
	if reloaded.Properties[graph.PropAliases] == "" {
		t.Error("alternate wording was not recorded")
	}
}

func TestTopicRegistryKeepsDistinctSubjectsApart(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	reg, _ := LoadTopicRegistry(ctx, store)

	a, _ := reg.Resolve(ctx, "OAuth token revocation")
	b, _ := reg.Resolve(ctx, "OAuth token lifetimes")
	if a.ID == b.ID {
		t.Error("two different subjects were merged")
	}
}

func TestTopicRegistryRejectsEmptyLabels(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	reg, _ := LoadTopicRegistry(ctx, store)

	for _, bad := range []string{"", "   ", ":"} {
		if _, err := reg.Resolve(ctx, bad); err == nil {
			t.Errorf("Resolve(%q) succeeded; want an error", bad)
		}
	}
}

func TestTopicRegistryVocabularyOrdersByUse(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	reg, _ := LoadTopicRegistry(ctx, store)

	for i := 0; i < 3; i++ {
		if _, err := reg.Resolve(ctx, "MCP"); err != nil {
			t.Fatalf("resolve: %v", err)
		}
	}
	if _, err := reg.Resolve(ctx, "Billing"); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// The labels that have proven reusable lead, since that is the list a
	// model is shown and asked to reuse from.
	vocab := reg.Vocabulary(10)
	if len(vocab) < 2 || vocab[0] != "MCP" {
		t.Errorf("vocabulary = %v, want the most-used label first", vocab)
	}
}

func TestApplyLevelBuildsHierarchy(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	reg, _ := LoadTopicRegistry(ctx, store)

	names := []string{"OAuth token revocation", "OAuth token lifetimes", "MCP logging"}
	var items []TopicUsage
	for _, n := range names {
		node, err := reg.Resolve(ctx, n)
		if err != nil {
			t.Fatalf("resolve %q: %v", n, err)
		}
		items = append(items, TopicUsage{Node: node, Name: node.Name, Weight: 1})
	}

	tax := &Taxonomy{Themes: []Theme{
		{
			Name:        "OAuth",
			Description: "Token issuance, lifetime and revocation.",
			Members:     []string{"OAuth token revocation", "OAuth token lifetimes"},
		},
		{
			// A group of one is a rename, not a grouping.
			Name:    "Logging",
			Members: []string{"MCP logging"},
		},
		{
			// A member that was never in the list must not invent a topic.
			Name:    "Invented",
			Members: []string{"something nobody said", "another thing"},
		},
	}}

	created, edges, err := applyLevel(ctx, store, items, tax, 1)
	if err != nil {
		t.Fatalf("apply level: %v", err)
	}
	if created != 1 || edges != 2 {
		t.Errorf("created=%d edges=%d, want 1 concept over 2 topics", created, edges)
	}

	oauth, err := store.GetNode(ctx, graph.NewNodeID(string(graph.NodeTopic), "", "OAuth"))
	if err != nil || oauth == nil {
		t.Fatal("OAuth concept was not created")
	}
	if oauth.Properties[PropTopicLevel] != LevelTheme {
		t.Errorf("concept level = %q, want %q", oauth.Properties[PropTopicLevel], LevelTheme)
	}

	children, err := TopicChildren(ctx, store, oauth.ID)
	if err != nil {
		t.Fatalf("children: %v", err)
	}
	if len(children) != 2 {
		t.Errorf("got %d children, want 2", len(children))
	}
	if _, err := store.GetNode(ctx, graph.NewNodeID(string(graph.NodeTopic), "", "Invented")); err == nil {
		t.Error("a concept was created over topics that do not exist")
	}
}

func TestApplyLevelGivesEachTopicOneParent(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	reg, _ := LoadTopicRegistry(ctx, store)

	var items []TopicUsage
	for _, n := range []string{"Token lifetimes", "Token revocation"} {
		node, _ := reg.Resolve(ctx, n)
		items = append(items, TopicUsage{Node: node, Name: node.Name, Weight: 1})
	}

	// Both concepts claim the same two topics; the second claim must be
	// ignored rather than turning the tree into a graph.
	tax := &Taxonomy{Themes: []Theme{
		{Name: "OAuth", Members: []string{"Token lifetimes", "Token revocation"}},
		{Name: "Security", Members: []string{"Token lifetimes", "Token revocation"}},
	}}
	created, _, err := applyLevel(ctx, store, items, tax, 1)
	if err != nil {
		t.Fatalf("apply level: %v", err)
	}
	if created != 1 {
		t.Errorf("created %d concepts, want 1", created)
	}

	for _, it := range items {
		parents, err := store.GetNeighbors(ctx, it.Node.ID, graph.EdgeContains, graph.Incoming)
		if err != nil {
			t.Fatalf("parents: %v", err)
		}
		n := 0
		for _, p := range parents {
			if p.Type == graph.NodeTopic {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%q has %d parents, want 1", it.Name, n)
		}
	}
}

func TestTopicRootsExcludesGroupedTopics(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	reg, _ := LoadTopicRegistry(ctx, store)

	var items []TopicUsage
	for _, n := range []string{"Token lifetimes", "Token revocation"} {
		node, _ := reg.Resolve(ctx, n)
		items = append(items, TopicUsage{Node: node, Name: node.Name, Weight: 1})
	}
	if _, _, err := applyLevel(ctx, store, items,
		&Taxonomy{Themes: []Theme{{Name: "OAuth", Members: []string{"Token lifetimes", "Token revocation"}}}}, 1); err != nil {
		t.Fatalf("apply level: %v", err)
	}

	roots, err := TopicRoots(ctx, store)
	if err != nil {
		t.Fatalf("roots: %v", err)
	}
	// Only the new concept is ungrouped; its members now sit beneath it.
	if len(roots) != 1 || roots[0].Name != "OAuth" {
		t.Errorf("roots = %v, want just OAuth", rootNames(roots))
	}
}

func rootNames(roots []TopicUsage) []string {
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		out = append(out, r.Name)
	}
	return out
}

func TestTopicMeetingCountCountsEachMeetingOnce(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	people, _ := LoadPersonRegistry(ctx, store)
	w := NewWriter(store, people, WriterOptions{MinConfidence: 0.7})

	res := sampleResult("/tmp/sessions/one/session.json")
	res.Analysis.Topics[0].Parent = "Data platform"
	if _, err := w.Write(ctx, res); err != nil {
		t.Fatalf("write: %v", err)
	}

	topic, err := store.GetNode(ctx, graph.NewNodeID(string(graph.NodeTopic), "", "schema migration"))
	if err != nil || topic == nil {
		t.Fatal("topic was not created")
	}
	// One meeting reaches the topic directly and again through its segment,
	// decision and action item; it must still count once.
	if got := TopicMeetingCount(ctx, store, topic); got != 1 {
		t.Errorf("TopicMeetingCount = %d, want 1", got)
	}

	parent, err := store.GetNode(ctx, graph.NewNodeID(string(graph.NodeTopic), "", "Data platform"))
	if err != nil || parent == nil {
		t.Fatal("parent concept was not created from the topic's parent field")
	}
	// A parent covers everything beneath it, so it is never smaller than a child.
	if got := TopicMeetingCount(ctx, store, parent); got != 1 {
		t.Errorf("parent count = %d, want 1", got)
	}
	children, _ := TopicChildren(ctx, store, parent.ID)
	if len(children) != 1 || children[0].Name != "schema migration" {
		t.Errorf("parent children = %v, want the subject beneath it", children)
	}
}

func TestTargetGroups(t *testing.T) {
	tests := []struct{ in, want int }{
		{1, 3}, {4, 3}, {9, 3}, {25, 5}, {100, 10}, {1000, 20}, {5000, 20},
	}
	for _, tt := range tests {
		if got := targetGroups(tt.in); got != tt.want {
			t.Errorf("targetGroups(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
