package generic

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// extractODT extracts plain text from an ODT (OpenDocument Text) file.
// Parses content.xml for <text:p> and <text:span> elements.
func extractODT(content []byte) (string, error) {
	xmlData, err := readODFContent(content)
	if err != nil {
		return "", err
	}
	return parseODFText(xmlData)
}

// extractODS extracts plain text from an ODS (OpenDocument Spreadsheet) file.
// Parses content.xml for table cells containing <text:p> elements.
func extractODS(content []byte) (string, error) {
	xmlData, err := readODFContent(content)
	if err != nil {
		return "", err
	}
	return parseODSText(xmlData)
}

// extractODP extracts plain text from an ODP (OpenDocument Presentation) file.
// Parses content.xml for <text:p> elements within draw frames.
func extractODP(content []byte) (string, error) {
	xmlData, err := readODFContent(content)
	if err != nil {
		return "", err
	}
	return parseODPText(xmlData)
}

// readODFContent opens a ZIP archive and reads content.xml.
func readODFContent(content []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, fmt.Errorf("open ZIP: %w", err)
	}

	f := findZipFile(zr, "content.xml")
	if f == nil {
		return nil, fmt.Errorf("content.xml not found in ODF archive")
	}

	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// parseODFText extracts text from ODF content.xml (ODT format).
// Collects character data from <text:p>, <text:span>, <text:h> elements.
func parseODFText(xmlData []byte) (string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(xmlData))
	var b strings.Builder
	depth := 0 // Track nesting in text paragraph/heading elements.

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
			local := t.Name.Local
			if local == "p" || local == "h" {
				depth++
			}
		case xml.EndElement:
			local := t.Name.Local
			if local == "p" || local == "h" {
				if depth > 0 {
					depth--
				}
				if depth == 0 {
					b.WriteByte('\n')
				}
			}
		case xml.CharData:
			if depth > 0 {
				b.Write(t)
			}
		}
	}

	return strings.TrimSpace(b.String()), nil
}

// parseODSText extracts text from ODF content.xml (ODS format).
// Walks table → row → cell → text:p, producing tab-separated rows.
func parseODSText(xmlData []byte) (string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(xmlData))
	var b strings.Builder
	inTable := false
	inRow := false
	inCell := false
	inText := false
	firstCellInRow := true
	tableCount := 0

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
			switch t.Name.Local {
			case "table":
				inTable = true
				tableCount++
				if tableCount > 1 {
					b.WriteByte('\n')
				}
				b.WriteString(fmt.Sprintf("--- Sheet %d ---\n", tableCount))
			case "table-row":
				if inTable {
					inRow = true
					firstCellInRow = true
				}
			case "table-cell":
				if inRow {
					inCell = true
				}
			case "p":
				if inCell {
					inText = true
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "table":
				inTable = false
			case "table-row":
				if inRow {
					b.WriteByte('\n')
				}
				inRow = false
			case "table-cell":
				inCell = false
			case "p":
				inText = false
			}
		case xml.CharData:
			if inText {
				text := string(t)
				if text != "" {
					if !firstCellInRow {
						b.WriteByte('\t')
					}
					b.WriteString(text)
					firstCellInRow = false
				}
			}
		}
	}

	return strings.TrimSpace(b.String()), nil
}

// parseODPText extracts text from ODF content.xml (ODP format).
// Walks draw:page → draw:frame → text:p, inserting slide markers.
func parseODPText(xmlData []byte) (string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(xmlData))
	var b strings.Builder
	inPage := false
	textDepth := 0
	pageCount := 0

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
			switch t.Name.Local {
			case "page":
				inPage = true
				pageCount++
				if pageCount > 1 {
					b.WriteString("\n\n")
				}
				b.WriteString(fmt.Sprintf("--- Slide %d ---\n", pageCount))
			case "p", "h":
				if inPage {
					textDepth++
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "page":
				inPage = false
			case "p", "h":
				if textDepth > 0 {
					textDepth--
				}
				if inPage && textDepth == 0 {
					b.WriteByte('\n')
				}
			}
		case xml.CharData:
			if textDepth > 0 {
				b.Write(t)
			}
		}
	}

	return strings.TrimSpace(b.String()), nil
}
