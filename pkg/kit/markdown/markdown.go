// Package markdown turns a Markdown file into graph nodes and edges: the
// document, a section per heading carrying its own text, and optionally a
// node per list item.
//
// IDs are stable across edits that keep a node's identity. A section is
// named by its title, with "#2", "#3"… for later sections of the same title;
// a list item by its heading path and the hash of its words. Editing a
// section's text keeps its ID and changes its section_hash; rewording a list
// item gives it a new ID.
package markdown

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
)

// Language is the language recorded on every node.
const Language = "markdown"

// MaxSectionText caps the text stored on one section or list item. A section
// is the unit an agent is shown and the unit that is embedded, so it should
// stay a readable size; a longer one is still found by its opening.
const MaxSectionText = 6000

// Result is what Parse found in one file.
type Result struct {
	Nodes []*graph.Node
	Edges []*graph.Edge
}

// ListItems configures list-item nodes; see WithListItems.
type ListItems struct {
	// NodeType is the type of each list-item node; graph.NodeDocument when
	// empty.
	NodeType graph.NodeType
	// Kind is the node's "kind" property; "list_item" when empty.
	Kind string
}

type options struct {
	sectionText    bool
	listItems      *ListItems
	links          bool
	todos          bool
	codeBlocks     bool
	guidelineTyped bool
}

// Option configures Parse.
type Option func(*options)

// WithSectionText controls whether each section carries its own text. It is
// on by default: without it a section is a heading and a line number, which
// nothing can embed, so markdown is invisible to search by meaning and
// cannot be quoted as evidence. Off restores heading-only sections.
func WithSectionText(on bool) Option {
	return func(o *options) { o.sectionText = on }
}

// WithListItems adds a node per list item ("-", "*", "+", "1." or "1)"),
// contained by its section, or by the document before the first heading.
//
// An item's text is its own line and continuation lines; a nested item is a
// node of its own, with a depth one more than its parent's, and is not part
// of the parent's text. The ID is NewNodeID(NodeType, path, heading path +
// "\x00" + TextHash(text)), with "#2", "#3"… added when the same words recur
// under the same heading path in one file. The marker, the number and a task
// box ("[ ]", "[x]") are not part of the text, so renumbering a list or
// ticking a task keeps the ID; the box is recorded as "checked". Deleting an
// earlier duplicate renumbers the later ones.
func WithListItems(li ListItems) Option {
	return func(o *options) { o.listItems = &li }
}

// WithoutLinks leaves out the node CodeEagle makes for each relative link.
func WithoutLinks() Option { return func(o *options) { o.links = false } }

// WithoutTODOs leaves out the node made for each TODO, FIXME, HACK or XXX.
func WithoutTODOs() Option { return func(o *options) { o.todos = false } }

// WithoutCodeBlocks leaves out the node made for each fenced code block.
func WithoutCodeBlocks() Option { return func(o *options) { o.codeBlocks = false } }

// WithoutAIGuidelineType records CLAUDE.md, AGENTS.md and the like as plain
// documents rather than graph.NodeAIGuideline.
func WithoutAIGuidelineType() Option { return func(o *options) { o.guidelineTyped = false } }

// Parse extracts nodes and edges from one Markdown file.
func Parse(filePath string, content []byte, opts ...Option) *Result {
	o := options{sectionText: true, links: true, todos: true, codeBlocks: true, guidelineTyped: true}
	for _, opt := range opts {
		opt(&o)
	}
	e := &extractor{
		filePath: filePath,
		lines:    strings.Split(string(content), "\n"),
		opts:     o,
	}
	e.extract()
	return &Result{Nodes: e.nodes, Edges: e.edges}
}

