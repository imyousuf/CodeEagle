package relations

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/pkg/jev"
	"github.com/imyousuf/CodeEagle/pkg/llm"
)

// stubLLM replies with a fixed draft and counts calls.
type stubLLM struct {
	mu    sync.Mutex
	reply string
	calls int
	last  string
}

func (s *stubLLM) Chat(_ context.Context, _ string, msgs []llm.Message) (*llm.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.last = msgs[len(msgs)-1].Content
	return &llm.Response{Content: s.reply, Usage: llm.TokenUsage{InputTokens: 1200, OutputTokens: 300}}, nil
}
func (s *stubLLM) Model() string    { return "stub-model" }
func (s *stubLLM) Provider() string { return "stub" }
func (s *stubLLM) Close() error     { return nil }

// namer names the owner's microphone.
type namer struct{}

func (namer) Name(_ *Meeting, label string) string {
	if label == "Person 1" {
		return "Ana"
	}
	return label
}

// conflictAsker answers every contradiction question with p.
type conflictAsker struct{ p float64 }

func (a conflictAsker) Ask(_ context.Context, _ any, q jev.Questions) (*jev.Response, error) {
	answers := jev.Answers{}
	for name := range q {
		answers[name] = jev.Answer{Type: jev.TypeNoul, Noul: a.p}
	}
	return &jev.Response{Model: "jev-test", Answers: answers, Usage: jev.Usage{InputTokens: 200}}, nil
}

