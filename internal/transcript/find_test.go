package transcript

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// searchFixture writes two enriched meetings through the real writer, so the
// search is exercised against the graph shape indexing actually produces.
func searchFixture(t *testing.T) graph.Store {
	t.Helper()
	ctx := context.Background()
	store := testStore(t)
	people, err := LoadPersonRegistry(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter(store, people, WriterOptions{MinConfidence: 0.7, Owner: "Imran Yousuf"})

	first := sampleResult("/tmp/sessions/abc/session.json")
	if _, err := w.Write(ctx, first); err != nil {
		t.Fatalf("write first: %v", err)
	}

	second := sampleResult("/tmp/sessions/def/session.json")
	second.Session.ID = "session-def"
	second.Session.Segments = second.Session.Segments[:1]
	second.Identities = second.Identities[:1]
	second.Analysis = &Analysis{
		Title:   "AGI feasibility debate with the platform team",
		Summary: "A debate on whether current models amount to artificial general intelligence.",
		Topics: []Topic{{
			Name:      "AGI feasibility",
			Summary:   "Imran argued models lack self-improvement, so this is not AGI.",
			StartTime: 0, EndTime: 60,
			Keywords: []string{"self-improvement", "RSI"},
		}},
		Decisions: []Decision{{
			Text:  "Treat AGI claims as marketing until a model retrains itself",
			Quote: "not a real quote",
			Topic: "AGI feasibility",
		}},
		Mentions: []string{"Okta"},
	}
	if _, err := w.Write(ctx, second); err != nil {
		t.Fatalf("write second: %v", err)
	}
	return store
}

func TestFindMeetingsReportsWhereEachWordMatched(t *testing.T) {
	store := searchFixture(t)
	found, err := FindMeetings(context.Background(), store, Query{Text: "migration"})
	if err != nil {
		t.Fatal(err)
	}
	if found.Total != 1 || found.Considered != 2 {
		t.Fatalf("total=%d considered=%d, want 1 of 2", found.Total, found.Considered)
	}
	h := found.Hits[0]
	if h.Meeting.Name != "Schema migration status" {
		t.Errorf("hit = %q", h.Meeting.Name)
	}
	if !contains(h.Participants, "Kevin") || !contains(h.Participants, "Mona") {
		t.Errorf("participants = %v, want Kevin and Mona", h.Participants)
	}
	kinds := map[MatchKind]bool{}
	for _, m := range h.Matches {
		kinds[m.Kind] = true
	}
	for _, want := range []MatchKind{MatchTitle, MatchSummary, MatchTopic, MatchSegment, MatchDecision} {
		if !kinds[want] {
			t.Errorf("no %s match; matches: %+v", want, h.Matches)
		}
	}
	if kinds[MatchFollowUp] {
		// "Update the client" says nothing about migration.
		t.Errorf("follow-up matched without containing the word: %+v", h.Matches)
	}
}

// TestFindMeetingsMatchesWholeWords covers the acronym failure: "AGI" must
// find the meeting about AGI and nothing that merely contains the letters.
func TestFindMeetingsMatchesWholeWords(t *testing.T) {
	store := searchFixture(t)
	found, err := FindMeetings(context.Background(), store, Query{Text: "AGI"})
	if err != nil {
		t.Fatal(err)
	}
	if found.Total != 1 || !strings.HasPrefix(found.Hits[0].Meeting.Name, "AGI feasibility") {
		t.Fatalf("hits = %v, want only the AGI meeting", titles(found))
	}
	// The decision's quote is not in the transcript: the node carries that.
	for _, m := range found.Hits[0].Matches {
		if m.Kind == MatchDecision && m.Node.Properties["quote_verified"] == "true" {
			t.Errorf("an invented quote was recorded as verified")
		}
	}

	// A word that appears nowhere matches nothing, rather than something.
	found, err = FindMeetings(context.Background(), store, Query{Text: "kubernetes"})
	if err != nil {
		t.Fatal(err)
	}
	if found.Total != 0 {
		t.Errorf("kubernetes matched %v", titles(found))
	}
}

func TestFindMeetingsGroupsAnInitialismWithItsExpansion(t *testing.T) {
	store := searchFixture(t)
	found, err := FindMeetings(context.Background(), store,
		Query{Text: "AGI artificial general intelligence"})
	if err != nil {
		t.Fatal(err)
	}
	if found.Total != 1 {
		t.Fatalf("hits = %v", titles(found))
	}
	// The meeting says both "AGI" and "artificial general intelligence";
	// either alone covers the whole query, so the score is a full one.
	if s := found.Hits[0].Score; s < 1 {
		t.Errorf("score = %v, want at least 1 (phrase hit)", s)
	}
}

func TestFindMeetingsFiltersByPersonAndDate(t *testing.T) {
	store := searchFixture(t)
	ctx := context.Background()

	found, err := FindMeetings(ctx, store, Query{Text: "status", Person: "kevin"})
	if err != nil {
		t.Fatal(err)
	}
	if found.Total != 1 || found.Considered != 1 {
		t.Errorf("person filter: total=%d considered=%d", found.Total, found.Considered)
	}

	found, err = FindMeetings(ctx, store, Query{Text: "status", Person: "Nobody"})
	if err != nil {
		t.Fatal(err)
	}
	if found.Considered != 0 {
		t.Errorf("unknown person considered %d meetings", found.Considered)
	}

	found, err = FindMeetings(ctx, store, Query{Text: "status", Since: time.Now().Add(24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if found.Considered != 0 {
		t.Errorf("future cutoff considered %d meetings", found.Considered)
	}
}

// TestFindMeetingsListsOneKindWithoutWords: "every decision" is a search with
// no words restricted to decisions, and the answer is every decision.
func TestFindMeetingsListsOneKindWithoutWords(t *testing.T) {
	store := searchFixture(t)
	found, err := FindMeetings(context.Background(), store, Query{Only: MatchDecision})
	if err != nil {
		t.Fatal(err)
	}
	if found.Total != 2 {
		t.Fatalf("hits = %v, want both meetings", titles(found))
	}
	for _, h := range found.Hits {
		for _, m := range h.Matches {
			if m.Kind != MatchDecision {
				t.Errorf("--only decision returned a %s match", m.Kind)
			}
		}
		if len(h.Matches) == 0 {
			t.Errorf("%q has no decision listed", h.Meeting.Name)
		}
	}
	// Most recent first when nothing else separates them.
	if a, b := found.Hits[0].Meeting.UpdatedAt, found.Hits[1].Meeting.UpdatedAt; a.Before(b) {
		t.Errorf("order: %v before %v", a, b)
	}

	if _, err := FindMeetings(context.Background(), store, Query{}); err == nil {
		t.Error("an empty query with no filters was accepted")
	}
	if _, err := FindMeetings(context.Background(), store, Query{Only: "gossip"}); err == nil {
		t.Error("an unknown kind was accepted")
	}
}

func TestFindMeetingsLimitKeepsTotal(t *testing.T) {
	store := searchFixture(t)
	found, err := FindMeetings(context.Background(), store, Query{Text: "Imran", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if found.Total != 2 || len(found.Hits) != 1 {
		t.Errorf("total=%d shown=%d, want 2 and 1", found.Total, len(found.Hits))
	}
	// The owner attended both, so this is a participant hit in each.
	if found.Hits[0].Matches[0].Kind != MatchParticipant && !hasKind(found.Hits[0], MatchParticipant) {
		t.Errorf("no participant match: %+v", found.Hits[0].Matches)
	}
}

func hasKind(h *Hit, k MatchKind) bool {
	for _, m := range h.Matches {
		if m.Kind == k {
			return true
		}
	}
	return false
}

func titles(f *Found) []string {
	var out []string
	for _, h := range f.Hits {
		out = append(out, h.Meeting.Name)
	}
	return out
}

func contains(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}
