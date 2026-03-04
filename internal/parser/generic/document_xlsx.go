package generic

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// extractXLSX extracts plain text from an XLSX file.
// Reads the shared string table and then iterates sheets, producing
// tab-separated rows per sheet.
func extractXLSX(content []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", fmt.Errorf("open ZIP: %w", err)
	}

	// Parse shared strings table.
	sharedStrings, err := parseSharedStrings(zr)
	if err != nil {
		// Not all XLSX files have shared strings; continue without.
		sharedStrings = nil
	}

	// Find and sort sheet files.
	var sheetFiles []string
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "xl/worksheets/sheet") && strings.HasSuffix(f.Name, ".xml") {
			sheetFiles = append(sheetFiles, f.Name)
		}
	}
	sort.Strings(sheetFiles)

	var parts []string
	for i, name := range sheetFiles {
		f := findZipFile(zr, name)
		if f == nil {
			continue
		}
		text, err := parseSheetText(f, sharedStrings)
		if err != nil {
			continue
		}
		if text != "" {
			parts = append(parts, fmt.Sprintf("--- Sheet %d ---\n%s", i+1, text))
		}
	}

	return strings.Join(parts, "\n\n"), nil
}

// parseSharedStrings reads xl/sharedStrings.xml and returns a lookup table.
func parseSharedStrings(zr *zip.Reader) ([]string, error) {
	f := findZipFile(zr, "xl/sharedStrings.xml")
	if f == nil {
		return nil, fmt.Errorf("no shared strings")
	}

	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	decoder := xml.NewDecoder(rc)
	var strings_ []string
	var currentStr strings.Builder
	inSI := false
	inT := false

	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inSI = true
				currentStr.Reset()
			case "t":
				if inSI {
					inT = true
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "si":
				strings_ = append(strings_, currentStr.String())
				inSI = false
			case "t":
				inT = false
			}
		case xml.CharData:
			if inT {
				currentStr.Write(t)
			}
		}
	}

	return strings_, nil
}

// parseSheetText extracts cell values from a worksheet XML.
// Handles shared string references (t="s"), inline strings (t="inlineStr"),
// and literal values.
func parseSheetText(f *zip.File, sharedStrings []string) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	decoder := xml.NewDecoder(rc)
	var b strings.Builder
	var cellType string
	var cellValue strings.Builder
	inRow := false
	inCell := false
	inValue := false
	inInlineT := false
	firstCellInRow := true

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
			case "row":
				inRow = true
				firstCellInRow = true
			case "c":
				if inRow {
					inCell = true
					cellType = ""
					cellValue.Reset()
					for _, attr := range t.Attr {
						if attr.Name.Local == "t" {
							cellType = attr.Value
						}
					}
				}
			case "v":
				if inCell {
					inValue = true
				}
			case "t":
				// Inline string text element inside <is>.
				if inCell && cellType == "inlineStr" {
					inInlineT = true
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "row":
				if inRow {
					b.WriteByte('\n')
				}
				inRow = false
			case "c":
				if inCell {
					val := cellValue.String()
					resolved := resolveCellValue(val, cellType, sharedStrings)
					if resolved != "" {
						if !firstCellInRow {
							b.WriteByte('\t')
						}
						b.WriteString(resolved)
						firstCellInRow = false
					}
				}
				inCell = false
			case "v":
				inValue = false
			case "t":
				inInlineT = false
			}
		case xml.CharData:
			if inValue || inInlineT {
				cellValue.Write(t)
			}
		}
	}

	return strings.TrimSpace(b.String()), nil
}

// resolveCellValue converts a raw cell value to its display string.
func resolveCellValue(raw, cellType string, sharedStrings []string) string {
	if raw == "" {
		return ""
	}
	switch cellType {
	case "s":
		// Shared string reference.
		idx, err := strconv.Atoi(raw)
		if err != nil || sharedStrings == nil || idx < 0 || idx >= len(sharedStrings) {
			return raw
		}
		return sharedStrings[idx]
	default:
		return raw
	}
}
