package generic

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/dslipak/pdf"
)

const (
	// maxPDFPages is the maximum number of pages to extract from a PDF.
	// Beyond this, a truncation notice is appended.
	maxPDFPages = 200

	// pdfPageTimeout is the maximum time allowed to extract a single page.
	// Pages that exceed this are skipped (some pages have complex vector graphics
	// or embedded fonts that cause very slow parsing).
	pdfPageTimeout = 5 * time.Second
)

// extractPDF extracts plain text from a PDF file page by page.
// Large PDFs are handled gracefully: extraction stops after maxPDFPages pages,
// and individual pages that take longer than pdfPageTimeout are skipped.
// Malformed PDFs that cause panics in the dslipak/pdf library are caught and
// returned as errors.
func extractPDF(content []byte) (result string, retErr error) {
	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("PDF parsing panic: %v", r)
		}
	}()

	r, err := pdf.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", fmt.Errorf("open PDF: %w", err)
	}

	totalPages := r.NumPage()
	pagesToExtract := min(totalPages, maxPDFPages)

	var buf strings.Builder
	fonts := make(map[string]*pdf.Font)
	skippedPages := 0

	for i := 1; i <= pagesToExtract; i++ {
		text, err := extractPDFPage(r, i, fonts)
		if err != nil {
			skippedPages++
			continue
		}
		if text != "" {
			fmt.Fprintf(&buf, "--- Page %d ---\n%s\n\n", i, text)
		}
	}

	if totalPages > maxPDFPages {
		fmt.Fprintf(&buf, "\n[Extracted %d of %d pages; %d skipped]\n", pagesToExtract, totalPages, skippedPages)
	} else if skippedPages > 0 {
		fmt.Fprintf(&buf, "\n[%d of %d pages skipped due to extraction errors]\n", skippedPages, totalPages)
	}

	return buf.String(), nil
}

// extractPDFPage extracts text from a single page with a timeout.
// Returns the page text or an error if the page couldn't be parsed in time.
func extractPDFPage(r *pdf.Reader, pageNum int, fonts map[string]*pdf.Font) (string, error) {
	type result struct {
		text string
		err  error
	}

	ch := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- result{err: fmt.Errorf("page %d: panic: %v", pageNum, r)}
			}
		}()
		p := r.Page(pageNum)
		if p.V.IsNull() {
			ch <- result{err: fmt.Errorf("page %d not found", pageNum)}
			return
		}

		// Cache fonts for efficient reuse across pages.
		for _, name := range p.Fonts() {
			if _, ok := fonts[name]; !ok {
				f := p.Font(name)
				fonts[name] = &f
			}
		}

		text, err := p.GetPlainText(fonts)
		ch <- result{text: strings.TrimSpace(text), err: err}
	}()

	select {
	case res := <-ch:
		return res.text, res.err
	case <-time.After(pdfPageTimeout):
		return "", fmt.Errorf("page %d: extraction timed out", pageNum)
	}
}
