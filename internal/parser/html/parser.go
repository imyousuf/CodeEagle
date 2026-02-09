package html

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
	"golang.org/x/net/html"
)

// HTMLParser extracts knowledge graph nodes and edges from HTML and template files.
type HTMLParser struct{}

// NewParser creates a new HTML/template parser.
func NewParser() *HTMLParser {
	return &HTMLParser{}
}

func (p *HTMLParser) Language() parser.Language {
	return parser.LangHTML
}

func (p *HTMLParser) Extensions() []string {
	return parser.FileExtensions[parser.LangHTML]
}

func (p *HTMLParser) ParseFile(filePath string, content []byte) (*parser.ParseResult, error) {
	e := &extractor{
		filePath: filePath,
		content:  string(content),
	}
	e.extract()

	return &parser.ParseResult{
		Nodes:    e.nodes,
		Edges:    e.edges,
		FilePath: filePath,
		Language: parser.LangHTML,
	}, nil
}

type extractor struct {
	filePath   string
	content    string
	nodes      []*graph.Node
	edges      []*graph.Edge
	docNodeID  string
}

func (e *extractor) extract() {
	e.extractDocumentNode()
	e.extractHTMLElements()
	e.extractTemplateDirectives()
}

func (e *extractor) extractDocumentNode() {
	e.docNodeID = graph.NewNodeID(string(graph.NodeDocument), e.filePath, e.filePath)

	props := make(map[string]string)

	// Determine template type based on extension.
	ext := strings.ToLower(e.filePath)
	switch {
	case strings.HasSuffix(ext, ".jinja2") || strings.HasSuffix(ext, ".j2"):
		props["template_type"] = "jinja2"
	case strings.HasSuffix(ext, ".tmpl") || strings.HasSuffix(ext, ".gohtml"):
		props["template_type"] = "go"
	case strings.HasSuffix(ext, ".vue"):
		props["template_type"] = "vue"
	case strings.HasSuffix(ext, ".svelte"):
		props["template_type"] = "svelte"
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:         e.docNodeID,
		Type:       graph.NodeDocument,
		Name:       e.filePath,
		FilePath:   e.filePath,
		Language:   string(parser.LangHTML),
		Properties: props,
	})
}

func (e *extractor) extractHTMLElements() {
	doc, err := html.Parse(strings.NewReader(e.content))
	if err != nil {
		// Best-effort: if HTML parsing fails, we still have the document node
		// and template directives may still be extracted.
		return
	}
	e.walkHTML(doc)
}

func (e *extractor) walkHTML(n *html.Node) {
	if n.Type == html.ElementNode {
		switch n.Data {
		case "script":
			e.extractScript(n)
		case "link":
			e.extractLink(n)
		case "form":
			e.extractForm(n)
		case "meta":
			e.extractMeta(n)
		default:
			// Check for custom elements (contain a hyphen).
			if strings.Contains(n.Data, "-") {
				e.extractCustomElement(n)
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		e.walkHTML(c)
	}
}

func (e *extractor) extractScript(n *html.Node) {
	src := getAttr(n, "src")
	if src == "" {
		return
	}
	depID := graph.NewNodeID(string(graph.NodeDependency), e.filePath, src)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       depID,
		Type:     graph.NodeDependency,
		Name:     src,
		FilePath: e.filePath,
		Language: string(parser.LangHTML),
		Properties: map[string]string{
			"kind": "script",
		},
	})
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.docNodeID, depID, string(graph.EdgeDependsOn)),
		Type:     graph.EdgeDependsOn,
		SourceID: e.docNodeID,
		TargetID: depID,
	})
}

func (e *extractor) extractLink(n *html.Node) {
	href := getAttr(n, "href")
	if href == "" {
		return
	}
	rel := getAttr(n, "rel")
	kind := "link"
	if rel == "stylesheet" {
		kind = "stylesheet"
	} else if rel == "icon" {
		kind = "icon"
	}

	depID := graph.NewNodeID(string(graph.NodeDependency), e.filePath, href)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       depID,
		Type:     graph.NodeDependency,
		Name:     href,
		FilePath: e.filePath,
		Language: string(parser.LangHTML),
		Properties: map[string]string{
			"kind": kind,
			"rel":  rel,
		},
	})
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.docNodeID, depID, string(graph.EdgeDependsOn)),
		Type:     graph.EdgeDependsOn,
		SourceID: e.docNodeID,
		TargetID: depID,
	})
}

