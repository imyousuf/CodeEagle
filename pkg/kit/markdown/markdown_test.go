package markdown_test

import (
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
	"github.com/imyousuf/CodeEagle/pkg/kit/markdown"
)

const factsDoc = `---
title: Facts
tags:
  - memory
---
- Prefers metric units.

## Standing rules

- Never send email on my behalf.
- [x] Draft replies in chat first.
  Show the draft before anything is sent.
1. Ask before booking travel.
2) Keep invoices in Finance/.

## Facts

* Works with Kevin on Mark.
  - Kevin owns the rollout.
+ Works with Kevin on Mark.

- - -

` + "```" + `md
- not an item, inside a fence
` + "```" + `
`

// items returns the list-item nodes, keyed by their text, and the edges
// into each.
func items(t *testing.T, res *markdown.Result) (map[string]*graph.Node, map[string][]*graph.Edge) {
	t.Helper()
	nodes := make(map[string]*graph.Node)
	byID := make(map[string]*graph.Node)
	for _, n := range res.Nodes {
		if n.Properties["kind"] == "fact" {
			key := n.DocComment
			if _, dup := nodes[key]; dup {
				key += " (2)"
			}
			nodes[key] = n
			byID[n.ID] = n
		}
	}
	in := make(map[string][]*graph.Edge)
	for _, e := range res.Edges {
		if n, ok := byID[e.TargetID]; ok {
			in[n.DocComment] = append(in[n.DocComment], e)
		}
	}
	return nodes, in
}

func parseFacts(t *testing.T, doc string) *markdown.Result {
	t.Helper()
	return markdown.Parse("memories/FACTS.md", []byte(doc),
		markdown.WithListItems(markdown.ListItems{NodeType: "Fact", Kind: "fact"}))
}

func TestListItemsBecomeNodes(t *testing.T) {
	res := parseFacts(t, factsDoc)
	got, in := items(t, res)

	want := map[string]struct {
		heading, depth, position, checked string
		line, end                         int
	}{
		"Prefers metric units.":          {"", "0", "1", "", 6, 6},
		"Never send email on my behalf.": {"Standing rules", "0", "1", "", 10, 10},
		"Draft replies in chat first.\nShow the draft before anything is sent.": {"Standing rules", "0", "2", "true", 11, 12},
		"Ask before booking travel.":    {"Standing rules", "0", "3", "", 13, 13},
		"Keep invoices in Finance/.":    {"Standing rules", "0", "4", "", 14, 14},
		"Works with Kevin on Mark.":     {"Facts", "0", "1", "", 18, 18},
		"Kevin owns the rollout.":       {"Facts", "1", "2", "", 19, 19},
		"Works with Kevin on Mark. (2)": {"Facts", "0", "3", "", 20, 20},
	}
	if len(got) != len(want) {
		var texts []string
		for k := range got {
			texts = append(texts, k)
		}
		t.Fatalf("got %d items %q, want %d", len(got), texts, len(want))
	}
	for text, w := range want {
		n := got[text]
		if n == nil {
			t.Errorf("no item %q", text)
			continue
		}
		if n.Type != "Fact" || n.QualifiedName != w.heading || n.Properties["heading_path"] != w.heading ||
			n.Properties["depth"] != w.depth || n.Properties["position"] != w.position ||
			n.Properties["checked"] != w.checked || n.Line != w.line || n.EndLine != w.end {
			t.Errorf("%q = %+v", text, n)
		}
		if n.Properties["text_hash"] != markdown.TextHash(n.DocComment) {
			t.Errorf("%q: text_hash does not hash its text", text)
		}
		if len(in[strings.TrimSuffix(text, " (2)")]) == 0 {
			t.Errorf("%q has no containing edge", text)
		}
	}
	// A nested item is its own node, not part of its parent's text.
	if strings.Contains(got["Works with Kevin on Mark."].DocComment, "rollout") {
		t.Error("the parent item carries its child's text")
	}
}

