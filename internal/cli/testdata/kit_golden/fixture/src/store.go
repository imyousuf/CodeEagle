// Package store is an in-memory key-value store with age-based eviction.
package store

import "time"

// MaxAge is how long an entry is kept before eviction.
const MaxAge = time.Hour

// Store holds string values by key.
type Store struct {
	entries map[string]entry
}

type entry struct {
	value   string
	written time.Time
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{entries: make(map[string]entry)}
}

// Get returns the value for key and whether it was present.
func (s *Store) Get(key string) (string, bool) {
	e, ok := s.entries[key]
	return e.value, ok
}

// Put stores value under key, evicting entries older than MaxAge first.
func (s *Store) Put(key, value string, now time.Time) {
	s.evict(now)
	s.entries[key] = entry{value: value, written: now}
}

func (s *Store) evict(now time.Time) {
	for k, e := range s.entries {
		if now.Sub(e.written) > MaxAge {
			delete(s.entries, k)
		}
	}
}
