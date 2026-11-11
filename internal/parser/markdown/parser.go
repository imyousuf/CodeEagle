// Package markdown is CodeEagle's Markdown parser: pkg/kit/markdown behind the
// parser.Parser interface the indexer's registry expects.
package markdown

import (
	"github.com/imyousuf/CodeEagle/internal/parser"
	kitmarkdown "github.com/imyousuf/CodeEagle/pkg/kit/markdown"
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

// StoresSectionText reports whether sections carry their own text, so a
// caller can tell whether nodes indexed before it did need bringing up to
// date.
func (p *MarkdownParser) StoresSectionText() bool { return p.sectionText }

// NewParser creates a new Markdown parser.
func NewParser(opts ...Option) *MarkdownParser {
	p := &MarkdownParser{sectionText: true}
	for _, o := range opts {
		o(p)
	}
	return p
}

// maxSectionText caps the text stored on one section.
const maxSectionText = kitmarkdown.MaxSectionText

// TextHash identifies a passage by its words, not its layout: reflowing a
// paragraph or re-indenting it is not a change, rewording it is.
func TextHash(text string) string { return kitmarkdown.TextHash(text) }

func (p *MarkdownParser) Language() parser.Language {
	return parser.LangMarkdown
}

func (p *MarkdownParser) Extensions() []string {
	return parser.FileExtensions[parser.LangMarkdown]
}

func (p *MarkdownParser) ParseFile(filePath string, content []byte) (*parser.ParseResult, error) {
	res := kitmarkdown.Parse(filePath, content, kitmarkdown.WithSectionText(p.sectionText))
	return &parser.ParseResult{
		Nodes:    res.Nodes,
		Edges:    res.Edges,
		FilePath: filePath,
		Language: parser.LangMarkdown,
	}, nil
}
