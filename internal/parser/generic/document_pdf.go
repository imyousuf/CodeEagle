package generic

import (
	"context"
	"fmt"
	"strings"

	"github.com/wassup05/poppler-go/poppler"
)

// extractPDF extracts plain text from a PDF file using poppler (via CGo).
// Poppler is the industry-standard PDF rendering library used by most Linux
// PDF viewers. It handles complex PDFs (embedded fonts, vector graphics, CJK
// text) that pure Go libraries struggle with, and is orders of magnitude
// faster — a 99MB, 714-page PDF extracts in ~2 seconds.
//
// The context is checked between pages for cancellation support.
func extractPDF(ctx context.Context, content []byte) (string, error) {
	doc, pErr := poppler.NewDocFromBytes(content)
	if pErr != nil {
		return "", fmt.Errorf("open PDF: %v", pErr)
	}
	defer doc.Close()

	nPages := doc.GetPageCount()
	if nPages == 0 {
		return "", nil
	}

	var buf strings.Builder
	for i := range nPages {
		// Check context between pages for cancellation.
		select {
		case <-ctx.Done():
			fmt.Fprintf(&buf, "\n[Stopped at page %d of %d: %v]\n", i+1, nPages, ctx.Err())
			return buf.String(), nil
		default:
		}

		page := doc.GetPage(int(i))
		text := strings.TrimSpace(page.GetText())
		if text != "" {
			fmt.Fprintf(&buf, "--- Page %d ---\n%s\n\n", i+1, text)
		}
	}

	return strings.TrimSpace(buf.String()), nil
}
