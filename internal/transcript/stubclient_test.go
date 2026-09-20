package transcript

import (
	"context"

	"github.com/imyousuf/CodeEagle/pkg/llm"
)

// stubStructuredClient returns a canned reply, so tests can exercise what the
// analyzer does with a model's answer without calling one.
//
// It implements llm.StructuredClient because that is the path the analyzer
// prefers when the provider can enforce a schema.
type stubStructuredClient struct {
	// reply is the JSON document the model "returned".
	reply string
	// err, when set, is returned instead.
	err error
	// calls counts requests, for tests that assert a model was not consulted.
	calls int
}

func (c *stubStructuredClient) Chat(_ context.Context, _ string, _ []llm.Message) (*llm.Response, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return &llm.Response{Content: c.reply, FinishReason: "end_turn"}, nil
}

func (c *stubStructuredClient) ChatJSON(
	ctx context.Context, systemPrompt string, messages []llm.Message, _ *llm.JSONSchema,
) (*llm.Response, error) {
	return c.Chat(ctx, systemPrompt, messages)
}

func (c *stubStructuredClient) Model() string    { return "stub" }
func (c *stubStructuredClient) Provider() string { return "stub" }
func (c *stubStructuredClient) Close() error     { return nil }
