package transcript

import (
	"testing"
	"time"
)

// build assembles a session from (speaker, source, text) triples, giving each
// turn a plausible ten-second slot so adjacency logic has real timestamps.
func build(turns ...[3]string) *Session {
	s := &Session{
		ID:        "test-session",
		Title:     "Unknown Meeting 2026-01-01 09:00",
		CreatedAt: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC),
		Duration:  float64(len(turns)) * 10,
	}
	for i, tn := range turns {
		s.Segments = append(s.Segments, Segment{
			ID:        tn[0],
			Speaker:   tn[0],
			Source:    tn[1],
			Text:      tn[2],
			StartTime: float64(i) * 10,
			EndTime:   float64(i)*10 + 9,
		})
	}
	return s
}

// findHint returns the first hint with the given kind.
func findHint(hints []Hint, kind HintKind) (Hint, bool) {
	for _, h := range hints {
		if h.Kind == kind {
			return h, true
		}
	}
	return Hint{}, false
}

func TestExtractHintsSelfIntro(t *testing.T) {
	s := build(
		[3]string{"Person 1", SourceMonitor, "Okay, let's get started."},
		[3]string{"Person 2", SourceMonitor, "Hello everyone, this is Saki from Dashboard One."},
	)
	h, ok := findHint(ExtractHints(s), HintSelfIntro)
	if !ok {
		t.Fatal("no self-introduction found")
	}
	if h.Name != "Saki" {
		t.Errorf("name = %q, want Saki", h.Name)
	}
	// A self-introduction identifies the speaker, not a neighbour.
	if h.Target != "Person 2" {
		t.Errorf("target = %q, want Person 2", h.Target)
	}
}

func TestExtractHintsVocativePointsForward(t *testing.T) {
	s := build(
		[3]string{"Person 1", SourceMonitor, "Kevin, what do you think about the rollout?"},
		[3]string{"Person 3", SourceMonitor, "I think we should wait a week."},
	)
	h, ok := findHint(ExtractHints(s), HintVocative)
	if !ok {
		t.Fatal("no vocative found")
	}
	if h.Name != "Kevin" {
		t.Errorf("name = %q, want Kevin", h.Name)
	}
	// Addressing someone identifies whoever answers.
	if h.Target != "Person 3" {
		t.Errorf("target = %q, want Person 3", h.Target)
	}
}

func TestExtractHintsThanksPointsBackward(t *testing.T) {
	s := build(
		[3]string{"Person 4", SourceMonitor, "So the migration finished overnight and nothing broke."},
		[3]string{"You", SourceMic, "Thanks, Mona."},
	)
	h, ok := findHint(ExtractHints(s), HintThanks)
	if !ok {
		t.Fatal("no thanks hint found")
	}
	// Thanking someone identifies whoever just spoke.
	if h.Target != "Person 4" {
		t.Errorf("target = %q, want Person 4", h.Target)
	}
}

func TestExtractHintsHandoff(t *testing.T) {
	s := build(
		[3]string{"You", SourceMic, "I'll hand it over to Jeremiah for the design walkthrough."},
		[3]string{"Person 7", SourceMonitor, "Thanks. So the concept here is a single canvas."},
	)
	h, ok := findHint(ExtractHints(s), HintHandoff)
	if !ok {
		t.Fatal("no handoff found")
	}
	if h.Name != "Jeremiah" || h.Target != "Person 7" {
		t.Errorf("got name=%q target=%q, want Jeremiah / Person 7", h.Name, h.Target)
	}
}

func TestHintSkipsUnansweredAddress(t *testing.T) {
	// Nobody else speaks afterwards, so the hint points at no one rather than
	// misattributing the name to the speaker themselves.
	s := build(
		[3]string{"Person 1", SourceMonitor, "Kevin, what do you think?"},
		[3]string{"Person 1", SourceMonitor, "Anyway, moving on."},
	)
	h, ok := findHint(ExtractHints(s), HintVocative)
	if !ok {
		t.Fatal("no vocative found")
	}
	if h.Target != "" {
		t.Errorf("target = %q, want empty", h.Target)
	}
}

