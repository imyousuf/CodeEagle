package indexer

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// BranchSyncState tracks the sync state for a single branch.
type BranchSyncState struct {
	LastCommit string    `json:"last_commit,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// SyncState tracks the last synchronization point for a repository.
type SyncState struct {
	// BranchStates holds per-branch sync state.
	BranchStates map[string]*BranchSyncState `json:"branch_states,omitempty"`
	// LastImportTime is when the export file was last imported.
	LastImportTime time.Time `json:"last_import_time,omitempty"`
	// FileTimes records file modification times for non-git directories.
	FileTimes map[string]time.Time `json:"file_times,omitempty"`

	// Legacy fields for backward-compatible loading.
	LastCommit string    `json:"last_commit,omitempty"`
	Timestamp  time.Time `json:"timestamp,omitempty"`
}

// GetBranchState returns the state for the given branch, creating it if needed.
func (s *SyncState) GetBranchState(branch string) *BranchSyncState {
	if s.BranchStates == nil {
		s.BranchStates = make(map[string]*BranchSyncState)
	}
	if bs, ok := s.BranchStates[branch]; ok {
		return bs
	}
	bs := &BranchSyncState{}
	s.BranchStates[branch] = bs
	return bs
}

// MigrateLegacy converts flat state fields to branch-aware state on first load.
// If legacy fields are set and BranchStates is empty, moves them into the given branch.
func (s *SyncState) MigrateLegacy(branch string) {
	if s.LastCommit == "" && s.Timestamp.IsZero() {
		return // nothing to migrate
	}
	if s.BranchStates != nil && len(s.BranchStates) > 0 {
		return // already migrated
	}
	bs := s.GetBranchState(branch)
	bs.LastCommit = s.LastCommit
	bs.Timestamp = s.Timestamp
	// Clear legacy fields.
	s.LastCommit = ""
	s.Timestamp = time.Time{}
}

// CleanupStaleBranches removes branch states for branches that no longer exist.
// Returns the list of branches that were cleaned up.
func (s *SyncState) CleanupStaleBranches(existing map[string]struct{}) []string {
	var cleaned []string
	for branch := range s.BranchStates {
		if _, ok := existing[branch]; !ok {
			cleaned = append(cleaned, branch)
			delete(s.BranchStates, branch)
		}
	}
	return cleaned
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
