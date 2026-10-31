package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

// countingVectors has no vectors and counts the lookups, which any run that
// gets as far as proposing makes.
type countingVectors struct{ lookups int }

func (c *countingVectors) Vector(string) ([]float32, bool) {
	c.lookups++
	return nil, false
}

// TestAutoRelationsDoesNothingWhenOff covers the automatic run in sync and
// meetings sync: without a decision model, or with relations turned off, it
// reads nothing, calls nothing and writes nothing.
func TestAutoRelationsDoesNothingWhenOff(t *testing.T) {
	off := false
	for name, cfg := range map[string]*config.Config{
		"no decision model": {},
		"turned off": {
			Relations:   config.RelationsConfig{Enabled: &off},
			Transcripts: config.TranscriptsConfig{JevAPIKey: "k"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			store, err := embedded.NewBranchStore(t.TempDir(), "default", []string{"default"})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			ctx := context.Background()
			meetings := store.View(embedded.MeetingScope, []string{embedded.MeetingScope})
			for _, n := range []*graph.Node{
				{ID: "m1", Type: graph.NodeMeeting, Name: "Platform sync",
					Properties: map[string]string{graph.PropMeetingID: "s1"}},
				{ID: "g1", Type: graph.NodeTopicSegment, Name: "Retry cap",
					Properties: map[string]string{graph.PropMeetingID: "s1", graph.PropSummary: "Raise the retry cap."}},
			} {
				if err := meetings.AddNode(ctx, n); err != nil {
					t.Fatal(err)
				}
			}

			vec := &countingVectors{}
			var said []string
			log := func(f string, _ ...any) { said = append(said, f) }
			autoRelations(ctx, cfg, store, nil, vec, log, log)

			if vec.lookups != 0 || len(said) != 0 {
				t.Errorf("%d vector lookups and %d messages: %v", vec.lookups, len(said), said)
			}
			types, err := store.ScopeNodeTypes(embedded.RelationScope)
			if err != nil {
				t.Fatal(err)
			}
			if len(types) != 0 {
				t.Errorf("wrote to %s: %v", embedded.RelationScope, types)
			}
		})
	}
}

// TestRelationsResetTouchesOnlyRelations covers relations --reset: the whole
// relations scope goes, and documents and meetings stay.
func TestRelationsResetTouchesOnlyRelations(t *testing.T) {
	cfg := &config.Config{Graph: config.GraphConfig{DBPath: t.TempDir()}}
	store, _, err := openBranchStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for scope, n := range map[string]*graph.Node{
		store.WriteBranch():    {ID: "d1", Type: graph.NodeDocument, Name: "spec.md", FilePath: "spec.md"},
		embedded.MeetingScope:  {ID: "m1", Type: graph.NodeMeeting, Name: "Platform sync"},
		embedded.RelationScope: {ID: "r1", Type: graph.NodeRelation, Name: "spec.md"},
	} {
		if err := store.View(scope, []string{scope}).AddNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	store.Close()

	var out strings.Builder
	if err := runRelationsReset(&out, cfg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Deleted 1 relations") {
		t.Errorf("output = %q", out.String())
	}
	store, _, err = openBranchStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for id, want := range map[string]bool{"d1": true, "m1": true, "r1": false} {
		n, _ := store.GetNode(ctx, id)
		if (n != nil) != want {
			t.Errorf("%s present = %v, want %v", id, n != nil, want)
		}
	}
}
