package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectLLMProvider(t *testing.T) {
	// Save and clear env vars that affect detection.
	origKey := os.Getenv("ANTHROPIC_API_KEY")
	origCreds := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	origProject := os.Getenv("GOOGLE_CLOUD_PROJECT")
	os.Unsetenv("ANTHROPIC_API_KEY")
	os.Unsetenv("GOOGLE_APPLICATION_CREDENTIALS")
	os.Unsetenv("GOOGLE_CLOUD_PROJECT")
	defer func() {
		if origKey != "" {
			os.Setenv("ANTHROPIC_API_KEY", origKey)
		}
		if origCreds != "" {
			os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", origCreds)
		}
		if origProject != "" {
			os.Setenv("GOOGLE_CLOUD_PROJECT", origProject)
		}
	}()

	t.Run("anthropic key", func(t *testing.T) {
		os.Setenv("ANTHROPIC_API_KEY", "sk-test")
		defer os.Unsetenv("ANTHROPIC_API_KEY")
		p, h := DetectLLMProvider()
		if p != "anthropic" {
			t.Errorf("provider = %q, want anthropic", p)
		}
		if h != "ANTHROPIC_API_KEY set" {
			t.Errorf("hint = %q, want 'ANTHROPIC_API_KEY set'", h)
		}
	})

	t.Run("gcp credentials", func(t *testing.T) {
		os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/tmp/creds.json")
		defer os.Unsetenv("GOOGLE_APPLICATION_CREDENTIALS")
		p, _ := DetectLLMProvider()
		if p != "vertex-ai" {
			t.Errorf("provider = %q, want vertex-ai", p)
		}
	})
}

func TestDetectLanguages(t *testing.T) {
	dir := t.TempDir()

	// Create some files to detect.
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(dir, "app.py"), []byte("print('hi')"), 0644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	os.WriteFile(filepath.Join(dir, "sub", "index.ts"), []byte("export {}"), 0644)

	langs := DetectLanguages(dir)
	want := map[string]bool{"go": true, "python": true, "typescript": true}
	got := make(map[string]bool, len(langs))
	for _, l := range langs {
		got[l] = true
	}
	for lang := range want {
		if !got[lang] {
			t.Errorf("expected to detect %q, but didn't; got %v", lang, langs)
		}
	}
}

func TestDetectLanguagesDepthLimit(t *testing.T) {
	dir := t.TempDir()

	// File at depth 3 should be ignored.
	os.MkdirAll(filepath.Join(dir, "a", "b", "c"), 0755)
	os.WriteFile(filepath.Join(dir, "a", "b", "c", "deep.go"), []byte("package deep"), 0644)

	langs := DetectLanguages(dir)
	if len(langs) != 0 {
		t.Errorf("expected no languages at depth 3, got %v", langs)
	}
}

func TestAllLanguages(t *testing.T) {
	if len(AllLanguages) != 14 {
		t.Errorf("AllLanguages has %d entries, want 14", len(AllLanguages))
	}
}
