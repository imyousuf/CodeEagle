package faces

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveFilePath_Absolute(t *testing.T) {
	got := ResolveFilePath("/absolute/path.jpg", []string{"/some/root"})
	if got != "/absolute/path.jpg" {
		t.Errorf("expected /absolute/path.jpg, got %s", got)
	}
}

func TestResolveFilePath_DirectJoin(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(sub, "photo.jpg")
	if err := os.WriteFile(f, []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := ResolveFilePath("sub/photo.jpg", []string{dir})
	if got != f {
		t.Errorf("expected %s, got %s", f, got)
	}
}

func TestResolveFilePath_BasenamePrefix(t *testing.T) {
	dir := t.TempDir()
	// Create root like /tmp/xxx/Pictures
	root := filepath.Join(dir, "Pictures")
	sub := filepath.Join(root, "vacation")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(sub, "photo.jpg")
	if err := os.WriteFile(f, []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}

	// relPath has basename prefix "Pictures/vacation/photo.jpg"
	got := ResolveFilePath("Pictures/vacation/photo.jpg", []string{root})
	if got != f {
		t.Errorf("expected %s, got %s", f, got)
	}
}

func TestResolveFilePath_Fallback(t *testing.T) {
	got := ResolveFilePath("nonexistent/file.txt", []string{"/no/such/root"})
	if got != "nonexistent/file.txt" {
		t.Errorf("expected fallback to original, got %s", got)
	}
}

func TestResolveFilePath_MultipleRoots(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	f := filepath.Join(dir2, "test.txt")
	if err := os.WriteFile(f, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := ResolveFilePath("test.txt", []string{dir1, dir2})
	if got != f {
		t.Errorf("expected %s, got %s", f, got)
	}
}
