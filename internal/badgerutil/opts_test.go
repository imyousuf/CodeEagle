package badgerutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dgraph-io/badger/v4"
)

func TestTunedOptions_SetsLogger(t *testing.T) {
	dir := t.TempDir()
	opts := TunedOptions(filepath.Join(dir, "db"), DBRolePrimary)
	if opts.Logger != nil {
		t.Error("expected Logger to be nil")
	}
}

func TestTunedOptions_PrimaryHasLargerBlockCache(t *testing.T) {
	dir := t.TempDir()
	primary := TunedOptions(filepath.Join(dir, "primary"), DBRolePrimary)
	tertiary := TunedOptions(filepath.Join(dir, "tertiary"), DBRoleTertiary)

	if primary.BlockCacheSize < tertiary.BlockCacheSize {
		t.Errorf("primary BlockCacheSize (%d) should be >= tertiary (%d)",
			primary.BlockCacheSize, tertiary.BlockCacheSize)
	}
}

func TestTunedOptions_CanOpenDB(t *testing.T) {
	dir := t.TempDir()
	for _, role := range []DBRole{DBRolePrimary, DBRoleSecondary, DBRoleTertiary} {
		dbPath := filepath.Join(dir, "db_"+roleName(role))
		opts := TunedOptions(dbPath, role)
		db, err := badger.Open(opts)
		if err != nil {
			t.Fatalf("failed to open db with role %d: %v", role, err)
		}
		db.Close()
	}
}

func TestTunedOptionsReadOnly_SetsReadOnly(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db")

	// Create the DB first (can't open read-only if it doesn't exist).
	opts := TunedOptions(dbPath, DBRolePrimary)
	db, err := badger.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	roOpts := TunedOptionsReadOnly(dbPath, DBRolePrimary)
	if !roOpts.ReadOnly {
		t.Error("expected ReadOnly=true")
	}
	db, err = badger.Open(roOpts)
	if err != nil {
		t.Fatalf("failed to open read-only: %v", err)
	}
	db.Close()
}

func TestDetectSystemRAM(t *testing.T) {
	ram := detectSystemRAM()
	if ram <= 0 {
		t.Errorf("expected positive RAM, got %d", ram)
	}
	// On any real system, should be at least 1GB.
	if ram < 1<<30 {
		// Could be running in a very constrained container; just check fallback.
		if _, err := os.ReadFile("/proc/meminfo"); err != nil {
			// /proc/meminfo not available, should get fallback of 8GB.
			if ram != 8<<30 {
				t.Errorf("expected 8GB fallback, got %d", ram)
			}
		}
	}
}

func TestClamp64(t *testing.T) {
	tests := []struct {
		v, lo, hi, want int64
	}{
		{50, 10, 100, 50},
		{5, 10, 100, 10},
		{200, 10, 100, 100},
		{10, 10, 100, 10},
		{100, 10, 100, 100},
	}
	for _, tt := range tests {
		got := clamp64(tt.v, tt.lo, tt.hi)
		if got != tt.want {
			t.Errorf("clamp64(%d, %d, %d) = %d, want %d", tt.v, tt.lo, tt.hi, got, tt.want)
		}
	}
}

func TestRoleWeight(t *testing.T) {
	pw := roleWeight(DBRolePrimary)
	sw := roleWeight(DBRoleSecondary)
	tw := roleWeight(DBRoleTertiary)

	if pw <= sw {
		t.Errorf("primary weight (%f) should be > secondary (%f)", pw, sw)
	}
	if sw <= tw {
		t.Errorf("secondary weight (%f) should be > tertiary (%f)", sw, tw)
	}
}

func roleName(r DBRole) string {
	switch r {
	case DBRolePrimary:
		return "primary"
	case DBRoleSecondary:
		return "secondary"
	case DBRoleTertiary:
		return "tertiary"
	default:
		return "unknown"
	}
}
