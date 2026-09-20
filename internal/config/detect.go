package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	internalllm "github.com/imyousuf/CodeEagle/internal/llm"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

// AllLanguages is the user-facing list of supported languages (excludes manifest).
var AllLanguages = []string{
	"go", "python", "typescript", "javascript", "java",
	"rust", "csharp", "ruby",
	"html", "markdown", "makefile", "shell", "terraform", "yaml",
}

// FilenameToLanguage maps well-known filenames to their language for auto-detection.
var FilenameToLanguage = map[string]string{
	"Makefile":         "makefile",
	"makefile":         "makefile",
	"GNUmakefile":      "makefile",
	"go.mod":           "go",
	"package.json":     "javascript",
	"pyproject.toml":   "python",
	"requirements.txt": "python",
	"setup.py":         "python",
	"Cargo.toml":       "rust",
	"Gemfile":          "ruby",
}

// DetectLLMProvider checks environment variables and Claude CLI to auto-detect
// the best LLM provider. Returns (provider, hint).
func DetectLLMProvider() (provider, hint string) {
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return "anthropic", "ANTHROPIC_API_KEY set"
	}
	if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") != "" || os.Getenv("GOOGLE_CLOUD_PROJECT") != "" {
		return "vertex-ai", "Google Cloud credentials detected"
	}
	if internalllm.FindClaudeCLI() != "" {
		return "claude-cli", "Claude Code CLI detected"
	}
	return "anthropic", ""
}

// DetectLanguages walks rootDir (depth-limited to 2 levels) and returns
// languages detected by file extensions and well-known filenames.
func DetectLanguages(rootDir string) []string {
	found := make(map[string]bool)

	// Build reverse map: extension -> language
	extToLang := make(map[string]string)
	for lang, exts := range parser.FileExtensions {
		langStr := string(lang)
		if langStr == "manifest" {
			continue
		}
		for _, ext := range exts {
			extToLang[ext] = langStr
		}
	}

	rootDepth := strings.Count(filepath.ToSlash(rootDir), "/")
	_ = filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		// Depth limit: 2 levels below root
		depth := strings.Count(filepath.ToSlash(path), "/") - rootDepth
		if d.IsDir() {
			if depth >= 2 {
				return fs.SkipDir
			}
			base := d.Name()
			if base == ".git" || base == "node_modules" || base == "vendor" || base == "__pycache__" || base == "dist" || base == "build" {
				return fs.SkipDir
			}
			return nil
		}
		if lang, ok := extToLang[filepath.Ext(path)]; ok {
			found[lang] = true
		}
		if lang, ok := FilenameToLanguage[d.Name()]; ok {
			found[lang] = true
		}
		return nil
	})

	result := make([]string, 0, len(found))
	for lang := range found {
		result = append(result, lang)
	}
	sort.Strings(result)
	return result
}
