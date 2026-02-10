package parser

import (
	"path/filepath"
	"sync"
)

// Registry manages a collection of language parsers.
type Registry struct {
	mu            sync.RWMutex
	parsers       map[Language]Parser
	extIndex      map[string]Parser
	filenameIndex map[string]Parser
	order         []Language
}

// NewRegistry creates a new parser registry.
func NewRegistry() *Registry {
	return &Registry{
		parsers:       make(map[Language]Parser),
		extIndex:      make(map[string]Parser),
		filenameIndex: make(map[string]Parser),
		order:         make([]Language, 0),
	}
}

// Register adds a parser to the registry, indexing it by language and file extensions.
// If the parser implements FilenameParser, it is also indexed by exact filenames.
func (r *Registry) Register(p Parser) {
	r.mu.Lock()
	defer r.mu.Unlock()

	lang := p.Language()
	if _, exists := r.parsers[lang]; !exists {
		r.order = append(r.order, lang)
	}
	r.parsers[lang] = p
	for _, ext := range p.Extensions() {
		r.extIndex[ext] = p
	}

	if fp, ok := p.(FilenameParser); ok {
		for _, name := range fp.Filenames() {
			r.filenameIndex[name] = p
		}
	}
}

// Get retrieves a parser by language.
func (r *Registry) Get(lang Language) (Parser, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.parsers[lang]
	return p, ok
}

// GetByExtension retrieves a parser by file extension (e.g. ".go", ".py").
func (r *Registry) GetByExtension(ext string) (Parser, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.extIndex[ext]
	return p, ok
}

// ParserForFile resolves the appropriate parser for a given file path.
// It first tries extension-based lookup, then falls back to filename-based lookup.
func (r *Registry) ParserForFile(filePath string) (Parser, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ext := filepath.Ext(filePath)
	if p, ok := r.extIndex[ext]; ok {
		return p, true
	}

	base := filepath.Base(filePath)
	if p, ok := r.filenameIndex[base]; ok {
		return p, true
	}

	return nil, false
}

// All returns all registered parsers in registration order.
func (r *Registry) All() []Parser {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Parser, len(r.order))
	for i, lang := range r.order {
		result[i] = r.parsers[lang]
	}
	return result
}

// SupportedExtensions returns all file extensions that have a registered parser.
func (r *Registry) SupportedExtensions() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	exts := make([]string, 0, len(r.extIndex))
	for ext := range r.extIndex {
		exts = append(exts, ext)
	}
	return exts
}

// SupportedFilenames returns all filenames that have a registered parser.
func (r *Registry) SupportedFilenames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.filenameIndex))
	for name := range r.filenameIndex {
		names = append(names, name)
	}
	return names
}
