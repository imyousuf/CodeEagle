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

func TestIsLocalBuild(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		// Official releases — NOT local builds.
		{"v1.0.0", false},
		{"v0.1.0", false},
		{"v12.34.56", false},
		// Dev versions — NOT local builds (handled separately).
		{"dev", false},
		{"dev-abc1234", false},
		// Local builds from git describe — IS local build.
		{"v1.2.0-dirty", true},
		{"v1.2.0-5-g12b180f", true},
		{"v1.2.0-5-g12b180f-dirty", true},
		{"12b180f", true},
		{"12b180f-dirty", true},
		// Edge cases.
		{"", true},           // empty version
		{"v", false},         // just "v" with no digits — technically clean (degenerate)
		{"1.0.0", true},      // no v prefix
		{"latest", true},     // random string
		{"v1.0.0-rc1", true}, // pre-release suffix
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			if got := isLocalBuild(tt.version); got != tt.want {
				t.Errorf("isLocalBuild(%q) = %v, want %v", tt.version, got, tt.want)
			}
		})
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

func TestShouldUpdateDevDates(t *testing.T) {
	now := time.Now()
	hourAgo := now.Add(-1 * time.Hour)
	dayAgo := now.Add(-24 * time.Hour)
	hourLater := now.Add(1 * time.Hour)

	tests := []struct {
		name       string
		remoteDate time.Time
		localDate  time.Time
		binModTime time.Time
		want       bool
	}{
		{
			name:       "remote newer than both local and binary",
			remoteDate: hourLater,
			localDate:  dayAgo,
			binModTime: hourAgo,
			want:       true,
		},
		{
			name:       "remote newer than local but older than binary (local build)",
			remoteDate: hourAgo,
			localDate:  dayAgo,
			binModTime: now,
			want:       false,
		},
		{
			name:       "remote older than local date",
			remoteDate: dayAgo,
			localDate:  hourAgo,
			binModTime: hourAgo,
			want:       false,
		},
		{
			name:       "remote equal to local date",
			remoteDate: now,
			localDate:  now,
			binModTime: hourAgo,
			want:       false,
		},
		{
			name:       "remote equal to binary mtime",
			remoteDate: now,
			localDate:  dayAgo,
			binModTime: now,
			want:       false,
		},
		{
			name:       "zero binary mtime skips binary check",
			remoteDate: hourAgo,
			localDate:  dayAgo,
			binModTime: time.Time{},
			want:       true,
		},
		{
			name:       "zero binary mtime but remote older than local",
			remoteDate: dayAgo,
			localDate:  hourAgo,
			binModTime: time.Time{},
			want:       false,
		},
		{
			name:       "zero local date with newer remote and older binary",
			remoteDate: now,
			localDate:  time.Time{},
			binModTime: dayAgo,
			want:       true,
		},
		{
			name:       "zero local date with newer binary than remote",
			remoteDate: hourAgo,
			localDate:  time.Time{},
			binModTime: now,
			want:       false,
		},
		{
			name:       "both zero — remote is after zero time",
			remoteDate: now,
			localDate:  time.Time{},
			binModTime: time.Time{},
			want:       true,
		},
		{
			name:       "stale release-date file but fresh binary (the bug scenario)",
			remoteDate: now.Add(-30 * time.Minute),
			localDate:  dayAgo,
			binModTime: now,
			want:       false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldUpdateDevDates(tt.remoteDate, tt.localDate, tt.binModTime)
			if got != tt.want {
				t.Errorf("shouldUpdateDevDates() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoadUpdateConfigFrom_Defaults(t *testing.T) {
	dir := t.TempDir()
	// No update.yaml exists — should return defaults.
	cfg := loadUpdateConfigFrom(filepath.Join(dir, "update.yaml"))
	if !cfg.AutoUpdateDev {
		t.Error("default AutoUpdateDev should be true")
	}
	if cfg.Disabled {
		t.Error("default Disabled should be false")
	}
}

func TestLoadUpdateConfigFrom_DisableAutoUpdate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "update.yaml")
	if err := os.WriteFile(path, []byte("auto_update_dev: false\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := loadUpdateConfigFrom(path)
	if cfg.AutoUpdateDev {
		t.Error("AutoUpdateDev should be false from config file")
	}
	if cfg.Disabled {
		t.Error("Disabled should remain false (not set in file)")
	}
}

func TestLoadUpdateConfigFrom_DisableAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "update.yaml")
	if err := os.WriteFile(path, []byte("auto_update_dev: false\ndisabled: true\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := loadUpdateConfigFrom(path)
	if cfg.AutoUpdateDev {
		t.Error("AutoUpdateDev should be false")
	}
	if !cfg.Disabled {
		t.Error("Disabled should be true")
	}
}

func TestLoadUpdateConfigFrom_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "update.yaml")
	if err := os.WriteFile(path, []byte("{{invalid yaml"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := loadUpdateConfigFrom(path)
	if !cfg.AutoUpdateDev {
		t.Error("corrupt file should return default AutoUpdateDev=true")
	}
}
