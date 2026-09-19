package generic

import (
	"testing"
	"time"
)

// TestEnsureDirectoryHierarchyTerminates guards against walking past the
// filesystem root. filepath.Dir("/") is "/", so an absolute path once looped
// forever here, appending "/" until the process ran out of memory — reached
// whenever a file lies outside every configured repository root, which is the
// case for a transcript named directly rather than discovered under one.
func TestEnsureDirectoryHierarchyTerminates(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		wantDirs []string
	}{
		{
			name:     "relative path, as indexing normally produces",
			filePath: "docs/meetings/standup.vtt",
			wantDirs: []string{"docs", "docs/meetings"},
		},
		{
			name:     "absolute path stops at the root",
			filePath: "/srv/recordings/standup.vtt",
			wantDirs: []string{"/srv", "/srv/recordings"},
		},
		{
			name:     "a file directly at the root",
			filePath: "/standup.vtt",
			wantDirs: nil,
		},
		{
			name:     "a bare filename has no hierarchy",
			filePath: "standup.vtt",
			wantDirs: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done := make(chan []string, 1)
			go func() {
				nodes, _ := EnsureDirectoryHierarchy(tt.filePath, make(map[string]bool))
				names := make([]string, 0, len(nodes))
				for _, n := range nodes {
					names = append(names, n.QualifiedName)
				}
				done <- names
			}()

			// A failure here is an infinite loop, so it must not hang the suite.
			select {
			case got := <-done:
				if len(got) != len(tt.wantDirs) {
					t.Fatalf("directories = %v, want %v", got, tt.wantDirs)
				}
				for i := range got {
					if got[i] != tt.wantDirs[i] {
						t.Errorf("directories[%d] = %q, want %q", i, got[i], tt.wantDirs[i])
					}
				}
			case <-time.After(10 * time.Second):
				t.Fatal("EnsureDirectoryHierarchy did not return: it is looping")
			}
		})
	}
}
