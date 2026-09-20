package transcript

import (
	"strings"
	"testing"
)

func TestLooksLikeSession(t *testing.T) {
	tests := []struct {
		name string
		json string
		want bool
	}{
		{
			name: "full transcript",
			json: `{"id":"abc","title":"Meeting","created_at":"2026-01-01T09:00:00Z",
			        "duration":611.7,"segments":[{"id":"seg-1","speaker":"You","text":"hi"}]}`,
			want: true,
		},
		{
			// A recording that captured no speech still is a transcript.
			name: "null segments",
			json: `{"id":"abc","created_at":"2026-01-01T09:00:00Z","duration":4.5,"segments":null}`,
			want: true,
		},
		{
			name: "empty segments",
			json: `{"id":"abc","created_at":"2026-01-01T09:00:00Z","duration":4.5,"segments":[]}`,
			want: true,
		},
		{
			name: "missing id",
			json: `{"created_at":"2026-01-01T09:00:00Z","duration":4.5,"segments":[]}`,
			want: false,
		},
		{
			name: "empty id",
			json: `{"id":"","created_at":"2026-01-01T09:00:00Z","duration":1,"segments":[]}`,
			want: false,
		},
		{
			// An unrelated JSON file that happens to use some of the same keys.
			name: "decoy",
			json: `{"id":"x","created_at":"now","duration":"forever","segments":[]}`,
			want: false,
		},
		{name: "package manifest", json: `{"name":"app","version":"1.0.0","dependencies":{}}`, want: false},
		{name: "not json", json: `package main`, want: false},
		{name: "empty", json: ``, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LooksLikeSession([]byte(tt.json)); got != tt.want {
				t.Errorf("LooksLikeSession() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseRejectsMissingID(t *testing.T) {
	if _, err := Parse([]byte(`{"title":"x"}`), "t.json"); err == nil {
		t.Error("expected an error for a transcript with no id")
	}
}

func TestTurnsMergeConsecutiveSegments(t *testing.T) {
	s := build(
		[3]string{"Person 1", SourceMonitor, "So the first thing is"},
		[3]string{"Person 1", SourceMonitor, "we need to ship the migration."},
		[3]string{"You", SourceMic, "Agreed."},
	)
	turns := s.Turns()
	if len(turns) != 2 {
		t.Fatalf("got %d turns, want 2", len(turns))
	}
	if !strings.Contains(turns[0].Text, "first thing") || !strings.Contains(turns[0].Text, "migration") {
		t.Errorf("fragments not merged: %q", turns[0].Text)
	}
	// A merged turn spans from its first fragment to its last.
	if turns[0].StartTime != 0 || turns[0].EndTime != 19 {
		t.Errorf("turn span = %v..%v, want 0..19", turns[0].StartTime, turns[0].EndTime)
	}
}

func TestOwnerDetection(t *testing.T) {
	// Microphone audio is by construction the person who made the recording.
	if !IsOwnerLabel("Person 1", SourceMic) {
		t.Error("mic audio should be the owner regardless of label")
	}
	if !IsOwnerLabel(OwnerLabel, SourceMonitor) {
		t.Error(`the "You" label should be the owner`)
	}
	if IsOwnerLabel("Person 1", SourceMonitor) {
		t.Error("monitor audio from Person 1 is not the owner")
	}
}

func TestIsSubstantive(t *testing.T) {
	tests := []struct {
		name string
		stat SpeakerStat
		want bool
	}{
		{"owner always counts", SpeakerStat{IsOwner: true, SpeakingSeconds: 0.4}, true},
		{"long speaker", SpeakerStat{SpeakingSeconds: 60, Utterances: 1}, true},
		{"frequent interjector", SpeakerStat{SpeakingSeconds: 6, Utterances: 8}, true},
		{"backchannel noise", SpeakerStat{SpeakingSeconds: 0.4, Utterances: 1}, false},
		{"brief and rare", SpeakerStat{SpeakingSeconds: 3, Utterances: 2}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.stat.IsSubstantive(); got != tt.want {
				t.Errorf("IsSubstantive() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSpeakerStats(t *testing.T) {
	s := build(
		[3]string{"You", SourceMic, "one two three"},
		[3]string{"Person 1", SourceMonitor, "four five"},
		[3]string{"You", SourceMic, "six"},
	)
	stats := s.SpeakerStats()
	if len(stats) != 2 {
		t.Fatalf("got %d speakers, want 2", len(stats))
	}
	// Ordered by speaking time, so the owner (two turns) comes first.
	if stats[0].Label != "You" {
		t.Errorf("stats[0] = %q, want You", stats[0].Label)
	}
	if !stats[0].IsOwner {
		t.Error("You should be marked as owner")
	}
	if stats[0].Utterances != 2 {
		t.Errorf("utterances = %d, want 2", stats[0].Utterances)
	}
	if stats[0].Words != 4 {
		t.Errorf("words = %d, want 4", stats[0].Words)
	}
}

func TestHasGenericTitle(t *testing.T) {
	tests := []struct {
		title string
		want  bool
	}{
		{"Unknown Meeting 2026-08-20 11:33", true},
		{"Zoom Meeting 2026-04-09 10:17", true},
		{"Meeting 2026-03-10 14:40", true},
		{"Session 2026-03-10 12:55", true},
		{"", true},
		{"Q3 Platform Architecture Review", false},
	}
	for _, tt := range tests {
		s := &Session{Title: tt.title}
		if got := s.HasGenericTitle(); got != tt.want {
			t.Errorf("HasGenericTitle(%q) = %v, want %v", tt.title, got, tt.want)
		}
	}
}

func TestFormatTimestamp(t *testing.T) {
	tests := []struct {
		in   float64
		want string
	}{
		{0, "00:00"},
		{61, "01:01"},
		{599, "09:59"},
		{3600, "1:00:00"},
		{3725, "1:02:05"},
		{-5, "00:00"},
	}
	for _, tt := range tests {
		if got := FormatTimestamp(tt.in); got != tt.want {
			t.Errorf("FormatTimestamp(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTranscriptRendering(t *testing.T) {
	s := build(
		[3]string{"You", SourceMic, "Let's start."},
		[3]string{"Person 1", SourceMonitor, "Sounds good."},
	)
	got := s.Transcript()
	for _, want := range []string{"[00:00] You: Let's start.", "[00:10] Person 1: Sounds good."} {
		if !strings.Contains(got, want) {
			t.Errorf("transcript missing %q:\n%s", want, got)
		}
	}
}

func TestDurationSecondsFallsBackToSegments(t *testing.T) {
	s := build([3]string{"You", SourceMic, "hello"})
	s.Duration = 0
	s.CreatedAt = s.CreatedAt.UTC()
	s.EndedAt = s.CreatedAt // no span recorded
	if got := s.DurationSeconds(); got != 9 {
		t.Errorf("DurationSeconds() = %v, want 9 from segment extent", got)
	}
}

func TestIsEmpty(t *testing.T) {
	if !(&Session{}).IsEmpty() {
		t.Error("a session with no segments should be empty")
	}
	blank := build([3]string{"You", SourceMic, "   "})
	if !blank.IsEmpty() {
		t.Error("a session with only whitespace should be empty")
	}
	if build([3]string{"You", SourceMic, "hi"}).IsEmpty() {
		t.Error("a session with speech should not be empty")
	}
}
