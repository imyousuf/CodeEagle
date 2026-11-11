//go:build !poppler

package generic

import (
	"context"

	"github.com/imyousuf/CodeEagle/pkg/kit/extract"
)

// extractPDF is the PDF reader this build uses, the kit's pure Go one, so
// the PDF tests here run against whichever reader the build has; with
// -tags poppler they reach document_pdf_poppler.go's instead.
func extractPDF(ctx context.Context, content []byte) (string, error) {
	return extract.Document(ctx, "document.pdf", content)
}
