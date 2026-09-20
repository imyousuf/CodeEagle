//go:build !poppler

package generic

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/dslipak/pdf"
)

// extractPDF extracts plain text from a PDF file in pure Go.
//
// This is the default because it builds anywhere: no system library, no
// pkg-config, and it cross-compiles, which the poppler path cannot do for
// linux-arm64 without an arm64 copy of libpoppler. Build with `-tags poppler`
// — which `make build` does automatically when pkg-config finds poppler-glib —
// to get the faster and more capable extractor in document_pdf_poppler.go.
// The two must agree on output shape: the same page markers, the same note
// when cancelled, and the same silence for a page holding no text, because
// callers and tests read that text rather than knowing which build produced it.
//
// Extraction is per page rather than via the whole-document helper so the
// context can be honoured between pages, and so one unreadable page costs that
// page instead of the document.
func extractPDF(ctx context.Context, content []byte) (string, error) {
	r, err := newPDFReader(content)
	if err != nil {
		return "", fmt.Errorf("open PDF: %w", err)
	}

	nPages := r.NumPage()
	if nPages <= 0 {
		return "", nil
	}

	var buf strings.Builder
	for i := 1; i <= nPages; i++ {
		select {
		case <-ctx.Done():
			fmt.Fprintf(&buf, "\n[Stopped at page %d of %d: %v]\n", i, nPages, ctx.Err())
			return buf.String(), nil
		default:
		}

		text := strings.TrimSpace(pageText(r, i))
		if text != "" {
			fmt.Fprintf(&buf, "--- Page %d ---\n%s\n\n", i, text)
		}
	}

	return strings.TrimSpace(buf.String()), nil
}

// newPDFReader opens the document, converting a panic into an error.
//
// The parser indexes whatever is on disk, including files that are damaged or
// only claim to be PDFs, and this library reports some of those by panicking
// rather than returning an error. A panic here would take down the indexing
// run over a single bad file.
func newPDFReader(content []byte) (r *pdf.Reader, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			r, err = nil, fmt.Errorf("malformed PDF: %v", rec)
		}
	}()

	return pdf.NewReader(bytes.NewReader(content), int64(len(content)))
}

// pageText returns one page's text, or "" if that page cannot be read.
//
// A page missing from the tree, or one whose content stream does not parse, is
// skipped the same way the poppler path skips a page with no text on it: a
// partial document is worth more than none, and the surrounding pages are
// still accurate.
func pageText(r *pdf.Reader, num int) (text string) {
	defer func() {
		if rec := recover(); rec != nil {
			text = ""
		}
	}()

	p := r.Page(num)
	if p.V.IsNull() {
		return ""
	}

	s, err := p.GetPlainText(nil)
	if err != nil {
		return ""
	}
	return s
}
