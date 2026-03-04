package generic

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strings"
)

// extractDOCX extracts plain text from a DOCX file (ZIP containing word/document.xml).
// Collects text from <w:t> elements, inserting newlines at paragraph boundaries.
func extractDOCX(content []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", fmt.Errorf("open ZIP: %w", err)
	}

	var parts []string

	// Extract from headers, document body, and footers in order.
	var headers, footers []string
	var bodyFile *zip.File
	for _, f := range zr.File {
		switch {
		case strings.HasPrefix(f.Name, "word/header") && strings.HasSuffix(f.Name, ".xml"):
			headers = append(headers, f.Name)
		case f.Name == "word/document.xml":
			bodyFile = f
		case strings.HasPrefix(f.Name, "word/footer") && strings.HasSuffix(f.Name, ".xml"):
			footers = append(footers, f.Name)
		}
	}

	// Process headers.
	sort.Strings(headers)
	for _, name := range headers {
		if f := findZipFile(zr, name); f != nil {
			if text, err := parseOOXMLText(f, "t", "p"); err == nil && text != "" {
				parts = append(parts, text)
			}
		}
	}

	// Process main document body.
	if bodyFile != nil {
		text, err := parseOOXMLText(bodyFile, "t", "p")
		if err != nil {
			return "", fmt.Errorf("parse document.xml: %w", err)
		}
		parts = append(parts, text)
	}

	// Process footers.
	sort.Strings(footers)
	for _, name := range footers {
		if f := findZipFile(zr, name); f != nil {
			if text, err := parseOOXMLText(f, "t", "p"); err == nil && text != "" {
				parts = append(parts, text)
			}
		}
	}

	return strings.Join(parts, "\n"), nil
}

// extractPPTX extracts plain text from a PPTX file (ZIP containing slide XMLs).
// Reads [Content_Types].xml to discover slide files, then extracts <a:t> elements.
func extractPPTX(content []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", fmt.Errorf("open ZIP: %w", err)
	}

	// Find slide files from [Content_Types].xml or by path pattern.
	slideFiles := discoverSlideFiles(zr)
	if len(slideFiles) == 0 {
		return "", fmt.Errorf("no slides found in PPTX")
	}

	sort.Strings(slideFiles)

	var parts []string
	for i, name := range slideFiles {
		f := findZipFile(zr, name)
		if f == nil {
			continue
		}
		text, err := parseOOXMLText(f, "t", "p")
		if err != nil {
			continue
		}
		if text != "" {
			parts = append(parts, fmt.Sprintf("--- Slide %d ---\n%s", i+1, text))
		}
	}

	return strings.Join(parts, "\n\n"), nil
}

// parseOOXMLText extracts text from an OOXML ZIP entry by collecting character data
// inside the specified text element local name (e.g., "t" for both DOCX and PPTX).
// Inserts newlines at paragraph element boundaries (local name "p").
// Note: encoding/xml strips namespace prefixes — "w:t" becomes Local="t", Space="...".
func parseOOXMLText(f *zip.File, textElem, paraElem string) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	decoder := xml.NewDecoder(rc)
	var b strings.Builder
	inText := false
	skipInstr := false

	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			localName := t.Name.Local
			if localName == textElem && !skipInstr {
				inText = true
			}
			// Skip field codes in DOCX (instrText).
			if localName == "instrText" {
				skipInstr = true
			}
		case xml.EndElement:
			localName := t.Name.Local
			if localName == textElem {
				inText = false
			}
			if localName == "instrText" {
				skipInstr = false
			}
			// Insert newline at paragraph boundary.
			if localName == paraElem {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
			}
		case xml.CharData:
			if inText {
				b.Write(t)
			}
		}
	}

	return strings.TrimSpace(b.String()), nil
}

// discoverSlideFiles finds slide XML paths in a PPTX archive.
// First tries [Content_Types].xml, falls back to matching ppt/slides/slide*.xml.
func discoverSlideFiles(zr *zip.Reader) []string {
	// Try [Content_Types].xml first.
	ctFile := findZipFile(zr, "[Content_Types].xml")
	if ctFile != nil {
		if slides := parseSlidesFromContentTypes(ctFile); len(slides) > 0 {
			return slides
		}
	}

	// Fallback: match by path pattern.
	var slides []string
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "ppt/slides/slide") && strings.HasSuffix(f.Name, ".xml") {
			slides = append(slides, f.Name)
		}
	}
	return slides
}

// parseSlidesFromContentTypes parses [Content_Types].xml to find slide part names.
func parseSlidesFromContentTypes(f *zip.File) []string {
	rc, err := f.Open()
	if err != nil {
		return nil
	}
	defer rc.Close()

	decoder := xml.NewDecoder(rc)
	var slides []string

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local != "Override" {
			continue
		}
		var partName, contentType string
		for _, attr := range se.Attr {
			switch attr.Name.Local {
			case "PartName":
				partName = attr.Value
			case "ContentType":
				contentType = attr.Value
			}
		}
		if strings.Contains(contentType, "presentationml.slide+xml") && partName != "" {
			// Remove leading "/" from part name.
			slides = append(slides, strings.TrimPrefix(partName, "/"))
		}
	}

	return slides
}

// findZipFile finds a file by name in a zip.Reader.
func findZipFile(zr *zip.Reader, name string) *zip.File {
	for _, f := range zr.File {
		if f.Name == name {
			return f
		}
	}
	return nil
}