func TestHintRespectsTimeGap(t *testing.T) {
	s := build(
		[3]string{"Person 1", SourceMonitor, "Kevin, can you take this one?"},
		[3]string{"Person 2", SourceMonitor, "Sorry, I was on mute."},
	)
	// Push the reply far past the adjacency window: an answer arriving minutes
	// later is no longer evidence about who was addressed.
	s.Segments[1].StartTime = 600
	s.Segments[1].EndTime = 610

	h, ok := findHint(ExtractHints(s), HintVocative)
	if !ok {
		t.Fatal("no vocative found")
	}
	if h.Target != "" {
		t.Errorf("target = %q, want empty across a long gap", h.Target)
	}
}

func TestEvidenceAggregatesAndRanks(t *testing.T) {
	// Person 2 is addressed as Mona twice and is a substantive speaker.
	s := build(
		[3]string{"Person 1", SourceMonitor, "Mona, do you have the numbers?"},
		[3]string{"Person 2", SourceMonitor, "Yes, revenue is up about four percent this quarter overall."},
		[3]string{"Person 1", SourceMonitor, "Mona, can you send that around?"},
		[3]string{"Person 2", SourceMonitor, "Sure, I will send it right after this meeting ends today."},
	)
	// Give Person 2 enough floor time to count as a participant.
	for i := range s.Segments {
		s.Segments[i].EndTime = s.Segments[i].StartTime + 9
	}

	ev := Evidence(s, ExtractHints(s))
	var found bool
	for _, e := range ev {
		if e.Stat.Label != "Person 2" {
			continue
		}
		found = true
		best, ok := e.Best()
		if !ok {
			t.Fatal("Person 2 has no votes")
		}
		if best.Name != "Mona" {
			t.Errorf("best name = %q, want Mona", best.Name)
		}
		if best.Weight < 2*hintWeight[HintVocative] {
			t.Errorf("weight = %v, want both votes accumulated", best.Weight)
		}
	}
	if !found {
		t.Error("Person 2 missing from evidence")
	}
}

func TestEvidenceMergesTranscriptionVariants(t *testing.T) {
	// The same person's name is misheard differently across the meeting; the
	// votes must land on one candidate rather than splitting.
	s := build(
		[3]string{"You", SourceMic, "Imran, can you review the pull request today please?"},
		[3]string{"Person 1", SourceMonitor, "Yes I will get to it this afternoon without fail."},
		[3]string{"You", SourceMic, "Thanks, Imron."},
	)
	ev := Evidence(s, ExtractHints(s))
	for _, e := range ev {
		if e.Stat.Label != "Person 1" {
			continue
		}
		if len(e.Votes) != 1 {
			t.Fatalf("got %d candidates, want 1 merged: %+v", len(e.Votes), e.Votes)
		}
	}
}

func TestEvidenceExcludesDiarizationNoise(t *testing.T) {
	s := build(
		[3]string{"You", SourceMic, "Let me walk through the architecture for a moment here."},
		[3]string{"Person 99", SourceMonitor, "Mm-hmm."},
	)
	// A half-second backchannel is diarization debris, not a participant.
	s.Segments[1].StartTime = 10
	s.Segments[1].EndTime = 10.4

	for _, e := range Evidence(s, ExtractHints(s)) {
		if e.Stat.Label == "Person 99" {
			t.Error("noise label treated as a participant")
		}
	}
}

func TestRosterOrdersByFrequency(t *testing.T) {
	s := build(
		[3]string{"Person 1", SourceMonitor, "As Kevin mentioned, we should ship it."},
		[3]string{"Person 2", SourceMonitor, "Kevin's point about latency still stands."},
		[3]string{"Person 1", SourceMonitor, "Mona's team will handle the rollout."},
	)
	roster := Roster(ExtractHints(s))
	if len(roster) == 0 {
		t.Fatal("empty roster")
	}
	if roster[0] != "Kevin" {
		t.Errorf("roster[0] = %q, want Kevin (most mentioned)", roster[0])
	}
}
