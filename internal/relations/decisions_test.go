package relations

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDocDecisionsReadsDatedLines(t *testing.T) {
	subj := &Subject{Path: "docs/plan.md", Sections: []*Section{
		{Heading: "Decision log", HeadingPath: "Plan > Decision log", Hash: "h",
			Text: "- 2026-07-21: no code step at a workflow's entry point\n- 2026-08-28: code step allowed at entry again\nnot dated"},
		{Heading: "Design", HeadingPath: "Plan > Design", Text: "- 2026-01-01: dated but not a decision section"},
	}}
	entries, evidence := DocDecisions(New(subj.Path), subj)
	if len(entries) != 2 || entries[0].Date != "2026-07-21" || entries[0].DateBasis != DateStated || len(evidence) != 2 {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestMeetingDecisionsFollowTheJudgedStretch(t *testing.T) {
	_, meetings := corpus()
	m := meetings["s-plat"]
	m.Decisions = []*Decided{
		{Text: "Raise the retry cap to five", Quote: "make the retry cap five", Topic: "Retry cap"},
		{Text: "Lunch on Friday", Quote: "Friday works", Topic: "Team lunch"},
	}
	rel := New("docs/retry-budget.md")
	rel.Links["s-plat"] = &Verdict{SessionID: "s-plat", Accepted: true, Ranges: []Range{{Start: 10, End: 80}}}
	entries, _ := MeetingDecisions(rel, meetings)
	if len(entries) != 1 || entries[0].Text != "Raise the retry cap to five" || entries[0].Date != "2026-10-14" {
		t.Errorf("entries = %+v", entries)
	}
}

func TestWriterDecisionsAreCheckedAndDated(t *testing.T) {
	draftWith := func(t *testing.T) string {
		return draft(t, map[string]any{
			"headline": map[string]any{"text": "cap is five", "support": []map[string]any{{"label": "M1", "quote": "make the retry cap five"}}},
			"current":  []any{}, "events": []any{}, "unresolved": []any{}, "conflicts": []any{}, "timeline": []any{}, "document": []any{},
			"decisions": []map[string]any{
				{"date": "", "text": "raise the cap to five", "support": []map[string]any{{"label": "M1", "quote": "make the retry cap five"}}},
			},
		})
	}
	for _, tt := range []struct {
		name   string
		p      float64
		asker  bool
		status string
	}{{"settled", 0.9, true, StatusDecided}, {"only proposed", 0.3, true, StatusProposed}, {"no decision model", 0, false, StatusProposed}} {
		rel, subj, meetings := briefFixture()
		var w *BriefWriter
		if tt.asker {
			w = newWriter(&stubLLM{reply: draftWith(t)}, conflictAsker{p: tt.p}, day)
		} else {
			w = newWriter(&stubLLM{reply: draftWith(t)}, nil, day)
		}
		if _, err := w.Update(context.Background(), rel, subj, meetings); err != nil {
			t.Fatal(err)
		}
		var got *DecisionEntry
		for i := range rel.Decisions {
			if rel.Decisions[i].Source == "writer" {
				got = &rel.Decisions[i]
			}
		}
		if got == nil || got.Status != tt.status || got.Date != "2026-10-14" || got.DateBasis != DateMeeting {
			t.Errorf("%s: %+v", tt.name, got)
		}
	}
}

func TestMergeDecisionsKeepsHistory(t *testing.T) {
	t0 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	old := []DecisionEntry{{ID: "a", Date: "2026-07-21", Text: "old wording", FirstRecorded: t0}}
	merged := MergeDecisions(old, []DecisionEntry{{ID: "a", Date: "2026-07-21", Text: "new wording"}, {ID: "b", Date: "2026-08-28"}}, day)
	if len(merged) != 2 || merged[0].ID != "b" || merged[1].Text != "new wording" || !merged[1].FirstRecorded.Equal(t0) {
		t.Errorf("merged = %+v", merged)
	}
}

func TestRenderShowsDecisionsAndWhatChangedSince(t *testing.T) {
	rel, meetings := renderFixture()
	rel.Decisions = []DecisionEntry{
		{ID: "b", Date: "2026-08-28", Text: "code step allowed at entry again", Status: StatusDecided, Source: "doc", Cites: []string{"aaa111"}},
		{ID: "a", Date: "2026-07-21", Text: "no code step at entry", Status: StatusDecided, Source: "doc", Cites: []string{"bbb222"}},
	}
	out := Render(rel, RenderOptions{Budget: MaxBriefBudget, Meetings: meetings})
	if !strings.Contains(out, "decisions, newest first:\n- 2026-08-28 code step allowed at entry again (doc) [aaa111]") {
		t.Errorf("decisions not shown:\n%s", out)
	}
	since, _ := time.Parse("2006-01-02", "2026-08-01")
	out = Render(rel, RenderOptions{Budget: MaxBriefBudget, Meetings: meetings, Since: since, SinceLabel: "2026-08-01"})
	delta := out[strings.Index(out, "changes since"):]
	if !strings.Contains(delta, "code step allowed at entry again") {
		t.Errorf("a decision after the date is not among the changes:\n%s", out)
	}
}
