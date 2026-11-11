package extract

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
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
