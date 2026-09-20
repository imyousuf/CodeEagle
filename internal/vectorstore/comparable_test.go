package vectorstore

import (
	"strings"
	"testing"
)

// TestMetaComparable covers refusing to rank vectors from two embedding
// models against each other.
//
// A similarity score means something only relative to other scores from the
// same model. Two models place text in different spaces, so merging their
// rankings produces an order that looks authoritative and is arbitrary. The
// same dimensionality from a different model is the dangerous case: nothing
// downstream would fail, the answers would just be wrong.
func TestMetaComparable(t *testing.T) {
	base := &VectorIndexMeta{Provider: "ollama", Model: "nomic-embed-text-v2-moe", Dimensions: 768}

	tests := []struct {
		name  string
		other *VectorIndexMeta
		want  bool
	}{
		{
			name:  "the same provider, model and size",
			other: &VectorIndexMeta{Provider: "ollama", Model: "nomic-embed-text-v2-moe", Dimensions: 768},
			want:  true,
		},
		{
			name:  "a different model at the same size",
			other: &VectorIndexMeta{Provider: "ollama", Model: "mxbai-embed-large", Dimensions: 768},
		},
		{
			name:  "a different provider with the same model name and size",
			other: &VectorIndexMeta{Provider: "vertex-ai", Model: "nomic-embed-text-v2-moe", Dimensions: 768},
		},
		{
			name:  "the same model at a different size",
			other: &VectorIndexMeta{Provider: "ollama", Model: "nomic-embed-text-v2-moe", Dimensions: 384},
		},
		{
			name:  "nothing at all",
			other: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := base.Comparable(tt.other); got != tt.want {
				t.Errorf("Comparable() = %v, want %v", got, tt.want)
			}
		})
	}

	var absent *VectorIndexMeta
	if absent.Comparable(base) {
		t.Error("an absent index compared as comparable")
	}
}

// TestMetaDescribe covers naming an embedding well enough to explain a
// refusal.
func TestMetaDescribe(t *testing.T) {
	meta := &VectorIndexMeta{Provider: "ollama", Model: "nomic-embed-text-v2-moe", Dimensions: 768}
	got := meta.Describe()
	for _, want := range []string{"ollama", "nomic-embed-text-v2-moe", "768"} {
		if !strings.Contains(got, want) {
			t.Errorf("Describe() = %q, want it to mention %q", got, want)
		}
	}

	var absent *VectorIndexMeta
	if absent.Describe() == "" {
		t.Error("Describe() on an absent index says nothing at all")
	}
}