func TestListItemsAreContainedByTheirSection(t *testing.T) {
	res := parseFacts(t, factsDoc)
	sections := make(map[string]string) // section ID -> title
	var docID string
	for _, n := range res.Nodes {
		if n.Properties["kind"] == "section" {
			sections[n.ID] = n.Name
		}
		if n.Name == "memories/FACTS.md" {
			docID = n.ID
		}
	}
	_, in := items(t, res)
	for text, edges := range in {
		e := edges[0]
		if e.Type != graph.EdgeContains {
			t.Errorf("%q: edge type %s", text, e.Type)
		}
		if text == "Prefers metric units." {
			if e.SourceID != docID {
				t.Errorf("an item before any heading is contained by %s, want the document", e.SourceID)
			}
			continue
		}
		if _, ok := sections[e.SourceID]; !ok {
			t.Errorf("%q is contained by %s, not a section", text, e.SourceID)
		}
	}
}

func TestListItemIDsSurviveLayoutButNotRewording(t *testing.T) {
	id := func(doc string) string {
		t.Helper()
		for _, n := range parseFacts(t, doc).Nodes {
			if n.Properties["kind"] == "fact" {
				return n.ID
			}
		}
		t.Fatalf("no item in %q", doc)
		return ""
	}
	base := id("## Rules\n\n- Never send email on my behalf.\n")
	for name, doc := range map[string]string{
		"another marker":  "## Rules\n\n* Never send email on my behalf.\n",
		"numbered":        "## Rules\n\n7. Never send email on my behalf.\n",
		"ticked":          "## Rules\n\n- [x] Never send email on my behalf.\n",
		"reflowed":        "## Rules\n\n- Never send email\n  on my behalf.\n",
		"other text near": "## Rules\n\nSome intro.\n\n- Never send email on my behalf.\n- Another rule.\n",
	} {
		if got := id(doc); got != base {
			t.Errorf("%s changed the ID", name)
		}
	}
	if id("## Rules\n\n- Never send email for me.\n") == base {
		t.Error("rewording kept the ID")
	}
	if id("## Other\n\n- Never send email on my behalf.\n") == base {
		t.Error("moving the item under another heading kept the ID")
	}
	want := graph.NewNodeID("Fact", "memories/FACTS.md", "Rules\x00"+markdown.TextHash("Never send email on my behalf."))
	if base != want {
		t.Errorf("ID = %s, want NewNodeID(type, path, heading path + NUL + text hash) = %s", base, want)
	}
}

func TestListItemsAreOffByDefault(t *testing.T) {
	plain := markdown.Parse("memories/FACTS.md", []byte(factsDoc))
	withItems := parseFacts(t, factsDoc)
	got, _ := items(t, withItems)
	if len(withItems.Nodes) != len(plain.Nodes)+len(got) {
		t.Errorf("list items changed other nodes: %d plain, %d with %d items", len(plain.Nodes), len(withItems.Nodes), len(got))
	}
	for i, n := range plain.Nodes {
		if withItems.Nodes[i].ID != n.ID {
			t.Fatalf("node %d differs: list items must only add nodes after the rest", i)
		}
	}
}

func TestNodeOptionsLeaveOutCodeEagleSpecificNodes(t *testing.T) {
	doc := "# Notes\n\nSee [the plan](plan.md).\n\nTODO: write it\n\n```go\nx := 1\n```\n"
	kinds := func(res *markdown.Result) map[string]int {
		k := make(map[string]int)
		for _, n := range res.Nodes {
			k[n.Properties["kind"]]++
		}
		return k
	}
	full := kinds(markdown.Parse("AGENTS.md", []byte(doc)))
	if full["cross-reference"] != 1 || full["todo"] != 1 || full["code-block"] != 1 {
		t.Fatalf("defaults = %v", full)
	}
	lean := markdown.Parse("AGENTS.md", []byte(doc),
		markdown.WithoutLinks(), markdown.WithoutTODOs(), markdown.WithoutCodeBlocks(), markdown.WithoutAIGuidelineType())
	if k := kinds(lean); k["cross-reference"] != 0 || k["todo"] != 0 || k["code-block"] != 0 || k["section"] != 1 {
		t.Errorf("with the options = %v", k)
	}
	if lean.Nodes[0].Type != graph.NodeDocument {
		t.Errorf("AGENTS.md typed %s, want a plain document", lean.Nodes[0].Type)
	}
	if markdown.Parse("AGENTS.md", []byte(doc)).Nodes[0].Type != graph.NodeAIGuideline {
		t.Error("AGENTS.md is no longer an AI guideline by default")
	}
}
