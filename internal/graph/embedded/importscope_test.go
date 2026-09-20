package embedded

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// meetingCorpus writes a small but representative corpus into a scope.
func meetingCorpus(t *testing.T, s *BranchStore, scope string, meetings ...string) {
	t.Helper()
	orig := s.writeBranch
	s.writeBranch = scope
	defer func() { s.writeBranch = orig }()

	ctx := context.Background()
	for _, name := range meetings {
		meeting := &graph.Node{
			ID:            graph.NewNodeID(string(graph.NodeMeeting), name+".json", name),
			Type:          graph.NodeMeeting,
			Name:          name,
			QualifiedName: name,
			FilePath:      name + ".json",
		}
		if err := s.AddNode(ctx, meeting); err != nil {
			t.Fatalf("add meeting: %v", err)
		}
		speaker := &graph.Node{
			ID:            graph.NewNodeID(string(graph.NodeSpeaker), name+".json", "Person 1"),
			Type:          graph.NodeSpeaker,
			Name:          "Person 1",
			QualifiedName: name + "/Person 1",
			FilePath:      name + ".json",
		}
		if err := s.AddNode(ctx, speaker); err != nil {
			t.Fatalf("add speaker: %v", err)
		}
		if err := s.AddEdge(ctx, &graph.Edge{
			ID:       graph.NewNodeID("edge", meeting.ID, speaker.ID),
			Type:     graph.EdgeContains,
			SourceID: meeting.ID,
			TargetID: speaker.ID,
		}); err != nil {
			t.Fatalf("add edge: %v", err)
		}
	}
}

func countIn(t *testing.T, s *BranchStore, scope string, typ graph.NodeType) int {
	t.Helper()
	counts, err := s.ScopeNodeTypes(scope)
	if err != nil {
		t.Fatalf("count %s: %v", scope, err)
	}
	return counts[typ]
}

// TestImportScopeFromMovesACorpus covers carrying an enriched corpus into
// another database without re-deriving it.
func TestImportScopeFromMovesACorpus(t *testing.T) {
	srcPath := filepath.Join(t.TempDir(), "src")
	src, err := NewBranchStore(srcPath, MeetingScope, []string{MeetingScope})
	if err != nil {
		t.Fatal(err)
	}
	meetingCorpus(t, src, MeetingScope, "standup", "retro")
	src.Close() // released, since the move opens it itself

	dstPath := filepath.Join(t.TempDir(), "dst")
	dst, err := NewBranchStore(dstPath, MeetingScope, []string{MeetingScope})
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()

	res, err := dst.ImportScopeFrom(context.Background(), srcPath, MeetingScope, MeetingScope, false)
	if err != nil {
		t.Fatalf("ImportScopeFrom: %v", err)
	}
	if res.Keys != 4 {
		t.Errorf("reported %d nodes, want 4 (2 meetings + 2 speakers)", res.Keys)
	}
	if got := countIn(t, dst, MeetingScope, graph.NodeMeeting); got != 2 {
		t.Errorf("target holds %d meetings, want 2", got)
	}

	// The edges must come across too, or the speakers are orphans.
	ctx := context.Background()
	meetings, err := dst.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeMeeting})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range meetings {
		speakers, err := dst.GetNeighbors(ctx, m.ID, graph.EdgeContains, graph.Outgoing)
		if err != nil || len(speakers) != 1 {
			t.Errorf("meeting %q has %d speakers, want 1 (%v)", m.Name, len(speakers), err)
		}
	}
}

// TestImportScopeFromMergesRatherThanReplaces covers collecting a corpus that
// is split across two scopes. Clearing the target would make the second run
// destroy what the first brought over.
func TestImportScopeFromMergesRatherThanReplaces(t *testing.T) {
	srcPath := filepath.Join(t.TempDir(), "src")
	src, err := NewBranchStore(srcPath, MeetingScope, []string{MeetingScope})
	if err != nil {
		t.Fatal(err)
	}
	meetingCorpus(t, src, MeetingScope, "standup", "retro")
	meetingCorpus(t, src, "old-branch", "planning")
	src.Close()

	dstPath := filepath.Join(t.TempDir(), "dst")
	dst, err := NewBranchStore(dstPath, MeetingScope, []string{MeetingScope})
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()

	ctx := context.Background()
	if _, err := dst.ImportScopeFrom(ctx, srcPath, MeetingScope, MeetingScope, false); err != nil {
		t.Fatalf("first move: %v", err)
	}
	if _, err := dst.ImportScopeFrom(ctx, srcPath, "old-branch", MeetingScope, false); err != nil {
		t.Fatalf("second move: %v", err)
	}

	if got := countIn(t, dst, MeetingScope, graph.NodeMeeting); got != 3 {
		t.Errorf("target holds %d meetings, want 3: the second move replaced the first", got)
	}
}

// TestImportScopeFromIsRepeatable covers running the same move twice, which a
// person checking whether it worked will do.
func TestImportScopeFromIsRepeatable(t *testing.T) {
	srcPath := filepath.Join(t.TempDir(), "src")
	src, err := NewBranchStore(srcPath, MeetingScope, []string{MeetingScope})
	if err != nil {
		t.Fatal(err)
	}
	meetingCorpus(t, src, MeetingScope, "standup", "retro")
	src.Close()

	dstPath := filepath.Join(t.TempDir(), "dst")
	dst, err := NewBranchStore(dstPath, MeetingScope, []string{MeetingScope})
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()

	ctx := context.Background()
	for range 2 {
		if _, err := dst.ImportScopeFrom(ctx, srcPath, MeetingScope, MeetingScope, false); err != nil {
			t.Fatalf("move: %v", err)
		}
	}
	if got := countIn(t, dst, MeetingScope, graph.NodeMeeting); got != 2 {
		t.Errorf("target holds %d meetings after two identical moves, want 2", got)
	}
}

