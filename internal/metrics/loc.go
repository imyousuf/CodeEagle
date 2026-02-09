package metrics

import (
	"strings"
)

// LinesOfCodeCalculator counts total lines, blank lines, comment lines, and code lines.
type LinesOfCodeCalculator struct{}

func (c *LinesOfCodeCalculator) Calculate(_ string, content []byte, language string) (map[MetricType]float64, error) {
	lines := strings.Split(string(content), "\n")
	// Trim trailing empty line from final newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	total := float64(len(lines))

	var blank, comment float64
	inBlock := false // tracks multi-line comment state
	lang := strings.ToLower(language)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			blank++
			continue
		}

		if inBlock {
			comment++
			if blockCommentEnd(lang, trimmed, true) {
				inBlock = false
			}
			continue
		}

		if isBlockCommentStart(lang, trimmed) {
			comment++
			if !blockCommentEnd(lang, trimmed, false) {
				inBlock = true
			}
			continue
		}

		if isLineComment(lang, trimmed) {
			comment++
			continue
		}
	}

	code := total - blank - comment
	if code < 0 {
		code = 0
	}

	return map[MetricType]float64{
		LinesOfCode:  total,
		BlankLines:   blank,
		CommentLines: comment,
		CodeLines:    code,
	}, nil
}

// isLineComment checks if a trimmed line starts with a single-line comment marker.
func isLineComment(lang, trimmed string) bool {
	switch lang {
	case "go", "java", "typescript", "javascript":
		return strings.HasPrefix(trimmed, "//")
	case "python":
		return strings.HasPrefix(trimmed, "#")
	case "html":
		// Full-line HTML comments handled via block comment logic.
		return false
	default:
		return strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#")
	}
}

// isBlockCommentStart checks if a trimmed line opens a multi-line comment.
func isBlockCommentStart(lang, trimmed string) bool {
	switch lang {
	case "go", "java", "typescript", "javascript":
		return strings.HasPrefix(trimmed, "/*")
	case "python":
		return strings.HasPrefix(trimmed, `"""`) || strings.HasPrefix(trimmed, "'''")
	case "html":
		return strings.HasPrefix(trimmed, "<!--")
	default:
		return strings.HasPrefix(trimmed, "/*")
	}
}

// blockCommentEnd checks if a trimmed line closes a multi-line comment.
// When insideBlock is true, we are looking for the closing marker on subsequent lines.
// When insideBlock is false, we are checking if the opening line also closes on the same line.
func blockCommentEnd(lang, trimmed string, insideBlock bool) bool {
	switch lang {
	case "go", "java", "typescript", "javascript":
		if insideBlock {
			return strings.Contains(trimmed, "*/")
		}
		// Same-line close: e.g. /* comment */
		return strings.HasSuffix(trimmed, "*/") && strings.Contains(trimmed, "/*")
	case "python":
		if insideBlock {
			return strings.HasSuffix(trimmed, `"""`) || strings.HasSuffix(trimmed, "'''")
		}
		// Same-line close: e.g. """docstring"""
		return (strings.HasSuffix(trimmed, `"""`) || strings.HasSuffix(trimmed, "'''")) &&
			len(trimmed) > 3
	case "html":
		if insideBlock {
			return strings.Contains(trimmed, "-->")
		}
		return strings.HasSuffix(trimmed, "-->")
	default:
		if insideBlock {
			return strings.Contains(trimmed, "*/")
		}
		return strings.HasSuffix(trimmed, "*/")
	}
}
