package relations

import (
	"context"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

func TestLocateQuoteFindsTheFewestTurns(t *testing.T) {
	turns := []Turn{
		{Speaker: "Person 1", Text: "Can you hear me?", Start: 9, End: 11},
		{Speaker: "Person 2", Text: "Loud and clear. So about the trackers,", Start: 11, End: 20},
		{Speaker: "Person 1", Text: "VTs should not see manager boards by default.", Start: 20, End: 30},
		{Speaker: "Person 2", Text: "Only through data access settings.", Start: 30, End: 36},
	}
	for _, tt := range []struct {
		name        string
		quote       string
		first, last int
		ok          bool
	}{
		{"inside one turn", "should not see manager boards", 2, 2, true},
		{"across two turns", "by default. Only through data access", 2, 3, true},
		{"with the labels a rendered transcript adds", "about the trackers, 00:20 Person 1: VTs should not", 1, 2, true},
		{"not said", "VTs can read every board", 0, 0, false},
	} {
		first, last, ok := LocateQuote(turns, tt.quote)
		if ok != tt.ok || first != tt.first || last != tt.last {
			t.Errorf("%s: got %d-%d %v, want %d-%d %v", tt.name, first, last, ok, tt.first, tt.last, tt.ok)
		}
	}
}

// TestConflictIsShownInItsQuotes covers the swap a paraphrase can make: the
// rendered conflict uses each side's verified quote where one is recorded.
func TestConflictIsShownInItsQuotes(t *testing.T) {
	rel, meetings := renderFixture()
	rel.Evidence = []Evidence{
		{ID: "fff666", Kind: EvidenceDoc, Quote: "Clients retry at most three times."},
		{ID: "ddd444", Kind: EvidenceSaid, Quote: "let's make the retry cap five"},
	}
	out := Render(rel, RenderOptions{Budget: MaxBriefBudget, Meetings: meetings})
	want := `- document: "Clients retry at most three times." / 2026-10-14 meeting: "let's make the retry cap five"`
	if !strings.Contains(out, want) {
		t.Errorf("conflict not shown in its quotes:\n%s", out)
	}
}

// TestConflictNeedsAMeetingOnItsSaidSide covers a draft citing the document
// for both sides of a conflict.
func TestConflictNeedsAMeetingOnItsSaidSide(t *testing.T) {
	rel, subj, meetings := briefFixture()
	reply := draft(t, map[string]any{
		"headline": map[string]any{"text": "cap is five now",
			"support": []map[string]any{{"label": "M1", "quote": "make the retry cap five"}}},
		"current": []any{}, "events": []any{}, "unresolved": []any{},
		"conflicts": []map[string]any{{
			"doc_says": "three retries", "discussion_says": "full jitter", "date": "2026-10-14",
			"doc":  map[string]any{"label": "D1", "quote": "at most three times"},
			"said": map[string]any{"label": "D1", "quote": "at most three times"},
		}},
	})
	if _, err := newWriter(&stubLLM{reply: reply}, conflictAsker{p: 0.9}, day).Update(context.Background(), rel, subj, meetings); err != nil {
		t.Fatal(err)
	}
	if len(rel.Brief.Conflicts) != 0 {
		t.Errorf("a conflict quoting the document on both sides was kept: %+v", rel.Brief.Conflicts)
	}
}

// recording is a whole meeting's turns.
type recording []Turn

func (r recording) Span(_ *Meeting, start, end float64) ([]Turn, error) {
	var out []Turn
	for _, t := range r {
		if t.End >= start && t.Start <= end {
			out = append(out, t)
		}
	}
	return out, nil
}

// TestShowFindsAQuoteItsAnchorMissed covers evidence anchored at the start of
// a meeting, as a decision's quote is: show finds the words in the recording.
func TestShowFindsAQuoteItsAnchorMissed(t *testing.T) {
	_, meetings := corpus()
	rec := recording{{Speaker: "Person 1", Text: "Can you hear me?", Start: 9, End: 11}}
	for i := 0; i < 200; i++ {
		rec = append(rec, Turn{Speaker: "Person 2", Text: strings.Repeat("filler words ", 8), Start: float64(20 + i*10), End: float64(29 + i*10)})
	}
	rec = append(rec, Turn{Speaker: "Person 1", Text: "We agreed VTs only get access through a grant.", Start: 3000, End: 3010})
	lib := &Library{Meetings: meetings, Transcripts: rec}
	out, err := lib.ShowEvidence(nil, &Evidence{Kind: EvidenceDecided, SessionID: "s-plat", Quote: "VTs only get access through a grant"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "VTs only get access through a grant") || strings.Contains(out, "Can you hear me") {
		t.Errorf("show did not land on the quote:\n%s", out)
	}
}

// TestOutlineLeadsToEachSection covers reading a document a part at a time:
// its outline lists each section with an ID, and that section is shown from
// the indexed text when the file cannot be read.
func TestOutlineLeadsToEachSection(t *testing.T) {
	subjects, _ := corpus()
	retry := subjects[0]
	retry.Sections[0].Line, retry.Sections[0].EndLine = 3, 9
	retry.Sections[1].Line, retry.Sections[1].EndLine = 10, 14
	lib := &Library{Subjects: map[string]*Subject{retry.Path: retry}}

	out := lib.Outline(retry)
	if !strings.Contains(out, ShortID("sec-jitter")+" § Retry budget design > Jitter (lines 10–14)") || !strings.Contains(out, "lines 1–14") {
		t.Errorf("outline:\n%s", out)
	}
	subj, sec := lib.SectionByID("sec-jitter")
	if sec == nil || subj != retry {
		t.Fatal("section not found by its ID")
	}
	if got := lib.ShowSection(subj.Path, sec, 0); !strings.Contains(got, "Full jitter on every backoff.") {
		t.Errorf("section shown as:\n%s", got)
	}
}

// TestConflictTakesItsMeetingsDate covers the drafting model copying the
// wrong meeting's date onto a conflict: the anchor's meeting decides it.
func TestConflictTakesItsMeetingsDate(t *testing.T) {
	rel, subj, meetings := briefFixture()
	reply := strings.Replace(conflictDraft(t), `"date":"2026-10-14"`, `"date":"2026-01-01"`, 1)
	if !strings.Contains(reply, "2026-01-01") {
		t.Fatal("setup: the draft's date was not replaced")
	}
	if _, err := newWriter(&stubLLM{reply: reply}, conflictAsker{p: 0.9}, day).Update(context.Background(), rel, subj, meetings); err != nil {
		t.Fatal(err)
	}
	if len(rel.Brief.Conflicts) != 1 || rel.Brief.Conflicts[0].Date != "2026-10-14" {
		t.Errorf("conflicts = %+v, want the meeting's date 2026-10-14", rel.Brief.Conflicts)
	}
}

// TestMeetingBriefSaysWhatTheMeetingSettled covers brief on a meeting: its
// decisions with quotes, its topics with IDs show expands, its follow-ups.
func TestMeetingBriefSaysWhatTheMeetingSettled(t *testing.T) {
	store, err := embedded.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	_, meetings := corpus()
	m := meetings["s-plat"]
	m.Summary = "The platform team agreed on retries."
	nodes := []*graph.Node{
		{ID: m.NodeID, Type: graph.NodeMeeting, Name: m.Title},
		{ID: "seg-cap", Type: graph.NodeTopicSegment, Name: "Retry cap",
			Properties: map[string]string{graph.PropStartTime: "10", graph.PropEndTime: "90", graph.PropSummary: "The cap goes to five."}},
		{ID: "dec-1", Type: graph.NodeDecision, Name: "d",
			Properties: map[string]string{graph.PropSummary: "Raise the retry cap to five", graph.PropQuote: "make the retry cap five", "quote_verified": "true"}},
		{ID: "act-1", Type: graph.NodeActionItem, Name: "a",
			Properties: map[string]string{graph.PropSummary: "Update the retry spec", graph.PropAssignee: "Ana"}},
	}
	for _, n := range nodes {
		if err := store.AddNode(ctx, n); err != nil {
			t.Fatal(err)
		}
		if n.ID != m.NodeID {
			if err := store.AddEdge(ctx, &graph.Edge{ID: "c-" + n.ID, Type: graph.EdgeContains, SourceID: m.NodeID, TargetID: n.ID}); err != nil {
				t.Fatal(err)
			}
		}
	}
	lib := &Library{Store: store, Meetings: meetings, Relations: map[string]*Relation{}}
	out := lib.MeetingBrief(ctx, m, 400)
	for _, want := range []string{
		"The platform team agreed on retries.",
		`Raise the retry cap to five — "make the retry cap five"`,
		ShortID("seg-cap") + " 00:10–01:30 Retry cap: The cap goes to five.",
		"Update the retry spec (Ana)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("meeting brief lacks %q:\n%s", want, out)
		}
	}
}

// TestBriefTimelineAndDocumentPoints covers the timeline, a line per meeting,
// and the document's points, which must cite the document itself.
func TestBriefTimelineAndDocumentPoints(t *testing.T) {
	rel, subj, meetings := briefFixture()
	reply := draft(t, map[string]any{
		"headline": map[string]any{"text": "cap is five now",
			"support": []map[string]any{{"label": "M1", "quote": "make the retry cap five"}}},
		"current": []any{}, "events": []any{}, "unresolved": []any{}, "conflicts": []any{},
		"document": []map[string]any{
			{"text": "Clients retry at most three times", "support": []map[string]any{{"label": "D1", "quote": "at most three times"}}},
			{"text": "a point resting on a meeting", "support": []map[string]any{{"label": "M1", "quote": "make the retry cap five"}}},
		},
		"timeline": []map[string]any{
			{"date": "2026-01-01", "text": "Ana proposed a cap of five", "support": []map[string]any{{"label": "M1", "quote": "make the retry cap five"}}},
		},
	})
	if _, err := newWriter(&stubLLM{reply: reply}, nil, day).Update(context.Background(), rel, subj, meetings); err != nil {
		t.Fatal(err)
	}
	b := rel.Brief
	if len(b.Points) != 1 || b.Points[0].Text != "Clients retry at most three times" {
		t.Errorf("points = %+v; want only the one citing the document", b.Points)
	}
	if len(b.Timeline) != 1 || b.Timeline[0].Date != "2026-10-14" {
		t.Errorf("timeline = %+v; want one line dated from its meeting", b.Timeline)
	}
}
