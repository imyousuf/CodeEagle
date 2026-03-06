package faces

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExtractImageDate_EXIFDate(t *testing.T) {
	// Create a minimal JPEG with EXIF data is complex; test the EXIF path
	// indirectly by verifying that a non-EXIF file falls through.
	dir := t.TempDir()
	path := filepath.Join(dir, "photo.jpg")
	os.WriteFile(path, []byte("not a real jpeg"), 0644)

	result := ExtractImageDate(path)
	// Should fall through to mtime since there's no valid EXIF.
	if result.DateSource == DateSourceEXIF {
		t.Error("should not detect EXIF from fake JPEG")
	}
	if result.DateTaken.IsZero() {
		t.Error("expected non-zero date (should fallback to mtime)")
	}
}

func TestExtractImageDate_FolderDateUnderscore(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "vacation_20240715")
	os.MkdirAll(subdir, 0755)
	path := filepath.Join(subdir, "photo.jpg")
	os.WriteFile(path, []byte("data"), 0644)

	result := ExtractImageDate(path)
	if result.DateSource != DateSourceFolder {
		t.Errorf("DateSource = %q, want folder", result.DateSource)
	}
	expected := time.Date(2024, 7, 15, 0, 0, 0, 0, time.UTC)
	if !result.DateTaken.Equal(expected) {
		t.Errorf("DateTaken = %v, want %v", result.DateTaken, expected)
	}
	if result.FolderName != "vacation_20240715" {
		t.Errorf("FolderName = %q", result.FolderName)
	}
}

func TestExtractImageDate_FolderDateHyphen(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "trip-20230101")
	os.MkdirAll(subdir, 0755)
	path := filepath.Join(subdir, "img.png")
	os.WriteFile(path, []byte("data"), 0644)

	result := ExtractImageDate(path)
	if result.DateSource != DateSourceFolder {
		t.Errorf("DateSource = %q, want folder", result.DateSource)
	}
	expected := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	if !result.DateTaken.Equal(expected) {
		t.Errorf("DateTaken = %v, want %v", result.DateTaken, expected)
	}
}

func TestExtractImageDate_FilenameIMG(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "IMG_20231225_143022.jpg")
	os.WriteFile(path, []byte("data"), 0644)

	result := ExtractImageDate(path)
	if result.DateSource != DateSourceFilename {
		t.Errorf("DateSource = %q, want filename", result.DateSource)
	}
	expected := time.Date(2023, 12, 25, 0, 0, 0, 0, time.UTC)
	if !result.DateTaken.Equal(expected) {
		t.Errorf("DateTaken = %v, want %v", result.DateTaken, expected)
	}
}

func TestExtractImageDate_FilenameDSC(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "DSC_20220601_001.jpg")
	os.WriteFile(path, []byte("data"), 0644)

	result := ExtractImageDate(path)
	if result.DateSource != DateSourceFilename {
		t.Errorf("DateSource = %q, want filename", result.DateSource)
	}
}

func TestExtractImageDate_FilenameCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "img_20220101_hello.jpg")
	os.WriteFile(path, []byte("data"), 0644)

	result := ExtractImageDate(path)
	if result.DateSource != DateSourceFilename {
		t.Errorf("DateSource = %q, want filename (case insensitive)", result.DateSource)
	}
}

func TestExtractImageDate_FallbackMtime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "random_file.jpg")
	os.WriteFile(path, []byte("data"), 0644)

	result := ExtractImageDate(path)
	if result.DateSource != DateSourceMtime {
		t.Errorf("DateSource = %q, want mtime", result.DateSource)
	}
	if result.DateTaken.IsZero() {
		t.Error("expected non-zero mtime")
	}
}

func TestExtractImageDate_NonexistentFile(t *testing.T) {
	result := ExtractImageDate("/nonexistent/path/photo.jpg")
	if result.DateSource != DateSourceUnknown {
		t.Errorf("DateSource = %q, want unknown", result.DateSource)
	}
}

func TestParseFolderDate_NoMatch(t *testing.T) {
	_, ok := parseFolderDate("some-random-folder")
	if ok {
		t.Error("should not match random folder name")
	}
}

func TestParseFilenameDate_NoMatch(t *testing.T) {
	_, ok := parseFilenameDate("document.pdf")
	if ok {
		t.Error("should not match non-image filename")
	}
}
