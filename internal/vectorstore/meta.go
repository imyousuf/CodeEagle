package vectorstore

import (
	"fmt"
	"time"
)

// VectorIndexMeta stores metadata about the vector index.
// Persisted in BadgerDB at key "vec:meta:<branch>".
type VectorIndexMeta struct {
	Provider   string    `json:"provider"`   // "ollama" or "vertex-ai"
	Model      string    `json:"model"`      // embedding model name
	Dimensions int       `json:"dimensions"` // vector dimensionality
	ChunkSize  int       `json:"chunk_size"` // chars per chunk
	Overlap    int       `json:"overlap"`    // overlap chars
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	NodeCount  int       `json:"node_count"` // number of nodes indexed
	Version    int       `json:"version"`    // bumped on full reindex
}

// Comparable reports whether vectors from two indices can be ranked against
// one another.
//
// A similarity score only means something relative to other scores from the
// same embedding model. Two models place text in different spaces, and their
// cosine similarities are not on one scale — ranking across them produces an
// order that looks authoritative and is arbitrary. Differing dimensionality is
// the obvious case; the same dimensionality from a different model is the
// dangerous one, because nothing downstream would fail.
func (m *VectorIndexMeta) Comparable(other *VectorIndexMeta) bool {
	if m == nil || other == nil {
		return false
	}
	return m.Provider == other.Provider &&
		m.Model == other.Model &&
		m.Dimensions == other.Dimensions
}

// Describe names the embedding behind an index, for saying why two of them
// cannot be ranked together.
func (m *VectorIndexMeta) Describe() string {
	if m == nil {
		return "no index"
	}
	return fmt.Sprintf("%s/%s (%d-dim)", m.Provider, m.Model, m.Dimensions)
}
