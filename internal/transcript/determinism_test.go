package transcript

import (
	"strings"
	"testing"
	"time"
)

// TestRosterIsDeterministic covers the merge order in Roster.
//
// Which existing candidate a new mention merges into decides the counts, and
// ranging a Go map made that depend on iteration order: the same transcript
// produced a different candidate list — and so potentially a different
// identification — on every run, in code whose stated purpose is to be
// reproducible.
func TestRosterIsDeterministic(t *testing.T) {
	// Two colleagues sharing a first name, plus bare first-name mentions that
	// could merge into either.
	hints := []Hint{
		{Name: "Imran Khan", At: 10},
		{Name: "Imran Sharma", At: 20},
	}
	for i := range 30 {
		hints = append(hints, Hint{Name: "Imran", At: float64(30 + i)})
	}

	first := strings.Join(Roster(hints), "|")
	for range 200 {
		if got := strings.Join(Roster(hints), "|"); got != first {
			t.Fatalf("Roster is not deterministic:\n  %s\n  %s", first, got)
		}
	}
}

// TestTimeFromNameSeparators covers the filename timestamp formats exporters
// produce. The underscore variant was advertised by the pattern but never
// normalized, so it silently fell back to the file's modification time —
// which reflects when the file was downloaded, not when the meeting happened,
// and so put the recording in the wrong place in the chronological order that
// carries identified people forward.
func TestTimeFromNameSeparators(t *testing.T) {
	want := time.Date(2026, time.July, 10, 14, 7, 14, 0, time.UTC)

	tests := []struct {
		name string
		file string
	}{
		{"underscore between date and time", "recording-2026-07-10_14-07-14.json"},
		{"T between date and time", "recording-2026-07-10T14-07-14.json"},
		{"space between date and time", "recording 2026-07-10 14-07-14.json"},
		{"dotted time", "recording-2026-07-10 14.07.14.json"},
		{"compact UTC", "meeting-20260710_140714UTC.json"},
		{"zoom GMT", "GMT20260710-140714_Recording.transcript.vtt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := timeFromName(tt.file)
			if !ok {
				t.Fatalf("timeFromName(%q) found no timestamp", tt.file)
			}
			if !got.Equal(want) {
				t.Errorf("timeFromName(%q) = %s, want %s", tt.file, got, want)
			}
		})
	}
}

// TestTimeFromNameRejectsPlainNames covers files with no timestamp, which must
// fall through to the other sources rather than inventing one.
func TestTimeFromNameRejectsPlainNames(t *testing.T) {
	for _, name := range []string{"standup.vtt", "notes.json", "meeting-transcript.docx"} {
		if _, ok := timeFromName(name); ok {
			t.Errorf("timeFromName(%q) claimed a timestamp", name)
		}
	}
}
