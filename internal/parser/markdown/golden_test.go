package markdown_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser/markdown"
)

// These tests pin every node and edge the parser emits, field by field, so a
// move or refactor of the parser can prove it changed nothing. Regenerate the
// golden files only when a change in output is intended:
//
//	UPDATE_GOLDEN=1 go test -race -count=1 -run Golden ./internal/parser/markdown/

// goldenNode is every field of a graph.Node the parser could set. UpdatedAt is
// left out because the parser never sets it; the indexer stamps it later.
type goldenNode struct {
	ID            string             `json:"id"`
	Type          graph.NodeType     `json:"type"`
	Name          string             `json:"name"`
	QualifiedName string             `json:"qualified_name"`
	FilePath      string             `json:"file_path"`
	Line          int                `json:"line"`
	EndLine       int                `json:"end_line"`
	Package       string             `json:"package"`
	Language      string             `json:"language"`
	Exported      bool               `json:"exported"`
	Signature     string             `json:"signature"`
	DocComment    string             `json:"doc_comment"`
	Properties    map[string]string  `json:"properties"`
	Metrics       map[string]float64 `json:"metrics"`
}

type goldenEdge struct {
	ID         string            `json:"id"`
	Type       graph.EdgeType    `json:"type"`
	SourceID   string            `json:"source_id"`
	TargetID   string            `json:"target_id"`
	Properties map[string]string `json:"properties"`
}

type goldenResult struct {
	FilePath string       `json:"file_path"`
	Language string       `json:"language"`
	Nodes    []goldenNode `json:"nodes"`
	Edges    []goldenEdge `json:"edges"`
}

// renderGolden parses content and renders the result with nodes and edges
// sorted by ID. Duplicate IDs are possible (a link repeated on one line
// range), so ties fall back to line and name to keep the order stable.
func renderGolden(t *testing.T, filePath string, content []byte, opts ...markdown.Option) string {
	t.Helper()
	res, err := markdown.NewParser(opts...).ParseFile(filePath, content)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	out := goldenResult{FilePath: res.FilePath, Language: string(res.Language)}
	for _, n := range res.Nodes {
		if !n.UpdatedAt.IsZero() {
			t.Errorf("node %s has UpdatedAt %v; the parser is not expected to set it", n.ID, n.UpdatedAt)
		}
		out.Nodes = append(out.Nodes, goldenNode{
			ID: n.ID, Type: n.Type, Name: n.Name, QualifiedName: n.QualifiedName,
			FilePath: n.FilePath, Line: n.Line, EndLine: n.EndLine, Package: n.Package,
			Language: n.Language, Exported: n.Exported, Signature: n.Signature,
			DocComment: n.DocComment, Properties: n.Properties, Metrics: n.Metrics,
		})
	}
	for _, e := range res.Edges {
		out.Edges = append(out.Edges, goldenEdge{
			ID: e.ID, Type: e.Type, SourceID: e.SourceID, TargetID: e.TargetID, Properties: e.Properties,
		})
	}
	sort.SliceStable(out.Nodes, func(i, j int) bool {
		a, b := out.Nodes[i], out.Nodes[j]
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Name < b.Name
	})
	sort.SliceStable(out.Edges, func(i, j int) bool { return out.Edges[i].ID < out.Edges[j].ID })

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data) + "\n"
}

// checkGolden compares got against testdata/golden/name, or rewrites the file
// when UPDATE_GOLDEN is set.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden file: %v (regenerate with UPDATE_GOLDEN=1)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs from its golden file (regenerate with UPDATE_GOLDEN=1 if the change is intended):\n%s",
			name, firstDiff(string(want), got))
	}
}

// firstDiff shows where two texts first differ, with a little context.
func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	i := 0
	for i < len(w) && i < len(g) && w[i] == g[i] {
		i++
	}
	var b strings.Builder
	fmt.Fprintf(&b, "first difference at line %d (want %d lines, got %d)\n", i+1, len(w), len(g))
	for j := max(0, i-2); j < min(len(w), i+3); j++ {
		fmt.Fprintf(&b, "  want %4d: %s\n", j+1, w[j])
	}
	for j := max(0, i-2); j < min(len(g), i+3); j++ {
		fmt.Fprintf(&b, "  got  %4d: %s\n", j+1, g[j])
	}
	return b.String()
}

// repoSample reads the repository-wide testdata/sample.md, located the same
// way TestParseMarkdownFixture does.
func repoSample(t *testing.T) []byte {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	content, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "testdata", "sample.md"))
	if err != nil {
		t.Fatalf("read testdata/sample.md: %v", err)
	}
	return content
}

