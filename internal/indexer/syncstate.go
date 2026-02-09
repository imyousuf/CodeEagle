package indexer

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// SyncState tracks the last synchronization point for a repository.
type SyncState struct {
	// LastCommit is the git commit hash at the last sync (for git repos).
	LastCommit string `json:"last_commit,omitempty"`
	// Timestamp is when the last sync occurred.
	Timestamp time.Time `json:"timestamp"`
	// FileTimes records file modification times for non-git directories.
	FileTimes map[string]time.Time `json:"file_times,omitempty"`
}

// LoadSyncState reads sync state from the given file path.
// Returns an empty state (no error) if the file does not exist.
func LoadSyncState(path string) (*SyncState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &SyncState{}, nil
		}
		return nil, fmt.Errorf("read sync state: %w", err)
	}
	var state SyncState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("unmarshal sync state: %w", err)
	}
	return &state, nil
}

// Save writes the sync state to the given file path.
func (s *SyncState) Save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal sync state: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write sync state: %w", err)
	}
	return nil
}
