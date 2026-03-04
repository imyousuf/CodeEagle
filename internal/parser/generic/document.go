package generic

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ExtractDocument extracts plain text from a document file based on its extension.
// Supports OOXML (DOCX, PPTX, XLSX), ODF (ODT, ODS, ODP), and PDF formats.
func ExtractDocument(filePath string, content []byte) (string, error) {
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
