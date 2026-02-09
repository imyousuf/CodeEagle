package parser

import "github.com/imyousuf/CodeEagle/internal/graph"

// Language represents a supported programming language.
type Language string

const (
	LangGo         Language = "go"
	LangPython     Language = "python"
	LangTypeScript Language = "typescript"
	LangJavaScript Language = "javascript"
	LangJava       Language = "java"
	LangHTML       Language = "html"
	LangMarkdown   Language = "markdown"
)

// FileExtensions maps each language to its recognized file extensions.
var FileExtensions = map[Language][]string{
	LangGo:         {".go"},
	LangPython:     {".py", ".pyi"},
	LangTypeScript: {".ts", ".tsx"},
	LangJavaScript: {".js", ".jsx", ".mjs", ".cjs"},
	LangJava:       {".java"},
	LangHTML:       {".html", ".htm", ".jinja2", ".j2", ".tmpl", ".gohtml", ".vue", ".svelte"},
	LangMarkdown:   {".md", ".mdx"},
}

// ParseResult holds the extracted nodes and edges from parsing a file.
type ParseResult struct {
	Nodes    []*graph.Node
	Edges    []*graph.Edge
	FilePath string
	Language Language
}

// Parser defines the interface for language-specific source code parsers.
type Parser interface {
	// Language returns which language this parser handles.
	Language() Language

	// Extensions returns the file extensions this parser can handle.
	Extensions() []string

	// ParseFile parses the given file content and returns extracted nodes and edges.
	ParseFile(filePath string, content []byte) (*ParseResult, error)
}
