package indexer

import (
	"path/filepath"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// TestToRelativePathDistinguishesRepositories covers the path a node's ID is
// derived from.
//
// `NewNodeID` hashes that path, so two repositories both holding `cmd/main.go`
// produced the same ID for two unrelated functions, and whichever was indexed
// second silently overwrote the first. Naming the repository keeps them apart.
func TestToRelativePathDistinguishesRepositories(t *testing.T) {
	alpha := filepath.FromSlash("/src/alpha")
	beta := filepath.FromSlash("/src/beta")

	t.Run("one repository keeps the bare path", func(t *testing.T) {
		idx := &Indexer{repoRoots: []string{alpha}, nonGitRoots: map[string]bool{}}
		got := idx.toRelativePath(filepath.Join(alpha, "cmd", "main.go"))
		if want := filepath.FromSlash("cmd/main.go"); got != want {
			// Changing this would invalidate every ID already indexed, for no
			// gain: there is nothing for a lone repository to collide with.
			t.Errorf("toRelativePath = %q, want %q", got, want)
		}
	})

	t.Run("two repositories are told apart", func(t *testing.T) {
		idx := &Indexer{repoRoots: []string{alpha, beta}, nonGitRoots: map[string]bool{}}
		gotA := idx.toRelativePath(filepath.Join(alpha, "cmd", "main.go"))
		gotB := idx.toRelativePath(filepath.Join(beta, "cmd", "main.go"))
		if gotA == gotB {
			t.Fatalf("both repositories' cmd/main.go resolve to %q", gotA)
		}
		if want := filepath.FromSlash("alpha/cmd/main.go"); gotA != want {
			t.Errorf("alpha resolved to %q, want %q", gotA, want)
		}
		if want := filepath.FromSlash("beta/cmd/main.go"); gotB != want {
			t.Errorf("beta resolved to %q, want %q", gotB, want)
		}
	})

	t.Run("and so get different node IDs", func(t *testing.T) {
		idx := &Indexer{repoRoots: []string{alpha, beta}, nonGitRoots: map[string]bool{}}
		idA := graph.NewNodeID(string(graph.NodeFunction),
			idx.toRelativePath(filepath.Join(alpha, "cmd", "main.go")), "main")
		idB := graph.NewNodeID(string(graph.NodeFunction),
			idx.toRelativePath(filepath.Join(beta, "cmd", "main.go")), "main")
		if idA == idB {
			t.Error("two unrelated main functions share a node ID; one will overwrite the other")
		}
	})

	t.Run("a non-git root is named whether alone or not", func(t *testing.T) {
		docs := filepath.FromSlash("/home/me/Documents")
		idx := &Indexer{
			repoRoots:   []string{docs},
			nonGitRoots: map[string]bool{docs: true},
		}
		got := idx.toRelativePath(filepath.Join(docs, "notes.md"))
		if want := filepath.FromSlash("Documents/notes.md"); got != want {
			t.Errorf("toRelativePath = %q, want %q", got, want)
		}
	})

	t.Run("a path outside every root is left absolute", func(t *testing.T) {
		idx := &Indexer{repoRoots: []string{alpha}, nonGitRoots: map[string]bool{}}
		outside := filepath.FromSlash("/elsewhere/notes.md")
		if got := idx.toRelativePath(outside); got != outside {
			t.Errorf("toRelativePath = %q, want it left alone", got)
		}
	})
}
