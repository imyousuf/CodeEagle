package watcher

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestGitIgnoreMatcherBasicPatterns(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		path     string
		want     bool
	}{
		{
			name:     "match wildcard extension",
			patterns: []string{"*.log"},
			path:     "/project/app.log",
			want:     true,
		},
		{
			name:     "no match different extension",
			patterns: []string{"*.log"},
			path:     "/project/app.go",
			want:     false,
		},
		{
			name:     "match directory name",
			patterns: []string{"node_modules"},
			path:     "/project/node_modules/package/index.js",
			want:     true,
		},
		{
			name:     "match double star pattern",
			patterns: []string{"**/*.pyc"},
			path:     "/project/deep/nested/module.pyc",
			want:     true,
		},
		{
			name:     "match double star directory",
			patterns: []string{"**/vendor/**"},
			path:     "/project/service/vendor/lib/code.go",
			want:     true,
		},
		{
			name:     "match .git directory",
			patterns: []string{".git"},
			path:     "/project/.git/config",
			want:     true,
		},
		{
			name:     "match __pycache__",
			patterns: []string{"__pycache__"},
			path:     "/project/app/__pycache__/module.cpython-39.pyc",
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewGitIgnoreMatcher(nil, tt.patterns)
			// Don't call LoadPatterns since we have no repo roots.
			m.rules = nil
			for _, p := range tt.patterns {
				m.rules = append(m.rules, parsePattern(p, ""))
			}

			got := m.Match(tt.path)
			if got != tt.want {
				t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestGitIgnoreMatcherNegation(t *testing.T) {
	m := NewGitIgnoreMatcher(nil, nil)
	m.rules = []ignoreRule{
		parsePattern("*.log", ""),
		parsePattern("!important.log", ""),
	}

	if !m.Match("/project/debug.log") {
		t.Error("expected debug.log to be ignored")
	}
	if m.Match("/project/important.log") {
		t.Error("expected important.log to NOT be ignored (negation)")
	}
}

func TestGitIgnoreMatcherDirOnlyPattern(t *testing.T) {
	m := NewGitIgnoreMatcher(nil, nil)
	m.rules = []ignoreRule{
		parsePattern("build/", ""),
	}

	if !m.Match("/project/build/output.js") {
		t.Error("expected build directory path to be ignored")
	}
}

func TestGitIgnoreMatcherRelativePattern(t *testing.T) {
	m := NewGitIgnoreMatcher(nil, nil)
	m.rules = []ignoreRule{
		parsePattern("src/*.tmp", "/project"),
	}

	if !m.Match("/project/src/file.tmp") {
		t.Error("expected /project/src/file.tmp to be matched by src/*.tmp")
	}
	if m.Match("/project/other/file.tmp") {
		t.Error("expected /project/other/file.tmp to NOT be matched by src/*.tmp")
	}
}

func TestGitIgnoreLoadFromFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a .gitignore file.
	gitignoreContent := "*.log\nbuild/\n# comment\n\n!keep.log\n"
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(gitignoreContent), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewGitIgnoreMatcher([]string{tmpDir}, nil)
	if err := m.LoadPatterns(); err != nil {
		t.Fatal(err)
	}

	if !m.Match(filepath.Join(tmpDir, "app.log")) {
		t.Error("expected app.log to be ignored")
	}
	if m.Match(filepath.Join(tmpDir, "keep.log")) {
		t.Error("expected keep.log to NOT be ignored (negation)")
	}
	if !m.Match(filepath.Join(tmpDir, "build", "output.js")) {
		t.Error("expected build/output.js to be ignored")
	}
	if m.Match(filepath.Join(tmpDir, "main.go")) {
		t.Error("expected main.go to NOT be ignored")
	}
}

func TestExcludePatterns(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		path     string
		want     bool
	}{
		{
			name:     "exclude node_modules",
			patterns: []string{"**/node_modules/**"},
			path:     "/project/frontend/node_modules/react/index.js",
			want:     true,
		},
		{
			name:     "exclude .git",
			patterns: []string{"**/.git/**"},
			path:     "/project/.git/HEAD",
			want:     true,
		},
		{
			name:     "exclude vendor",
			patterns: []string{"**/vendor/**"},
			path:     "/project/service/vendor/github.com/lib/pq/pq.go",
			want:     true,
		},
		{
			name:     "exclude dist",
			patterns: []string{"**/dist/**"},
			path:     "/project/frontend/dist/bundle.js",
			want:     true,
		},
		{
			name:     "do not exclude source",
			patterns: []string{"**/node_modules/**", "**/dist/**"},
			path:     "/project/frontend/src/App.tsx",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewGitIgnoreMatcher(nil, tt.patterns)
			m.rules = nil
			for _, p := range tt.patterns {
				m.rules = append(m.rules, parsePattern(p, ""))
			}

			got := m.Match(tt.path)
			if got != tt.want {
				t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestEventDebouncing(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a test file.
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("initial"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := WatcherConfig{
		Paths: []string{tmpDir},
	}

	w, err := NewWatcher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	events, err := w.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Give the watcher time to initialize.
	time.Sleep(200 * time.Millisecond)

	// Write to the file multiple times in rapid succession.
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(testFile, []byte("content "+string(rune('0'+i))), 0644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Wait for debounce window to pass.
	time.Sleep(300 * time.Millisecond)

	// Collect events that arrived.
	var collected []Event
	timeout := time.After(500 * time.Millisecond)
loop:
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				break loop
			}
			collected = append(collected, evt)
		case <-timeout:
			break loop
		}
	}

	// Due to debouncing, we should get significantly fewer events than the 5 writes.
	// Typically 1-2 events (the debounce collapses rapid writes).
	if len(collected) == 0 {
		t.Error("expected at least one debounced event, got none")
	}
	if len(collected) >= 5 {
		t.Errorf("expected debouncing to reduce events, got %d events for 5 writes", len(collected))
	}

	// All events should be for our test file.
	for _, evt := range collected {
		if evt.Path != testFile {
			t.Errorf("unexpected event path: %s", evt.Path)
		}
	}
}

func TestWatcherNewDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := WatcherConfig{
		Paths: []string{tmpDir},
	}

	w, err := NewWatcher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	events, err := w.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Give the watcher time to initialize.
	time.Sleep(200 * time.Millisecond)

	// Create a new subdirectory and file.
	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Wait a bit for the directory to be added to the watcher.
	time.Sleep(300 * time.Millisecond)

	newFile := filepath.Join(subDir, "new.txt")
	if err := os.WriteFile(newFile, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	// Wait for debounce.
	time.Sleep(300 * time.Millisecond)

	// Collect events.
	var collected []Event
	timeout := time.After(500 * time.Millisecond)
loop:
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				break loop
			}
			collected = append(collected, evt)
		case <-timeout:
			break loop
		}
	}

	if len(collected) == 0 {
		t.Error("expected events for new directory/file creation, got none")
	}
}

func TestWatcherExcludedPath(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a node_modules directory with a file.
	nmDir := filepath.Join(tmpDir, "node_modules")
	if err := os.MkdirAll(nmDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nmDir, "pkg.js"), []byte("module"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := WatcherConfig{
		Paths:           []string{tmpDir},
		ExcludePatterns: []string{"node_modules"},
	}

	w, err := NewWatcher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	events, err := w.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(200 * time.Millisecond)

	// Write to excluded directory.
	if err := os.WriteFile(filepath.Join(nmDir, "pkg.js"), []byte("updated"), 0644); err != nil {
		t.Fatal(err)
	}

	// Write to non-excluded file.
	srcFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(srcFile, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	time.Sleep(300 * time.Millisecond)

	var collected []Event
	timeout := time.After(500 * time.Millisecond)
loop:
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				break loop
			}
			collected = append(collected, evt)
		case <-timeout:
			break loop
		}
	}

	// Verify no events from node_modules.
	for _, evt := range collected {
		if filepath.Dir(evt.Path) == nmDir || evt.Path == nmDir {
			t.Errorf("got event from excluded directory: %s", evt.Path)
		}
	}
}

func TestConvertOp(t *testing.T) {
	tests := []struct {
		name   string
		op     fsnotify.Op
		want   EventOp
		wantOk bool
	}{
		{"create", fsnotify.Create, Create, true},
		{"write", fsnotify.Write, Write, true},
		{"remove", fsnotify.Remove, Remove, true},
		{"rename", fsnotify.Rename, Rename, true},
		{"chmod only", fsnotify.Chmod, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := convertOp(tt.op)
			if ok != tt.wantOk {
				t.Errorf("convertOp(%v) ok = %v, want %v", tt.op, ok, tt.wantOk)
			}
			if ok && got != tt.want {
				t.Errorf("convertOp(%v) = %v, want %v", tt.op, got, tt.want)
			}
		})
	}
}

func TestEventOpString(t *testing.T) {
	tests := []struct {
		op   EventOp
		want string
	}{
		{Create, "Create"},
		{Write, "Write"},
		{Remove, "Remove"},
		{Rename, "Rename"},
		{EventOp(99), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.op.String(); got != tt.want {
				t.Errorf("EventOp(%d).String() = %q, want %q", tt.op, got, tt.want)
			}
		})
	}
}
