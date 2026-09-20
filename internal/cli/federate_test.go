package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/config"
)

// TestShouldFederate covers when other indices are searched.
//
// Listing indices is the decision to search them, so no flag is needed to
// turn it on; the flags exist to override that for one command.
func TestShouldFederate(t *testing.T) {
	tests := []struct {
		name       string
		configured []string
		federate   bool
		noFederate bool
		want       bool
	}{
		{name: "nothing configured, nothing asked"},
		{
			name:       "configured is enough",
			configured: []string{"~/.CodeEagle"},
			want:       true,
		},
		{
			name:       "--no-federate overrides the configuration",
			configured: []string{"~/.CodeEagle"},
			noFederate: true,
		},
		{
			name:     "--federate works with nothing configured",
			federate: true,
			want:     true,
		},
		{
			name:       "--no-federate beats --federate",
			federate:   true,
			noFederate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Federate: tt.configured}
			if got := shouldFederate(cfg, tt.federate, tt.noFederate); got != tt.want {
				t.Errorf("shouldFederate() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestFederatedName covers labelling results by where they came from. Every
// index lives in a directory called ".CodeEagle", so that name distinguishes
// nothing.
func TestFederatedName(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}

	tests := []struct {
		dir  string
		want string
	}{
		{filepath.Join(home, ".CodeEagle"), "home"},
		{filepath.Join("/srv", "opal-app", ".CodeEagle"), "opal-app"},
		{filepath.Join("/srv", "projects", "space-element", ".CodeEagle"), "space-element"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := federatedName(tt.dir); got != tt.want {
				t.Errorf("federatedName(%q) = %q, want %q", tt.dir, got, tt.want)
			}
		})
	}
}

// TestSameDir covers the guard that stops an index federating with itself,
// which would open one database twice for no gain.
func TestSameDir(t *testing.T) {
	dir := t.TempDir()

	if !sameDir(dir, dir) {
		t.Error("a directory did not match itself")
	}
	if !sameDir(dir, filepath.Join(dir, "sub", "..")) {
		t.Error("a path did not match its own normalised form")
	}
	if sameDir(dir, filepath.Join(dir, "sub")) {
		t.Error("a subdirectory matched its parent")
	}
	if sameDir("", dir) || sameDir(dir, "") {
		t.Error("an empty path matched something")
	}
}

// TestOpenOneFederatedRefusesWhatItCannotSearch covers the checks that run
// before anything is opened, so a query carries on past an index that cannot
// contribute rather than failing.
func TestOpenOneFederatedRefusesWhatItCannotSearch(t *testing.T) {
	// A directory with no graph database at all.
	missing := filepath.Join(t.TempDir(), "nowhere", ".CodeEagle")
	if _, err := openOneFederated(missing, nil, nil); err == nil {
		t.Error("opened an index from a directory that does not exist")
	}

	// A directory that exists but holds nothing.
	empty := filepath.Join(t.TempDir(), ".CodeEagle")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := openOneFederated(empty, nil, nil); err == nil {
		t.Error("opened an index from an empty directory")
	}
}