// TestGoldenParse pins the full output for the shared sample and for a
// fixture built to reach every branch: front matter with quoted, nested,
// empty and commented keys; a preamble; duplicate and nested headings; a
// bullet list (which produces no nodes); fenced code with and without a
// language, holding a heading and a TODO that must be ignored; relative,
// repeated, absolute, mailto and anchor links; TODO and FIXME items; an
// empty section; and a level-four heading under a level-two one.
//
// The file path given to the parser is a fixed relative one, never the
// absolute path the content was read from, because node IDs hash it.
func TestGoldenParse(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "characterization.md"))
	if err != nil {
		t.Fatal(err)
	}
	sample := repoSample(t)

	tests := []struct {
		golden   string
		filePath string
		content  []byte
		opts     []markdown.Option
	}{
		{"sample.json", "testdata/sample.md", sample, nil},
		{"sample_no_text.json", "testdata/sample.md", sample, []markdown.Option{markdown.WithSectionText(false)}},
		{"characterization.json", "docs/payments.md", fixture, nil},
		{"characterization_no_text.json", "docs/payments.md", fixture, []markdown.Option{markdown.WithSectionText(false)}},
		// An AI guideline file name changes the document node's type, and
		// with it the document ID every edge hangs from.
		{"characterization_claude.json", "svc/CLAUDE.md", fixture, nil},
	}
	for _, tt := range tests {
		t.Run(tt.golden, func(t *testing.T) {
			checkGolden(t, tt.golden, renderGolden(t, tt.filePath, tt.content, tt.opts...))
		})
	}
}

// TestGoldenExplicitSectionTextOnMatchesDefault pins that the default is
// sections with text, so WithSectionText(true) is a no-op.
func TestGoldenExplicitSectionTextOnMatchesDefault(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "characterization.md"))
	if err != nil {
		t.Fatal(err)
	}
	def := renderGolden(t, "docs/payments.md", fixture)
	on := renderGolden(t, "docs/payments.md", fixture, markdown.WithSectionText(true))
	if def != on {
		t.Errorf("WithSectionText(true) differs from the default:\n%s", firstDiff(def, on))
	}
}

func TestStoresSectionText(t *testing.T) {
	tests := []struct {
		name string
		opts []markdown.Option
		want bool
	}{
		{"default", nil, true},
		{"on", []markdown.Option{markdown.WithSectionText(true)}, true},
		{"off", []markdown.Option{markdown.WithSectionText(false)}, false},
		{"last option wins", []markdown.Option{markdown.WithSectionText(false), markdown.WithSectionText(true)}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := markdown.NewParser(tt.opts...).StoresSectionText(); got != tt.want {
				t.Errorf("StoresSectionText() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestTextHashValues pins exact hashes. The hash is sha256 over the text's
// whitespace-separated words joined by single spaces, so reflowing or
// re-indenting is not a change. The expected values were computed outside
// Go, with: printf '%s' "<words>" | sha256sum
func TestTextHashValues(t *testing.T) {
	const (
		emptyHash = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
		retryHash = "sha256:52b2888355f79dd228cdd951a347d3a6daa10f46e97fb434491a48dae1e68ce6"
		fiveHash  = "sha256:402ad8ea2f4e7d98d950ad3beb1b2c695d6cc2315c6482fd7f23a801e20c6b2d"
		cafeHash  = "sha256:7c413039fbb2248e2b18b98e7a8d4d85bdcac7cd79b9477a0923f97e3a1f2b50"
	)
	tests := []struct {
		name string
		text string
		want string
	}{
		{"empty", "", emptyHash},
		{"only whitespace", " \n\t\r\n ", emptyHash},
		{"single line", "At most three retries per call.", retryHash},
		{"reflowed", "At most three retries\nper call.", retryHash},
		{"indented and padded", "\n\n    At most   three\tretries\r\n  per call.  \n", retryHash},
		{"reworded", "At most five retries per call.", fiveHash},
		{"non-ASCII words", "café au lait", cafeHash},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := markdown.TextHash(tt.text); got != tt.want {
				t.Errorf("TextHash(%q) = %s, want %s", tt.text, got, tt.want)
			}
		})
	}
}

// TestSectionHashCoversTheWholeTextNotTheStoredCut pins that a capped
// section stores its first 6000 bytes while its hash covers all of it, so
// an edit past the cut still reads as a change.
func TestSectionHashCoversTheWholeTextNotTheStoredCut(t *testing.T) {
	body := strings.Repeat("word ", 1500) + "tail"
	res, err := markdown.NewParser().ParseFile("docs/long.md", []byte("# Long\n\n"+body+"\n"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	var section *graph.Node
	for _, n := range res.Nodes {
		if n.Properties["kind"] == "section" {
			section = n
		}
	}
	if section == nil {
		t.Fatal("no section node")
	}
	if got := len(section.DocComment); got != 6000 {
		t.Errorf("stored text is %d bytes, want 6000", got)
	}
	if got := section.Properties["text_truncated"]; got != "true" {
		t.Errorf("text_truncated = %q, want true", got)
	}
	if got, want := section.Properties["section_hash"], markdown.TextHash(body); got != want {
		t.Errorf("section_hash = %s, want the hash of the full text %s", got, want)
	}
	if section.Properties["section_hash"] == markdown.TextHash(section.DocComment) {
		t.Error("section_hash equals the hash of the stored cut")
	}
}
