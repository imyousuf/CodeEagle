package vertex

import (
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/kit/embedding"
)

func TestVertexProviderRequiresProject(t *testing.T) {
	_, err := New(embedding.Config{
		Provider: "vertex-ai",
		Location: "us-central1",
	})
	if err == nil {
		t.Error("expected error when project is missing")
	}
}

func TestVertexProviderRequiresLocation(t *testing.T) {
	_, err := New(embedding.Config{
		Provider: "vertex-ai",
		Project:  "my-project",
	})
	if err == nil {
		t.Error("expected error when location is missing")
	}
}

func TestVertexProviderDefaults(t *testing.T) {
	if DefaultModel != "gemini-embedding-001" {
		t.Errorf("default vertex model = %q, want gemini-embedding-001", DefaultModel)
	}
	if DefaultDimensions != 768 {
		t.Errorf("default vertex dims = %d, want 768", DefaultDimensions)
	}
}
