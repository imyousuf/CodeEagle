package indexer

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLoadSyncStateMissingFile(t *testing.T) {
	state, err := LoadSyncState("/tmp/nonexistent-sync-state-xyz.json")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if state.LastCommit != "" {
		t.Errorf("LastCommit = %q, want empty", state.LastCommit)
	}
}

func TestSyncStateSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "sync.state")

	now := time.Now().Truncate(time.Second)
	original := &SyncState{
		BranchStates: map[string]*BranchSyncState{
			"main": {LastCommit: "abc123def456", Timestamp: now},
		},
	}

	if err := original.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadSyncState(path)
	if err != nil {
		t.Fatalf("LoadSyncState: %v", err)
	}

	bs := loaded.GetBranchState("main")
	if bs.LastCommit != "abc123def456" {
		t.Errorf("BranchStates[main].LastCommit = %q, want %q", bs.LastCommit, "abc123def456")
	}
}

func TestSyncStateOverwrite(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "sync.state")

	first := &SyncState{
		BranchStates: map[string]*BranchSyncState{
			"main": {LastCommit: "first"},
		},
	}
	if err := first.Save(path); err != nil {
		t.Fatal(err)
	}

	second := &SyncState{
		BranchStates: map[string]*BranchSyncState{
			"main": {LastCommit: "second"},
		},
	}
	if err := second.Save(path); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadSyncState(path)
	if err != nil {
		t.Fatal(err)
	}
	bs := loaded.GetBranchState("main")
	if bs.LastCommit != "second" {
		t.Errorf("LastCommit = %q, want %q", bs.LastCommit, "second")
	}
}

func TestBranchStateGetOrCreate(t *testing.T) {
	state := &SyncState{}

	// First call should create.
	bs := state.GetBranchState("feature")
	if bs == nil {
		t.Fatal("GetBranchState returned nil")
	}
	if bs.LastCommit != "" {
		t.Errorf("new branch state should have empty LastCommit")
	}

	// Second call should return same.
	bs.LastCommit = "abc123"
	bs2 := state.GetBranchState("feature")
	if bs2.LastCommit != "abc123" {
		t.Errorf("second call should return same instance")
	}
}

func TestMigrateLegacy(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	state := &SyncState{
		LastCommit: "legacy-commit",
		Timestamp:  now,
	}

	state.MigrateLegacy("main")

	// Legacy fields should be cleared.
	if state.LastCommit != "" {
		t.Errorf("LastCommit should be empty after migration, got %q", state.LastCommit)
	}
	if !state.Timestamp.IsZero() {
		t.Errorf("Timestamp should be zero after migration, got %v", state.Timestamp)
	}

	// Branch state should have the legacy values.
	bs := state.GetBranchState("main")
	if bs.LastCommit != "legacy-commit" {
		t.Errorf("BranchState.LastCommit = %q, want %q", bs.LastCommit, "legacy-commit")
	}
	if !bs.Timestamp.Equal(now) {
		t.Errorf("BranchState.Timestamp = %v, want %v", bs.Timestamp, now)
	}
}

func TestMigrateLegacyNoOp(t *testing.T) {
	// Should not migrate when BranchStates already populated.
	state := &SyncState{
		LastCommit: "legacy",
		BranchStates: map[string]*BranchSyncState{
			"main": {LastCommit: "already-migrated"},
		},
	}
	state.MigrateLegacy("main")
	if state.GetBranchState("main").LastCommit != "already-migrated" {
		t.Error("should not overwrite existing branch state")
	}
}

func TestCleanupStaleBranches(t *testing.T) {
	state := &SyncState{
		BranchStates: map[string]*BranchSyncState{
			"main":      {LastCommit: "abc"},
			"feature-a": {LastCommit: "def"},
			"feature-b": {LastCommit: "ghi"},
		},
	}

	existing := map[string]struct{}{
		"main":      {},
		"feature-a": {},
	}

	cleaned := state.CleanupStaleBranches(existing)
	if len(cleaned) != 1 {
		t.Fatalf("expected 1 cleaned branch, got %d: %v", len(cleaned), cleaned)
	}
	if cleaned[0] != "feature-b" {
		t.Errorf("cleaned branch = %q, want %q", cleaned[0], "feature-b")
	}
	if _, ok := state.BranchStates["feature-b"]; ok {
		t.Error("feature-b should have been removed")
	}
	if _, ok := state.BranchStates["main"]; !ok {
		t.Error("main should still exist")
	}
}
