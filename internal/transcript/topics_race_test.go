package transcript

import (
	"context"
	"sync"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// TestTopicRegistryConcurrentLevelAndVocabulary reproduces the shape of a real
// `meetings sync`, where one registry is read by the enrichment workers and
// written by the projection goroutine at the same time.
//
// The workers call Vocabulary to build their prompts while the writer marks
// each topic's place in the hierarchy. Both reach the same *graph.Node
// property maps. Mutating one outside the registry's lock is a data race that
// Go turns into an unrecoverable process abort, killing a sync partway with
// whatever it had already written left behind.
//
// Run with -race; without the fix this fails there, and can abort outright.
func TestTopicRegistryConcurrentLevelAndVocabulary(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)

	reg, err := LoadTopicRegistry(ctx, store)
	if err != nil {
		t.Fatalf("LoadTopicRegistry: %v", err)
	}

	names := []string{
		"Authentication", "Retention", "Billing", "Onboarding",
		"Search relevance", "Rate limiting", "Schema migration", "Observability",
	}
	nodes := make([]*graph.Node, 0, len(names))
	for _, name := range names {
		n, err := reg.Resolve(ctx, name)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", name, err)
		}
		nodes = append(nodes, n)
	}

	const rounds = 200
	var wg sync.WaitGroup

	// The writer: promotes and demotes, as placing topics does.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range rounds {
			n := nodes[i%len(nodes)]
			reg.PromoteToTheme(n)
			reg.DefaultToSubject(nodes[(i+1)%len(nodes)])
		}
	}()

	// The workers: read the vocabulary while building a prompt.
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range rounds {
				if got := reg.Vocabulary(20); len(got) == 0 {
					t.Error("vocabulary came back empty")
					return
				}
			}
		}()
	}

	wg.Wait()
}

// TestPromoteToThemeSnapshotIsIndependent checks that the value handed back
// for persisting does not share the live node's property map.
//
// Serializing the live node would read the same map the workers are reading,
// which is the race this exists to avoid.
func TestPromoteToThemeSnapshotIsIndependent(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)

	reg, err := LoadTopicRegistry(ctx, store)
	if err != nil {
		t.Fatalf("LoadTopicRegistry: %v", err)
	}
	node, err := reg.Resolve(ctx, "Authentication")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	snapshot, changed := reg.PromoteToTheme(node)
	if !changed || snapshot == nil {
		t.Fatal("promoting a fresh topic reported no change")
	}
	if snapshot.Properties[PropTopicLevel] != LevelTheme {
		t.Errorf("snapshot level = %q, want %q", snapshot.Properties[PropTopicLevel], LevelTheme)
	}

	snapshot.Properties["scribbled"] = "on the copy"
	if _, ok := node.Properties["scribbled"]; ok {
		t.Error("the snapshot shares the live node's property map")
	}

	// Promoting again is a no-op, so the caller does not write every time.
	if _, changed := reg.PromoteToTheme(node); changed {
		t.Error("promoting an already-promoted topic reported a change")
	}
}
