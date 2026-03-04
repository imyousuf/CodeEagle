package linker

import (
	"context"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

func TestLinkCalls_CrossFileResolution(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Set up two Go functions in different files of the same package.
	// "ParseFile" calls "ExtractDocument" (cross-file, same package).
	caller := &graph.Node{
		ID:       graph.NewNodeID(string(graph.NodeMethod), "internal/parser/generic/parser.go", "GenericParser.ParseFile"),
		Type:     graph.NodeMethod,
		Name:     "ParseFile",
		FilePath: "internal/parser/generic/parser.go",
		Package:  "generic",
		Language: "go",
		Properties: map[string]string{
			"receiver":         "GenericParser",
			"unresolved_calls": "ExtractDocument,detectMIMEType",
		},
	}
	target1 := &graph.Node{
		ID:       graph.NewNodeID(string(graph.NodeFunction), "internal/parser/generic/document.go", "ExtractDocument"),
		Type:     graph.NodeFunction,
		Name:     "ExtractDocument",
		FilePath: "internal/parser/generic/document.go",
		Package:  "generic",
		Language: "go",
		Exported: true,
	}
	target2 := &graph.Node{
		ID:       graph.NewNodeID(string(graph.NodeFunction), "internal/parser/generic/parser.go", "detectMIMEType"),
		Type:     graph.NodeFunction,
		Name:     "detectMIMEType",
		FilePath: "internal/parser/generic/parser.go", // same file — should NOT be linked
		Package:  "generic",
		Language: "go",
	}

	addNodes(t, store, caller, target1, target2)

	linker := NewLinker(store, nil, nil, false)
	count, err := linker.linkCalls(ctx)
	if err != nil {
		t.Fatalf("linkCalls: %v", err)
	}

	// Should resolve ExtractDocument (cross-file) but not detectMIMEType (same file).
	if count != 1 {
		t.Errorf("expected 1 linked call, got %d", count)
	}

	// Verify the edge was created.
	edges, err := store.GetEdges(ctx, target1.ID, graph.EdgeCalls)
	if err != nil {
		t.Fatalf("GetEdges: %v", err)
	}
	found := false
	for _, e := range edges {
		if e.SourceID == caller.ID && e.TargetID == target1.ID {
			found = true
			if e.Properties["kind"] != "cross_file" {
				t.Errorf("expected kind=cross_file, got %q", e.Properties["kind"])
			}
		}
	}
	if !found {
		t.Error("expected Calls edge from ParseFile to ExtractDocument")
	}

	// Verify unresolved_calls property was cleared on caller.
	updatedCaller, err := store.GetNode(ctx, caller.ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if v, ok := updatedCaller.Properties["unresolved_calls"]; ok {
		t.Errorf("expected unresolved_calls to be cleared, got %q", v)
	}
}

func TestLinkCalls_NoMatch(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Function with unresolved call to a name that doesn't exist in the package.
	caller := &graph.Node{
		ID:       graph.NewNodeID(string(graph.NodeFunction), "pkg/foo.go", "DoWork"),
		Type:     graph.NodeFunction,
		Name:     "DoWork",
		FilePath: "pkg/foo.go",
		Package:  "foo",
		Language: "go",
		Properties: map[string]string{
			"unresolved_calls": "nonExistentFunc",
		},
	}

	addNodes(t, store, caller)

	linker := NewLinker(store, nil, nil, false)
	count, err := linker.linkCalls(ctx)
	if err != nil {
		t.Fatalf("linkCalls: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 linked calls, got %d", count)
	}
}

func TestLinkCalls_MultipleTargets_PreferSameDir(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	caller := &graph.Node{
		ID:       graph.NewNodeID(string(graph.NodeFunction), "internal/parser/generic/parser.go", "doStuff"),
		Type:     graph.NodeFunction,
		Name:     "doStuff",
		FilePath: "internal/parser/generic/parser.go",
		Package:  "generic",
		Language: "go",
		Properties: map[string]string{
			"unresolved_calls": "helperFunc",
		},
	}
	// Target in same directory (preferred).
	sameDir := &graph.Node{
		ID:       graph.NewNodeID(string(graph.NodeFunction), "internal/parser/generic/helpers.go", "helperFunc"),
		Type:     graph.NodeFunction,
		Name:     "helperFunc",
		FilePath: "internal/parser/generic/helpers.go",
		Package:  "generic",
		Language: "go",
	}
	// Target in different directory but same package (unlikely but possible with test setup).
	diffDir := &graph.Node{
		ID:       graph.NewNodeID(string(graph.NodeFunction), "internal/other/generic/utils.go", "helperFunc"),
		Type:     graph.NodeFunction,
		Name:     "helperFunc",
		FilePath: "internal/other/generic/utils.go",
		Package:  "generic",
		Language: "go",
	}

	addNodes(t, store, caller, sameDir, diffDir)

	linker := NewLinker(store, nil, nil, false)
	count, err := linker.linkCalls(ctx)
	if err != nil {
		t.Fatalf("linkCalls: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 linked call, got %d", count)
	}

	// Verify it linked to the same-directory target.
	edges, err := store.GetEdges(ctx, sameDir.ID, graph.EdgeCalls)
	if err != nil {
		t.Fatalf("GetEdges: %v", err)
	}
	found := false
	for _, e := range edges {
		if e.SourceID == caller.ID && e.TargetID == sameDir.ID {
			found = true
		}
	}
	if !found {
		t.Error("expected Calls edge to same-directory target")
	}
}

func TestLinkCalls_SkipsNonGo(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Python function with unresolved_calls — should be ignored.
	pyFunc := &graph.Node{
		ID:       graph.NewNodeID(string(graph.NodeFunction), "src/app.py", "process"),
		Type:     graph.NodeFunction,
		Name:     "process",
		FilePath: "src/app.py",
		Package:  "app",
		Language: "python",
		Properties: map[string]string{
			"unresolved_calls": "helper",
		},
	}
	pyTarget := &graph.Node{
		ID:       graph.NewNodeID(string(graph.NodeFunction), "src/utils.py", "helper"),
		Type:     graph.NodeFunction,
		Name:     "helper",
		FilePath: "src/utils.py",
		Package:  "app",
		Language: "python",
	}

	addNodes(t, store, pyFunc, pyTarget)

	linker := NewLinker(store, nil, nil, false)
	count, err := linker.linkCalls(ctx)
	if err != nil {
		t.Fatalf("linkCalls: %v", err)
	}
	// Only queries Go nodes, so Python should be skipped.
	if count != 0 {
		t.Errorf("expected 0 linked calls for Python, got %d", count)
	}
}

func TestPickCallTarget(t *testing.T) {
	caller := &graph.Node{
		FilePath: "internal/parser/generic/parser.go",
	}

	t.Run("single different-file candidate", func(t *testing.T) {
		target := &graph.Node{
			ID:       "target1",
			FilePath: "internal/parser/generic/document.go",
		}
		got := pickCallTarget(caller, []*graph.Node{target})
		if got != target {
			t.Error("expected target to be selected")
		}
	})

	t.Run("same-file only returns nil", func(t *testing.T) {
		sameFile := &graph.Node{
			ID:       "same",
			FilePath: "internal/parser/generic/parser.go",
		}
		got := pickCallTarget(caller, []*graph.Node{sameFile})
		if got != nil {
			t.Error("expected nil for same-file candidate")
		}
	})

	t.Run("prefers same directory", func(t *testing.T) {
		sameDir := &graph.Node{
			ID:       "samedir",
			FilePath: "internal/parser/generic/helpers.go",
		}
		diffDir := &graph.Node{
			ID:       "diffdir",
			FilePath: "internal/other/utils.go",
		}
		got := pickCallTarget(caller, []*graph.Node{diffDir, sameDir})
		if got != sameDir {
			t.Errorf("expected same-directory target, got %v", got)
		}
	})
}