type extractor struct {
	filePath    string
	lines       []string
	nodes       []*graph.Node
	edges       []*graph.Edge
	docNodeID   string
	opts        options
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
	// listItemRe matches a list item: indentation, a bullet or a number with
	// "." or ")", at least one space, then the item's text.
	listItemRe = regexp.MustCompile(`^(\s*)([-*+]|\d{1,9}[.)])(\s+)(.*)$`)
	// thematicBreakRe matches "---", "- - -", "* * *" and the like, which
	// would otherwise read as list items.
	thematicBreakRe = regexp.MustCompile(`^\s*([-*_])(\s*[-*_]){2,}\s*$`)
	taskBoxRe       = regexp.MustCompile(`^\[([ xX])\]\s+`)
)

func (e *extractor) extract() {
	e.extractDocumentNode()
	e.extractFrontMatter()
	e.extractContent()
}

func (e *extractor) extractDocumentNode() {
	nodeType := graph.NodeDocument
	if e.opts.guidelineTyped && isAIGuidelineFile(e.filePath) {
		nodeType = graph.NodeAIGuideline
	}
	e.docNodeID = graph.NewNodeID(string(nodeType), e.filePath, e.filePath)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       e.docNodeID,
		Type:     nodeType,
		Name:     e.filePath,
		FilePath: e.filePath,
		Language: Language,
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
	var lists listScanner

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
			lists.close()
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
			lists.close()
			continue
		}

		if e.opts.listItems != nil {
			lists.line(i, line, len(headings)-1)
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

	sectionIDs, headingPaths := e.addSections(headings)
	if e.opts.sectionText {
		e.addPreamble(startLine, headings)
	}
	if e.opts.listItems != nil {
		e.addListItems(lists.items, sectionIDs, headingPaths)
	}
}

