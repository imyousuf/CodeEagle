package llm

import (
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/llm"
)

func TestVertexAIProviderRegistration(t *testing.T) {
	if !llm.IsProviderRegistered("vertex-ai") {
		t.Fatal("expected 'vertex-ai' provider to be registered via init()")
	}
}

func TestVertexAIConfigValidation(t *testing.T) {
	_, err := llm.NewClient(llm.Config{
		Provider: "vertex-ai",
		// No project
	})
	if err == nil {
		t.Fatal("expected error when project is missing")
	}
	expected := "project is required for Vertex AI provider"
	if err.Error() != expected {
		t.Fatalf("expected error %q, got %q", expected, err.Error())
	}
}
