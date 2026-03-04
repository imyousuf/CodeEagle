package generic

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
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

// --- XLSX fixture ---

func createTestXLSX() []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	ct, _ := w.Create("[Content_Types].xml")
	ct.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>
  <Override PartName="/xl/sharedStrings.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"/>
</Types>`))

	// Shared strings
	ss, _ := w.Create("xl/sharedStrings.xml")
	ss.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="3" uniqueCount="3">
  <si><t>Name</t></si>
  <si><t>Language</t></si>
  <si><t>CodeEagle</t></si>
</sst>`))

	// Sheet with shared string refs and numeric values
	sh, _ := w.Create("xl/worksheets/sheet1.xml")
	sh.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <sheetData>
    <row r="1">
      <c r="A1" t="s"><v>0</v></c>
      <c r="B1" t="s"><v>1</v></c>
    </row>
    <row r="2">
      <c r="A2" t="s"><v>2</v></c>
      <c r="B2"><v>42</v></c>
    </row>
  </sheetData>
</worksheet>`))

	w.Close()
	return buf.Bytes()
}

// --- ODT fixture ---

func createTestODT() []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	f, _ := w.Create("content.xml")
	f.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<office:document-content
  xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0"
  xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0">
  <office:body>
    <office:text>
      <text:p>CodeEagle indexes your codebase.</text:p>
      <text:p>It builds a <text:span>knowledge graph</text:span> of source code.</text:p>
    </office:text>
  </office:body>
</office:document-content>`))

	w.Close()
	return buf.Bytes()
}

// --- ODS fixture ---

func createTestODS() []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	f, _ := w.Create("content.xml")
	f.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<office:document-content
  xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0"
  xmlns:table="urn:oasis:names:tc:opendocument:xmlns:table:1.0"
  xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0">
  <office:body>
    <office:spreadsheet>
      <table:table table:name="Sheet1">
        <table:table-row>
          <table:table-cell><text:p>Parser</text:p></table:table-cell>
          <table:table-cell><text:p>Language</text:p></table:table-cell>
        </table:table-row>
        <table:table-row>
          <table:table-cell><text:p>Go</text:p></table:table-cell>
          <table:table-cell><text:p>golang</text:p></table:table-cell>
        </table:table-row>
      </table:table>
    </office:spreadsheet>
  </office:body>
</office:document-content>`))

	w.Close()
	return buf.Bytes()
}

// --- ODP fixture ---

func createTestODP() []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	f, _ := w.Create("content.xml")
	f.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<office:document-content
  xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0"
  xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0"
  xmlns:presentation="urn:oasis:names:tc:opendocument:xmlns:presentation:1.0"
  xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0">
  <office:body>
    <office:presentation>
      <draw:page draw:name="Slide1">
        <draw:frame><draw:text-box>
          <text:p>CodeEagle Overview</text:p>
        </draw:text-box></draw:frame>
      </draw:page>
      <draw:page draw:name="Slide2">
        <draw:frame><draw:text-box>
          <text:p>Semantic Search</text:p>
          <text:p>Topic Extraction</text:p>
        </draw:text-box></draw:frame>
      </draw:page>
    </office:presentation>
  </office:body>
</office:document-content>`))

	w.Close()
	return buf.Bytes()
}

// --- PDF fixture ---

