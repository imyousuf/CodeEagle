package watcher

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// GitIgnoreMatcher matches file paths against .gitignore patterns.
type GitIgnoreMatcher struct {
	repoRoots       []string
	excludePatterns []string
	rules           []ignoreRule
}

type ignoreRule struct {
	pattern  string
	negation bool
	dirOnly  bool
	basePath string // directory where the .gitignore was found
}

// NewGitIgnoreMatcher creates a new matcher for the given repo roots.
// excludePatterns are additional patterns from .CodeEagle/config.yaml.
func NewGitIgnoreMatcher(repoRoots []string, excludePatterns []string) *GitIgnoreMatcher {
	return &GitIgnoreMatcher{
		repoRoots:       repoRoots,
		excludePatterns: excludePatterns,
	}
}

// LoadPatterns finds and parses .gitignore files in repo roots and subdirectories.
// It also loads the excludePatterns from config.
func (m *GitIgnoreMatcher) LoadPatterns() error {
	m.rules = nil

	// Load config exclude patterns as global rules.
	for _, p := range m.excludePatterns {
		m.rules = append(m.rules, parsePattern(p, ""))
	}

	// Walk each repo root and load .gitignore files.
	for _, root := range m.repoRoots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // skip inaccessible entries
			}
			if info.IsDir() {
				base := info.Name()
				if base == ".git" || base == "node_modules" || base == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}
			if info.Name() == ".gitignore" {
				rules, loadErr := loadGitIgnoreFile(path)
				if loadErr != nil {
					return nil // skip unreadable gitignore files
				}
				m.rules = append(m.rules, rules...)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// Match returns true if the given path should be ignored.
func (m *GitIgnoreMatcher) Match(path string) bool {
	matched := false
	for _, rule := range m.rules {
		if matchRule(rule, path) {
			matched = !rule.negation
		}
	}
	return matched
}

func loadGitIgnoreFile(gitignorePath string) ([]ignoreRule, error) {
	f, err := os.Open(gitignorePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	basePath := filepath.Dir(gitignorePath)
	var rules []ignoreRule

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rules = append(rules, parsePattern(line, basePath))
	}
	return rules, scanner.Err()
}

func parsePattern(pattern string, basePath string) ignoreRule {
	rule := ignoreRule{basePath: basePath}

	// Check for negation.
	if strings.HasPrefix(pattern, "!") {
		rule.negation = true
		pattern = pattern[1:]
	}

	// Check for directory-only pattern.
	if strings.HasSuffix(pattern, "/") {
		rule.dirOnly = true
		pattern = strings.TrimSuffix(pattern, "/")
	}

	rule.pattern = pattern
	return rule
}

func matchRule(rule ignoreRule, path string) bool {
	// For directory-only rules, we check if the path is a directory.
	// Since we often match paths before stat, we check if any path component matches.
	if rule.dirOnly {
		return matchDirOnlyPattern(rule, path)
	}

	return matchPattern(rule.pattern, rule.basePath, path)
}

func matchDirOnlyPattern(rule ignoreRule, path string) bool {
	// Check if any directory component in the path matches the pattern.
	return matchPattern(rule.pattern, rule.basePath, path)
}

func matchPattern(pattern string, basePath string, path string) bool {
	// If pattern contains /, it is relative to the basePath.
	if strings.Contains(pattern, "/") {
		return matchRelativePattern(pattern, basePath, path)
	}

	// When a basePath is set (pattern from a .gitignore file), the pattern
	// only applies to files within that directory tree. Verify the path is
	// under basePath before matching.
	if basePath != "" {
		relPath, err := filepath.Rel(basePath, path)
		if err != nil || strings.HasPrefix(relPath, "..") {
			return false
		}
	}

	// Otherwise match against any component or the full path.
	// First try matching against the basename.
	base := filepath.Base(path)
	if matched, _ := filepath.Match(pattern, base); matched {
		return true
	}

	// Try matching against each path component.
	parts := splitPath(path)
	for _, part := range parts {
		if matched, _ := filepath.Match(pattern, part); matched {
			return true
		}
	}

	return false
}

func matchRelativePattern(pattern string, basePath string, path string) bool {
	// Handle ** patterns by expanding into component matching.
	if strings.Contains(pattern, "**") {
		return matchDoubleStarPattern(pattern, basePath, path)
	}

	// Make path relative to basePath if basePath is set.
	relPath := path
	if basePath != "" {
		var err error
		relPath, err = filepath.Rel(basePath, path)
		if err != nil {
			return false
		}
		// If the relative path starts with .., the path is outside basePath.
		if strings.HasPrefix(relPath, "..") {
			return false
		}
	}

	matched, _ := filepath.Match(pattern, relPath)
	return matched
}

func matchDoubleStarPattern(pattern string, basePath string, path string) bool {
	// Make path relative to basePath if basePath is set.
	relPath := path
	if basePath != "" {
		var err error
		relPath, err = filepath.Rel(basePath, path)
		if err != nil {
			return false
		}
		if strings.HasPrefix(relPath, "..") {
			return false
		}
	}

	// Split pattern and path into components.
	patternParts := splitPath(pattern)
	pathParts := splitPath(relPath)

	return matchParts(patternParts, pathParts)
}

func matchParts(patternParts, pathParts []string) bool {
	if len(patternParts) == 0 {
		return len(pathParts) == 0
	}

	if patternParts[0] == "**" {
		// ** matches zero or more directories.
		rest := patternParts[1:]
		// Try matching ** against 0, 1, 2, ... path components.
		for i := 0; i <= len(pathParts); i++ {
			if matchParts(rest, pathParts[i:]) {
				return true
			}
		}
		return false
	}

	if len(pathParts) == 0 {
		return false
	}

	matched, _ := filepath.Match(patternParts[0], pathParts[0])
	if !matched {
		return false
	}
	return matchParts(patternParts[1:], pathParts[1:])
}

func splitPath(path string) []string {
	// Normalize to forward slashes for consistency.
	path = filepath.ToSlash(path)
	parts := strings.Split(path, "/")
	// Filter empty parts.
	var result []string
	for _, p := range parts {
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
