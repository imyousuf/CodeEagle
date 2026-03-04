// Package generic provides a fallback parser for non-code files (text, images).
package generic

import (
	"net/http"
	"path/filepath"
	"strings"
)

// FileClass represents how a file should be processed.
type FileClass int

const (
	// FileClassText indicates a text-based file to read and extract.
	FileClassText FileClass = iota
	// FileClassImage indicates an image to describe with a vision model.
	FileClassImage
	// FileClassSkip indicates a file to skip (excluded extension or binary).
	FileClassSkip
)

// imageExtensions lists known image file extensions.
var imageExtensions = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".gif":  true,
	".webp": true,
	".bmp":  true,
	".ico":  true,
	".tiff": true,
	".tif":  true,
}

// Classify determines how to process a file based on its extension and content.
// Design: maximally inclusive — any file not explicitly excluded is a candidate.
func Classify(filePath string, excludeExts []string) FileClass {
	ext := strings.ToLower(filepath.Ext(filePath))
	base := strings.ToLower(filepath.Base(filePath))

	// Check explicit exclusion list. Supports compound extensions like ".min.js".
	for _, excl := range excludeExts {
		if ext == excl {
			return FileClassSkip
		}
		// Check compound extension (e.g., ".min.js" matches "bundle.min.js").
		if strings.HasSuffix(base, excl) {
			return FileClassSkip
		}
	}

	// Check image extensions.
	if imageExtensions[ext] {
		return FileClassImage
	}

	// Everything else is treated as text.
	return FileClassText
}

// ClassifyContent uses file content to distinguish text from binary.
// Reads the first 512 bytes and checks for null bytes.
// Returns FileClassSkip for binary files, FileClassText otherwise.
func ClassifyContent(content []byte) FileClass {
	if len(content) == 0 {
		return FileClassText
	}

	// Check first 512 bytes for null bytes (binary indicator).
	checkLen := len(content)
	if checkLen > 512 {
		checkLen = 512
	}
	for _, b := range content[:checkLen] {
		if b == 0 {
			return FileClassSkip
		}
	}

	// Use net/http content type detection as additional check.
	ct := http.DetectContentType(content)
	if strings.HasPrefix(ct, "application/octet-stream") {
		// Could be binary but null-byte check passed — treat as text.
		return FileClassText
	}

	return FileClassText
}
