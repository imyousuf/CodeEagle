package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPruneTargetMapsLikeTheIndexer covers the one thing the prune must get
// right before it deletes anything: the prefix it builds is the path the
// indexer recorded, or it removes nothing -- or the wrong tree.
func TestPruneTargetMapsLikeTheIndexer(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	downloads := filepath.Join(home, "Downloads")
	roots := []string{filepath.Join(home, "Documents"), downloads}
	nonGit := map[string]bool{roots[0]: true, downloads: true}

	tests := []struct {
		name, in, wantPrefix, wantAbs string
	}{
		{"absolute under a non-git root", filepath.Join(downloads, "mark"), "Downloads/mark", filepath.Join(downloads, "mark")},
		{"home-relative", "~/Downloads/mark", "Downloads/mark", filepath.Join(downloads, "mark")},
		{"trailing slash", filepath.Join(downloads, "mark") + "/", "Downloads/mark", filepath.Join(downloads, "mark")},
		{"graph path that is not on disk here", "Downloads/codeeagle-no-such-dir/x", "Downloads/codeeagle-no-such-dir/x", ""},
		{"graph path with slashes around it", "/Downloads-codeeagle-nowhere/", "/Downloads-codeeagle-nowhere", "/Downloads-codeeagle-nowhere"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefix, abs := pruneTarget(roots, nonGit, tt.in)
			if prefix != tt.wantPrefix || abs != tt.wantAbs {
				t.Errorf("pruneTarget(%q) = (%q, %q), want (%q, %q)", tt.in, prefix, abs, tt.wantPrefix, tt.wantAbs)
			}
		})
	}
}
