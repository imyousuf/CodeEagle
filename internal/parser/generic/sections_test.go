package generic

import (
	"fmt"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

func TestSectionize(t *testing.T) {
	long := strings.Repeat("word ", 320) // 1,600 characters
	tests := []struct {
		name     string
		path     string
		text     string
		headings []string
	}{
		{"not prose", "data.csv", "a,b\n1,2", nil},
		{"code", "main.c", "int main() { return 0; }", nil},
		{"config", "settings.yaml", "key: value", nil},
		{"empty", "notes.txt", "  \n ", nil},
		{"short pages merge", "report.pdf",
			"--- Page 1 ---\nIntro\n\n--- Page 2 ---\n" + long + "\n\n--- Page 3 ---\nAppendix",
			[]string{"Pages 1-2", "Page 3"}},
		{"long pages stand alone", "report.pdf",
			"--- Page 1 ---\n" + long + "\n--- Page 2 ---\n" + long,
			[]string{"Page 1", "Page 2"}},
		{"slides titled, empty skipped", "deck.pptx",
			"--- Slide 1 ---\nWhere the time went\nbody\n--- Slide 2 ---\n\n--- Slide 3 ---\nAsks\nmore",
			[]string{"Slide 1: Where the time went", "Slide 3: Asks"}},
		{"paragraphs grouped", "notes.txt",
			"Monday standup\n\n" + strings.Repeat("x", 3500) + "\n\nTuesday review\n" + strings.Repeat("y", 3000),
			[]string{"Part 1: Monday standup", "Part 2: Tuesday review"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secs := Sectionize(tt.path, tt.text)
			var got []string
			for _, s := range secs {
				got = append(got, s.Heading)
			}
			if strings.Join(got, "|") != strings.Join(tt.headings, "|") {
				t.Errorf("headings = %q, want %q", got, tt.headings)
			}
		})
	}
}

func TestSectionizeBounds(t *testing.T) {
	secs := Sectionize("big.txt", strings.Repeat("z", 9000))
	if len(secs) != 1 || len(secs[0].Text) != maxSectionTextLen {
		t.Fatalf("got %d sections, first %d long; want 1 of %d", len(secs), len(secs[0].Text), maxSectionTextLen)
	}
	var b strings.Builder
	for i := 1; i <= maxSections+20; i++ {
		fmt.Fprintf(&b, "--- Slide %d ---\nSlide title %d\n", i, i)
	}
	if n := len(Sectionize("deck.pptx", b.String())); n != maxSections {
		t.Errorf("slides = %d, want the cap %d", n, maxSections)
	}
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"the ﬁrst ﬂow":             "the first flow",
		"imple-\nmentation detail": "implementation detail",
		"Mark-\nNotes":             "Mark-\nNotes", // a capital starts a new word
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGenericParserSections(t *testing.T) {
	p := NewGenericParser(nil, nil, nil, 0)
	content := "Incident review\n\nThe prefill stalled for 21 minutes.\n\nFollow-ups: add a timeout."
	result, err := p.ParseFile("notes/review.txt", []byte(content))
	if err != nil {
		t.Fatal(err)
	}
	root := result.Nodes[0]
	if root.Language != LangDocument || root.Properties["sections"] != "1" {
		t.Fatalf("root language %q, sections %q; want %q, 1", root.Language, root.Properties["sections"], LangDocument)
	}
	var sec *graph.Node
	for _, n := range result.Nodes {
		if n.Properties["kind"] == "section" {
			sec = n
		}
	}
	if sec == nil {
		t.Fatal("no section node")
	}
	if sec.Properties["heading_path"] != "review > Part 1: Incident review" || sec.Properties["ordinal"] != "1" ||
		sec.Properties["section_hash"] == "" || sec.Properties["text_only"] != "true" {
		t.Errorf("section properties = %v", sec.Properties)
	}
	if !strings.Contains(sec.DocComment, "stalled for 21 minutes") {
		t.Errorf("section text = %q", sec.DocComment)
	}
	contains := false
	for _, e := range result.Edges {
		if e.Type == graph.EdgeContains && e.SourceID == root.ID && e.TargetID == sec.ID {
			contains = true
		}
	}
	if !contains {
		t.Error("no Contains edge from the document to its section")
	}

	data, err := p.ParseFile("exports/rows.csv", []byte("a,b\n1,2\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range data.Nodes {
		if n.Properties["kind"] == "section" || n.Language == LangDocument {
			t.Errorf("a data file got sections: %+v", n)
		}
	}
}

func TestGenericParserDeckSlides(t *testing.T) {
	p := NewGenericParser(nil, nil, nil, 0)
	result, err := p.ParseFile("decks/intro.pptx", createTestPPTX())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, n := range result.Nodes {
		if n.Properties["kind"] == "section" {
			got = append(got, n.Properties["position"]+" "+n.Name)
		}
	}
	want := []string{"1 Slide 1: Welcome to CodeEagle", "2 Slide 2: Knowledge Graph Features"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("slides = %q, want %q", got, want)
	}
}
