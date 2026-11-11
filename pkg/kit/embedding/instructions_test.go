package embedding_test

import (
	"context"
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/kit/embedding"
)

// echo records the texts it was asked to embed.
type echo struct {
	docs    []string
	queries []string
}

func (e *echo) Embed(_ context.Context, texts []string) ([][]float32, error) {
	e.docs = append(e.docs, texts...)
	return make([][]float32, len(texts)), nil
}
func (e *echo) EmbedQuery(_ context.Context, text string) ([]float32, error) {
	e.queries = append(e.queries, text)
	return nil, nil
}
func (e *echo) Dimensions() int   { return 3 }
func (e *echo) Name() string      { return "echo" }
func (e *echo) ModelName() string { return "echo-1" }

func TestInstructionsArePrependedVerbatim(t *testing.T) {
	inner := &echo{}
	p := embedding.WithInstructions(inner, "title: none | text: ", "task: search result | query: ")
	if _, err := p.Embed(context.Background(), []string{"Retry budget", "Rollout plan"}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.EmbedQuery(context.Background(), "how do retries work"); err != nil {
		t.Fatal(err)
	}
	if got := inner.docs; len(got) != 2 || got[0] != "title: none | text: Retry budget" || got[1] != "title: none | text: Rollout plan" {
		t.Errorf("documents sent = %q", got)
	}
	if got := inner.queries; len(got) != 1 || got[0] != "task: search result | query: how do retries work" {
		t.Errorf("queries sent = %q", got)
	}
	if p.Name() != "echo" || p.ModelName() != "echo-1" || p.Dimensions() != 3 {
		t.Error("the wrapper does not report the wrapped provider")
	}
}

func TestEmptyInstructionsChangeNothing(t *testing.T) {
	inner := &echo{}
	p := embedding.WithInstructions(inner, "", "")
	_, _ = p.Embed(context.Background(), []string{"as given"})
	_, _ = p.EmbedQuery(context.Background(), "as asked")
	if inner.docs[0] != "as given" || inner.queries[0] != "as asked" {
		t.Errorf("sent %q and %q", inner.docs, inner.queries)
	}
	if f := p.(embedding.Fingerprinter).EmbedConfig(); f != "" {
		t.Errorf("fingerprint of no instructions = %q, want empty", f)
	}
}

func TestInstructionFingerprintsTellChangesApart(t *testing.T) {
	fp := func(doc, query string) string {
		return embedding.WithInstructions(&echo{}, doc, query).(embedding.Fingerprinter).EmbedConfig()
	}
	a, b, c := fp("title: none | text: ", "task: search result | query: "), fp("title: none | text: ", "task: question answering | query: "), fp("title: none | text: ", "task: search result | query: ")
	if a == "" || a == b || a != c {
		t.Errorf("fingerprints: %q, %q, %q; want stable and distinct", a, b, c)
	}
	// Prefixes that only move the boundary between document and query are
	// still different instructions.
	if fp("ab", "") == fp("a", "b") {
		t.Error("fingerprint ignores where one prefix ends")
	}
}
