package relations

import (
	"testing"
	"time"
)

// fakeVectors stands in for the vector index.
type fakeVectors map[string][]float32

func (f fakeVectors) Vector(id string) ([]float32, bool) {
	v, ok := f[id]
	return v, ok
}

var day = time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)

// corpus builds a small corpus: three documents, so title words are rare
// enough to count, and two meetings.
func corpus() ([]*Subject, map[string]*Meeting) {
	retry := &Subject{Path: "docs/retry-budget.md", Title: "Retry budget design", Hash: "h-retry", UpdatedAt: day,
		Sections: []*Section{
			{NodeID: "sec-limits", Heading: "Limits", HeadingPath: "Retry budget design > Limits", Hash: "hl",
				Text: "Clients retry at most three times. Tracked in PLAT-412."},
			{NodeID: "sec-jitter", Heading: "Jitter", HeadingPath: "Retry budget design > Jitter", Hash: "hj",
				Text: "Full jitter on every backoff."},
		}}
	billing := &Subject{Path: "docs/billing.md", Title: "Invoice rendering", Hash: "h-bill",
		Sections: []*Section{{NodeID: "sec-bill", Heading: "Templates", HeadingPath: "Invoice rendering > Templates", Hash: "hb",
			Text: "Invoices render from templates."}}}
	onboarding := &Subject{Path: "docs/onboarding.md", Title: "Laptop onboarding", Hash: "h-onb",
		Sections: []*Section{{NodeID: "sec-onb", Heading: "Accounts", HeadingPath: "Laptop onboarding > Accounts", Hash: "ho",
			Text: "New starters get accounts on day one."}}}

	platform := &Meeting{NodeID: "m-plat", SessionID: "s-plat", Title: "Platform sync", ContentHash: "c-plat", Date: day}
	platform.Segments = []*Segment{
		{NodeID: "seg-cap", Name: "Retry cap", Summary: "Agreed to raise the retry cap to five and keep the retry budgt design.",
			Keywords: "retry, PLAT-412", Start: 10, End: 90, meeting: platform},
		{NodeID: "seg-lunch", Name: "Team lunch", Summary: "Picked a place for Friday.", Start: 90, End: 120, meeting: platform},
	}
	finance := &Meeting{NodeID: "m-fin", SessionID: "s-fin", Title: "Finance review", ContentHash: "c-fin", Date: day.AddDate(0, -2, 0)}
	finance.Segments = []*Segment{
		{NodeID: "seg-inv", Name: "Invoices", Summary: "Invoice templates need the new logo.", Start: 0, End: 60, meeting: finance},
	}
	return []*Subject{retry, billing, onboarding}, map[string]*Meeting{"s-plat": platform, "s-fin": finance}
}

func find(cands []*Candidate, path, session string) *Candidate {
	for _, c := range cands {
		if c.Subject.Path == path && c.Meeting.SessionID == session {
			return c
		}
	}
	return nil
}

func TestSaidProposesTheNearestSegments(t *testing.T) {
	subjects, meetings := corpus()
	vec := fakeVectors{
		"sec-limits": {1, 0, 0}, "sec-jitter": {0.9, 0.1, 0},
		"sec-bill": {0, 1, 0}, "sec-onb": {0, 0, 1},
		"seg-cap": {0.95, 0.05, 0}, "seg-lunch": {0, 0.2, 0.9}, "seg-inv": {0.05, 0.95, 0},
	}
	p := NewProposer(subjects, meetings, vec, Options{K: 1})
	cands := p.ForSubjects("docs/retry-budget.md")

	c := find(cands, "docs/retry-budget.md", "s-plat")
	if c == nil || c.Signals&SignalSaid == 0 {
		t.Fatalf("the retry document was not proposed for the meeting that discussed it: %+v", c)
	}
	if c.Cosine < 0.9 {
		t.Errorf("cosine %.2f, want the near segment's", c.Cosine)
	}
	if c.Segments[0].NodeID != "seg-cap" || c.Sections[0].NodeID != "sec-limits" {
		t.Errorf("the closest pair does not lead: %s / %s", c.Sections[0].NodeID, c.Segments[0].NodeID)
	}
	if fin := find(cands, "docs/retry-budget.md", "s-fin"); fin != nil && fin.Signals&SignalSaid != 0 {
		t.Error("with K=1 a far segment was proposed by meaning")
	}
}

