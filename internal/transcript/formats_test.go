package transcript

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const zoomVTT = `WEBVTT

1
00:00:01.000 --> 00:00:04.500
Kevin Smith: Let's start with the migration status.

2
00:00:04.800 --> 00:00:08.000
Kevin Smith: It finished overnight.

3
00:00:09.000 --> 00:00:12.000
Mona Ali: And nothing broke, which is the good news.
`

const teamsVTT = `WEBVTT

00:00:00.000 --> 00:00:03.000
<v Kevin Smith>Let's start with the migration status.</v>

00:00:04.000 --> 00:00:07.000
<v Mona Ali>Nothing broke.</v>
`

const plainSRT = `1
00:00:01,000 --> 00:00:04,000
Kevin Smith: Let's start.

2
00:00:05,000 --> 00:00:08,000
Mona Ali: Sounds good.
`

func writeTemp(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestZoomVTT(t *testing.T) {
	path := writeTemp(t, "GMT20260918-080717_Recording.transcript.vtt", zoomVTT)
	data, _ := os.ReadFile(path)

	s, err := ParseAny(path, data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Format != "webvtt" {
		t.Errorf("format = %q, want webvtt", s.Format)
	}
	if !s.NamedSpeakers {
		t.Error("a caption file naming its speakers should set NamedSpeakers")
	}

	// Consecutive cues from one speaker are one utterance, not three.
	if len(s.Segments) != 2 {
		t.Fatalf("got %d segments, want 2 merged turns: %+v", len(s.Segments), s.Segments)
	}
	if s.Segments[0].Speaker != "Kevin Smith" {
		t.Errorf("speaker = %q, want Kevin Smith", s.Segments[0].Speaker)
	}
	if !strings.Contains(s.Segments[0].Text, "finished overnight") {
		t.Errorf("consecutive cues were not merged: %q", s.Segments[0].Text)
	}

	// Zoom stamps the meeting time into the filename, which beats the file's
	// own timestamp.
	if s.StartedAt().Format("2006-01-02 15:04") != "2026-09-18 08:07" {
		t.Errorf("start = %s, want the time from the filename", s.StartedAt())
	}
}

func TestTeamsVTTVoiceTags(t *testing.T) {
	path := writeTemp(t, "standup.vtt", teamsVTT)
	data, _ := os.ReadFile(path)

	s, err := ParseAny(path, data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(s.Segments) != 2 {
		t.Fatalf("got %d segments, want 2", len(s.Segments))
	}
	if s.Segments[0].Speaker != "Kevin Smith" || s.Segments[1].Speaker != "Mona Ali" {
		t.Errorf("voice tags not read: %+v", s.Segments)
	}
	// The tag markup must not leak into the text.
	if strings.Contains(s.Segments[0].Text, "<v") {
		t.Errorf("markup left in text: %q", s.Segments[0].Text)
	}
}

func TestSRT(t *testing.T) {
	path := writeTemp(t, "meeting.srt", plainSRT)
	data, _ := os.ReadFile(path)

	s, err := ParseAny(path, data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Format != "srt" {
		t.Errorf("format = %q, want srt", s.Format)
	}
	if len(s.Segments) != 2 {
		t.Fatalf("got %d segments, want 2", len(s.Segments))
	}
}

func TestCaptionWithoutSpeakers(t *testing.T) {
	body := "WEBVTT\n\n00:00:01.000 --> 00:00:04.000\nSo here's the thing: we ship on Friday.\n"
	path := writeTemp(t, "notes.vtt", body)
	data, _ := os.ReadFile(path)

	s, err := ParseAny(path, data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.NamedSpeakers {
		t.Error("a caption file with no speaker names must not claim to have them")
	}
	// A mid-sentence colon is not a speaker label.
	if !strings.Contains(s.Segments[0].Text, "here's the thing") {
		t.Errorf("a clause before a colon was mistaken for a speaker: %q", s.Segments[0].Text)
	}
}

func TestFormatDetection(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{"a.vtt", zoomVTT, "webvtt"},
		{"a.srt", plainSRT, "srt"},
		{"session.json", `{"id":"x","created_at":"2026-01-01T09:00:00Z","duration":10,"segments":[]}`, "tomoe"},
		{"notes.md", "# Notes\n\nnothing here", ""},
		{"package.json", `{"name":"app","version":"1.0.0"}`, ""},
		{"a.vtt", "not really a caption file", ""},
	}
	for _, tt := range tests {
		path := writeTemp(t, tt.name, tt.body)
		data, _ := os.ReadFile(path)
		f := FormatFor(path, data)
		got := ""
		if f != nil {
			got = f.Name()
		}
		if got != tt.want {
			t.Errorf("FormatFor(%s) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestParseClock(t *testing.T) {
	tests := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"00:00:04.500", 4.5, true},
		{"01:02:03", 3723, true},
		{"1:30", 90, true},
		{"00:00:04,500", 4.5, true},
		{"", 0, false},
		{"nope", 0, false},
		{"1:2:3:4", 0, false},
	}
	for _, tt := range tests {
		got, ok := parseClock(tt.in)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("parseClock(%q) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestCleanRunTogether(t *testing.T) {
	// Word stores each sentence as its own run, so extraction concatenates
	// them without spaces.
	got := cleanRunTogether("Hello?What is this?Oh boy.Okay.")
	want := "Hello? What is this? Oh boy. Okay."
	if got != want {
		t.Errorf("cleanRunTogether() = %q, want %q", got, want)
	}
	// A decimal is not a sentence boundary.
	if got := cleanRunTogether("It costs 1.5 million"); got != "It costs 1.5 million" {
		t.Errorf("decimal was split: %q", got)
	}
}

func TestPreferOneExportPerMeeting(t *testing.T) {
	base := time.Date(2026, 6, 22, 14, 0, 0, 0, time.UTC)

	// The same meeting exported twice, plus a different instance of the same
	// recurring meeting months later.
	exports := []export{
		{path: "/x/Opti Models Standup.docx", started: base},
		{path: "/x/Opti Models Standup.vtt", started: base.Add(20 * time.Hour)},
		{path: "/x/Opti Models Standup.vtt.later", started: base.AddDate(0, 3, 0)},
	}
	// The third is a different file type; name it plausibly for the test.
	exports[2].path = "/y/Opti Models Standup.vtt"

	got := preferOneExportPerMeeting(exports)
	if len(got) != 2 {
		t.Fatalf("got %d kept, want 2 (one per instance): %v", len(got), got)
	}
	// Captions time every cue, so they win over a document of the same call.
	if !strings.HasSuffix(got[0], ".vtt") {
		t.Errorf("kept %q for the first instance, want the caption file", got[0])
	}
	// The instance three months later must survive: a recurring meeting
	// exports to the same filename every time.
	if got[1] != "/y/Opti Models Standup.vtt" {
		t.Errorf("a later instance of the recurring meeting was dropped: %v", got)
	}
}

func TestPreferOneExportKeepsDistinctMeetings(t *testing.T) {
	now := time.Date(2026, 6, 22, 14, 0, 0, 0, time.UTC)
	exports := []export{
		{path: "/x/Architecture Review.vtt", started: now},
		{path: "/x/Budget Planning.vtt", started: now},
	}
	if got := preferOneExportPerMeeting(exports); len(got) != 2 {
		t.Errorf("got %d, want both distinct meetings: %v", len(got), got)
	}
}

func TestMeetingKeyFromPath(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/x/Opti Models Standup.docx", "opti-models-standup"},
		{"/x/Opti Models Standup-transcript.vtt", "opti-models-standup"},
		{"/x/abc-123/session.json", "abc-123"},
		{"/x/Call with Imran.vtt", "call-with-imran"},
	}
	for _, tt := range tests {
		if got := meetingKeyFromPath(tt.in); got != tt.want {
			t.Errorf("meetingKeyFromPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTimeFromName(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"Opti Models Standup-20260710_140714UTC-Meeting Recording.docx", "2026-07-10 14:07", true},
		{"GMT20260918-080717_Recording.transcript.vtt", "2026-09-18 08:07", true},
		{"just-a-name.vtt", "", false},
	}
	for _, tt := range tests {
		got, ok := timeFromName(tt.in)
		if ok != tt.ok {
			t.Errorf("timeFromName(%q) ok = %v, want %v", tt.in, ok, tt.ok)
			continue
		}
		if ok && got.Format("2006-01-02 15:04") != tt.want {
			t.Errorf("timeFromName(%q) = %s, want %s", tt.in, got.Format("2006-01-02 15:04"), tt.want)
		}
	}
}

func TestTeamsTranscriptParsing(t *testing.T) {
	text := strings.Join([]string{
		"Opti Models Standup-20260710_140714UTC-Meeting Recording",
		"July 10, 2026, 2:07PM",
		"29m 7s",
		"Carlos Pereira   0:16Morning all.",
		"Benjamin Schaefer   0:18Hello?What is this?Good to see you.",
		"Carlos Pereira   1:22Shall we start?",
	}, "\n")

	s, err := parseTeamsTranscript("/x/Opti Models Standup.docx", text)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Title != "Opti Models Standup" {
		t.Errorf("title = %q, want the name without the stamp and suffix", s.Title)
	}
	if s.StartedAt().Format("2006-01-02 15:04") != "2026-07-10 14:07" {
		t.Errorf("start = %s, want the stamp from the title", s.StartedAt())
	}
	if s.DurationSeconds() != 29*60+7 {
		t.Errorf("duration = %v, want 29m7s", s.DurationSeconds())
	}
	if !s.NamedSpeakers {
		t.Error("a Teams transcript names its speakers")
	}
	if len(s.Segments) != 3 {
		t.Fatalf("got %d segments, want 3", len(s.Segments))
	}
	if s.Segments[0].Speaker != "Carlos Pereira" || s.Segments[0].Text != "Morning all." {
		t.Errorf("first utterance = %+v", s.Segments[0])
	}
	// Run-together sentences are separated again.
	if !strings.Contains(s.Segments[1].Text, "Hello? What is this?") {
		t.Errorf("sentence spacing not repaired: %q", s.Segments[1].Text)
	}
	// An utterance runs until the next begins.
	if s.Segments[0].EndTime != 18 {
		t.Errorf("first utterance ends at %v, want the next one's start", s.Segments[0].EndTime)
	}
}

func TestTeamsDurationParsing(t *testing.T) {
	tests := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"29m 7s", 29*60 + 7, true},
		{"1h 4m 53s", 3600 + 4*60 + 53, true},
		{"45s", 45, true},
		{"July 10, 2026", 0, false},
		{"", 0, false},
	}
	for _, tt := range tests {
		got, ok := parseTeamsDuration(tt.in)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("parseTeamsDuration(%q) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestNamedSpeakersSkipIdentification(t *testing.T) {
	// A transcript that names its speakers needs no model call, so the
	// analyzer must produce identities without one. A nil client would panic
	// if it tried.
	s := build(
		[3]string{"Kevin Smith", SourceMonitor, "Let's start with the migration status today."},
		[3]string{"Mona Ali", SourceMonitor, "Nothing broke overnight, which is the good news."},
	)
	s.NamedSpeakers = true

	a := NewAnalyzer(nil, Options{Owner: "Imran Yousuf"})
	ids, err := a.Identify(context.Background(), s, nil)
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("got %d identities, want 2", len(ids))
	}
	for _, id := range ids {
		if id.Method != MethodTranscript {
			t.Errorf("%s resolved via %q, want %q", id.Label, id.Method, MethodTranscript)
		}
		if id.Confidence != 1.0 {
			t.Errorf("%s confidence = %v, want 1.0", id.Label, id.Confidence)
		}
	}
}
