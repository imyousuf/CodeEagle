package extract

import (
	"strings"
	"testing"
)

func TestTextSVG(t *testing.T) {
	svg := `<svg><text>Hello</text><rect/><text>World</text></svg>`
	result := Text("diagram.svg", []byte(svg))
	if !strings.Contains(result, "Hello") || !strings.Contains(result, "World") {
		t.Errorf("expected stripped SVG text, got %s", result)
	}
	if strings.Contains(result, "<svg>") {
		t.Error("SVG tags should be stripped")
	}
}

func TestTextCSV(t *testing.T) {
	csv := "name,age,city\nAlice,30,NYC\nBob,25,LA\n"
	result := Text("data.csv", []byte(csv))
	if !strings.Contains(result, "CSV headers:") {
		t.Errorf("expected CSV headers prefix, got %s", result)
	}
	if !strings.Contains(result, "name,age,city") {
		t.Error("expected header row in output")
	}
}

func TestTextPlain(t *testing.T) {
	text := "Just a plain text file."
	result := Text("notes.txt", []byte(text))
	if result != text {
		t.Errorf("expected plain passthrough, got %s", result)
	}
}