// createTestPDF builds a minimal valid PDF with "Hello World" text
// and correct xref byte offsets.
func createTestPDF() []byte {
	var b bytes.Buffer

	// Track object byte offsets for xref table.
	offsets := make([]int, 6) // objects 0-5

	b.WriteString("%PDF-1.0\n")

	offsets[1] = b.Len()
	b.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")

	offsets[2] = b.Len()
	b.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")

	offsets[3] = b.Len()
	b.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>\nendobj\n")

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

func TestExtractDOCX(t *testing.T) {
	content := createTestDOCX()
	text, err := extractDOCX(content)
	if err != nil {
		t.Fatalf("extractDOCX() error: %v", err)
	}

	if !strings.Contains(text, "Hello CodeEagle") {
		t.Errorf("expected 'Hello CodeEagle' in output, got: %s", text)
	}
	if !strings.Contains(text, "test document") {
		t.Errorf("expected 'test document' in output, got: %s", text)
	}
	if !strings.Contains(text, "multiple paragraphs") {
		t.Errorf("expected 'multiple paragraphs' in output, got: %s", text)
	}
}

func TestExtractPPTX(t *testing.T) {
	content := createTestPPTX()
	text, err := extractPPTX(content)
	if err != nil {
		t.Fatalf("extractPPTX() error: %v", err)
	}

	if !strings.Contains(text, "Welcome to CodeEagle") {
		t.Errorf("expected slide 1 text, got: %s", text)
	}
	if !strings.Contains(text, "Knowledge Graph Features") {
		t.Errorf("expected slide 2 text, got: %s", text)
	}
	if !strings.Contains(text, "Semantic search and RAG") {
		t.Errorf("expected slide 2 body text, got: %s", text)
	}
	if !strings.Contains(text, "Slide 1") {
		t.Error("expected slide markers")
	}
}

func TestExtractXLSX(t *testing.T) {
	content := createTestXLSX()
	text, err := extractXLSX(content)
	if err != nil {
		t.Fatalf("extractXLSX() error: %v", err)
	}

	// Should resolve shared string references.
	if !strings.Contains(text, "Name") {
		t.Errorf("expected shared string 'Name', got: %s", text)
	}
	if !strings.Contains(text, "Language") {
		t.Errorf("expected shared string 'Language', got: %s", text)
	}
	if !strings.Contains(text, "CodeEagle") {
		t.Errorf("expected shared string 'CodeEagle', got: %s", text)
	}
	// Should have numeric value.
	if !strings.Contains(text, "42") {
		t.Errorf("expected numeric value '42', got: %s", text)
	}
}

func TestExtractODT(t *testing.T) {
	content := createTestODT()
	text, err := extractODT(content)
	if err != nil {
		t.Fatalf("extractODT() error: %v", err)
	}

	if !strings.Contains(text, "CodeEagle indexes your codebase") {
		t.Errorf("expected paragraph text, got: %s", text)
	}
	if !strings.Contains(text, "knowledge graph") {
		t.Errorf("expected span text, got: %s", text)
	}
}

func TestExtractODS(t *testing.T) {
	content := createTestODS()
	text, err := extractODS(content)
	if err != nil {
		t.Fatalf("extractODS() error: %v", err)
	}

	if !strings.Contains(text, "Parser") {
		t.Errorf("expected cell value 'Parser', got: %s", text)
	}
	if !strings.Contains(text, "golang") {
		t.Errorf("expected cell value 'golang', got: %s", text)
	}
}

func TestExtractODP(t *testing.T) {
	content := createTestODP()
	text, err := extractODP(content)
	if err != nil {
		t.Fatalf("extractODP() error: %v", err)
	}

	if !strings.Contains(text, "CodeEagle Overview") {
		t.Errorf("expected slide 1 text, got: %s", text)
	}
	if !strings.Contains(text, "Semantic Search") {
		t.Errorf("expected slide 2 text, got: %s", text)
	}
	if !strings.Contains(text, "Topic Extraction") {
		t.Errorf("expected slide 2 body text, got: %s", text)
	}
}

func TestExtractPDF(t *testing.T) {
	content := createTestPDF()
	text, err := extractPDF(content)
	if err != nil {
		t.Fatalf("extractPDF() error: %v", err)
	}

	if !strings.Contains(text, "Hello World") {
		t.Errorf("expected 'Hello World', got: %s", text)
	}
}

func TestExtractDocument_Errors(t *testing.T) {
	// Corrupt ZIP.
	_, err := ExtractDocument("test.docx", []byte("not a zip"))
	if err == nil {
		t.Error("expected error for corrupt DOCX")
	}

	// Unsupported format.
	_, err = ExtractDocument("test.xyz", []byte("data"))
	if err == nil {
		t.Error("expected error for unsupported format")
	}

	// Empty ZIP (no document.xml).
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	w.Close()
	_, err = extractDOCX(buf.Bytes())
	// Should not error, just return empty string.
	if err != nil {
		t.Errorf("empty DOCX should not error: %v", err)
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
