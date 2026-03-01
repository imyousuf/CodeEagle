package embedding

import (
	"testing"
)

func TestVertexProviderRequiresProject(t *testing.T) {
	_, err := newVertexProvider(Config{
		Provider: "vertex-ai",
		Location: "us-central1",
	})
	if err == nil {
		t.Error("expected error when project is missing")
	}
}

func TestVertexProviderRequiresLocation(t *testing.T) {
	_, err := newVertexProvider(Config{
		Provider: "vertex-ai",
		Project:  "my-project",
	})
	if err == nil {
		t.Error("expected error when location is missing")
	}
}

func TestVertexProviderDefaults(t *testing.T) {
	if defaultVertexModel != "gemini-embedding-001" {
		t.Errorf("default vertex model = %q, want gemini-embedding-001", defaultVertexModel)
	}
	if defaultVertexDims != 768 {
		t.Errorf("default vertex dims = %d, want 768", defaultVertexDims)
	}
}