func (e *extractor) extractForm(n *html.Node) {
	action := getAttr(n, "action")
	if action == "" {
		return
	}
	method := strings.ToUpper(getAttr(n, "method"))
	if method == "" {
		method = "GET"
	}

	name := fmt.Sprintf("%s %s", method, action)
	epID := graph.NewNodeID(string(graph.NodeAPIEndpoint), e.filePath, name)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       epID,
		Type:     graph.NodeAPIEndpoint,
		Name:     name,
		FilePath: e.filePath,
		Language: string(parser.LangHTML),
		Properties: map[string]string{
			"method": method,
			"action": action,
		},
	})
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.docNodeID, epID, string(graph.EdgeConsumes)),
		Type:     graph.EdgeConsumes,
		SourceID: e.docNodeID,
		TargetID: epID,
	})
}

func (e *extractor) extractMeta(n *html.Node) {
	name := getAttr(n, "name")
	content := getAttr(n, "content")
	if name == "" || content == "" {
		return
	}
	// Store meta info as properties on the document node.
	for _, node := range e.nodes {
		if node.ID == e.docNodeID {
			if node.Properties == nil {
				node.Properties = make(map[string]string)
			}
			node.Properties["meta:"+name] = content
			break
		}
	}
}

func (e *extractor) extractCustomElement(n *html.Node) {
	depID := graph.NewNodeID(string(graph.NodeDependency), e.filePath, "component:"+n.Data)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       depID,
		Type:     graph.NodeDependency,
		Name:     n.Data,
		FilePath: e.filePath,
		Language: string(parser.LangHTML),
		Properties: map[string]string{
			"kind": "component",
		},
	})
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.docNodeID, depID, string(graph.EdgeDependsOn)),
		Type:     graph.EdgeDependsOn,
		SourceID: e.docNodeID,
		TargetID: depID,
	})
}

// Template directive patterns.
var (
	// Jinja2: {% extends "base.html" %}, {% include "partial.html" %}
	jinjaExtendsRe = regexp.MustCompile(`\{%[-\s]*extends\s+["']([^"']+)["']\s*[-]?%\}`)
	jinjaIncludeRe = regexp.MustCompile(`\{%[-\s]*include\s+["']([^"']+)["']\s*[-]?%\}`)
	jinjaImportRe  = regexp.MustCompile(`\{%[-\s]*(?:from\s+["']([^"']+)["']\s+)?import\s+["']?([^"'%]+)["']?\s*(?:as\s+\w+)?\s*[-]?%\}`)

	// Go templates: {{template "name" .}}, {{block "name" .}}
	goTemplateRe = regexp.MustCompile(`\{\{[-\s]*(?:template|block)\s+"([^"]+)"`)
)

func (e *extractor) extractTemplateDirectives() {
	ext := strings.ToLower(e.filePath)

	// Jinja2 templates.
	if strings.HasSuffix(ext, ".jinja2") || strings.HasSuffix(ext, ".j2") || strings.HasSuffix(ext, ".html") || strings.HasSuffix(ext, ".htm") {
		e.extractJinjaDirectives()
	}

	// Go templates.
	if strings.HasSuffix(ext, ".tmpl") || strings.HasSuffix(ext, ".gohtml") {
		e.extractGoTemplateDirectives()
	}

	// Vue SFC.
	if strings.HasSuffix(ext, ".vue") {
		e.extractVueSections()
	}

	// Svelte.
	if strings.HasSuffix(ext, ".svelte") {
		e.extractSvelteSections()
	}
}

