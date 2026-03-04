package generic

import (
	"path/filepath"
	"regexp"
	"strings"
)

// ExtractText processes raw file content based on the file extension.
// For most files, returns the content as-is. For specific formats,
// performs basic extraction (e.g., SVG tag stripping).
func ExtractText(filePath string, content []byte) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	text := string(content)

	switch ext {
	case ".svg":
		return stripSVGTags(text)
	case ".csv", ".tsv":
		return extractCSVPreview(text)
	default:
		return text
	}
}

// stripSVGTags removes XML/SVG tags, keeping only text content.
var svgTagRegexp = regexp.MustCompile(`<[^>]*>`)

func stripSVGTags(svg string) string {
	stripped := svgTagRegexp.ReplaceAllString(svg, " ")
	// Collapse whitespace.
	parts := strings.Fields(stripped)
	return strings.Join(parts, " ")
}

// extractCSVPreview returns the header row and a preview of the CSV content.
func extractCSVPreview(text string) string {
	lines := strings.SplitN(text, "\n", 21) // header + up to 20 rows
	if len(lines) == 0 {
		return text
	}

	var b strings.Builder
	b.WriteString("CSV headers: ")
	b.WriteString(strings.TrimSpace(lines[0]))

	if len(lines) > 1 {
		b.WriteString("\nSample rows:\n")
		end := len(lines)
		if end > 6 { // header + 5 rows
			end = 6
		}
		for i := 1; i < end; i++ {
			b.WriteString(strings.TrimSpace(lines[i]))
			b.WriteString("\n")
		}
		if len(lines) > 6 {
			b.WriteString("...\n")
		}
	}

	return b.String()
}