// TestImportScopeFromLeavesOtherScopesAlone covers the promise that matters
// most: moving meetings in must not disturb the documents already there.
func TestImportScopeFromLeavesOtherScopesAlone(t *testing.T) {
	srcPath := filepath.Join(t.TempDir(), "src")
	src, err := NewBranchStore(srcPath, MeetingScope, []string{MeetingScope})
	if err != nil {
		t.Fatal(err)
	}
	meetingCorpus(t, src, MeetingScope, "standup")
	src.Close()

	dstPath := filepath.Join(t.TempDir(), "dst")
	dst, err := NewBranchStore(dstPath, "default", []string{"default", MeetingScope})
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()

	ctx := context.Background()
	for _, name := range []string{"notes.md", "spec.md"} {
		if err := dst.AddNode(ctx, &graph.Node{
			ID:       graph.NewNodeID(string(graph.NodeDocument), name, name),
			Type:     graph.NodeDocument,
			Name:     name,
			FilePath: name,
		}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := dst.ImportScopeFrom(ctx, srcPath, MeetingScope, MeetingScope, false); err != nil {
		t.Fatalf("move: %v", err)
	}

	if got := countIn(t, dst, "default", graph.NodeDocument); got != 2 {
		t.Errorf("the default scope holds %d documents, want 2 untouched", got)
	}
	if got := countIn(t, dst, MeetingScope, graph.NodeMeeting); got != 1 {
		t.Errorf("the meeting scope holds %d meetings, want 1", got)
	}
}

// TestImportScopeFromRefusesACodebase covers the guard that stops a code graph
// being dragged into the meeting scope, where it would leak into every
// branch's reads.
func TestImportScopeFromRefusesACodebase(t *testing.T) {
	srcPath := filepath.Join(t.TempDir(), "src")
	src, err := NewBranchStore(srcPath, "main", []string{"main"})
	if err != nil {
		t.Fatal(err)
	}
	meetingCorpus(t, src, "main", "standup")
	if err := src.AddNode(context.Background(), &graph.Node{
		ID:       graph.NewNodeID(string(graph.NodeFunction), "server.go", "ServeHTTP"),
		Type:     graph.NodeFunction,
		Name:     "ServeHTTP",
		FilePath: "server.go",
	}); err != nil {
		t.Fatal(err)
	}
	src.Close()

	dstPath := filepath.Join(t.TempDir(), "dst")
	dst, err := NewBranchStore(dstPath, MeetingScope, []string{MeetingScope})
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()

	_, err = dst.ImportScopeFrom(context.Background(), srcPath, "main", MeetingScope, false)
	if err == nil {
		t.Fatal("a scope holding a codebase was moved into the meeting scope")
	}
	if !strings.Contains(err.Error(), "Function") {
		t.Errorf("the refusal does not say what it found: %v", err)
	}
	if got := countIn(t, dst, MeetingScope, graph.NodeMeeting); got != 0 {
		t.Errorf("the target gained %d meetings from a refused move", got)
	}
}

// TestImportScopeFromDryRunChangesNothing covers previewing before committing.
func TestImportScopeFromDryRunChangesNothing(t *testing.T) {
	srcPath := filepath.Join(t.TempDir(), "src")
	src, err := NewBranchStore(srcPath, MeetingScope, []string{MeetingScope})
	if err != nil {
		t.Fatal(err)
	}
	meetingCorpus(t, src, MeetingScope, "standup", "retro")
	src.Close()

	dstPath := filepath.Join(t.TempDir(), "dst")
	dst, err := NewBranchStore(dstPath, MeetingScope, []string{MeetingScope})
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()

	res, err := dst.ImportScopeFrom(context.Background(), srcPath, MeetingScope, MeetingScope, true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if res.Keys != 4 {
		t.Errorf("dry run reported %d nodes, want 4", res.Keys)
	}
	if got := countIn(t, dst, MeetingScope, graph.NodeMeeting); got != 0 {
		t.Errorf("a dry run moved %d meetings", got)
	}
}

// TestImportScopeFromRejectsItsOwnDatabase covers naming the database you are
// already writing to, where the read-only open would deadlock on the lock the
// caller holds.
func TestImportScopeFromRejectsItsOwnDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	store, err := NewBranchStore(path, MeetingScope, []string{MeetingScope})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	_, err = store.ImportScopeFrom(context.Background(), path, "old-branch", MeetingScope, false)
	if err == nil {
		t.Fatal("ImportScopeFrom accepted its own database")
	}
	if !strings.Contains(err.Error(), "same database") {
		t.Errorf("unhelpful refusal: %v", err)
	}
}

// TestImportScopeFromEmptySource covers a scope that does not exist, which is
// what a mistyped name looks like.
func TestImportScopeFromEmptySource(t *testing.T) {
	srcPath := filepath.Join(t.TempDir(), "src")
	src, err := NewBranchStore(srcPath, MeetingScope, []string{MeetingScope})
	if err != nil {
		t.Fatal(err)
	}
	meetingCorpus(t, src, MeetingScope, "standup")
	src.Close()

	dstPath := filepath.Join(t.TempDir(), "dst")
	dst, err := NewBranchStore(dstPath, MeetingScope, []string{MeetingScope})
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()

	res, err := dst.ImportScopeFrom(context.Background(), srcPath, "typo", MeetingScope, false)
	if err != nil {
		t.Fatalf("a missing scope should report nothing found, not fail: %v", err)
	}
	if res.Keys != 0 {
		t.Errorf("found %d nodes under a scope that does not exist", res.Keys)
	}
}