// TestNameToleratesTranscriptionErrors covers a title said aloud and written
// down slightly wrong ("budgt"), which is how transcription renders names.
func TestNameToleratesTranscriptionErrors(t *testing.T) {
	subjects, meetings := corpus()
	p := NewProposer(subjects, meetings, nil, DefaultOptions())
	c := find(p.ForSubjects("docs/retry-budget.md"), "docs/retry-budget.md", "s-plat")
	if c == nil || c.Signals&SignalName == 0 {
		t.Fatalf("the title, said with one letter dropped, did not propose the pair: %+v", c)
	}
	if c.NameScore <= 0 {
		t.Errorf("name score %.2f", c.NameScore)
	}
}

func TestSharedIdentifiersPropose(t *testing.T) {
	subjects, meetings := corpus()
	p := NewProposer(subjects, meetings, nil, DefaultOptions())
	c := find(p.ForSubjects("docs/retry-budget.md"), "docs/retry-budget.md", "s-plat")
	if c == nil || c.Signals&SignalIDs == 0 || !contains(c.IDs, "plat-412") {
		t.Fatalf("a shared issue key did not propose the pair: %+v", c)
	}
}

func TestForMeetingsProposesDocumentsForANewMeeting(t *testing.T) {
	subjects, meetings := corpus()
	vec := fakeVectors{"sec-bill": {0, 1, 0}, "sec-limits": {1, 0, 0}, "sec-jitter": {0.9, 0.1, 0}, "sec-onb": {0, 0, 1},
		"seg-inv": {0.05, 0.95, 0}}
	p := NewProposer(subjects, meetings, vec, Options{K: 1})
	cands := p.ForMeetings("s-fin")
	if find(cands, "docs/billing.md", "s-fin") == nil {
		t.Error("the billing document was not proposed for the finance meeting")
	}
	for _, c := range cands {
		if c.Meeting.SessionID != "s-fin" {
			t.Errorf("proposed a pair for another meeting: %s", c.Meeting.SessionID)
		}
	}
}

// TestSelectSkipsJudgedPairsAndCaps covers the bounds on a run: a pair judged
// under the same signatures is never asked again, and the caps hold.
func TestSelectSkipsJudgedPairsAndCaps(t *testing.T) {
	subjects, meetings := corpus()
	p := NewProposer(subjects, meetings, nil, Options{K: 5, MaxPerSubject: 1, MaxPerMeeting: 8, MaxPairs: 8})
	var cands []*Candidate
	for _, s := range subjects {
		for _, m := range meetings {
			cands = append(cands, &Candidate{Subject: s, Meeting: m, Signals: SignalName, NameScore: 0.5})
		}
	}

	rel := New("docs/retry-budget.md")
	plat := find(cands, "docs/retry-budget.md", "s-plat")
	rel.Links["s-plat"] = &Verdict{SessionID: "s-plat", SigDoc: plat.SigDoc(), SigMeeting: plat.SigMeeting()}
	judged := func(c *Candidate) bool {
		return c.Subject.Path == rel.SubjectPath && rel.Judged(c.Meeting.SessionID, c.SigDoc(), c.SigMeeting())
	}

	prop := p.Select(cands, judged)
	if prop.AlreadyJudged != 1 {
		t.Errorf("AlreadyJudged = %d, want 1", prop.AlreadyJudged)
	}
	perSubject := make(map[string]int)
	for _, c := range prop.Candidates {
		perSubject[c.Subject.Path]++
		if c == plat {
			t.Error("a judged pair was selected again")
		}
	}
	for path, n := range perSubject {
		if n > 1 {
			t.Errorf("%s got %d meetings, cap is 1", path, n)
		}
	}
	if prop.CutByCaps == 0 {
		t.Error("nothing was reported cut by the caps")
	}
	if prop.Proposed != len(cands) {
		t.Errorf("Proposed = %d, want %d", prop.Proposed, len(cands))
	}
}

// TestSigDocTracksOnlyTheMatchedSections covers when a judged pair becomes
// eligible again: a change to a section it rests on, not a change anywhere.
func TestSigDocTracksOnlyTheMatchedSections(t *testing.T) {
	subjects, meetings := corpus()
	retry := subjects[0]
	c := &Candidate{Subject: retry, Meeting: meetings["s-plat"], Sections: []*Section{retry.Sections[0]}}
	before := c.SigDoc()

	retry.Sections[1].Hash = "jitter-edited"
	if c.SigDoc() != before {
		t.Error("editing a section the pair does not rest on changed its signature")
	}
	retry.Sections[0].Hash = "limits-edited"
	if c.SigDoc() == before {
		t.Error("editing the section the pair rests on left its signature unchanged")
	}
}
