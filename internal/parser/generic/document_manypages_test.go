package generic

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
)

// createManyPagePDF builds a valid PDF of n pages, each carrying a line of
// text, with a correct cross-reference table.
//
// Extraction over many pages used to be covered by committing a real 99 MB
// car manual. That cost every clone of this repository the file in full,
// forever, to assert that extraction produces text and page markers -- and
// it was a copy of someone else's copyrighted manual. Neither re-hosting it
// nor downloading it at test time fixes the second problem, and downloading
// adds a network dependency to a unit test. Generating one removes all of it:
// no fixture, no network, and the page count is a parameter rather than
// whatever a manufacturer happened to print.
func createManyPagePDF(n int) []byte {
	var b bytes.Buffer

	// 1 catalog, 2 pages tree, 3 font, then two objects per page.
	const fixed = 3
	total := fixed + 2*n
	offsets := make([]int, total+1)

	b.WriteString("%PDF-1.4\n")

	offsets[1] = b.Len()
	b.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")

	kids := make([]string, 0, n)
	for i := range n {
		kids = append(kids, fmt.Sprintf("%d 0 R", fixed+1+2*i))
	}
	offsets[2] = b.Len()
	fmt.Fprintf(&b, "2 0 obj\n<< /Type /Pages /Kids [%s] /Count %d >>\nendobj\n",
		strings.Join(kids, " "), n)

	offsets[3] = b.Len()
	b.WriteString("3 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n")

	for i := range n {
		pageObj := fixed + 1 + 2*i
		contentObj := pageObj + 1

		offsets[pageObj] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "+
			"/Contents %d 0 R /Resources << /Font << /F1 3 0 R >> >> >>\nendobj\n",
			pageObj, contentObj)

		stream := fmt.Sprintf(
			"BT /F1 12 Tf 72 720 Td (Maintenance schedule section %d) Tj ET", i+1)
		offsets[contentObj] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n",
			contentObj, len(stream), stream)
	}

	xrefOffset := b.Len()
	b.WriteString("xref\n")
	fmt.Fprintf(&b, "0 %d\n", total+1)
	fmt.Fprintf(&b, "%010d 65535 f \n", 0)
	for i := 1; i <= total; i++ {
		fmt.Fprintf(&b, "%010d 00000 n \n", offsets[i])
	}

	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\n", total+1)
	fmt.Fprintf(&b, "startxref\n%d\n%%%%EOF\n", xrefOffset)

	return b.Bytes()
}

// TestExtractPDFManyPages covers extraction across a document of many pages,
// which is what the committed car manual was there to do.
func TestExtractPDFManyPages(t *testing.T) {
	const pages = 250

	content := createManyPagePDF(pages)
	text, err := extractPDF(context.Background(), content)
	if err != nil {
		t.Fatalf("extractPDF() on a %d-page document: %v", pages, err)
	}

	if len(text) < 1000 {
		t.Errorf("extracted %d bytes from %d pages, want substantial text",
			len(text), pages)
	}
	if !strings.Contains(text, "--- Page 1 ---") {
		t.Error("no page marker for the first page")
	}
	if !strings.Contains(text, fmt.Sprintf("--- Page %d ---", pages)) {
		t.Errorf("no page marker for page %d; extraction stopped early", pages)
	}
	// The text of a late page, to show pages are read rather than counted.
	if !strings.Contains(text, fmt.Sprintf("Maintenance schedule section %d", pages)) {
		t.Errorf("the content of page %d was not extracted", pages)
	}
}

// TestExtractPDFHonoursCancellation checks the context is observed between
// pages, so indexing a large document can be interrupted.
//
// Cancellation is not an error here: what has been read is returned, with a
// note saying where it stopped. A partial document is worth more than none,
// and saying so in the text keeps a truncated extraction from being mistaken
// for a short document.
func TestExtractPDFHonoursCancellation(t *testing.T) {
	content := createManyPagePDF(500)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	text, err := extractPDF(ctx, content)
	if err != nil {
		t.Fatalf("extractPDF() on a cancelled context: %v", err)
	}
	if !strings.Contains(text, "Stopped at page") {
		t.Error("a cancelled extraction did not say where it stopped")
	}
	if strings.Contains(text, "--- Page 500 ---") {
		t.Error("extraction continued past cancellation")
	}
}
