package vectorstore

import "time"

// VectorIndexMeta stores metadata about the vector index.
// Persisted in BadgerDB at key "vec:meta:<branch>".
type VectorIndexMeta struct {
	Provider  string    `json:"provider"`   // "ollama" or "vertex-ai"
	Model     string    `json:"model"`      // embedding model name
	Dimensions int      `json:"dimensions"` // vector dimensionality
	ChunkSize int       `json:"chunk_size"` // chars per chunk
	Overlap   int       `json:"overlap"`    // overlap chars
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	NodeCount int       `json:"node_count"` // number of nodes indexed
	Version   int       `json:"version"`    // bumped on full reindex
}