func (e *extractor) extractJinjaDirectives() {
	// extends
	for _, match := range jinjaExtendsRe.FindAllStringSubmatch(e.content, -1) {
		ref := match[1]
		e.addTemplateDependency(ref, "extends")
	}

	// include
	for _, match := range jinjaIncludeRe.FindAllStringSubmatch(e.content, -1) {
		ref := match[1]
		e.addTemplateDependency(ref, "include")
	}

	// import
	for _, match := range jinjaImportRe.FindAllStringSubmatch(e.content, -1) {
		ref := match[1]
		if ref == "" {
			ref = match[2]
		}
		if ref != "" {
			e.addTemplateDependency(strings.TrimSpace(ref), "import")
		}
	}
}

func (e *extractor) extractGoTemplateDirectives() {
	for _, match := range goTemplateRe.FindAllStringSubmatch(e.content, -1) {
		ref := match[1]
		e.addTemplateDependency(ref, "template")
	}
}

func (e *extractor) extractVueSections() {
	// Detect <template>, <script>, <style> sections in Vue SFC.
	vueSectionRe := regexp.MustCompile(`<(template|script|style)([^>]*)>`)
	for _, match := range vueSectionRe.FindAllStringSubmatch(e.content, -1) {
		section := match[1]
		attrs := match[2]

		props := map[string]string{
			"kind": "vue-section",
			"section": section,
		}

		// Check for src attribute in sections.
		srcRe := regexp.MustCompile(`src=["']([^"']+)["']`)
		if srcMatch := srcRe.FindStringSubmatch(attrs); srcMatch != nil {
			props["src"] = srcMatch[1]
			e.addTemplateDependency(srcMatch[1], "vue-src")
		}

		// Check for lang attribute.
		langRe := regexp.MustCompile(`lang=["']([^"']+)["']`)
		if langMatch := langRe.FindStringSubmatch(attrs); langMatch != nil {
			props["lang"] = langMatch[1]
		}

		sectionID := graph.NewNodeID(string(graph.NodeDocument), e.filePath, "vue:"+section)
		e.nodes = append(e.nodes, &graph.Node{
			ID:         sectionID,
			Type:       graph.NodeDocument,
			Name:       section,
			FilePath:   e.filePath,
			Language:   string(parser.LangHTML),
			Properties: props,
		})
		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(e.docNodeID, sectionID, string(graph.EdgeContains)),
			Type:     graph.EdgeContains,
			SourceID: e.docNodeID,
			TargetID: sectionID,
		})
	}
}

func (e *extractor) extractSvelteSections() {
	// Svelte also uses <script> and <style> sections.
	svelteSectionRe := regexp.MustCompile(`<(script|style)([^>]*)>`)
	for _, match := range svelteSectionRe.FindAllStringSubmatch(e.content, -1) {
		section := match[1]
		attrs := match[2]

		props := map[string]string{
			"kind": "svelte-section",
			"section": section,
		}

		// Check for context="module" in script tags.
		if strings.Contains(attrs, `context="module"`) {
			props["context"] = "module"
		}

		sectionID := graph.NewNodeID(string(graph.NodeDocument), e.filePath, "svelte:"+section)
		e.nodes = append(e.nodes, &graph.Node{
			ID:         sectionID,
			Type:       graph.NodeDocument,
			Name:       section,
			FilePath:   e.filePath,
			Language:   string(parser.LangHTML),
			Properties: props,
		})
		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(e.docNodeID, sectionID, string(graph.EdgeContains)),
			Type:     graph.EdgeContains,
			SourceID: e.docNodeID,
			TargetID: sectionID,
		})
	}
}

func (e *extractor) addTemplateDependency(ref, kind string) {
	depID := graph.NewNodeID(string(graph.NodeDependency), e.filePath, "tmpl:"+ref)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       depID,
		Type:     graph.NodeDependency,
		Name:     ref,
		FilePath: e.filePath,
		Language: string(parser.LangHTML),
		Properties: map[string]string{
			"kind": kind,
		},
	})
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.docNodeID, depID, string(graph.EdgeDependsOn)),
		Type:     graph.EdgeDependsOn,
		SourceID: e.docNodeID,
		TargetID: depID,
	})
}

// Helper functions.

func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func edgeID(sourceID, targetID, edgeType string) string {
	return graph.NewNodeID(edgeType, sourceID, targetID)
}
