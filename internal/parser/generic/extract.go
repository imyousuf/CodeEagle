// Package generic is CodeEagle's fallback parser for files no language
// parser claims: text, images and documents. pkg/kit/extract reads their
// text and sections; this package adds what needs a model (summaries, topics
// and image descriptions, cached by content), the directory hierarchy, and
// the graph nodes for a document and its sections.
package generic

import (
	"context"

	"github.com/imyousuf/CodeEagle/pkg/kit/extract"
)

// FileClass represents how a file should be processed.
type FileClass = extract.FileClass

// File classes; see extract.FileClass.
const (
	FileClassText     = extract.FileClassText
	FileClassImage    = extract.FileClassImage
	FileClassDocument = extract.FileClassDocument
	FileClassSkip     = extract.FileClassSkip
)

// LangDocument is the language of non-code documents whose text is kept in
// sections (PDFs, slide decks, word-processor files, plain text), so they
// can be read, quoted and related like markdown.
const LangDocument = extract.LangDocument

// PropChatFormat marks a document that is a chat log, naming the layout it
// was read from.
const PropChatFormat = extract.PropChatFormat

// TextSection is one part of a document's extracted text.
type TextSection = extract.TextSection

// pdfReader reads PDFs instead of the kit's pure Go reader when set, which
// it is in a build with -tags poppler (document_pdf_poppler.go). Every PDF
// CodeEagle reads goes through ExtractDocumentWithCtx, so this is the one
// place the choice is made.
var pdfReader extract.PDFReader

// Classify determines how to process a file based on its extension.
func Classify(filePath string, excludeExts []string) FileClass {
	return extract.Classify(filePath, excludeExts)
}

// ClassifyContent uses file content to distinguish text from binary.
func ClassifyContent(content []byte) FileClass { return extract.ClassifyContent(content) }

// ExtractText processes raw file content based on the file extension.
func ExtractText(filePath string, content []byte) string { return extract.Text(filePath, content) }

// ExtractDocument extracts plain text from a document file based on its extension.
// Supports OOXML (DOCX, PPTX, XLSX), ODF (ODT, ODS, ODP), and PDF formats.
func ExtractDocument(filePath string, content []byte) (string, error) {
	return ExtractDocumentWithCtx(context.TODO(), filePath, content)
}

// ExtractDocumentWithCtx is like ExtractDocument but accepts a context for cancellation.
func ExtractDocumentWithCtx(ctx context.Context, filePath string, content []byte) (string, error) {
	return extract.Document(ctx, filePath, content, extract.WithPDFReader(pdfReader))
}

// MaySection reports whether a file may have sections: prose, or a format
// a chat log is looked for in.
func MaySection(path string) bool { return extract.MaySection(path) }

// Sectioned reports whether a file's text is kept in sections.
func Sectioned(path string) bool { return extract.Sectioned(path) }

// Normalize undoes what PDF extraction does to words; see extract.Normalize.
func Normalize(text string) string { return extract.Normalize(text) }

// Sectionize splits a document's extracted text into sections; see
// extract.Sectionize.
func Sectionize(path, text string) []TextSection { return extract.Sectionize(path, text) }
