package markdown

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

// MarkdownParser extracts knowledge graph nodes and edges from Markdown files.
type MarkdownParser struct {
	sectionText bool
}

// Option configures a MarkdownParser.
type Option func(*MarkdownParser)

// WithSectionText controls whether each section carries its own text. It is
// on by default: without it a section is a heading and a line number, which
// nothing can embed, so markdown is invisible to search by meaning and
// cannot be quoted as evidence. Off restores heading-only sections.
func WithSectionText(on bool) Option {
	return func(p *MarkdownParser) { p.sectionText = on }
}

// NewParser creates a new Markdown parser.
func NewParser(opts ...Option) *MarkdownParser {
	p := &MarkdownParser{sectionText: true}
	for _, o := range opts {
		o(p)
	}
	return p
}

// maxSectionText caps the text stored on one section. A section is the
// unit an agent is shown and the unit that is embedded, so it should stay a
// readable size; a longer one is still found by its opening.
const maxSectionText = 6000

func (p *MarkdownParser) Language() parser.Language {
	return parser.LangMarkdown
}

func (p *MarkdownParser) Extensions() []string {
	return parser.FileExtensions[parser.LangMarkdown]
}

func (p *MarkdownParser) ParseFile(filePath string, content []byte) (*parser.ParseResult, error) {
	e := &extractor{
		filePath:    filePath,
		lines:       strings.Split(string(content), "\n"),
		sectionText: p.sectionText,
	}
	e.extract()

	return &parser.ParseResult{
		Nodes:    e.nodes,
		Edges:    e.edges,
		FilePath: filePath,
		Language: parser.LangMarkdown,
	}, nil
}

type extractor struct {
	filePath    string
	lines       []string
	nodes       []*graph.Node
	edges       []*graph.Edge
	docNodeID   string
	sectionText bool
	frontMatter map[string]string
}

// heading is a heading line found outside a code block.
type heading struct {
	title string
	level int
	idx   int // 0-based line index
}

// aiGuidelineFiles lists filenames that are treated as AI guideline documents.
var aiGuidelineFiles = map[string]bool{
	"CLAUDE.md":  true,
	"AGENTS.md":  true,
	"GEMINI.md":  true,
	"COPILOT.md": true,
	"AI.md":      true,
}

// isAIGuidelineFile checks if the file should be treated as an AI guideline.
func isAIGuidelineFile(filePath string) bool {
	base := filepath.Base(filePath)
	return aiGuidelineFiles[base]
}

// Regex patterns for Markdown elements.
var (
	headingRe        = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)
	linkRe           = regexp.MustCompile(`\[([^\]]*)\]\(([^)]+)\)`)
	codeBlockRe      = regexp.MustCompile("^```(\\w*)\\s*$")
	codeBlockEnd     = regexp.MustCompile("^```\\s*$")
	frontMatterDelim = "---"
	todoRe           = regexp.MustCompile(`(?i)\b(TODO|FIXME|HACK|XXX)\b[:\s]*(.*)`)
)

func (e *extractor) extract() {
	e.extractDocumentNode()
	e.extractFrontMatter()
	e.extractContent()
}

func (e *extractor) extractDocumentNode() {
	nodeType := graph.NodeDocument
	if isAIGuidelineFile(e.filePath) {
		nodeType = graph.NodeAIGuideline
	}
	e.docNodeID = graph.NewNodeID(string(nodeType), e.filePath, e.filePath)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       e.docNodeID,
		Type:     nodeType,
		Name:     e.filePath,
		FilePath: e.filePath,
		Language: string(parser.LangMarkdown),
	})
}

