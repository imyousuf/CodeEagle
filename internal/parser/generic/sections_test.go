package generic

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

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
		{"hash headings in text", "notes.txt", "# Context\nThe queue stalls.\n\n# Decision\nTime out at 30s.\n",
			[]string{"Context", "Decision"}},
		{"asciidoc", "design.adoc", "= Title\n\nintro\n\n== Goals\nfast\n\n== Risks\nslow\n",
			[]string{"Title", "Goals", "Risks"}},
		{"org", "journal.org", "* Monday\nstandup notes\n* Tuesday\nreview notes\n",
			[]string{"Monday", "Tuesday"}},
		{"rst underlines", "guide.rst", "Install\n=======\n\nrun make\n\nConfigure\n---------\n\nedit the file\n",
			[]string{"Install", "Configure"}},
		{"one heading is not structure", "notes.txt", "# Notes\n" + strings.Repeat("plan ", 10),
			[]string{"Part 1: # Notes"}},
		{"paragraphs grouped", "notes.txt",
			"Monday standup\n\n" + strings.Repeat("plan ", 700) + "\n\nTuesday review\n" + strings.Repeat("ship ", 600),
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
	secs := Sectionize("big.txt", strings.Repeat("text ", 1800))
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

func TestGenericParserChatLog(t *testing.T) {
	p := NewGenericParser(nil, nil, nil, 0)
	content := `{"role":"user","content":"How should the prefill queue time out?"}
{"role":"assistant","content":"Per request, at 30 seconds."}
{"role":"user","content":"And alerting?"}
{"role":"assistant","content":"Alert on queue age above a minute."}`
	result, err := p.ParseFile("Downloads/chat_history_20250427164006.jsonl", []byte(content))
	if err != nil {
		t.Fatal(err)
	}
	root := result.Nodes[0]
	if root.Properties[PropChatFormat] != "role-jsonl" || root.Language != LangDocument {
		t.Fatalf("root = %+v", root)
	}
	var got []string
	for _, n := range result.Nodes {
		if n.Properties["kind"] == "section" {
			got = append(got, n.Properties["heading_path"])
		}
	}
	if strings.Join(got, "|") != "chat_history_20250427164006 > 2025-04-27" {
		t.Errorf("sections = %q", got)
	}

	// JSON that is not a chat stays data.
	data, err := p.ParseFile("config/app.json", []byte(`{"name":"app","ts":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if data.Nodes[0].Language == LangDocument {
		t.Error("a config file was sectioned")
	}
}

func TestGarbled(t *testing.T) {
	for _, tt := range []struct {
		name string
		text string
		want bool
	}{
		{"english", "Claude Code consumes the fine-tuned model via a custom endpoint while the classifier routes each request.", false},
		{"french", "La passerelle expose l'API des messages, et le classifieur oriente chaque requête vers le modèle approprié.", false},
		{"chinese", "网关公开消息接口，分类器将每个请求路由到合适的模型。网关公开消息接口，分类器将每个请求路由到合适的模型。", false},
		{"type 3 font", "\x14û®:ÆË\x01\x14\x11ÆË\x01TË**®ÙË*\x01\x02p8\x01L®TË&\x011\x14û\x11:Æ\x01u:\b2p:&!\x11*Ë\x03\x01®", true},
		{"accented capitals", "û®:ÆËÆËTË**®ÙË*p8L®TË&1û:Æu:2p:&!*Ë®û®:ÆËÆËÀ!®4å¾ûËÙ®4ËM®TËR!*åÙ4áË4á", true},
		{"no word breaks", strings.Repeat("abcdefghijklmnopqrstuvwxyzabcdefghij", 4), true},
		{"short", "ÆËÆË", false},
	} {
		if got := garbled(tt.text); got != tt.want {
			t.Errorf("%s: garbled = %v, want %v", tt.name, got, tt.want)
		}
	}
	if secs := Sectionize("x.pdf", "--- Page 1 ---\n"+strings.Repeat("û®:ÆËÆËTË**®ÙË*p8L®TË&", 10)); len(secs) != 0 {
		t.Errorf("a garbled page became %d sections", len(secs))
	}
}

func TestSectionCapsKeepCharactersWhole(t *testing.T) {
	text := strings.Repeat("停滞的队列 ", 1500) // multi-byte, well past the cap
	for _, sec := range Sectionize("notes.txt", text) {
		if !utf8.ValidString(sec.Text) || !utf8.ValidString(sec.Heading) {
			t.Errorf("section %q is not valid UTF-8", sec.Heading)
		}
	}
}
