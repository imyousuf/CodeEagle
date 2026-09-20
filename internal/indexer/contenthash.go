package indexer

import (
	"crypto/sha256"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
)

// computeContentHash returns a "sha256:<hex>" hash string for the given content.
func computeContentHash(content []byte) string {
	h := sha256.Sum256(content)
	return fmt.Sprintf("sha256:%x", h)
}

// computeContentHashFromFile computes a SHA-256 hash by streaming the file
// through a hash writer. This uses O(1) memory regardless of file size,
// unlike computeContentHash which requires the entire file in memory.
func computeContentHashFromFile(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil)), nil
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
