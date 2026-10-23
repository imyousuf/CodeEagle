package generic

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// LangDocument is the language of non-code documents whose text is kept in
// sections (PDFs, slide decks, word-processor files, plain text), so they
// can be read, quoted and related like markdown.
const LangDocument = "document"

// TextSection is one part of a document's extracted text.
type TextSection struct {
	Heading string
	Text    string
}

// Bounds on sectioning.
const (
	maxSections       = 200
	minPageChars      = 1500
	paragraphGroup    = 4000
	maxSectionTextLen = 6000
)

var (
	pageMarker  = regexp.MustCompile(`(?m)^--- (Page|Slide|Sheet) (\d+) ---\s*$`)
	ligatures   = strings.NewReplacer("ﬁ", "fi", "ﬂ", "fl", "ﬀ", "ff", "ﬃ", "ffi", "ﬄ", "ffl", "\u00ad", "")
	hyphenBreak = regexp.MustCompile(`(\p{L})-\n(\p{Ll})`)
)

// proseExtensions are the formats whose text is prose a person wrote: word
// processor files, slide decks, PDFs, and plain or lightly marked-up text.
// Spreadsheets, data, configuration and code are not, so they get no
// sections and are never subjects.
var proseExtensions = map[string]bool{
	".docx": true, ".pptx": true, ".odt": true, ".odp": true, ".pdf": true,
	".txt": true, ".text": true, ".rst": true, ".adoc": true, ".asciidoc": true, ".org": true,
	".textile": true, ".creole": true, ".wiki": true, ".mediawiki": true, ".rdoc": true,
}

// Sectioned reports whether a file's text is kept in sections.
func Sectioned(path string) bool { return proseExtensions[strings.ToLower(filepath.Ext(path))] }

// Normalize undoes what PDF extraction does to words, ligatures and
// hyphenation across line ends, once, where sections are made, so the text
// stored and the quotes checked against it agree.
func Normalize(text string) string {
	return hyphenBreak.ReplaceAllString(ligatures.Replace(text), "$1$2")
}

// Sectionize splits a document's extracted text into sections: pages
// (short ones merged), slides titled by their first line, or groups of
// paragraphs. Files that are not prose get none.
func Sectionize(path, text string) []TextSection {
	if !Sectioned(path) || strings.TrimSpace(text) == "" {
		return nil
	}
	text = Normalize(text)
	var out []TextSection
	if locs := pageMarker.FindAllStringSubmatchIndex(text, -1); len(locs) > 0 {
		kind := text[locs[0][2]:locs[0][3]]
		if kind == "Sheet" {
			return nil
		}
		type page struct {
			n    int
			text string
		}
		var pages []page
		for i, loc := range locs {
			end := len(text)
			if i+1 < len(locs) {
				end = locs[i+1][0]
			}
			n, _ := strconv.Atoi(text[loc[4]:loc[5]])
			pages = append(pages, page{n, strings.TrimSpace(text[loc[1]:end])})
		}
		if kind == "Slide" {
			for _, p := range pages {
				if p.text == "" {
					continue
				}
				title := firstLine(p.text)
				out = append(out, TextSection{Heading: fmt.Sprintf("Slide %d: %s", p.n, title), Text: p.text})
			}
		} else {
			// Pages run into each other; a page of a few lines is not a
			// section of its own.
			var buf strings.Builder
			first := 0
			flush := func(last int) {
				if buf.Len() == 0 {
					return
				}
				heading := fmt.Sprintf("Page %d", first)
				if last != first {
					heading = fmt.Sprintf("Pages %d-%d", first, last)
				}
				out = append(out, TextSection{Heading: heading, Text: strings.TrimSpace(buf.String())})
				buf.Reset()
			}
			for i, p := range pages {
				if buf.Len() == 0 {
					first = p.n
				}
				buf.WriteString(p.text)
				buf.WriteString("\n\n")
				if buf.Len() >= minPageChars || i == len(pages)-1 {
					flush(p.n)
				}
			}
		}
	} else {
		// Plain text: groups of paragraphs.
		var buf strings.Builder
		part := 0
		for _, para := range strings.Split(text, "\n\n") {
			if buf.Len()+len(para) > paragraphGroup && buf.Len() > 0 {
				part++
				out = append(out, TextSection{Heading: fmt.Sprintf("Part %d: %s", part, firstLine(buf.String())), Text: strings.TrimSpace(buf.String())})
				buf.Reset()
			}
			buf.WriteString(para)
			buf.WriteString("\n\n")
		}
		if strings.TrimSpace(buf.String()) != "" {
			part++
			out = append(out, TextSection{Heading: fmt.Sprintf("Part %d: %s", part, firstLine(buf.String())), Text: strings.TrimSpace(buf.String())})
		}
	}
	for i := range out {
		if len(out[i].Text) > maxSectionTextLen {
			out[i].Text = out[i].Text[:maxSectionTextLen]
		}
	}
	if len(out) > maxSections {
		out = out[:maxSections]
	}
	return out
}

func firstLine(text string) string {
	line := strings.TrimSpace(text)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	line = strings.Join(strings.Fields(line), " ")
	if len(line) > 60 {
		line = line[:60] + "…"
	}
	return line
}