func (e *extractor) extractFrontMatter() {
	if len(e.lines) < 3 {
		return
	}
	if strings.TrimSpace(e.lines[0]) != frontMatterDelim {
		return
	}

	// Find closing delimiter.
	closingIdx := -1
	for i := 1; i < len(e.lines); i++ {
		if strings.TrimSpace(e.lines[i]) == frontMatterDelim {
			closingIdx = i
			break
		}
	}
	if closingIdx < 0 {
		return
	}

	// Parse simple YAML key-value pairs from front matter.
	props := make(map[string]string)
	for i := 1; i < closingIdx; i++ {
		line := strings.TrimSpace(e.lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Handle simple key: value pairs (not nested).
		if strings.HasPrefix(line, "- ") {
			// List item under a key - skip (already handled in simple parsing).
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			if key != "" && val != "" {
				props[key] = val
			}
		}
	}

	e.frontMatter = props

	// Store front matter as properties on the document node.
	for _, node := range e.nodes {
		if node.ID == e.docNodeID {
			if node.Properties == nil {
				node.Properties = make(map[string]string)
			}
			for k, v := range props {
				node.Properties["frontmatter:"+k] = v
			}
			break
		}
	}
}

func (e *extractor) extractContent() {
	// Skip front matter lines if present.
	startLine := 0
	if len(e.lines) > 0 && strings.TrimSpace(e.lines[0]) == frontMatterDelim {
		for i := 1; i < len(e.lines); i++ {
			if strings.TrimSpace(e.lines[i]) == frontMatterDelim {
				startLine = i + 1
				break
			}
		}
	}

	inCodeBlock := false
	codeBlockLang := ""
	codeBlockStart := 0
	var headings []heading

	for i := startLine; i < len(e.lines); i++ {
		line := e.lines[i]

		// Handle code blocks.
		if inCodeBlock {
			if codeBlockEnd.MatchString(line) {
				e.addCodeBlock(codeBlockLang, codeBlockStart+1, i+1)
				inCodeBlock = false
				codeBlockLang = ""
			}
			continue
		}

		if matches := codeBlockRe.FindStringSubmatch(line); matches != nil {
			inCodeBlock = true
			codeBlockLang = matches[1]
			codeBlockStart = i
			continue
		}

		// Headings. Sections are emitted once all are known, since a
		// section's extent runs to the next heading of its level or above.
		if matches := headingRe.FindStringSubmatch(line); matches != nil {
			headings = append(headings, heading{
				title: strings.TrimSpace(matches[2]),
				level: len(matches[1]),
				idx:   i,
			})
			continue
		}

		// Links.
		for _, match := range linkRe.FindAllStringSubmatch(line, -1) {
			linkText := match[1]
			linkURL := match[2]
			e.addLink(linkText, linkURL, i+1)
		}

		// TODO/FIXME items.
		if match := todoRe.FindStringSubmatch(line); match != nil {
			e.addTodoItem(match[1], strings.TrimSpace(match[2]), i+1)
		}
	}

	e.addSections(headings)
	if e.sectionText {
		e.addPreamble(startLine, headings)
	}
}

// addSections emits a node per heading.
//
// A section's extent (Line to EndLine) runs to the next heading of the same
// or a higher level, so it includes its subsections. Its text, hash and
// embedding cover only its own words, up to the next heading of any level:
// a parent that carried its children's text would embed the document twice
// over, and an edit to a subsection would read as a change to every heading
// above it.
func (e *extractor) addSections(headings []heading) {
	seen := make(map[string]int)
	var path []heading
	for i, h := range headings {
		for len(path) > 0 && path[len(path)-1].level >= h.level {
			path = path[:len(path)-1]
		}
		path = append(path, h)
		titles := make([]string, len(path))
		for j, p := range path {
			titles[j] = p.title
		}
		headingPath := strings.Join(titles, " > ")

		end := len(e.lines)
		for _, n := range headings[i+1:] {
			if n.level <= h.level {
				end = n.idx
				break
			}
		}
		ownEnd := len(e.lines)
		if i+1 < len(headings) {
			ownEnd = headings[i+1].idx
		}

		// Two sections with one title used to share an ID, so the second
		// silently replaced the first. The first keeps the ID it always had;
		// later ones are numbered.
		seen[h.title]++
		ordinal := seen[h.title]
		name := "section:" + h.title
		if ordinal > 1 {
			name += "#" + strconv.Itoa(ordinal)
		}

		node := &graph.Node{
			ID:            graph.NewNodeID(string(graph.NodeDocument), e.filePath, name),
			Type:          graph.NodeDocument,
			Name:          h.title,
			QualifiedName: headingPath,
			FilePath:      e.filePath,
			Line:          h.idx + 1,
			EndLine:       e.lastContentLine(h.idx, end),
			Language:      string(parser.LangMarkdown),
			Properties: map[string]string{
				"kind":         "section",
				"level":        strings.Repeat("#", h.level),
				"heading_path": headingPath,
				"ordinal":      strconv.Itoa(ordinal),
			},
		}
		if e.sectionText {
			if text := e.textBetween(h.idx+1, ownEnd); text != "" {
				stored, truncated := capText(text)
				node.DocComment = stored
				node.Properties["section_hash"] = TextHash(text)
				if truncated {
					node.Properties["text_truncated"] = "true"
				}
			}
		}
		e.addSectionNode(node)
	}
}

// addPreamble gives the document node its title, description and the text
// before the first heading, so a document can be found by what it says it
// is about even where it has no headings at all.
func (e *extractor) addPreamble(startLine int, headings []heading) {
	end := len(e.lines)
	if len(headings) > 0 {
		end = headings[0].idx
	}
	var parts []string
	for _, key := range []string{"title", "description"} {
		if v := strings.Trim(e.frontMatter[key], `"'`); v != "" {
			parts = append(parts, v)
		}
	}
	if text := e.textBetween(startLine, end); text != "" {
		parts = append(parts, text)
	}
	if len(parts) == 0 {
		return
	}
	text, truncated := capText(strings.Join(parts, "\n\n"))
	for _, n := range e.nodes {
		if n.ID == e.docNodeID {
			n.DocComment = text
			if truncated {
				if n.Properties == nil {
					n.Properties = make(map[string]string)
				}
				n.Properties["text_truncated"] = "true"
			}
			return
		}
	}
}

// textBetween returns lines [from, to) trimmed of surrounding blank lines.
func (e *extractor) textBetween(from, to int) string {
	if from < 0 {
		from = 0
	}
	if to > len(e.lines) {
		to = len(e.lines)
	}
	if from >= to {
		return ""
	}
	return strings.TrimSpace(strings.Join(e.lines[from:to], "\n"))
}

// lastContentLine returns the 1-based number of the last non-blank line in
// [from, to), so a section does not claim the blank lines before the next.
func (e *extractor) lastContentLine(from, to int) int {
	if to > len(e.lines) {
		to = len(e.lines)
	}
	for i := to - 1; i > from; i-- {
		if strings.TrimSpace(e.lines[i]) != "" {
			return i + 1
		}
	}
	return from + 1
}

// capText bounds a section's stored text, reporting whether it cut. The cut
// falls on a rune boundary so the stored text stays valid UTF-8.
func capText(text string) (string, bool) {
	if len(text) <= maxSectionText {
		return text, false
	}
	cut := maxSectionText
	for cut > 0 && !utf8RuneStart(text[cut]) {
		cut--
	}
	return text[:cut], true
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

// TextHash identifies a passage by its words, not its layout: reflowing a
// paragraph or re-indenting it is not a change, rewording it is.
func TextHash(text string) string {
	sum := sha256.Sum256([]byte(strings.Join(strings.Fields(text), " ")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (e *extractor) addSectionNode(node *graph.Node) {
	sectionID := node.ID
	e.nodes = append(e.nodes, node)
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.docNodeID, sectionID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.docNodeID,
		TargetID: sectionID,
	})
}

func (e *extractor) addLink(text, url string, line int) {
	// Only create Documents edges for relative paths (likely source file references).
	if isRelativePath(url) {
		depID := graph.NewNodeID(string(graph.NodeDependency), e.filePath, "ref:"+url)
		e.nodes = append(e.nodes, &graph.Node{
			ID:       depID,
			Type:     graph.NodeDependency,
			Name:     url,
			FilePath: e.filePath,
			Line:     line,
			Language: string(parser.LangMarkdown),
			Properties: map[string]string{
				"kind":      "cross-reference",
				"link_text": text,
			},
		})
		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(e.docNodeID, depID, string(graph.EdgeDocuments)),
			Type:     graph.EdgeDocuments,
			SourceID: e.docNodeID,
			TargetID: depID,
		})
	}
}

func (e *extractor) addCodeBlock(lang string, startLine, endLine int) {
	if lang == "" {
		return
	}
	blockID := graph.NewNodeID(string(graph.NodeDocument), e.filePath, "codeblock:"+lang+":"+strings.Repeat(".", startLine))
	e.nodes = append(e.nodes, &graph.Node{
		ID:       blockID,
		Type:     graph.NodeDocument,
		Name:     "code:" + lang,
		FilePath: e.filePath,
		Line:     startLine,
		EndLine:  endLine,
		Language: string(parser.LangMarkdown),
		Properties: map[string]string{
			"kind":          "code-block",
			"code_language": lang,
		},
	})
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.docNodeID, blockID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.docNodeID,
		TargetID: blockID,
	})
}

func (e *extractor) addTodoItem(kind, text string, line int) {
	todoID := graph.NewNodeID(string(graph.NodeDocument), e.filePath, "todo:"+kind+":"+text)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       todoID,
		Type:     graph.NodeDocument,
		Name:     kind + ": " + text,
		FilePath: e.filePath,
		Line:     line,
		Language: string(parser.LangMarkdown),
		Properties: map[string]string{
			"kind":      "todo",
			"todo_type": strings.ToUpper(kind),
		},
	})
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.docNodeID, todoID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.docNodeID,
		TargetID: todoID,
	})
}

// isRelativePath checks if a URL is a relative file path (not http/https/mailto/etc.).
func isRelativePath(url string) bool {
	if url == "" {
		return false
	}
	// Skip URLs with schemes.
	if strings.Contains(url, "://") || strings.HasPrefix(url, "mailto:") {
		return false
	}
	// Skip anchors.
	if strings.HasPrefix(url, "#") {
		return false
	}
	return true
}

func edgeID(sourceID, targetID, edgeType string) string {
	return graph.NewNodeID(edgeType, sourceID, targetID)
}
