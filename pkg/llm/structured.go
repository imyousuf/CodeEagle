package llm

import "context"

// JSONSchema constrains a model's response to a known shape.
type JSONSchema struct {
	// Name identifies the schema to the provider.
	Name string `json:"name"`
	// Schema is the JSON Schema the response must satisfy.
	Schema map[string]any `json:"schema"`
	// Strict asks the provider to guarantee conformance rather than merely
	// encourage it. Providers that cannot enforce it ignore the flag.
	Strict bool `json:"strict"`
}

// StructuredClient extends Client with schema-constrained responses.
//
// Extraction pipelines need parseable output far more than they need prose.
// Asking a model to "reply with JSON" and hoping is a reliability problem at
// scale; providers that can enforce a schema remove that failure mode
// entirely, and this interface exposes that when it is available.
type StructuredClient interface {
	Client

	// ChatJSON sends messages and requires the reply to satisfy schema.
	// Response.Content holds the raw JSON document.
	ChatJSON(ctx context.Context, systemPrompt string, messages []Message, schema *JSONSchema) (*Response, error)
}

// SupportsStructured reports whether c can enforce a response schema.
func SupportsStructured(c Client) bool {
	_, ok := c.(StructuredClient)
	return ok
}
