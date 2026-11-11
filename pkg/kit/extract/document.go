// Package extract reads the text out of the files a person keeps (PDFs,
// word-processor files, slide decks, spreadsheets, plain and lightly
// marked-up text, chat logs) and splits prose into sections, with no model
// involved.
//
// A file is first classified by its name (Classify), so that nothing is read
// that cannot be used: videos, executables and raw photos are skipped
// without opening them. Documents in binary containers are read by Document,
// text by Text. Prose is then cut into sections by Sectionize (pages, slides
// or runs of paragraphs), and a chat log into days by DetectChat and
// chatlog.Log.Sections, so that a passage can be embedded, quoted and
// checked against the text it came from.
//
// Everything here is pure Go, so it builds with CGO_ENABLED=0 and
// cross-compiles. A faster or more capable PDF reader can be supplied with
// WithPDFReader.
package extract

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// PDFReader returns a PDF's text, one "--- Page N ---" block per page that
// holds text. If ctx is done between pages it returns what it has read so
// far with a note of where it stopped, rather than an error: a partial
// document is worth more than none, and the note keeps a truncated read from
// being mistaken for a short document.
type PDFReader func(ctx context.Context, content []byte) (string, error)

type options struct {
	pdf PDFReader
}

// Option configures Document.
type Option func(*options)

// WithPDFReader reads PDFs with r instead of the pure Go reader; nil keeps
// the pure Go reader. A reader built on a system library (poppler, say) is
// several times faster on a long document and reads PDFs the pure Go one
// cannot, but needs cgo and that library at build time, which this package
// will not impose on everyone who imports it. Such a reader must produce the
// same shape of text: callers and Sectionize read the page markers, not
// which reader wrote them.
func WithPDFReader(r PDFReader) Option {
	return func(o *options) { o.pdf = r }
}

// Document extracts plain text from a document file, choosing the format by
// the path's extension: OOXML (DOCX, PPTX, XLSX), ODF (ODT, ODS, ODP) and
// PDF. Slides and sheets are marked "--- Slide N ---" and "--- Sheet N ---",
// PDF pages "--- Page N ---". Only PDF extraction observes ctx; the other
// formats are read in full. Any other extension is an error, plain text
// included: read it with Text.
func Document(ctx context.Context, path string, content []byte, opts ...Option) (string, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	ext := strings.ToLower(filepath.Ext(path))
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
		if o.pdf != nil {
			return o.pdf(ctx, content)
		}
		return extractPDF(ctx, content)
	default:
		return "", fmt.Errorf("unsupported document format: %s", ext)
	}
}
