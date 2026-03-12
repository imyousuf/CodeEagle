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
	// FileClassDocument indicates a document format (DOCX, PPTX, XLSX, ODT, ODS, ODP, PDF)
	// that requires format-specific text extraction from binary containers.
	FileClassDocument
	// FileClassSkip indicates a file to skip (excluded extension or binary).
	FileClassSkip
)

// documentExtensions lists known document file extensions that require
// format-specific text extraction (ZIP-based Office/ODF formats and PDF).
var documentExtensions = map[string]bool{
	".docx": true,
	".pptx": true,
	".xlsx": true,
	".odt":  true,
	".ods":  true,
	".odp":  true,
	".pdf":  true,
}

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

// textExtensions lists file extensions recognized as text by the generic parser.
// Only files with extensions in this list (or in documentExtensions/imageExtensions)
// are processed. Everything else is skipped. This whitelist approach avoids reading
// unknown binary files (videos, executables, RAW photos, etc.).
var textExtensions = map[string]bool{
	// Plain text / data
	".txt": true, ".text": true, ".csv": true, ".tsv": true, ".log": true,
	// Structured data
	".json": true, ".jsonl": true, ".jsonc": true, ".xml": true, ".xsl": true,
	".xsd": true, ".dtd": true, ".xhtml": true, ".svg": true,
	// Config / serialization formats
	".yml": true, ".yaml": true, ".toml": true,
	".ini": true, ".cfg": true, ".conf": true, ".config": true, ".env": true,
	".properties": true, ".editorconfig": true, ".prettierrc": true,
	// Query / schema
	".sql": true, ".graphql": true, ".gql": true, ".proto": true,
	// Markup / rich text
	".md": true, ".mdx": true, ".rst": true, ".adoc": true, ".asciidoc": true,
	".tex": true, ".latex": true, ".bib": true, ".rtf": true,
	".textile": true, ".creole": true, ".wiki": true, ".mediawiki": true,
	".org": true, ".pod": true, ".rdoc": true, ".man": true, ".roff": true,
	// Styles (not handled by language-specific parsers)
	".css": true, ".scss": true, ".sass": true, ".less": true, ".styl": true,
	// Programming languages without dedicated parsers
	".c": true, ".h": true, ".cpp": true, ".cc": true, ".cxx": true,
	".hpp": true, ".hh": true, ".hxx": true, ".m": true, ".mm": true,
	".swift": true, ".kt": true, ".kts": true, ".scala": true,
	".r": true, ".lua": true, ".pl": true, ".pm": true, ".php": true,
	".dart": true, ".ex": true, ".exs": true, ".erl": true, ".hrl": true,
	".clj": true, ".cljs": true, ".cljc": true, ".hs": true, ".lhs": true,
	".ml": true, ".mli": true, ".fs": true, ".fsi": true, ".fsx": true,
	".nim": true, ".zig": true, ".d": true, ".pas": true, ".groovy": true,
	".v": true, ".sv": true, ".vhdl": true, ".vhd": true,
	// Build / CI (not handled by language-specific parsers)
	".gradle": true, ".cmake": true, ".sbt": true, ".bzl": true, ".bazel": true,
	// Scripting (not handled by shell parser)
	".bat": true, ".cmd": true, ".ps1": true, ".psm1": true, ".awk": true,
	// Notebooks / data science
	".ipynb": true, ".rmd": true,
	// Diff / patch
	".diff": true, ".patch": true,
}

// Classify determines how to process a file based on its extension.
// Design: whitelist — only files with known extensions are processed.
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

	// Check document extensions (ZIP-based Office/ODF + PDF).
	if documentExtensions[ext] {
		return FileClassDocument
	}

	// Check image extensions.
	if imageExtensions[ext] {
		return FileClassImage
	}

	// Check known text extensions — whitelist approach.
	if textExtensions[ext] {
		return FileClassText
	}

	// Unknown extension — skip. Only files in a known whitelist are processed.
	return FileClassSkip
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
