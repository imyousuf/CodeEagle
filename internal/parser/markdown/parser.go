package markdown

import (
	"regexp"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

// MarkdownParser extracts knowledge graph nodes and edges from Markdown files.
type MarkdownParser struct{}

// NewParser creates a new Markdown parser.
func NewParser() *MarkdownParser {
	return &MarkdownParser{}
}

func (p *MarkdownParser) Language() parser.Language {
	return parser.LangMarkdown
}

func (p *MarkdownParser) Extensions() []string {
	return parser.FileExtensions[parser.LangMarkdown]
}

func (p *MarkdownParser) ParseFile(filePath string, content []byte) (*parser.ParseResult, error) {
	e := &extractor{
		filePath: filePath,
		lines:    strings.Split(string(content), "\n"),
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
	filePath  string
	lines     []string
	nodes     []*graph.Node
	edges     []*graph.Edge
	docNodeID string
}

// Regex patterns for Markdown elements.
var (
	headingRe    = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)
	linkRe       = regexp.MustCompile(`\[([^\]]*)\]\(([^)]+)\)`)
	codeBlockRe  = regexp.MustCompile("^```(\\w*)\\s*$")
	codeBlockEnd = regexp.MustCompile("^```\\s*$")
	frontMatterDelim = "---"
	todoRe       = regexp.MustCompile(`(?i)\b(TODO|FIXME|HACK|XXX)\b[:\s]*(.*)`)
)

func (e *extractor) extract() {
	e.extractDocumentNode()
	e.extractFrontMatter()
	e.extractContent()
}

func (e *extractor) extractDocumentNode() {
	e.docNodeID = graph.NewNodeID(string(graph.NodeDocument), e.filePath, e.filePath)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       e.docNodeID,
		Type:     graph.NodeDocument,
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

		// Headings.
		if matches := headingRe.FindStringSubmatch(line); matches != nil {
			level := len(matches[1])
			title := strings.TrimSpace(matches[2])
			e.addSection(title, level, i+1)
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
}

func (e *extractor) addSection(title string, level, line int) {
	sectionID := graph.NewNodeID(string(graph.NodeDocument), e.filePath, "section:"+title)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       sectionID,
		Type:     graph.NodeDocument,
		Name:     title,
		FilePath: e.filePath,
		Line:     line,
		Language: string(parser.LangMarkdown),
		Properties: map[string]string{
			"kind":  "section",
			"level": strings.Repeat("#", level),
		},
	})
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
			"kind":           "code-block",
			"code_language":  lang,
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