// addSections emits a node per heading, and returns each heading's section
// ID and heading path in order.
//
// A section's extent (Line to EndLine) runs to the next heading of the same
// or a higher level, so it includes its subsections. Its text, hash and
// embedding cover only its own words, up to the next heading of any level:
// a parent that carried its children's text would embed the document twice
// over, and an edit to a subsection would read as a change to every heading
// above it.
func (e *extractor) addSections(headings []heading) (ids, paths []string) {
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
			Language:      Language,
			Properties: map[string]string{
				"kind":         "section",
				"level":        strings.Repeat("#", h.level),
				"heading_path": headingPath,
				"ordinal":      strconv.Itoa(ordinal),
			},
		}
		if e.opts.sectionText {
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
		ids = append(ids, node.ID)
		paths = append(paths, headingPath)
	}
	return ids, paths
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
	if len(text) <= MaxSectionText {
		return text, false
	}
	cut := MaxSectionText
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
	if !e.opts.links {
		return
	}
	// Only create Documents edges for relative paths (likely source file references).
	if isRelativePath(url) {
		depID := graph.NewNodeID(string(graph.NodeDependency), e.filePath, "ref:"+url)
		e.nodes = append(e.nodes, &graph.Node{
			ID:       depID,
			Type:     graph.NodeDependency,
			Name:     url,
			FilePath: e.filePath,
			Line:     line,
			Language: Language,
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
	if lang == "" || !e.opts.codeBlocks {
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
		Language: Language,
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
	if !e.opts.todos {
		return
	}
	todoID := graph.NewNodeID(string(graph.NodeDocument), e.filePath, "todo:"+kind+":"+text)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       todoID,
		Type:     graph.NodeDocument,
		Name:     kind + ": " + text,
		FilePath: e.filePath,
		Line:     line,
		Language: Language,
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

// listItem is one list item found while scanning.
type listItem struct {
	start, end int // 0-based first and last line
	depth      int
	heading    int // index of the heading above it, -1 before the first
	contentCol int // column the item's text starts at
	checked    string
	lines      []string
}

// listScanner groups lines into list items as the file is read.
type listScanner struct {
	items []*listItem
	// open holds the items a following line may still belong to, outermost
	// first.
	open      []*listItem
	lastBlank bool
}

func (s *listScanner) close() { s.open, s.lastBlank = nil, false }

func (s *listScanner) line(idx int, line string, heading int) {
	if strings.TrimSpace(line) == "" {
		s.lastBlank = true
		return
	}
	indent := leadingColumns(line)
	if !thematicBreakRe.MatchString(line) {
		if m := listItemRe.FindStringSubmatch(line); m != nil {
			// A nested item starts at or beyond its parent's text column;
			// anything shallower closes the items it is not inside.
			for len(s.open) > 0 && s.open[len(s.open)-1].contentCol > indent {
				s.open = s.open[:len(s.open)-1]
			}
			text := m[4]
			item := &listItem{
				start: idx, end: idx, depth: len(s.open), heading: heading,
				contentCol: indent + utf8.RuneCountInString(m[2]) + len(m[3]),
			}
			if box := taskBoxRe.FindStringSubmatch(text); box != nil {
				item.checked = strconv.FormatBool(box[1] != " ")
				text = text[len(box[0]):]
			}
			item.lines = []string{strings.TrimSpace(text)}
			s.items = append(s.items, item)
			s.open = append(s.open, item)
			s.lastBlank = false
			return
		}
	}
	// A continuation line belongs to the innermost open item whose text it is
	// indented under; directly after an item's last line, any text continues
	// it (a lazy continuation). After a blank line, unindented text ends the
	// list.
	for n := len(s.open) - 1; n >= 0; n-- {
		item := s.open[n]
		if indent >= item.contentCol || (!s.lastBlank && n == len(s.open)-1) {
			s.open = s.open[:n+1]
			item.lines = append(item.lines, strings.TrimSpace(line))
			item.end = idx
			s.lastBlank = false
			return
		}
	}
	s.close()
}

// leadingColumns counts a line's indentation, a tab as four columns.
func leadingColumns(line string) int {
	cols := 0
	for _, r := range line {
		switch r {
		case ' ':
			cols++
		case '\t':
			cols += 4 - cols%4
		default:
			return cols
		}
	}
	return cols
}

// addListItems emits a node per list item, contained by its section.
func (e *extractor) addListItems(items []*listItem, sectionIDs, headingPaths []string) {
	li := e.opts.listItems
	nodeType := li.NodeType
	if nodeType == "" {
		nodeType = graph.NodeDocument
	}
	kind := li.Kind
	if kind == "" {
		kind = "list_item"
	}
	seen := make(map[string]int)
	position := make(map[int]int)
	for _, item := range items {
		text := strings.Join(item.lines, "\n")
		if strings.TrimSpace(text) == "" {
			continue
		}
		parent, headingPath := e.docNodeID, ""
		if item.heading >= 0 {
			parent, headingPath = sectionIDs[item.heading], headingPaths[item.heading]
		}
		hash := TextHash(text)
		name := headingPath + "\x00" + hash
		seen[name]++
		if n := seen[name]; n > 1 {
			name += "#" + strconv.Itoa(n)
		}
		position[item.heading]++
		stored, truncated := capText(text)
		props := map[string]string{
			"kind":         kind,
			"heading_path": headingPath,
			"position":     strconv.Itoa(position[item.heading]),
			"depth":        strconv.Itoa(item.depth),
			"text_hash":    hash,
		}
		if item.checked != "" {
			props["checked"] = item.checked
		}
		if truncated {
			props["text_truncated"] = "true"
		}
		node := &graph.Node{
			ID:            graph.NewNodeID(string(nodeType), e.filePath, name),
			Type:          nodeType,
			Name:          itemName(text),
			QualifiedName: headingPath,
			FilePath:      e.filePath,
			Line:          item.start + 1,
			EndLine:       item.end + 1,
			Language:      Language,
			DocComment:    stored,
			Properties:    props,
		}
		e.nodes = append(e.nodes, node)
		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(parent, node.ID, string(graph.EdgeContains)),
			Type:     graph.EdgeContains,
			SourceID: parent,
			TargetID: node.ID,
		})
	}
}

// itemName is an item's opening words, for a node name.
func itemName(text string) string {
	name := strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(name) <= 120 {
		return name
	}
	r := []rune(name)
	return string(r[:120])
}
