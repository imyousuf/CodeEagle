package badgerutil

import (
	"os"
	"strconv"
	"strings"

	"github.com/dgraph-io/badger/v4"
)

// DBRole indicates the relative importance of a BadgerDB instance for
// proportional memory allocation.
type DBRole int

const (
	DBRolePrimary   DBRole = iota // graph store — largest, most queries
	DBRoleSecondary               // vector store, docs cache — moderate
	DBRoleTertiary                // queue, faces — lightweight
)

// roleWeight returns the fraction of the total BadgerDB budget for a role.
func roleWeight(role DBRole) float64 {
	switch role {
	case DBRolePrimary:
		return 0.40
	case DBRoleSecondary:
		return 0.20
	case DBRoleTertiary:
		return 0.10
	default:
		return 0.10
	}
}

// TunedOptions returns badger.Options with memory parameters scaled to the
// available system RAM. On generous systems, options stay near defaults.
// On constrained systems, they scale down proportionally.
func TunedOptions(dbPath string, role DBRole) badger.Options {
	opts := badger.DefaultOptions(dbPath)
	opts.Logger = nil

	totalRAM := detectSystemRAM()
	budget := int64(float64(totalRAM) * 0.10)          // 10% of total RAM
	share := int64(float64(budget) * roleWeight(role)) // per-role share

	opts.MemTableSize = clamp64(share/10, 16<<20, 64<<20)
	opts.BlockCacheSize = clamp64(share/4, 16<<20, 128<<20)
	opts.IndexCacheSize = clamp64(share/4, 16<<20, 128<<20)
	opts.ValueLogFileSize = clamp64(share/2, 32<<20, 256<<20)

	if share < 256<<20 {
		opts.NumMemtables = 2
		opts.NumLevelZeroTables = 2
		opts.NumLevelZeroTablesStall = 4
	}
	if share < 512<<20 {
		opts.NumCompactors = 2
	}

	return opts
}

// TunedOptionsReadOnly returns read-only badger.Options with scaled memory.
func TunedOptionsReadOnly(dbPath string, role DBRole) badger.Options {
	opts := TunedOptions(dbPath, role)
	opts.ReadOnly = true
	return opts
}

// detectSystemRAM reads total system memory from /proc/meminfo (Linux).
// Falls back to 8GB if detection fails.
func detectSystemRAM() int64 {
	const fallback = 8 << 30 // 8GB

	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return fallback
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return fallback
			}
			kb, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return fallback
			}
			return kb * 1024 // convert kB to bytes
		}
	}
	return fallback
}

// clamp64 clamps v between lo and hi.
func clamp64(v, lo, hi int64) int64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
