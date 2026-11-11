package generic_test

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/parser/generic"
)

// These tests pin the exported behaviour of section eligibility and document
// extraction exactly, from outside the package, so they survive the package
// moving: only the import path should ever need to change.

func TestMaySectionAndSectioned(t *testing.T) {
	tests := []struct {
		path      string
		may       bool
		sectioned bool
	}{
		// Prose: sectioned, and so may have sections.
		{"docs/spec.docx", true, true},
		{"slides/deck.pptx", true, true},
		{"docs/notes.odt", true, true},
		{"slides/talk.odp", true, true},
		{"papers/paper.pdf", true, true},
		{"notes/plan.txt", true, true},
		{"notes/plan.text", true, true},
		{"guide.rst", true, true},
		{"design.adoc", true, true},
		{"design.asciidoc", true, true},
		{"journal.org", true, true},
		{"page.textile", true, true},
		{"page.creole", true, true},
		{"page.wiki", true, true},
		{"page.mediawiki", true, true},
		{"README.rdoc", true, true},
		// Chat formats that are not prose: they may have sections, as a
		// chat log, but are not sectioned as documents.
		{"export/conversations.json", true, false},
		{"Downloads/chat_history_20250427164006.jsonl", true, false},
		// Case does not matter.
		{"REPORT.PDF", true, true},
		{"Export/Chat.JSON", true, false},
		{"Notes.TxT", true, true},
		// A dotfile named like an extension has that extension.
		{".txt", true, true},
		// Neither: markdown has its own parser; spreadsheets, data,
		// configuration and code are not prose.
		{"README.md", false, false},
		{"notes.markdown", false, false},
		{"data/report.xlsx", false, false},
		{"data/budget.ods", false, false},
		{"data/rows.csv", false, false},
		{"settings.jsonc", false, false},
		{"run.log", false, false},
		{"cmd/main.go", false, false},
		{"Makefile", false, false},
		{"archive.txt.gz", false, false},
		{"bundle.pdf/inside", false, false},
		{"", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := generic.MaySection(tt.path); got != tt.may {
				t.Errorf("MaySection(%q) = %v, want %v", tt.path, got, tt.may)
			}
			if got := generic.Sectioned(tt.path); got != tt.sectioned {
				t.Errorf("Sectioned(%q) = %v, want %v", tt.path, got, tt.sectioned)
			}
		})
	}
}

// zipEntry is one file of a ZIP container built for a test.
type zipEntry struct{ name, body string }

// zipOf builds a ZIP archive holding entries in the order given.
func zipOf(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		f, err := w.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const (
	wordNS   = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`
	officeNS = `xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" ` +
		`xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" ` +
		`xmlns:table="urn:oasis:names:tc:opendocument:xmlns:table:1.0" ` +
		`xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0"`
	slideCT = "application/vnd.openxmlformats-officedocument.presentationml.slide+xml"
)

func wordPart(root, paras string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><w:` + root + ` ` + wordNS + `>` + paras + `</w:` + root + `>`
}

func pptxSlide(paras ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><p:sld ` +
		`xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" ` +
		`xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><p:cSld><p:spTree><p:sp><p:txBody>`)
	for _, p := range paras {
		b.WriteString(`<a:p><a:r><a:t>` + p + `</a:t></a:r></a:p>`)
	}
	b.WriteString(`</p:txBody></p:sp></p:spTree></p:cSld></p:sld>`)
	return b.String()
}

