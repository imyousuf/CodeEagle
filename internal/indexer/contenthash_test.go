package indexer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeContentHash(t *testing.T) {
	hash := computeContentHash([]byte("hello world"))
	require.NotEmpty(t, hash)
	assert.True(t, len(hash) > len("sha256:"))
	assert.Contains(t, hash, "sha256:")

	// Same content produces same hash.
	hash2 := computeContentHash([]byte("hello world"))
	assert.Equal(t, hash, hash2)

	// Different content produces different hash.
	hash3 := computeContentHash([]byte("hello world!"))
	assert.NotEqual(t, hash, hash3)

	// Empty content is valid.
	hashEmpty := computeContentHash([]byte{})
	assert.Contains(t, hashEmpty, "sha256:")
}

func TestDetectMIMEType(t *testing.T) {
	tests := []struct {
		path       string
		mustPrefix string // MIME must start with this prefix
	}{
		{"photo.jpg", "image/jpeg"},
		{"photo.jpeg", "image/jpeg"},
		{"image.png", "image/png"},
		{"doc.pdf", "application/pdf"},
		{"config.json", "application/json"},
		{"config.yaml", "application/"},
		{"config.yml", "application/"},
		{"config.toml", "application/toml"},
		{"drawing.svg", "image/svg+xml"},
		{"data.csv", "text/csv"},
		{"data.tsv", "text/tab-separated-values"},
		{"noext", "text/plain"},
		{"unknown.xyz123", "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := detectMIMEType(tt.path)
			require.NotEmpty(t, got)
			assert.True(t, strings.HasPrefix(got, tt.mustPrefix),
				"detectMIMEType(%q) = %q, want prefix %q", tt.path, got, tt.mustPrefix)
		})
	}
}