func draft(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// briefFixture is a relation with one accepted meeting about the retry
// document, whose limits section says three retries.
func briefFixture() (*Relation, *Subject, map[string]*Meeting) {
	subjects, meetings := corpus()
	subj := subjects[0]
	subj.UpdatedAt = day.AddDate(0, 0, -30)
	rel := New(subj.Path)
	rel.Links["s-plat"] = &Verdict{SessionID: "s-plat", MeetingID: "m-plat", Accepted: true, SigMeeting: "c-plat",
		Ranges: []Range{{Start: 10, End: 90}}, Sections: []string{"Retry budget design > Limits"}}
	rel.Observe(subj, day.AddDate(0, 0, -30))
	return rel, subj, meetings
}

func newWriter(llmc llm.Client, asker jev.Asker, now time.Time) *BriefWriter {
	return NewBriefWriter(llmc, asker, spokenStub{}, namer{}, BriefOptions{Settle: time.Hour}, func() time.Time { return now })
}

// TestBriefKeepsOnlyWhatItsQuotesSupport covers grounding: a quote must be in
// the snippet it is filed under, and a line with no such quote is dropped.
func TestBriefKeepsOnlyWhatItsQuotesSupport(t *testing.T) {
	rel, subj, meetings := briefFixture()
	sup := func(label, quote string) []map[string]any {
		return []map[string]any{{"label": label, "quote": quote}}
	}
	stub := &stubLLM{reply: draft(t, map[string]any{
		"headline": map[string]any{"text": "Retry cap raised to five", "support": sup("M1", "make the retry cap five")},
		"current": []map[string]any{
			{"text": "Clients retry at most three times per the document", "support": sup("D1", "Clients retry at most three times")},
			{"text": "An invented claim", "support": sup("M1x", "something nobody said")},
			// In the meeting, not in the Jitter section it is filed under.
			{"text": "A misfiled quote", "support": sup("D2", "make the retry cap five")},
		},
		"events": []map[string]any{}, "unresolved": []map[string]any{}, "conflicts": []map[string]any{},
	})}
	rep, err := newWriter(stub, nil, day).Update(context.Background(), rel, subj, meetings)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Written || rel.Brief == nil {
		t.Fatalf("no brief written: %+v", rep)
	}
	if got := rel.Brief.Headline.Text; got != "Retry cap raised to five" {
		t.Errorf("headline = %q", got)
	}
	if len(rel.Brief.Current) != 1 || rep.Dropped != 2 || rel.Brief.Dropped != 2 {
		t.Errorf("current = %+v, dropped %d; want the one grounded line, two dropped", rel.Brief.Current, rep.Dropped)
	}

	var said, doc *Evidence
	for i := range rel.Evidence {
		switch rel.Evidence[i].Kind {
		case EvidenceSaid:
			said = &rel.Evidence[i]
		case EvidenceDoc:
			doc = &rel.Evidence[i]
		}
	}
	if said == nil || said.SessionID != "s-plat" || said.Start != 10 || said.Speaker != "Ana" {
		t.Errorf("said evidence not anchored to its turn and named: %+v", said)
	}
	if doc == nil || doc.Path != subj.Path || doc.HeadingPath != "Retry budget design > Limits" || doc.SectionHash == "" {
		t.Errorf("doc evidence not anchored to its section: %+v", doc)
	}
	if rel.Revision != 1 || rel.BriefModel != "stub-model" {
		t.Errorf("revision %d model %q", rel.Revision, rel.BriefModel)
	}
}

func TestBriefIsNotRewrittenWhenNothingChanged(t *testing.T) {
	rel, subj, meetings := briefFixture()
	stub := &stubLLM{reply: draft(t, map[string]any{
		"headline": map[string]any{"text": "cap raised", "support": []map[string]any{{"label": "M1", "quote": "retry cap five"}}},
		"current":  []any{}, "events": []any{}, "unresolved": []any{}, "conflicts": []any{},
	})}
	w := newWriter(stub, nil, day)
	if _, err := w.Update(context.Background(), rel, subj, meetings); err != nil {
		t.Fatal(err)
	}
	rep, err := w.Update(context.Background(), rel, subj, meetings)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Written || stub.calls != 1 {
		t.Errorf("an unchanged relation was briefed again (%d calls, %s)", stub.calls, rep.Reason)
	}
}

// TestBriefWaitsForTheDocumentToSettle covers a file being edited, which the
// working-tree index sees on every save.
func TestBriefWaitsForTheDocumentToSettle(t *testing.T) {
	rel, subj, meetings := briefFixture()
	subj.UpdatedAt = day.Add(-10 * time.Minute)
	stub := &stubLLM{reply: `{}`}
	rep, err := newWriter(stub, nil, day).Update(context.Background(), rel, subj, meetings)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Written || stub.calls != 0 {
		t.Errorf("a document edited ten minutes ago was briefed (%s)", rep.Reason)
	}
}

func conflictDraft(t *testing.T) string {
	return draft(t, map[string]any{
		"headline": map[string]any{"text": "cap is five now",
			"support": []map[string]any{{"label": "M1", "quote": "make the retry cap five"}}},
		"current": []any{}, "events": []any{}, "unresolved": []any{},
		"conflicts": []map[string]any{{
			"doc_says": "three retries", "discussion_says": "the cap is five", "date": "2026-10-14",
			"doc":  map[string]any{"label": "D1", "quote": "at most three times"},
			"said": map[string]any{"label": "M1", "quote": "make the retry cap five"},
		}},
	})
}

// TestConflictsNeedTheDecisionModelsAgreement covers the gate on conflicts:
// one the decision model does not confirm becomes an event, so nobody is sent
// to fix a document that is right.
func TestConflictsNeedTheDecisionModelsAgreement(t *testing.T) {
	rel, subj, meetings := briefFixture()
	rep, err := newWriter(&stubLLM{reply: conflictDraft(t)}, conflictAsker{p: 0.9}, day).Update(context.Background(), rel, subj, meetings)
	if err != nil {
		t.Fatal(err)
	}
	if len(rel.Brief.Conflicts) != 1 || rep.ConflictsKept != 1 {
		t.Errorf("a confirmed conflict was not kept: %+v", rel.Brief.Conflicts)
	}

	rel2, subj2, meetings2 := briefFixture()
	rep, err = newWriter(&stubLLM{reply: conflictDraft(t)}, conflictAsker{p: 0.3}, day).Update(context.Background(), rel2, subj2, meetings2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rel2.Brief.Conflicts) != 0 || rep.ConflictsDemoted != 1 {
		t.Errorf("an unconfirmed conflict was shown: %+v", rel2.Brief.Conflicts)
	}
	if len(rel2.Brief.Events) == 0 || !strings.Contains(rel2.Brief.Events[0].Text, "five") {
		t.Errorf("the demoted conflict was not kept as an event: %+v", rel2.Brief.Events)
	}
}

func TestFreshnessFlags(t *testing.T) {
	rel, subj, meetings := briefFixture()
	if _, err := newWriter(&stubLLM{reply: conflictDraft(t)}, conflictAsker{p: 0.9}, day).Update(context.Background(), rel, subj, meetings); err != nil {
		t.Fatal(err)
	}
	if got := Freshness(rel, subj, meetings); len(got) != 1 || got[0] != FlagDiscussedAfterDocChange {
		t.Errorf("after a meeting newer than the document: %v", got)
	}

	subj.UpdatedAt = day.AddDate(0, 0, 3)
	subj.Sections[0].Hash = "limits-edited"
	got := Freshness(rel, subj, meetings)
	for _, want := range []string{FlagDocChangedAfterDiscussion, FlagQuoteGone, FlagBriefPending} {
		if !contains(got, want) {
			t.Errorf("after editing the quoted section, flags %v lack %s", got, want)
		}
	}
	if got := Freshness(rel, nil, meetings); len(got) != 1 || got[0] != FlagDocMissing {
		t.Errorf("for a missing document: %v", got)
	}
}
