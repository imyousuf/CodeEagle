package relations

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/pkg/jev"
)

func TestDecideDirection(t *testing.T) {
	g := DefaultDocGates()
	old := &Subject{Path: "a.md", Preamble: "Status: draft, 2026-06-02"}
	newer := &Subject{Path: "b.md", Preamble: "Status: draft, 2026-09-14"}
	undated := &Subject{Path: "b.md"}
	for _, tt := range []struct {
		name string
		b    *Subject
		v    DocVerdict
		want []string // kind:from>to
	}{
		{"unrelated", newer, DocVerdict{PRelated: 0.5, PChanges: [2]float64{0, 0.99}}, nil},
		{"b changes a", newer, DocVerdict{PRelated: 0.9, PChanges: [2]float64{0.1, 0.9}}, []string{"changes:b.md>a.md"}},
		{"b replaces a", newer, DocVerdict{PRelated: 0.9, PChanges: [2]float64{0.1, 0.9}, PReplaces: [2]float64{0.1, 0.97}},
			[]string{"changes:b.md>a.md", "replaces:b.md>a.md"}},
		{"replaces needs changes", newer, DocVerdict{PRelated: 0.9, PReplaces: [2]float64{0.1, 0.97}}, []string{"companion:a.md>b.md"}},
		{"both ways, later wins", newer, DocVerdict{PRelated: 0.9, PChanges: [2]float64{0.9, 0.9}}, []string{"changes:b.md>a.md"}},
		{"both ways, no dates, neither", undated, DocVerdict{PRelated: 0.9, PChanges: [2]float64{0.9, 0.9}}, []string{"companion:a.md>b.md"}},
		{"records", newer, DocVerdict{PRelated: 0.9, PRecords: [2]float64{0.1, 0.9}}, []string{"records:b.md>a.md"}},
		{"records both ways, undated, neither", undated, DocVerdict{PRelated: 0.9, PRecords: [2]float64{0.9, 0.9}}, []string{"companion:a.md>b.md"}},
	} {
		v := tt.v
		v.Decide(old, tt.b, g)
		var got []string
		for _, l := range v.Links {
			got = append(got, l.Kind+":"+l.From+">"+l.To)
		}
		if strings.Join(got, ",") != strings.Join(tt.want, ",") {
			t.Errorf("%s: %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestStemAndOrderedWords(t *testing.T) {
	for _, tt := range []struct{ a, b string }{
		{"specs/home-slice/opal-7194-plan.md", "specs/home-slice/opal-7194-plan.v1-with-schema.md"},
		{"x/Dubai Airports – Mark Architecture Workshop.pptx", "x/Dubai Airports – Mark Architecture Workshop (with AO).pptx"},
	} {
		if stem(tt.a) != stem(tt.b) {
			t.Errorf("%q and %q stem to %q and %q", tt.a, tt.b, stem(tt.a), stem(tt.b))
		}
	}
	seq := strings.Fields("the tracker formerly stickyboard frontend federation design")
	if !wordsInOrder([]string{"stickyboard", "frontend"}, seq) || wordsInOrder([]string{"frontend", "stickyboard"}, seq) {
		t.Error("ordered words misjudged")
	}
}

// docAsker answers every document question from a table by name.
type docAsker struct{ p map[string]float64 }

func (a docAsker) Ask(_ context.Context, state any, q jev.Questions) (*jev.Response, error) {
	b, _ := json.Marshal(state)
	_ = b
	answers := jev.Answers{}
	for name := range q {
		answers[name] = jev.Answer{Type: jev.TypeNoul, Noul: a.p[name]}
	}
	return &jev.Response{Model: "jev-test", Answers: answers, Usage: jev.Usage{InputTokens: 300}}, nil
}

// TestJudgeDocPairsStoresAndLinks covers judging a pair end to end: the
// verdict on the lower path's relation, the edge, and what the briefs say.
func TestJudgeDocPairsStoresAndLinks(t *testing.T) {
	store, base, _ := incrementalStores(t)
	ctx := context.Background()
	indexMarkdown(t, base, "docs/tracker.md", "# Tracker\n\nStatus: draft, 2026-10-01\n\nThe redesign of `retry.md`. "+longBody, nil)
	subjects, err := LoadSubjects(ctx, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	rels := map[string]*Relation{}
	if _, err := EnsureRelations(ctx, store, subjects, rels, day); err != nil {
		t.Fatal(err)
	}
	if _, err := RefreshReferences(ctx, store, subjects, rels); err != nil {
		t.Fatal(err)
	}
	p := NewDocProposer(subjects, rels, nil, DefaultDocOptions())
	cands := p.ForDocuments("docs/tracker.md")
	if len(cands) != 1 || !contains(cands[0].Signals, DocSignalRef) {
		t.Fatalf("candidates = %+v", cands)
	}
	// A is docs/retry.md, the lower path; B is the tracker, which replaces it.
	asker := docAsker{p: map[string]float64{"related": 0.95, "changes_ba": 0.95, "replaces_ba": 0.97}}
	stats, err := JudgeDocPairs(ctx, store, NewDocJudge(asker), cands, rels, DefaultDocGates(), day)
	if err != nil || stats.Accepted[DocReplaces] != 1 {
		t.Fatalf("stats %+v, %v", stats, err)
	}
	edges, _ := store.GetEdges(ctx, RelationID("docs/tracker.md"), graph.EdgeSupersedes)
	if len(edges) != 1 || edges[0].TargetID != RelationID("docs/retry.md") {
		t.Errorf("supersedes edges = %+v", edges)
	}
	if JudgedDoc(rels)(cands[0]) == false {
		t.Error("a judged pair would be asked again")
	}
	lib, err := LoadLibrary(ctx, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	flags := lib.DocFlags("docs/retry.md")
	if len(flags) != 1 || !strings.HasPrefix(flags[0], FlagSupersededBy+":") {
		t.Errorf("flags on the replaced document = %v", flags)
	}
	if lines := lib.relatedLines("docs/retry.md"); len(lines) == 0 || !strings.Contains(lines[0], "replaced by docs/tracker.md") {
		t.Errorf("related lines = %v", lines)
	}
}
