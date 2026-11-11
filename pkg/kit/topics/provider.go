// Package topics names what things are about, and relates the names to each
// other.
//
// It has three layers, each usable without the next:
//
//   - Extraction. A Provider reads a document or an image and returns a short
//     summary and a few topic labels. A Cache keeps the results by content
//     hash, so an unchanged file is never sent twice.
//   - The registry. Labels resolve to durable Topic nodes, so the rewordings
//     of one subject collapse into one node rather than many, and any node --
//     a document, a section, a meeting, a fact -- can be filed under a topic
//     with a HasTopic edge.
//   - Relating. Cheap signals propose pairs of topics; a decide.Judge answers
//     two yes/no questions about each; every verdict is stored as a weighted
//     RelatedTo edge, so no pair is asked about twice. Search follows those
//     edges as far as a Breadth allows.
//
// Providers live in subpackages (baseten) and do not register themselves: a
// program constructs the one it wants, so importing a provider never changes
// global state behind the caller's back.
package topics

import (
	"context"
	"errors"
	"strings"
)

const (
	// DefaultContextWindow is the context window, in tokens, a provider
	// budgets for when none is configured. 120K covers documents up to
	// ~336KB (80% of the window at 3.5 characters a token), large PDFs
	// included.
	DefaultContextWindow = 120000

	// MaxExtractRetries bounds the attempts at one extraction. A reply
	// that fails or says nothing is worth another attempt; four is where
	// further ones stopped paying for themselves.
	MaxExtractRetries = 4
)

// ErrExtractionSkipped is returned when every attempt at an extraction
// produced nothing usable. The file should still be indexed, with its raw
// text as the fallback.
var ErrExtractionSkipped = errors.New("extraction skipped after max retries")

// TopicExtractionPrompt is the system prompt for extracting topics from
// text content.
const TopicExtractionPrompt = `Extract topics, entities, and key concepts from the following text. If the text is very long, first summarize it then extract. Output JSON with: {"summary": "2-3 sentence summary", "topics": ["topic1", "topic2", ...], "entities": ["FunctionName", "ClassName", "package_name", ...]}. Be concise. Topics should be abstract themes, not specific names.`

// ImageDescriptionPrompt is the system prompt for describing image content.
const ImageDescriptionPrompt = `Describe this image in detail. If it is a diagram, describe the components, flow, and relationships. Extract any text visible in the image. Output JSON with: {"summary": "2-3 sentence description", "topics": ["topic1", "topic2", ...], "entities": ["visible names, labels, identifiers", ...]}.`

// ExtractionResult holds the structured output from LLM topic extraction.
type ExtractionResult struct {
	// Summary is a 2-3 sentence description of the content.
	Summary string `json:"summary"`
	// Topics is a list of extracted topic strings (e.g., "authentication", "database schema").
	Topics []string `json:"topics"`
	// Entities is a list of specific names (functions, classes, packages) mentioned.
	Entities []string `json:"entities"`
}

// Usable reports whether an extraction says enough to keep: more than two
// topics, and a summary over twenty characters that is not a placeholder
// trailing off in an ellipsis. A reply that parses but fails this is
// garbage worth another attempt, not a result.
func Usable(r *ExtractionResult) bool {
	return r != nil && len(r.Topics) > 2 && !strings.Contains(r.Summary, "...") && len(r.Summary) > 20
}

// Provider extracts topics and descriptions from document content.
type Provider interface {
	// ExtractTopics processes text content and returns structured extraction.
	ExtractTopics(ctx context.Context, text string) (*ExtractionResult, error)

	// DescribeImage processes an image and returns structured extraction.
	DescribeImage(ctx context.Context, imageData []byte, mimeType string) (*ExtractionResult, error)

	// Name returns the provider name (e.g., "ollama", "vertex-ai").
	Name() string

	// ModelName returns the model identifier.
	ModelName() string
}

// Cache keeps extraction results by content hash, so a file whose content
// has not changed is not sent to a model again, and one whose extraction
// failed is not retried on every pass.
//
// Results are keyed by content hash and the hash last seen is recorded per
// path, so two copies of one file share a result while an edited file
// misses. Implementations are safe for concurrent use.
type Cache interface {
	// Check returns the result stored for a path when its content hash is
	// still contentHash, and nil (with a nil error) on any miss.
	Check(filePath, contentHash string) (*ExtractionResult, error)
	// Store records a result for a path at a content hash, clearing any
	// skip recorded for that hash.
	Store(filePath, contentHash string, result *ExtractionResult) error
	// MarkSkipped records that extraction failed for this content.
	MarkSkipped(filePath, contentHash string) error
	// IsSkipped reports whether extraction failed for this content before.
	IsSkipped(contentHash string) bool
}
