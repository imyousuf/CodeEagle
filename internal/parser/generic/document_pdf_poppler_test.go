//go:build poppler

package generic_test

import (
	"context"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/parser/generic"
	"github.com/imyousuf/CodeEagle/pkg/kit/extract"
)

// TestPopplerReadsPDFs checks that a poppler build reads PDFs with poppler,
// through the kit. The two readers differ on a wide gap inside a TJ array:
// poppler reads it as a space and the pure Go reader does not.
func TestPopplerReadsPDFs(t *testing.T) {
	pdf := pdfOf("BT /F1 12 Tf 72 720 Td [(Ker) -120 (ned)] TJ ET")

	got, err := generic.ExtractDocumentWithCtx(context.Background(), "papers/paper.pdf", pdf)
	if err != nil || got != "--- Page 1 ---\nKer ned" {
		t.Errorf("ExtractDocumentWithCtx = %q, %v; want poppler's %q", got, err, "--- Page 1 ---\nKer ned")
	}
	// The kit on its own still reads it in pure Go.
	got, err = extract.Document(context.Background(), "papers/paper.pdf", pdf)
	if err != nil || got != "--- Page 1 ---\nKerned" {
		t.Errorf("extract.Document = %q, %v; want the pure Go reader's %q", got, err, "--- Page 1 ---\nKerned")
	}
}
