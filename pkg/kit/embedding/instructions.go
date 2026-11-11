package embedding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

// Fingerprinter is implemented by a provider whose vectors depend on more
// than its name, model and dimensions. A vector index records the
// fingerprint and treats a change as a reason to embed everything again,
// since vectors made under different instructions are not comparable.
type Fingerprinter interface {
	EmbedConfig() string
}

// WithInstructions wraps p so every document text is prefixed with document
// and every query with query, verbatim. Models trained with task prompts
// want them: gemini-embedding-2 and EmbeddingGemma take
// "title: none | text: " for documents and "task: search result | query: "
// for queries.
//
// Empty prefixes send texts as given, so the wrapper changes nothing a
// request carries. A provider that adds prefixes of its own (Ollama's
// nomic-embed defaults) should have them turned off first.
func WithInstructions(p Provider, document, query string) Provider {
	return &instructed{Provider: p, doc: document, query: query}
}

type instructed struct {
	Provider
	doc, query string
}

func (i *instructed) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if i.doc == "" || len(texts) == 0 {
		return i.Provider.Embed(ctx, texts)
	}
	prefixed := make([]string, len(texts))
	for n, t := range texts {
		prefixed[n] = i.doc + t
	}
	return i.Provider.Embed(ctx, prefixed)
}

func (i *instructed) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	return i.Provider.EmbedQuery(ctx, i.query+text)
}

// EmbedConfig identifies the prefixes, together with anything the wrapped
// provider fingerprints, so an index notices when either changes. No
// prefixes and nothing underneath is an empty fingerprint.
func (i *instructed) EmbedConfig() string {
	var inner string
	if f, ok := i.Provider.(Fingerprinter); ok {
		inner = f.EmbedConfig()
	}
	if i.doc == "" && i.query == "" && inner == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("doc:" + i.doc + "\x00query:" + i.query + "\x00inner:" + inner))
	return "instructions:" + hex.EncodeToString(sum[:8])
}
