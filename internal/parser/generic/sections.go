package generic

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/imyousuf/CodeEagle/internal/parser/chatlog"
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

// PropChatFormat marks a document that is a chat log, naming the layout it
// was read from.
const PropChatFormat = "chat_format"

// chatExtensions are the formats a chat log is looked for in: exports and
// histories in JSON, and conversations pasted into text or a document.
var chatExtensions = map[string]bool{
	".json": true, ".jsonl": true, ".txt": true, ".text": true,
	".docx": true, ".odt": true, ".pdf": true,
}

// detectChat reads a file as a chat log if it is one.
func (p *GenericParser) detectChat(path string, class FileClass, content []byte, extractDoc func() (string, error)) (*chatlog.Log, bool) {
	if !chatExtensions[strings.ToLower(filepath.Ext(path))] {
		return nil, false
	}
	if class == FileClassDocument {
		text, err := extractDoc()
		if err != nil {
			return nil, false
		}
		return chatlog.DetectText(path, Normalize(text))
	}
	return chatlog.Detect(path, content)
}

// MaySection reports whether a file may have sections: prose, or a format
// a chat log is looked for in.
func MaySection(path string) bool {
	return Sectioned(path) || chatExtensions[strings.ToLower(filepath.Ext(path))]
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
	} else if secs := byHeadings(path, text); len(secs) > 0 {
		out = secs
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
	kept := out[:0]
	for _, sec := range out {
		if garbled(sec.Text) {
			continue
		}
		if len(sec.Text) > maxSectionTextLen {
			sec.Text = cutRunes(sec.Text, maxSectionTextLen)
		}
		kept = append(kept, sec)
	}
	out = kept
	if len(out) > maxSections {
		out = out[:maxSections]
	}
	return out
}

// Heading lines of the light markup plain text is written in: "# Title"
// (also in .txt), AsciiDoc "== Title", Org "* Title", and reStructuredText's
// title over a line of =, - or ~.
var (
	hashHeading = regexp.MustCompile(`^#{1,4}\s+(\S.{0,100})$`)
	adocHeading = regexp.MustCompile(`^={1,4}\s+(\S.{0,100})$`)
	orgHeading  = regexp.MustCompile(`^\*{1,4}\s+(\S.{0,100})$`)
	rstRule     = regexp.MustCompile(`^(={3,}|-{3,}|~{3,})\s*$`)
)

// minHeadings is the fewest headings that make text sectioned by them.
const minHeadings = 2

// byHeadings splits text at its headings, or returns nil when it has fewer
// than minHeadings. Text before the first heading is kept as a section of its
// own when there is enough of it to matter.
func byHeadings(path, text string) []TextSection {
	ext := strings.ToLower(filepath.Ext(path))
	lines := strings.Split(text, "\n")
	type mark struct {
		line    int // first line of the section's body
		heading string
	}
	var marks []mark
	for i, ln := range lines {
		ln = strings.TrimRight(ln, " \t\r")
		var m []string
		switch ext {
		case ".org":
			m = orgHeading.FindStringSubmatch(ln)
		case ".adoc", ".asciidoc":
			m = adocHeading.FindStringSubmatch(ln)
		case ".rst":
			if i > 0 && rstRule.MatchString(ln) && strings.TrimSpace(lines[i-1]) != "" && !rstRule.MatchString(lines[i-1]) &&
				len(strings.TrimSpace(lines[i-1])) <= len(ln)+2 {
				m = []string{"", strings.TrimSpace(lines[i-1])}
				// The title line belongs to the heading, not the section
				// before it.
				lines[i-1] = ""
			}
		default:
			m = hashHeading.FindStringSubmatch(ln)
		}
		if m != nil {
			marks = append(marks, mark{line: i + 1, heading: strings.TrimSpace(m[1])})
		}
	}
	if len(marks) < minHeadings {
		return nil
	}
	var out []TextSection
	if pre := strings.TrimSpace(strings.Join(lines[:max(0, marks[0].line-1)], "\n")); len(pre) >= 200 {
		out = append(out, TextSection{Heading: "Opening", Text: pre})
	}
	for i, mk := range marks {
		end := len(lines)
		if i+1 < len(marks) {
			end = marks[i+1].line - 1
		}
		body := strings.TrimSpace(strings.Join(lines[min(mk.line, end):end], "\n"))
		if body == "" {
			continue
		}
		out = append(out, TextSection{Heading: mk.heading, Text: body})
	}
	return out
}

// garbled reports whether extracted text is not text: what a font with no
// usable encoding map yields, control characters and runs of accented
// capitals where words should be. A model shown it reads nonsense, and no
// quote can be checked against it, so it is not kept.
func garbled(text string) bool {
	runes, control, letters, latin1, cjk := 0, 0, 0, 0, 0
	for _, r := range text {
		runes++
		switch {
		case r < 0x20 && r != '\n' && r != '\t' && r != '\r':
			control++
		case unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul):
			cjk++
			letters++
		case unicode.IsLetter(r):
			letters++
			if r >= 0xC0 && r <= 0xFF {
				latin1++
			}
		}
	}
	if runes < 40 {
		return false
	}
	// Prose has no control characters; French or German has far fewer
	// accented letters than a quarter of all of them.
	if control*100 > runes || (letters > 0 && latin1*4 > letters) {
		return true
	}
	// Words are separated; a page of 25-character "words" is not words,
	// except in scripts written without spaces.
	if cjk*3 < letters {
		if fields := len(strings.Fields(text)); fields > 0 && runes/fields > 25 {
			return true
		}
	}
	return false
}

func firstLine(text string) string {
	line := strings.TrimSpace(text)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	line = strings.Join(strings.Fields(line), " ")
	if len(line) > 60 {
		line = cutRunes(line, 60) + "…"
	}
	return line
}

// cutRunes cuts s to at most n bytes on a character boundary.
func cutRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
