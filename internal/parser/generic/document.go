package generic

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	// maxDocumentSize is the maximum file size for document text extraction.
	// Files larger than this are skipped to avoid long parse times (e.g., 99MB PDF manuals).
	maxDocumentSize = 20 * 1024 * 1024 // 20 MB
)

// ExtractDocument extracts plain text from a document file based on its extension.
// Supports OOXML (DOCX, PPTX, XLSX), ODF (ODT, ODS, ODP), and PDF formats.
// Files larger than 20MB are skipped to avoid excessive parse times.
func ExtractDocument(filePath string, content []byte) (string, error) {
	if len(content) > maxDocumentSize {
		return "", fmt.Errorf("file too large for extraction (%d bytes, max %d)", len(content), maxDocumentSize)
	}
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".docx":
		return extractDOCX(content)
	case ".pptx":
		return extractPPTX(content)
	case ".xlsx":
		return extractXLSX(content)
	case ".odt":
		return extractODT(content)
	case ".ods":
		return extractODS(content)
	case ".odp":
		return extractODP(content)
	case ".pdf":
		return extractPDF(content)
	default:
		return "", fmt.Errorf("unsupported document format: %s", ext)
	}
}
