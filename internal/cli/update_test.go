package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIsDevVersion(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"dev", true},
		{"dev-abc123", true},
		{"v1.0.0", false},
		{"1.0.0", false},
		{"", false},
		{"develop", false},
	}
	for _, tt := range tests {
		if got := isDevVersion(tt.version); got != tt.want {
			t.Errorf("isDevVersion(%q) = %v, want %v", tt.version, got, tt.want)
		}
	}
}

func TestBuildDownloadURL(t *testing.T) {
	url := buildDownloadURL("v1.2.3", "linux", "amd64")
	want := "https://github.com/imyousuf/CodeEagle/releases/download/v1.2.3/codeeagle-linux-amd64.tar.gz"
	if url != want {
		t.Errorf("buildDownloadURL() = %q, want %q", url, want)
	}
}

func TestReadWriteDateFileIn(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)

	if err := writeDateFileIn(dir, "test-date", now); err != nil {
		t.Fatalf("writeDateFileIn() error: %v", err)
	}

	got, err := readDateFileIn(dir, "test-date")
	if err != nil {
		t.Fatalf("readDateFileIn() error: %v", err)
	}

	if !got.Equal(now) {
		t.Errorf("readDateFileIn() = %v, want %v", got, now)
	}
}

func TestReadDateFileIn_Missing(t *testing.T) {
	dir := t.TempDir()
	_, err := readDateFileIn(dir, "nonexistent")
	if err == nil {
		t.Error("readDateFileIn() expected error for missing file, got nil")
	}
}

func TestShouldCheckNowIn_NoFile(t *testing.T) {
	dir := t.TempDir()
	// No last-check file exists — should return true.
	if !shouldCheckNowIn(dir) {
		t.Error("shouldCheckNowIn() = false with no check file, want true")
	}
}

func TestShouldCheckNowIn_RecentCheck(t *testing.T) {
	dir := t.TempDir()
	// Write a check time from 1 minute ago — should return false (within 6h interval).
	recent := time.Now().Add(-1 * time.Minute)
	if err := writeDateFileIn(dir, devLastCheckFile, recent); err != nil {
		t.Fatalf("writeDateFileIn() error: %v", err)
	}

	if shouldCheckNowIn(dir) {
		t.Error("shouldCheckNowIn() = true with 1-minute-old check, want false")
	}
}

func TestShouldCheckNowIn_StaleCheck(t *testing.T) {
	dir := t.TempDir()
	// Write a check time from 7 hours ago — should return true (past 6h interval).
	stale := time.Now().Add(-7 * time.Hour)
	if err := writeDateFileIn(dir, devLastCheckFile, stale); err != nil {
		t.Fatalf("writeDateFileIn() error: %v", err)
	}

	if !shouldCheckNowIn(dir) {
		t.Error("shouldCheckNowIn() = false with 7-hour-old check, want true")
	}
}

func TestShouldCheckNowIn_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	// Write garbage to the check file — should return true (treat as no file).
	if err := os.WriteFile(filepath.Join(dir, devLastCheckFile), []byte("not-a-date"), 0644); err != nil {
		t.Fatal(err)
	}

	if !shouldCheckNowIn(dir) {
		t.Error("shouldCheckNowIn() = false with corrupt check file, want true")
	}
}

func TestShouldCheckNowIn_ExactBoundary(t *testing.T) {
	dir := t.TempDir()
	// Write a check time from exactly devCheckInterval ago — should return true (>= comparison).
	boundary := time.Now().Add(-devCheckInterval)
	if err := writeDateFileIn(dir, devLastCheckFile, boundary); err != nil {
		t.Fatalf("writeDateFileIn() error: %v", err)
	}

	if !shouldCheckNowIn(dir) {
		t.Error("shouldCheckNowIn() = false at exact boundary, want true")
	}
}
