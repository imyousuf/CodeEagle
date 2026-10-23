package relations

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func renderFixture() (*Relation, map[string]*Meeting) {
	_, meetings := corpus()
	rel := New("docs/retry-budget.md")
	rel.Links["s-plat"] = &Verdict{SessionID: "s-plat", Accepted: true}
	rel.Revision = 3
	rel.Stamp(day.AddDate(0, 0, -7))
	b := &Brief{Headline: Line{Text: "Retry cap raised to five", Cites: []string{"aaa111"}}}
	for i := range 6 {
		b.Current = append(b.Current, Line{Text: fmt.Sprintf("current line %d %s", i, strings.Repeat("x", 60)), Cites: []string{"bbb222"}})
		b.Unresolved = append(b.Unresolved, Open{Text: fmt.Sprintf("open item %d %s", i, strings.Repeat("y", 60)), Raised: "2026-10-01", Cites: []string{"ccc333"}})
	}
	b.Events = []Event{
		{Date: "2026-10-14", Text: "cap raised from three to five", Cites: []string{"ddd444"}},
		{Date: "2026-09-01", Text: "budget introduced", Cites: []string{"eee555"}},
	}
	b.Conflicts = []Conflict{{DocSays: "three retries", DiscussionSays: "five retries", Date: "2026-10-14", DocCite: "fff666", SaidCite: "ddd444"}}
	rel.Brief = b
	return rel, meetings
}

// TestRenderTrimsInPriorityOrder covers the budget: lines are dropped in a
// fixed order and the header, headline and freshness always stay.
func TestRenderTrimsInPriorityOrder(t *testing.T) {
	rel, meetings := renderFixture()
	full := Render(rel, RenderOptions{Budget: MaxBriefBudget, Meetings: meetings, Flags: []string{FlagQuoteGone}})
	tight := Render(rel, RenderOptions{Budget: 120, Meetings: meetings, Flags: []string{FlagQuoteGone}})

	if EstimateTokens(tight) >= EstimateTokens(full) {
		t.Fatalf("a tight budget did not shorten the brief (%d vs %d tokens)", EstimateTokens(tight), EstimateTokens(full))
	}
	for _, must := range []string{"docs/retry-budget.md", "Retry cap raised to five", "fresh: quote_gone", "@r3"} {
		if !strings.Contains(tight, must) {
			t.Errorf("a tight budget dropped %q:\n%s", must, tight)
		}
	}
	if strings.Contains(tight, "changes:") {
		t.Error("the delta survived while other parts were trimmed; it goes first")
	}
	if n := strings.Count(tight, "open item"); n > 2 {
		t.Errorf("%d open items survived a tight budget, want at most 2", n)
	}
	if !strings.Contains(full, "conflicts:") || strings.Count(full, "current line") != 6 {
		t.Errorf("a generous budget lost lines:\n%s", full)
	}
}

func TestRenderSinceLeadsWithWhatChanged(t *testing.T) {
	rel, meetings := renderFixture()
	rel.Sections["Limits"] = SectionState{Hash: "h", FirstSeen: day.AddDate(0, 0, -2)}
	since := day.AddDate(0, 0, -5)
	out := Render(rel, RenderOptions{Budget: MaxBriefBudget, Since: since, SinceLabel: "@r3", Meetings: meetings})
	lines := strings.Split(out, "\n")
	if !strings.HasPrefix(lines[2], "changes since @r3:") {
		t.Errorf("the delta does not follow the headline:\n%s", out)
	}
	if !strings.Contains(out, "cap raised from three to five") || strings.Contains(out, "budget introduced") {
		t.Errorf("the delta is not limited to what came after --since:\n%s", out)
	}
	if !strings.Contains(out, `section "Limits" changed`) {
		t.Errorf("a document section changed after --since is not reported:\n%s", out)
	}
}

func TestParseSince(t *testing.T) {
	rel, _ := renderFixture()
	now := day
	for _, tt := range []struct {
		in   string
		want time.Time
	}{
		{"2026-10-01", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
		{"2026-10-01T12:00:00Z", time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)},
		{"7d", now.AddDate(0, 0, -7)},
		{"36h", now.Add(-36 * time.Hour)},
		{"@r3", day.AddDate(0, 0, -7)},
	} {
		got, err := ParseSince(tt.in, rel, now)
		if err != nil || !got.Equal(tt.want) {
			t.Errorf("ParseSince(%q) = %v, %v; want %v", tt.in, got, err, tt.want)
		}
	}
	for _, bad := range []string{"yesterday", "@rX", "@r99"} {
		if _, err := ParseSince(bad, rel, now); err == nil {
			t.Errorf("ParseSince(%q) did not fail", bad)
		}
	}
}

func TestRevisionHistoryIsBounded(t *testing.T) {
	r := New("x.md")
	for i := 1; i <= maxRevisionStamps+5; i++ {
		r.Revision = i
		r.Stamp(day.Add(time.Duration(i) * time.Hour))
	}
	if len(r.Revisions) != maxRevisionStamps {
		t.Errorf("kept %d stamps, want %d", len(r.Revisions), maxRevisionStamps)
	}
	if _, ok := r.RevisionTime(1); ok {
		t.Error("the oldest revision was kept past the bound")
	}
	if _, ok := r.RevisionTime(maxRevisionStamps + 5); !ok {
		t.Error("the newest revision was not kept")
	}
}

// TestRenderSaysWhatTheGraphKnows covers the parts of a brief that come from
// the graph: the document's opening, the confirmed meetings, and the closest
// unconfirmed ones, labelled as such.
func TestRenderSaysWhatTheGraphKnows(t *testing.T) {
	rel, meetings := renderFixture()
	rel.Links["s-fin"] = &Verdict{SessionID: "s-fin", P: 0.72}
	subj := &Subject{Path: rel.SubjectPath, Preamble: "**Status:** Draft, 2026-10-04.\n\nRetry budgets for clients."}
	out := Render(rel, RenderOptions{Budget: MaxBriefBudget, Meetings: meetings, Subject: subj})
	for _, want := range []string{
		"document: **Status:** Draft, 2026-10-04. Retry budgets for clients.",
		"meetings that discussed it, newest first:\n- 2026-10-14 Platform sync",
		"possibly also, below the gate and not confirmed:\n- 2026-08-14 Finance review",
		"p=0.72",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("brief lacks %q:\n%s", want, out)
		}
	}

	none := New("docs/quiet.md")
	if out := Render(none, RenderOptions{Meetings: meetings}); !strings.Contains(out, "that does not mean none did") {
		t.Errorf("a document with no confirmed meeting reads as never discussed:\n%s", out)
	}
}
