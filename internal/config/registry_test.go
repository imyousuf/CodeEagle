package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryRoundTrip(t *testing.T) {
	// Use a temp dir as HOME so we don't modify the real registry.
	tmpHome := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpHome)
	defer func() { _ = os.Setenv("HOME", origHome) }()

	regPath := RegistryPath()
	if regPath == "" {
		t.Fatal("RegistryPath() returned empty")
	}
	wantPath := filepath.Join(tmpHome, registryFileName)
	if regPath != wantPath {
		t.Errorf("RegistryPath() = %q, want %q", regPath, wantPath)
	}

	// Initially no projects.
	entries := ListProjects()
	if len(entries) != 0 {
		t.Errorf("ListProjects() = %d entries, want 0", len(entries))
	}

	// Register a project.
	err := RegisterProject("opal-app", "/home/user/opal-app", "/home/user/opal-app/.CodeEagle")
	if err != nil {
		t.Fatalf("RegisterProject() error: %v", err)
	}

	entries = ListProjects()
	if len(entries) != 1 {
		t.Fatalf("ListProjects() = %d entries, want 1", len(entries))
	}
	if entries[0].Name != "opal-app" {
		t.Errorf("Name = %q, want %q", entries[0].Name, "opal-app")
	}
	if entries[0].Root != "/home/user/opal-app" {
		t.Errorf("Root = %q, want %q", entries[0].Root, "/home/user/opal-app")
	}
	if entries[0].ConfigDir != "/home/user/opal-app/.CodeEagle" {
		t.Errorf("ConfigDir = %q, want %q", entries[0].ConfigDir, "/home/user/opal-app/.CodeEagle")
	}

	// Register a second project.
	err = RegisterProject("shared-lib", "/home/user/shared-lib", "/home/user/shared-lib/.CodeEagle")
	if err != nil {
		t.Fatalf("RegisterProject() error: %v", err)
	}

	entries = ListProjects()
	if len(entries) != 2 {
		t.Fatalf("ListProjects() = %d entries, want 2", len(entries))
	}

	// Update existing project (same root, new name).
	err = RegisterProject("opal-v2", "/home/user/opal-app", "/home/user/opal-app/.CodeEagle")
	if err != nil {
		t.Fatalf("RegisterProject() update error: %v", err)
	}

	entries = ListProjects()
	if len(entries) != 2 {
		t.Fatalf("ListProjects() = %d entries after update, want 2", len(entries))
	}
	// Find the updated entry.
	found := false
	for _, e := range entries {
		if e.Root == "/home/user/opal-app" {
			if e.Name != "opal-v2" {
				t.Errorf("updated Name = %q, want %q", e.Name, "opal-v2")
			}
			found = true
		}
	}
	if !found {
		t.Error("updated entry not found")
	}
}

func TestRegisterProjectDefaultName(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Empty name should default to filepath.Base(root).
	err := RegisterProject("", "/home/user/my-project", "/home/user/my-project/.CodeEagle")
	if err != nil {
		t.Fatalf("RegisterProject() error: %v", err)
	}

	entries := ListProjects()
	if len(entries) != 1 {
		t.Fatalf("ListProjects() = %d entries, want 1", len(entries))
	}
	if entries[0].Name != "my-project" {
		t.Errorf("Name = %q, want %q", entries[0].Name, "my-project")
	}
}

func TestLookupProject(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Register a project.
	if err := RegisterProject("test-proj", "/home/user/test-proj", "/home/user/test-proj/.CodeEagle"); err != nil {
		t.Fatalf("RegisterProject() error: %v", err)
	}

	// Exact root match.
	entry, ok := LookupProject("/home/user/test-proj")
	if !ok {
		t.Fatal("LookupProject() not found for exact root")
	}
	if entry.Name != "test-proj" {
		t.Errorf("Name = %q, want %q", entry.Name, "test-proj")
	}

	// Subdirectory match.
	entry, ok = LookupProject("/home/user/test-proj/pkg/foo")
	if !ok {
		t.Fatal("LookupProject() not found for subdirectory")
	}
	if entry.Name != "test-proj" {
		t.Errorf("Name = %q, want %q", entry.Name, "test-proj")
	}

	// No match.
	_, ok = LookupProject("/home/user/other-proj")
	if ok {
		t.Error("LookupProject() found for unregistered path")
	}
}
