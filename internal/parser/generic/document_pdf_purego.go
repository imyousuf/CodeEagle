//go:build !poppler

package generic

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"

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

		text, ok := boundedPageText(r, i)
		if !ok {
			fmt.Fprintf(&buf, "\n[Stopped at page %d of %d: the page could not be read in %s]\n", i, nPages, pageTimeout)
			return strings.TrimSpace(buf.String()), nil
		}
		text = strings.TrimSpace(text)
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

// Bounds on a page the extractor cannot finish. pageText avoids every way
// the library is known to loop, but a page it cannot finish must not hold up
// a sync for good: it is given up on, and since a goroutine cannot be
// stopped, the one still working on it is left behind. After a few, pure Go
// extraction stops for the rest of the process rather than leave every core
// spinning. Documents parsed in parallel can each give up on a page before
// any sees the count reach the limit, so it can be passed by as many pages as
// there are parsers; that is still bounded.
const (
	pageTimeout  = 20 * time.Second
	maxAbandoned = 3
)

var abandonedPages atomic.Int32

// boundedPageText is pageText within pageTimeout, reporting false when it
// gave up.
func boundedPageText(r *pdf.Reader, num int) (string, bool) {
	if abandonedPages.Load() >= maxAbandoned {
		return "", false
	}
	done := make(chan string, 1)
	go func() { done <- pageText(r, num) }()
	timer := time.NewTimer(pageTimeout)
	defer timer.Stop()
	select {
	case text := <-done:
		return text, true
	case <-timer.C:
		abandonedPages.Add(1)
		return "", false
	}
}

// pageText returns one page's text, or "" if that page cannot be read.
//
// A page missing from the tree, or one whose content stream does not parse, is
// skipped the same way the poppler path skips a page with no text on it: a
// partial document is worth more than none, and the surrounding pages are
// still accurate.
//
// It does what Page.GetPlainText does, but over each of the page's content
// streams, and only after reading each through. The library's lexer turns any
// read error other than EOF into an endless run of newlines, so it loops
// forever on a stream it cannot read: a content array, which GetPlainText
// hands to a reader that only takes one stream (two of ten PDFs in one
// corpus, every page of them); a stream that fails to inflate; a font's
// broken ToUnicode map, which the font's encoder interprets the same way.
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
	var streams []pdf.Value
	switch c := p.V.Key("Contents"); c.Kind() {
	case pdf.Stream:
		streams = []pdf.Value{c}
	case pdf.Array:
		for i := 0; i < c.Len(); i++ {
			streams = append(streams, c.Index(i))
		}
	}

	encoders := make(map[string]pdf.TextEncoding)
	encoder := func(name string) pdf.TextEncoding {
		if e, ok := encoders[name]; ok {
			return e
		}
		var e pdf.TextEncoding = rawEncoding{}
		if f := p.Font(name); !f.V.IsNull() {
			if tu := f.V.Key("ToUnicode"); tu.Kind() == pdf.Null || readableStream(tu) {
				e = f.Encoder()
			}
		}
		encoders[name] = e
		return e
	}

	var b strings.Builder
	for _, strm := range streams {
		if !readableStream(strm) {
			continue
		}
		var enc pdf.TextEncoding = rawEncoding{}
		show := func(raw string) { b.WriteString(enc.Decode(raw)) }
		pdf.Interpret(strm, func(stk *pdf.Stack, op string) {
			args := make([]pdf.Value, stk.Len())
			for i := len(args) - 1; i >= 0; i-- {
				args[i] = stk.Pop()
			}
			switch op {
			case "T*": // next line
				b.WriteString("\n")
			case "Tf": // font and size
				if len(args) == 2 {
					enc = encoder(args[0].Name())
				}
			case "Tj", "'", "\"": // show a string; the string is the last operand
				if len(args) > 0 {
					show(args[len(args)-1].RawString())
				}
			case "TJ": // show strings, with glyph positioning between them
				if len(args) == 1 {
					for i := 0; i < args[0].Len(); i++ {
						if x := args[0].Index(i); x.Kind() == pdf.String {
							show(x.RawString())
						}
					}
				}
			}
		})
	}
	return b.String()
}

// maxStreamBytes bounds how much of one stream is read to prove it reads.
const maxStreamBytes = 64 << 20

// readableStream reports whether v is a stream that reads to its end
// without error, which is what the library's interpreter needs to stop.
func readableStream(v pdf.Value) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	if v.Kind() != pdf.Stream {
		return false
	}
	_, err := io.ReadAll(io.LimitReader(v.Reader(), maxStreamBytes))
	return err == nil
}

// rawEncoding passes text through, for a font with no usable encoding.
type rawEncoding struct{}

func (rawEncoding) Decode(raw string) string { return raw }
