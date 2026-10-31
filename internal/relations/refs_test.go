package relations

import (
	"context"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

var corpusPaths = []string{
	"specs/agent-framework/stickyboard.md",
	"specs/agent-framework/tracker-chat-frontend-federation.md",
	"specs/agent-framework/monitor-remind.md",
	"specs/vau/README.md",
	"specs/tools/README.md",
	"specs/home-slice/opal-7194-plan.md",
}

func TestResolverFindsWhatAuthorsMean(t *testing.T) {
	r := NewResolver(corpusPaths)
	src := "specs/agent-framework/tracker-chat-frontend-federation.md"
	for _, tt := range []struct{ written, want string }{
		{"stickyboard.md", "specs/agent-framework/stickyboard.md"},
		{"./monitor-remind.md", "specs/agent-framework/monitor-remind.md"},
		{"../vau/README.md", "specs/vau/README.md"},
		{"docs/tech-spec/agent-framework/stickyboard.md", "specs/agent-framework/stickyboard.md"},
		{"README.md", ""},                           // two documents share the name
		{"missing.md", ""},                          // nothing by that name
		{"tracker-chat-frontend-federation.md", ""}, // the source itself
		{"https://example.com/stickyboard.md", ""},
	} {
		if got := r.Resolve(src, tt.written); got != tt.want {
			t.Errorf("%q: %q, want %q", tt.written, got, tt.want)
		}
	}
}

func TestReferencesFromLinksAndPaths(t *testing.T) {
	subj := &Subject{Path: "specs/agent-framework/tracker-chat-frontend-federation.md", Sections: []*Section{
		{Heading: "Companions", HeadingPath: "Tracker > Companions",
			Text: "- `docs/tech-spec/agent-framework/stickyboard.md` keeps the backend.\nSee [reminders](./monitor-remind.md#cadence) too."},
	}}
	refs := References(subj, NewResolver(corpusPaths))
	if len(refs) != 2 || refs[0].Target != "specs/agent-framework/monitor-remind.md" || refs[0].Via != "link" ||
		refs[1].Target != "specs/agent-framework/stickyboard.md" || refs[1].Via != "path" {
		t.Fatalf("refs = %+v", refs)
	}
	if refs[1].Section != "Tracker > Companions" || refs[1].Context == "" {
		t.Errorf("section or context missing: %+v", refs[1])
	}
}

// TestRefreshReferencesWritesEdges covers the stored facts: references on
// the relation, and an edge to the named document's relation.
func TestRefreshReferencesWritesEdges(t *testing.T) {
	store, base, _ := incrementalStores(t)
	ctx := context.Background()
	indexMarkdown(t, base, "docs/tracker.md", "# Tracker\n\nThe frontend re-home of `retry.md`, which keeps the backend. "+longBody, nil)
	subjects, err := LoadSubjects(ctx, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	rels := map[string]*Relation{}
	if _, err := EnsureRelations(ctx, store, subjects, rels, day); err != nil {
		t.Fatal(err)
	}
	if n, err := RefreshReferences(ctx, store, subjects, rels); err != nil || n != 1 {
		t.Fatalf("refreshed %d, %v", n, err)
	}
	edges, err := store.GetEdges(ctx, RelationID("docs/tracker.md"), graph.EdgeReferences)
	if err != nil || len(edges) != 1 || edges[0].TargetID != RelationID("docs/retry.md") {
		t.Errorf("edges = %+v, %v", edges, err)
	}
	if n, _ := RefreshReferences(ctx, store, subjects, rels); n != 0 {
		t.Errorf("a second refresh rewrote %d relations", n)
	}
}
