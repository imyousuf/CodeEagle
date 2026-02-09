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
	if state.FileTimes != nil {
		t.Errorf("FileTimes = %v, want nil", state.FileTimes)
	}
}

func TestSyncStateSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "sync.state")

	now := time.Now().Truncate(time.Second)
	original := &SyncState{
		LastCommit: "abc123def456",
		Timestamp:  now,
		FileTimes: map[string]time.Time{
			"/path/to/file.go":    now.Add(-time.Hour),
			"/path/to/other.py":   now.Add(-2 * time.Hour),
		},
	}

	if err := original.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadSyncState(path)
	if err != nil {
		t.Fatalf("LoadSyncState: %v", err)
	}

	if loaded.LastCommit != original.LastCommit {
		t.Errorf("LastCommit = %q, want %q", loaded.LastCommit, original.LastCommit)
	}

	if !loaded.Timestamp.Equal(original.Timestamp) {
		t.Errorf("Timestamp = %v, want %v", loaded.Timestamp, original.Timestamp)
	}

	if len(loaded.FileTimes) != len(original.FileTimes) {
		t.Fatalf("len(FileTimes) = %d, want %d", len(loaded.FileTimes), len(original.FileTimes))
	}

	for k, v := range original.FileTimes {
		got, ok := loaded.FileTimes[k]
		if !ok {
			t.Errorf("missing FileTimes key %q", k)
			continue
		}
		if !got.Equal(v) {
			t.Errorf("FileTimes[%q] = %v, want %v", k, got, v)
		}
	}
}

func TestSyncStateOverwrite(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "sync.state")

	first := &SyncState{LastCommit: "first", Timestamp: time.Now()}
	if err := first.Save(path); err != nil {
		t.Fatal(err)
	}

	second := &SyncState{LastCommit: "second", Timestamp: time.Now()}
	if err := second.Save(path); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadSyncState(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.LastCommit != "second" {
		t.Errorf("LastCommit = %q, want %q", loaded.LastCommit, "second")
	}
}
