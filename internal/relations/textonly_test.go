package relations

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/parser/generic"
)

func TestTextOnlyDocumentIsASubject(t *testing.T) {
	store, err := embedded.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	content := "Prefill stall review\n\n" + strings.Repeat("The prefill queue stalled behind one long request. ", 70) +
		"\n\nFollow-ups\n" + strings.Repeat("Add a per-request timeout and alert on queue age. ", 70)
	res, err := generic.NewGenericParser(nil, nil, nil, 0).ParseFile("notes/prefill-stall-review.txt", []byte(content))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range res.Nodes {
		if n.Type != graph.NodeDocument {
			continue
		}
		if n.Properties["kind"] != "section" {
			n.DocComment = "A model's two-sentence summary."
		}
		if err := store.AddNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}

	subjects, err := LoadSubjects(ctx, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(subjects) != 1 {
		t.Fatalf("subjects = %d, want the text file", len(subjects))
	}
	s := subjects[0]
	if !s.TextOnly || s.Title != "prefill stall review" {
		t.Errorf("text-only %v, title %q", s.TextOnly, s.Title)
	}
	if len(s.Sections) != 2 || !strings.HasPrefix(s.Sections[0].Heading, "Part 1: Prefill stall review") ||
		!strings.HasPrefix(s.Sections[1].Heading, "Part 2: Follow-ups") {
		t.Fatalf("sections = %+v", s.Sections)
	}
	if strings.Contains(s.Preamble, "summary") || !strings.Contains(s.Preamble, "prefill queue stalled") {
		t.Errorf("preamble = %q, want the document's opening words, not the summary", s.Preamble)
	}

	l := &Library{}
	outline := l.Outline(s)
	if strings.Contains(outline, "lines") || !strings.Contains(outline, "text as indexed") {
		t.Errorf("outline = %q", outline)
	}
	shown := l.ShowSection(s.Path, s.Sections[1], 0)
	if strings.Contains(shown, "(lines") || !strings.Contains(shown, "per-request timeout") {
		t.Errorf("show = %q", shown)
	}

	if asset, basis := ClassifyAsset(&Subject{Path: "decks/where-the-21-minutes-went.pptx"}); asset != AssetDeck || basis != "format" {
		t.Errorf("deck classified as %s (%s)", asset, basis)
	}
}

func TestWrapLinesKeepsRunesWhole(t *testing.T) {
	for _, width := range []int{1, 2, 4, 7} {
		for _, ln := range wrapLines([]string{"你好世界 prefill 停滞"}, width) {
			if !utf8.ValidString(ln) {
				t.Errorf("width %d: %q is not valid UTF-8", width, ln)
			}
		}
	}
}

func TestRefSentenceKeepsWhatItSays(t *testing.T) {
	for in, want := range map[string]string{
		"- `docs/tech-spec/agent-framework/stickyboard.md` — the original Stickyboard spec. Tracker keeps its backend.": "the original Stickyboard spec. Tracker keeps its backend.",
		"> Executes `docs/x.md`. Branch: `t`.":                           "Executes `docs/x.md`. Branch: `t`.",
		"See [the retry policy](retry.md): caps at three.":               "See [the retry policy](retry.md): caps at three.",
		"- [Stickyboard](../stickyboard.md) — superseded for UI by this": "superseded for UI by this",
	} {
		if got := refSentence(in); got != want {
			t.Errorf("refSentence(%q) = %q, want %q", in, got, want)
		}
	}
}
