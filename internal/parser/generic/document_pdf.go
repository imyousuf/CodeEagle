package generic

import (
	"bytes"
	"fmt"
	"io"

	"github.com/dslipak/pdf"
)

// extractPDF extracts plain text from a PDF file using the dslipak/pdf library.
func extractPDF(content []byte) (string, error) {
	r, err := pdf.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", fmt.Errorf("open PDF: %w", err)
	}

	plainText, err := r.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("extract PDF text: %w", err)
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, plainText); err != nil {
		return "", fmt.Errorf("read PDF text: %w", err)
	}

	return buf.String(), nil
}
