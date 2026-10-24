package markdown

import (
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

const sectionsDoc = `---
title: Retry policy
description: How clients retry failed calls
---

Clients retry idempotent calls only.

# Limits

At most three retries per call.

## Backoff

Exponential, with full jitter.

` + "```md" + `
# Not a heading
` + "```" + `

## Budget

A shared retry budget per client.

# Limits

The second limits section, about payload size.
`

func parseSections(t *testing.T, opts ...Option) map[string][]*graph.Node {
	t.Helper()
	res, err := NewParser(opts...).ParseFile("docs/retry.md", []byte(sectionsDoc))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	byName := make(map[string][]*graph.Node)
	for _, n := range res.Nodes {
		if n.Properties["kind"] == "section" {
			byName[n.Name] = append(byName[n.Name], n)
		}
	}
	return byName
}

// TestDuplicateHeadingsGetDistinctIDs covers two sections with one title,
// which used to share an ID so the second silently replaced the first. The
// first must keep the ID it always had, or every indexed ID would change.
func TestDuplicateHeadingsGetDistinctIDs(t *testing.T) {
	limits := parseSections(t)["Limits"]
	if len(limits) != 2 {
		t.Fatalf("got %d Limits sections, want 2", len(limits))
	}
	if limits[0].ID == limits[1].ID {
		t.Fatal("two sections titled Limits share one ID")
	}
	legacy := graph.NewNodeID(string(graph.NodeDocument), "docs/retry.md", "section:Limits")
	if limits[0].ID != legacy {
		t.Errorf("first Limits ID = %s, want the pre-existing %s", limits[0].ID, legacy)
	}
	if limits[0].Properties["ordinal"] != "1" || limits[1].Properties["ordinal"] != "2" {
		t.Errorf("ordinals = %q, %q; want 1, 2",
			limits[0].Properties["ordinal"], limits[1].Properties["ordinal"])
	}
}

func TestSectionExtentIncludesSubsections(t *testing.T) {
	s := parseSections(t)
	limits := s["Limits"][0]
	budget := s["Budget"][0]
	// The first Limits section runs through Backoff and Budget, up to the
	// second top-level heading.
	if limits.EndLine != budget.EndLine {
		t.Errorf("Limits ends at line %d, its last subsection at %d", limits.EndLine, budget.EndLine)
	}
	if got := s["Backoff"][0].Properties["heading_path"]; got != "Limits > Backoff" {
		t.Errorf("Backoff heading_path = %q", got)
	}
	if got := s["Backoff"][0].QualifiedName; got != "Limits > Backoff" {
		t.Errorf("Backoff QualifiedName = %q", got)
	}
	if got := s["Limits"][1].Properties["heading_path"]; got != "Limits" {
		t.Errorf("second Limits heading_path = %q", got)
	}
}

// TestSectionTextIsItsOwn covers the text a section carries: its own words,
// not its subsections'. A parent holding its children's text would embed the
// document again and change whenever any child was edited.
func TestSectionTextIsItsOwn(t *testing.T) {
	s := parseSections(t)
	limits := s["Limits"][0]
	if !strings.Contains(limits.DocComment, "At most three retries") {
		t.Errorf("Limits text = %q", limits.DocComment)
	}
	if strings.Contains(limits.DocComment, "jitter") {
		t.Error("Limits text includes its subsection's words")
	}
	if !strings.Contains(s["Backoff"][0].DocComment, "full jitter") {
		t.Errorf("Backoff text = %q", s["Backoff"][0].DocComment)
	}
}

func TestHeadingsInsideCodeFencesAreNotSections(t *testing.T) {
	if _, ok := parseSections(t)["Not a heading"]; ok {
		t.Error("a heading inside a code fence became a section")
	}
}

func TestSectionHashIgnoresLayoutButNotWords(t *testing.T) {
	a := TextHash("At most three retries\nper call.")
	if b := TextHash("At most   three retries per call."); a != b {
		t.Error("reflowing a paragraph changed its hash")
	}
	if c := TextHash("At most five retries per call."); a == c {
		t.Error("rewording a paragraph kept its hash")
	}
	if got := parseSections(t)["Backoff"][0].Properties["section_hash"]; !strings.HasPrefix(got, "sha256:") {
		t.Errorf("section_hash = %q", got)
	}
}

func TestDocumentCarriesTitleDescriptionAndPreamble(t *testing.T) {
	res, err := NewParser().ParseFile("docs/retry.md", []byte(sectionsDoc))
	if err != nil {
		t.Fatal(err)
	}
	doc := res.Nodes[0]
	for _, want := range []string{"Retry policy", "How clients retry failed calls", "idempotent calls only"} {
		if !strings.Contains(doc.DocComment, want) {
			t.Errorf("document text %q lacks %q", doc.DocComment, want)
		}
	}
	if strings.Contains(doc.DocComment, "three retries") {
		t.Error("document text runs past the first heading")
	}
}

func TestLongSectionsAreCappedOnARuneBoundary(t *testing.T) {
	long := "# Big\n\n" + strings.Repeat("é", maxSectionText)
	res, err := NewParser().ParseFile("big.md", []byte(long))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range res.Nodes {
		if n.Name != "Big" {
			continue
		}
		if len(n.DocComment) > maxSectionText {
			t.Errorf("stored %d bytes, cap is %d", len(n.DocComment), maxSectionText)
		}
		if !strings.HasSuffix(n.DocComment, "é") {
			t.Error("the cut split a multi-byte character")
		}
		if n.Properties["text_truncated"] != "true" {
			t.Error("a capped section is not marked truncated")
		}
		return
	}
	t.Fatal("no Big section")
}

// TestSectionTextCanBeTurnedOff covers docs.markdown_sections: false, which
// restores heading-only sections. Structure stays; text and its hash go.
func TestSectionTextCanBeTurnedOff(t *testing.T) {
	s := parseSections(t, WithSectionText(false))
	b := s["Backoff"][0]
	if b.DocComment != "" || b.Properties["section_hash"] != "" {
		t.Errorf("section text was stored with it turned off: %q", b.DocComment)
	}
	if b.Properties["heading_path"] == "" || b.EndLine == 0 {
		t.Error("structure was dropped along with the text")
	}
}
