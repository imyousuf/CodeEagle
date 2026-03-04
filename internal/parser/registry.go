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
	fallback      Parser   // fallback parser for files with no registered language parser
	excludeExts   []string // extensions to exclude from fallback processing
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
// It first tries extension-based lookup, then filename-based lookup,
// then falls back to the generic fallback parser (if set).
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

	if r.fallback != nil {
		return r.fallback, true
	}

	return nil, false
}

// SetFallback sets a fallback parser used when no language parser matches.
func (r *Registry) SetFallback(p Parser) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fallback = p
}

// Fallback returns the fallback parser, or nil if not set.
func (r *Registry) Fallback() Parser {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.fallback
}

// SetExcludeExtensions sets extensions to exclude from fallback processing.
func (r *Registry) SetExcludeExtensions(exts []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.excludeExts = exts
}

// ExcludeExtensions returns the list of extensions excluded from fallback processing.
func (r *Registry) ExcludeExtensions() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.excludeExts
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
