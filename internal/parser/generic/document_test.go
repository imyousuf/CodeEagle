package generic

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
)

// --- DOCX fixture ---

func createTestDOCX() []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	// [Content_Types].xml (required for valid OOXML)
	ct, _ := w.Create("[Content_Types].xml")
	ct.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="xml" ContentType="application/xml"/>
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`))

	// word/document.xml
	doc, _ := w.Create("word/document.xml")
	doc.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>Hello CodeEagle</w:t></w:r></w:p>
    <w:p><w:r><w:t>This is a test document for text extraction.</w:t></w:r></w:p>
    <w:p><w:r><w:t>It has multiple paragraphs.</w:t></w:r></w:p>
  </w:body>
</w:document>`))

	w.Close()
	return buf.Bytes()
}

// --- PPTX fixture ---

func createTestPPTX() []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	ct, _ := w.Create("[Content_Types].xml")
	ct.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/ppt/slides/slide1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>
  <Override PartName="/ppt/slides/slide2.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>
</Types>`))

	s1, _ := w.Create("ppt/slides/slide1.xml")
	s1.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
       xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
  <p:cSld><p:spTree>
    <p:sp><p:txBody>
      <a:p><a:r><a:t>Welcome to CodeEagle</a:t></a:r></a:p>
    </p:txBody></p:sp>
  </p:spTree></p:cSld>
</p:sld>`))

	s2, _ := w.Create("ppt/slides/slide2.xml")
	s2.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
       xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
  <p:cSld><p:spTree>
    <p:sp><p:txBody>
      <a:p><a:r><a:t>Knowledge Graph Features</a:t></a:r></a:p>
      <a:p><a:r><a:t>Semantic search and RAG</a:t></a:r></a:p>
    </p:txBody></p:sp>
  </p:spTree></p:cSld>
</p:sld>`))

	w.Close()
	return buf.Bytes()
}

// --- PDF fixture ---

// createTestPDF builds a minimal valid PDF with "Hello World" text
// and correct xref byte offsets.
func createTestPDF() []byte { return createTestPDFContents("4 0 R") }

// createTestPDFContents builds a one-page PDF whose page names its content
// as contents: a stream reference, or an array of them.
func createTestPDFContents(contents string) []byte {
	var b bytes.Buffer

	// Track object byte offsets for xref table.
	offsets := make([]int, 6) // objects 0-5

	b.WriteString("%PDF-1.0\n")

	offsets[1] = b.Len()
	b.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")

	offsets[2] = b.Len()
	b.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")

	offsets[3] = b.Len()
	b.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents " + contents + " /Resources << /Font << /F1 5 0 R >> >> >>\nendobj\n")

	// Stream content for the page — draws "Hello World".
	streamContent := "BT /F1 12 Tf 100 700 Td (Hello World) Tj ET"

	offsets[4] = b.Len()
	fmt.Fprintf(&b, "4 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(streamContent), streamContent)

	offsets[5] = b.Len()
	b.WriteString("5 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n")

	xrefOffset := b.Len()
	b.WriteString("xref\n")
	b.WriteString("0 6\n")
	fmt.Fprintf(&b, "%010d 65535 f \n", 0)
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&b, "%010d 00000 n \n", offsets[i])
	}

	b.WriteString("trailer\n<< /Size 6 /Root 1 0 R >>\n")
	fmt.Fprintf(&b, "startxref\n%d\n%%%%EOF\n", xrefOffset)

	return b.Bytes()
}

// --- Tests ---

func TestExtractPDF(t *testing.T) {
	content := createTestPDF()
	text, err := extractPDF(context.Background(), content)
	if err != nil {
		t.Fatalf("extractPDF() error: %v", err)
	}

	if !strings.Contains(text, "Hello World") {
		t.Errorf("expected 'Hello World', got: %s", text)
	}
}

// TestExtractPDF_ContentArray covers a page whose content is an array of
// streams, which is common and which the pure Go library's own text helper
// looped on forever.
func TestExtractPDF_ContentArray(t *testing.T) {
	done := make(chan string, 1)
	go func() {
		text, _ := extractPDF(context.Background(), createTestPDFContents("[4 0 R]"))
		done <- text
	}()
	select {
	case text := <-done:
		if !strings.Contains(text, "Hello World") {
			t.Errorf("text = %q", text)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("extraction did not finish")
	}
}

func TestExtractPDF_PageMarkers(t *testing.T) {
	content := createTestPDF()
	text, err := extractPDF(context.Background(), content)
	if err != nil {
		t.Fatalf("extractPDF() error: %v", err)
	}

	if !strings.Contains(text, "--- Page 1 ---") {
		t.Errorf("expected page marker, got: %s", text)
	}
}

func TestClassify_DocumentExtensions(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
	}{
		{"docx", "docs/spec.docx"},
		{"pptx", "slides/demo.pptx"},
		{"xlsx", "data/report.xlsx"},
		{"odt", "docs/notes.odt"},
		{"ods", "data/budget.ods"},
		{"odp", "slides/talk.odp"},
		{"pdf", "docs/manual.pdf"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.filePath, nil)
			if got != FileClassDocument {
				t.Errorf("Classify(%q) = %d, want FileClassDocument (%d)", tt.filePath, got, FileClassDocument)
			}
		})
	}
}

func TestParseFile_Document(t *testing.T) {
	p := NewGenericParser(nil, nil, nil, 0)
	content := createTestDOCX()

	result, err := p.ParseFile("docs/spec.docx", content)
	if err != nil {
		t.Fatalf("ParseFile() error: %v", err)
	}

	if len(result.Nodes) == 0 {
		t.Fatal("expected at least 1 node")
	}

	// Find the document node.
	var docNode *graph.Node
	for _, n := range result.Nodes {
		if n.Type == graph.NodeDocument {
			docNode = n
			break
		}
	}
	if docNode == nil {
		t.Fatal("expected a NodeDocument node")
	}

	if docNode.Properties["kind"] != "document" {
		t.Errorf("expected kind 'document', got %s", docNode.Properties["kind"])
	}
	if !strings.Contains(docNode.DocComment, "Hello CodeEagle") {
		t.Errorf("expected extracted text in DocComment, got: %s", docNode.DocComment)
	}
}
