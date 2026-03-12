package indexer

import (
	"crypto/sha256"
	"fmt"
	"mime"
	"path/filepath"
	"strings"
)

// computeContentHash returns a "sha256:<hex>" hash string for the given content.
func computeContentHash(content []byte) string {
	h := sha256.Sum256(content)
	return fmt.Sprintf("sha256:%x", h)
}

// detectMIMEType returns the MIME type for a file path based on its extension.
func detectMIMEType(filePath string) string {
	ext := filepath.Ext(filePath)
	if ext == "" {
		return "text/plain"
	}

	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		ext = strings.ToLower(ext)
		switch ext {
		case ".svg":
			return "image/svg+xml"
		case ".csv":
			return "text/csv"
		case ".tsv":
			return "text/tab-separated-values"
		case ".json":
			return "application/json"
		case ".yaml", ".yml":
			return "application/x-yaml"
		case ".toml":
			return "application/toml"
		case ".log":
			return "text/plain"
		default:
			return "application/octet-stream"
		}
	}
	return mimeType
}