func odfContent(body string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><office:document-content ` + officeNS +
		`><office:body>` + body + `</office:body></office:document-content>`
}

// pdfOf builds a valid PDF with one page per content stream, every page
// using a Helvetica font as /F1, with a correct cross-reference table.
func pdfOf(streams ...string) []byte {
	var b bytes.Buffer
	n := len(streams)
	// 1 catalog, 2 pages tree, 3 font, then a page and its content each.
	total := 3 + 2*n
	offsets := make([]int, total+1)

	b.WriteString("%PDF-1.4\n")
	offsets[1] = b.Len()
	b.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	kids := make([]string, 0, n)
	for i := range n {
		kids = append(kids, fmt.Sprintf("%d 0 R", 4+2*i))
	}
	offsets[2] = b.Len()
	fmt.Fprintf(&b, "2 0 obj\n<< /Type /Pages /Kids [%s] /Count %d >>\nendobj\n", strings.Join(kids, " "), n)
	offsets[3] = b.Len()
	b.WriteString("3 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n")
	for i, s := range streams {
		page, content := 4+2*i, 5+2*i
		offsets[page] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "+
			"/Contents %d 0 R /Resources << /Font << /F1 3 0 R >> >> >>\nendobj\n", page, content)
		offsets[content] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", content, len(s), s)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n%010d 65535 f \n", total+1, 0)
	for i := 1; i <= total; i++ {
		fmt.Fprintf(&b, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", total+1, xref)
	return b.Bytes()
}

// Page content streams for pdfOf.
const (
	helloPage   = "BT /F1 12 Tf 72 720 Td (Hello World) Tj ET"
	blankPage   = ""
	twoLinePage = "BT /F1 12 Tf 14 TL 72 720 Td (Line one) Tj T* (Line two) Tj ET"
	kernedPage  = "BT /F1 12 Tf 72 720 Td [(Ker) -10 (ned)] TJ ET"
)

// cancelAfter is a context that reports done once Done has been asked
// more than limit times, so a test can cancel extraction between two
// particular pages without racing it.
type cancelAfter struct {
	context.Context
	limit int32
	calls atomic.Int32
}

var closedDone = func() chan struct{} { c := make(chan struct{}); close(c); return c }()

func (c *cancelAfter) Done() <-chan struct{} {
	if c.calls.Add(1) > c.limit {
		return closedDone
	}
	return nil
}

func (c *cancelAfter) Err() error {
	if c.calls.Load() > c.limit {
		return context.Canceled
	}
	return nil
}

func TestExtractDocumentWithCtxFormats(t *testing.T) {
	docx := zipOf(t,
		zipEntry{"[Content_Types].xml", `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`},
		// Headers are read in name order, whatever order they are stored in.
		zipEntry{"word/header2.xml", wordPart("hdr", `<w:p><w:r><w:t>Second header</w:t></w:r></w:p>`)},
		zipEntry{"word/header1.xml", wordPart("hdr", `<w:p><w:r><w:t>First header</w:t></w:r></w:p>`)},
		zipEntry{"word/document.xml", wordPart("document", `<w:body>`+
			`<w:p><w:r><w:t>Title line</w:t></w:r></w:p>`+
			// An empty paragraph is an empty line.
			`<w:p/>`+
			`<w:p><w:r><w:t xml:space="preserve">Split </w:t></w:r><w:r><w:t>run</w:t></w:r></w:p>`+
			// Tabs and line breaks are not text: the words run together.
			`<w:p><w:r><w:t>one</w:t></w:r><w:r><w:tab/></w:r><w:r><w:t>two</w:t></w:r><w:r><w:br/></w:r><w:r><w:t>three</w:t></w:r></w:p>`+
			// A field's code is dropped and its result kept.
			`<w:p><w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:instrText xml:space="preserve"> PAGE </w:instrText></w:r>`+
			`<w:r><w:fldChar w:fldCharType="separate"/></w:r><w:r><w:t>7</w:t></w:r><w:r><w:fldChar w:fldCharType="end"/></w:r></w:p>`+
			`<w:p><w:r><w:t>Caf&#233; &amp; bar</w:t></w:r></w:p>`+
			`</w:body>`)},
		zipEntry{"word/footer1.xml", wordPart("ftr", `<w:p><w:r><w:t>Page footer</w:t></w:r></w:p>`)},
	)

	pptx := zipOf(t,
		zipEntry{"[Content_Types].xml", `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Override PartName="/ppt/slides/slide3.xml" ContentType="` + slideCT + `"/>` +
			`<Override PartName="/ppt/slides/slide1.xml" ContentType="` + slideCT + `"/>` +
			`<Override PartName="/ppt/slides/slide10.xml" ContentType="` + slideCT + `"/>` +
			`<Override PartName="/ppt/slides/slide2.xml" ContentType="` + slideCT + `"/>` +
			`<Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/>` +
			`</Types>`},
		zipEntry{"ppt/slides/slide1.xml", pptxSlide("Opening")},
		zipEntry{"ppt/slides/slide2.xml", pptxSlide()},
		zipEntry{"ppt/slides/slide3.xml", pptxSlide("Third slide", "With a bullet")},
		zipEntry{"ppt/slides/slide10.xml", pptxSlide("Tenth slide")},
	)

	// Without [Content_Types].xml, slides are found by their path.
	pptxNoTypes := zipOf(t,
		zipEntry{"ppt/slides/_rels/slide1.xml.rels", `<Relationships/>`},
		zipEntry{"ppt/slides/slide2.xml", pptxSlide("Second")},
		zipEntry{"ppt/slides/slide1.xml", pptxSlide("First")},
	)

	xlsx := zipOf(t,
		zipEntry{"xl/sharedStrings.xml", `<?xml version="1.0"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
			`<si><t>Name</t></si><si><r><t>Rich</t></r><r><t xml:space="preserve"> text</t></r></si></sst>`},
		zipEntry{"xl/worksheets/sheet1.xml", `<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` +
			`<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>` +
			`<row r="2"><c r="A2" t="inlineStr"><is><t>Inline</t></is></c><c r="B2" t="b"><v>1</v></c><c r="C2"><v>3.5</v></c></row>` +
			// An empty cell leaves no gap; an out-of-range shared string
			// reference is shown as the number it is.
			`<row r="3"><c r="A3"/><c r="B3" t="s"><v>99</v></c></row>` +
			`</sheetData></worksheet>`},
		zipEntry{"xl/worksheets/sheet2.xml", `<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData/></worksheet>`},
		zipEntry{"xl/worksheets/sheet3.xml", `<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` +
			`<row r="1"><c r="A1"><v>42</v></c></row></sheetData></worksheet>`},
	)

	odt := zipOf(t, zipEntry{"content.xml", odfContent(`<office:text>` +
		`<text:h text:outline-level="1">Heading</text:h>` +
		// Tabs, spaces and line breaks are elements, not text.
		`<text:p>one<text:tab/>two<text:s/>three<text:line-break/>four</text:p>` +
		`<text:p>It builds a <text:span>knowledge graph</text:span>.</text:p>` +
		`<text:list><text:list-item><text:p>bullet</text:p></text:list-item></text:list>` +
		`</office:text>`)})

	ods := zipOf(t, zipEntry{"content.xml", odfContent(`<office:spreadsheet>` +
		`<table:table table:name="Parsers">` +
		`<table:table-row><table:table-cell><text:p>Parser</text:p></table:table-cell><table:table-cell><text:p>Language</text:p></table:table-cell></table:table-row>` +
		// A span within a cell reads as a cell of its own, and an empty
		// cell leaves no gap.
		`<table:table-row><table:table-cell><text:p>Go<text:span>lang</text:span></text:p></table:table-cell><table:table-cell/>` +
		`<table:table-cell><text:p>yes</text:p></table:table-cell></table:table-row>` +
		`</table:table>` +
		`<table:table table:name="Second"><table:table-row><table:table-cell><text:p>x</text:p></table:table-cell></table:table-row></table:table>` +
		`</office:spreadsheet>`)})

	odp := zipOf(t, zipEntry{"content.xml", odfContent(`<office:presentation>` +
		`<draw:page draw:name="One"><draw:frame><draw:text-box><text:p>CodeEagle Overview</text:p></draw:text-box></draw:frame></draw:page>` +
		`<draw:page draw:name="Two"><draw:frame><draw:text-box><text:h>Semantic Search</text:h><text:p>Topic Extraction</text:p></draw:text-box></draw:frame></draw:page>` +
		`</office:presentation>`)})

	tests := []struct {
		name    string
		path    string
		content []byte
		want    string
	}{
		{"docx: headers, body, footers", "docs/spec.docx", docx,
			"First header\nSecond header\nTitle line\n\nSplit run\nonetwothree\n7\nCafé & bar\nPage footer"},
		{"docx extension case does not matter", "docs/SPEC.DOCX", docx,
			"First header\nSecond header\nTitle line\n\nSplit run\nonetwothree\n7\nCafé & bar\nPage footer"},
		// Slides are ordered by part name, so slide10 comes second, and
		// numbered by that order; an empty slide keeps its number but has
		// no marker.
		{"pptx: slides in part-name order", "slides/deck.pptx", pptx,
			"--- Slide 1 ---\nOpening\n\n--- Slide 2 ---\nTenth slide\n\n--- Slide 4 ---\nThird slide\nWith a bullet"},
		{"pptx: slides found by path", "slides/deck.pptx", pptxNoTypes,
			"--- Slide 1 ---\nFirst\n\n--- Slide 2 ---\nSecond"},
		{"xlsx: shared, inline, boolean and number cells", "data/report.xlsx", xlsx,
			"--- Sheet 1 ---\nName\tRich text\nInline\t1\t3.5\n99\n\n--- Sheet 3 ---\n42"},
		{"odt: headings, spans, list items", "docs/notes.odt", odt,
			"Heading\nonetwothreefour\nIt builds a knowledge graph.\nbullet"},
		{"ods: one sheet per table", "data/budget.ods", ods,
			"--- Sheet 1 ---\nParser\tLanguage\nGo\tlang\tyes\n\n--- Sheet 2 ---\nx"},
		{"odp: one slide per page", "slides/talk.odp", odp,
			"--- Slide 1 ---\nCodeEagle Overview\n\n\n--- Slide 2 ---\nSemantic Search\nTopic Extraction"},
		// A page with no text has no marker; T* is a line break, and the
		// pieces of a TJ array are joined. The adjustment between them is
		// kept small: poppler reads a large one (-120) as a space and the
		// pure Go build does not, so the two builds disagree there.
		{"pdf: pages with text", "papers/paper.pdf", pdfOf(helloPage, blankPage, twoLinePage, kernedPage),
			"--- Page 1 ---\nHello World\n\n--- Page 3 ---\nLine one\nLine two\n\n--- Page 4 ---\nKerned"},
		{"pdf: no pages", "papers/empty.pdf", pdfOf(), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := generic.ExtractDocumentWithCtx(context.Background(), tt.path, tt.content)
			if err != nil {
				t.Fatalf("ExtractDocumentWithCtx(%q) error: %v", tt.path, err)
			}
			if got != tt.want {
				t.Errorf("ExtractDocumentWithCtx(%q) =\n%q\nwant\n%q", tt.path, got, tt.want)
			}
			// ExtractDocument is the same read without a context.
			plain, err := generic.ExtractDocument(tt.path, tt.content)
			if err != nil || plain != got {
				t.Errorf("ExtractDocument(%q) = %q, %v; want %q, nil", tt.path, plain, err, got)
			}
		})
	}
}

func TestExtractDocumentWithCtxErrors(t *testing.T) {
	notZip := []byte("not a zip")
	// A word-processor file with no body is not an error, just empty.
	emptyZip := zipOf(t)
	headerOnly := zipOf(t, zipEntry{"word/header1.xml", wordPart("hdr", `<w:p><w:r><w:t>Only a header</w:t></w:r></w:p>`)})
	brokenBody := zipOf(t, zipEntry{"word/document.xml", `<w:document ` + wordNS + `><w:body><w:p>`})

	tests := []struct {
		name    string
		path    string
		content []byte
		want    string
		wantErr string
	}{
		{"unsupported extension", "notes/file.xyz", []byte("data"), "", "unsupported document format: .xyz"},
		{"no extension", "Makefile", []byte("data"), "", "unsupported document format: "},
		// Plain text is not a document: the caller reads it as it is.
		{"plain text", "notes/plan.txt", []byte("hello"), "", "unsupported document format: .txt"},
		{"markdown", "README.md", []byte("# hi"), "", "unsupported document format: .md"},
		{"docx not a zip", "a.docx", notZip, "", "open ZIP: zip: not a valid zip file"},
		{"pptx not a zip", "a.pptx", notZip, "", "open ZIP: zip: not a valid zip file"},
		{"xlsx not a zip", "a.xlsx", notZip, "", "open ZIP: zip: not a valid zip file"},
		{"odt not a zip", "a.odt", notZip, "", "open ZIP: zip: not a valid zip file"},
		{"ods not a zip", "a.ods", notZip, "", "open ZIP: zip: not a valid zip file"},
		{"odp not a zip", "a.odp", notZip, "", "open ZIP: zip: not a valid zip file"},
		{"docx without a body", "a.docx", emptyZip, "", ""},
		{"docx with only a header", "a.docx", headerOnly, "Only a header", ""},
		{"docx with a broken body", "a.docx", brokenBody, "", "parse document.xml: XML syntax error on line 1: unexpected EOF"},
		{"pptx without slides", "a.pptx", emptyZip, "", "no slides found in PPTX"},
		{"xlsx without sheets", "a.xlsx", emptyZip, "", ""},
		{"odt without content.xml", "a.odt", emptyZip, "", "content.xml not found in ODF archive"},
		{"ods without content.xml", "a.ods", emptyZip, "", "content.xml not found in ODF archive"},
		{"odp without content.xml", "a.odp", emptyZip, "", "content.xml not found in ODF archive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := generic.ExtractDocumentWithCtx(context.Background(), tt.path, tt.content)
			gotErr := ""
			if err != nil {
				gotErr = err.Error()
			}
			if got != tt.want || gotErr != tt.wantErr {
				t.Errorf("ExtractDocumentWithCtx(%q) = %q, %q; want %q, %q", tt.path, got, gotErr, tt.want, tt.wantErr)
			}
		})
	}

	// The PDF reader's own message differs between the pure Go and poppler
	// builds, so only the wrapping is pinned.
	got, err := generic.ExtractDocumentWithCtx(context.Background(), "a.pdf", []byte("not a pdf"))
	if err == nil || !strings.HasPrefix(err.Error(), "open PDF: ") || got != "" {
		t.Errorf("corrupt PDF = %q, %v; want \"\" and an \"open PDF: \" error", got, err)
	}
}

// TestExtractDocumentWithCtxCancellation pins how a context is observed:
// PDF extraction checks it before each page and, rather than failing,
// returns what it read with a note of where it stopped; the note is not
// trimmed the way a finished extraction is. No other format looks at it.
func TestExtractDocumentWithCtxCancellation(t *testing.T) {
	pdf := pdfOf(helloPage, twoLinePage, kernedPage)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, cancelExpired := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer cancelExpired()

	tests := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{"cancelled before the first page", cancelled,
			"\n[Stopped at page 1 of 3: context canceled]\n"},
		{"deadline passed before the first page", expired,
			"\n[Stopped at page 1 of 3: context deadline exceeded]\n"},
		{"cancelled after the first page", &cancelAfter{Context: context.Background(), limit: 1},
			"--- Page 1 ---\nHello World\n\n\n[Stopped at page 2 of 3: context canceled]\n"},
		{"cancelled after the last page", &cancelAfter{Context: context.Background(), limit: 3},
			"--- Page 1 ---\nHello World\n\n--- Page 2 ---\nLine one\nLine two\n\n--- Page 3 ---\nKerned"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := generic.ExtractDocumentWithCtx(tt.ctx, "papers/paper.pdf", pdf)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got\n%q\nwant\n%q", got, tt.want)
			}
		})
	}

	// Other formats ignore the context and extract in full.
	odt := zipOf(t, zipEntry{"content.xml", odfContent(`<office:text><text:p>still read</text:p></office:text>`)})
	for _, tc := range []struct {
		path    string
		content []byte
	}{
		{"docs/notes.odt", odt},
		{"docs/spec.docx", zipOf(t, zipEntry{"word/document.xml", wordPart("document", `<w:body><w:p><w:r><w:t>still read</w:t></w:r></w:p></w:body>`)})},
	} {
		got, err := generic.ExtractDocumentWithCtx(cancelled, tc.path, tc.content)
		if err != nil || got != "still read" {
			t.Errorf("%s with a cancelled context = %q, %v; want %q, nil", tc.path, got, err, "still read")
		}
	}
}
