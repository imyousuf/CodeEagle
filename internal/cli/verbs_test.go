package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/relations"
)

// TestVerbToolsAreSmallAndComplete covers the MCP surface: three tools,
// each with a required argument, and descriptions short enough that adding
// them costs an agent little on every turn.
func TestVerbToolsAreSmallAndComplete(t *testing.T) {
	tools := verbTools(newVerbs(nil))
	want := map[string]string{"find": "query", "brief": "target", "show": "id"}
	if len(tools) != len(want) {
		t.Fatalf("%d tools, want %d", len(tools), len(want))
	}
	total := 0
	for _, tool := range tools {
		req, ok := want[tool.Name()]
		if !ok {
			t.Errorf("unexpected tool %q", tool.Name())
			continue
		}
		params := tool.Parameters()
		required, _ := params["required"].([]string)
		if len(required) != 1 || required[0] != req {
			t.Errorf("%s requires %v, want [%s]", tool.Name(), required, req)
		}
		total += len(tool.Description())
	}
	if total > 600 {
		t.Errorf("tool descriptions total %d characters; every request pays for them", total)
	}
}

func TestVerbArgumentsAreReadLeniently(t *testing.T) {
	args := map[string]any{"query": "retry", "budget": float64(250), "from": 3}
	if str(args, "query") != "retry" || str(args, "missing") != "" {
		t.Error("string arguments")
	}
	if num(args, "budget") != 250 || num(args, "from") != 3 || num(args, "missing") != 0 {
		t.Error("numeric arguments, which JSON delivers as float64")
	}
}

// TestVerbsHoldNoLockBetweenCalls covers the verbs under MCP while the worker
// syncs: between calls the graph is free for a writer, and while a writer
// holds it a brief is still answered from what was last read.
func TestVerbsHoldNoLockBetweenCalls(t *testing.T) {
	cfg := &config.Config{Graph: config.GraphConfig{DBPath: t.TempDir()}}
	ctx := context.Background()
	w, _, err := openBranchStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rel := relations.New("docs/retry.md")
	rel.Brief = &relations.Brief{Headline: relations.Line{Text: "retry cap raised to five"}}
	node, err := rel.Node()
	if err != nil {
		t.Fatal(err)
	}
	if err := w.View(embedded.RelationScope, []string{embedded.RelationScope}).AddNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	w.Close()

	v := newVerbs(cfg)
	defer v.Close()
	id := relations.ShortID(relations.RelationID("docs/retry.md"))
	out, err := v.Brief(ctx, id, 400, "")
	if err != nil || !strings.Contains(out, "retry cap raised to five") {
		t.Fatalf("Brief = %q, %v", out, err)
	}

	w, _, err = openBranchStore(cfg)
	if err != nil {
		t.Fatalf("a writer could not open the graph after a brief: %v", err)
	}
	defer w.Close()
	if out, err := v.Brief(ctx, id, 400, ""); err != nil || !strings.Contains(out, "retry cap raised to five") {
		t.Errorf("while a writer held the graph, Brief = %q, %v; want the last brief read", out, err)
	}
	if _, err := v.Find(ctx, "retry"); !errors.Is(err, embedded.ErrBusy) {
		t.Errorf("while a writer held the graph, Find err = %v, want ErrBusy", err)
	}
}

// TestBriefJSONCarriesTheEvidence covers brief --json: a program gets each
// line with the evidence it cites, and the quotes.
func TestBriefJSONCarriesTheEvidence(t *testing.T) {
	cfg := &config.Config{Graph: config.GraphConfig{DBPath: t.TempDir()}}
	ctx := context.Background()
	w, _, err := openBranchStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rel := relations.New("docs/retry.md")
	rel.Brief = &relations.Brief{Headline: relations.Line{Text: "retry cap raised to five", Cites: []string{"abc123"}}}
	rel.Evidence = []relations.Evidence{{ID: "abc123", Kind: relations.EvidenceSaid, SessionID: "s1", Quote: "make the cap five"}}
	node, err := rel.Node()
	if err != nil {
		t.Fatal(err)
	}
	if err := w.View(embedded.RelationScope, []string{embedded.RelationScope}).AddNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	w.Close()

	v := newVerbs(cfg)
	defer v.Close()
	out, err := v.BriefJSON(ctx, relations.ShortID(relations.RelationID("docs/retry.md")))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Subject  string
		Brief    relations.Brief
		Evidence []relations.Evidence
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got.Subject != "docs/retry.md" || got.Brief.Headline.Cites[0] != "abc123" ||
		len(got.Evidence) != 1 || got.Evidence[0].Quote != "make the cap five" {
		t.Errorf("brief --json = %s", out)
	}
}

// TestAllBriefsJSONIsOneLinePerBrief covers brief --all --json, the export a
// program reads in one pass.
func TestAllBriefsJSONIsOneLinePerBrief(t *testing.T) {
	cfg := &config.Config{Graph: config.GraphConfig{DBPath: t.TempDir()}}
	ctx := context.Background()
	w, _, err := openBranchStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"docs/a.md", "docs/b.md", "docs/none.md"} {
		rel := relations.New(path)
		if path != "docs/none.md" {
			rel.Brief = &relations.Brief{Headline: relations.Line{Text: "about " + path}}
		}
		node, err := rel.Node()
		if err != nil {
			t.Fatal(err)
		}
		if err := w.View(embedded.RelationScope, []string{embedded.RelationScope}).AddNode(ctx, node); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	v := newVerbs(cfg)
	defer v.Close()
	var out strings.Builder
	if err := v.AllBriefsJSON(ctx, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"subject":"docs/a.md"`) || !strings.Contains(lines[1], `"subject":"docs/b.md"`) {
		t.Errorf("export = %q", out.String())
	}
}
