package transcript

import (
	"strings"
	"testing"
)

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare object", `{"a":1}`, `{"a":1}`},
		{"bare array", `[1,2]`, `[1,2]`},
		{
			// Small models habitually wrap JSON in a fence.
			name: "fenced",
			in:   "Here you go:\n```json\n{\"a\":1}\n```\nHope that helps!",
			want: `{"a":1}`,
		},
		{"fenced without language", "```\n{\"a\":1}\n```", `{"a":1}`},
		{"surrounded by prose", `Sure. {"a":1} Let me know.`, `{"a":1}`},
		{"nested", `{"a":{"b":[1,{"c":2}]}}`, `{"a":{"b":[1,{"c":2}]}}`},
		{
			// A brace inside a string literal is not structural, so it must not
			// end the document early — transcript quotes contain anything.
			name: "brace inside a string",
			in:   `{"quote":"we said } and { in the meeting","a":1}`,
			want: `{"quote":"we said } and { in the meeting","a":1}`,
		},
		{
			name: "escaped quote inside a string",
			in:   `{"quote":"he said \"ship it\" }","a":1}`,
			want: `{"quote":"he said \"ship it\" }","a":1}`,
		},
		{"no json", "I could not do that.", ""},
		{"empty", "", ""},
		{
			// Truncated output still returns what there is, so the decode error
			// names the real problem rather than "no JSON found".
			name: "unterminated",
			in:   `{"a":1,"b":`,
			want: `{"a":1,"b":`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractJSON(tt.in); got != tt.want {
				t.Errorf("extractJSON() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBudgetedTranscriptKeepsTheEnding(t *testing.T) {
	a := NewAnalyzer(nil, Options{MaxTranscriptChars: 400})

	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("[00:01] Person 1: filler line of conversation\n")
	}
	full := "[00:00] You: OPENING REMARK\n" + b.String() + "[99:00] You: CLOSING DECISION\n"

	got := a.budgetedTranscript(full)
	if len(got) > 600 {
		t.Errorf("budgeted transcript is %d chars, want it trimmed", len(got))
	}
	// Meetings settle their decisions and hand out follow-ups at the end, so
	// the tail must survive; a plain truncation would discard exactly the part
	// worth keeping.
	if !strings.Contains(got, "CLOSING DECISION") {
		t.Error("the end of the meeting was dropped")
	}
	if !strings.Contains(got, "OPENING REMARK") {
		t.Error("the start of the meeting was dropped")
	}
	if !strings.Contains(got, "omitted for length") {
		t.Error("the omission should be marked so the model knows the gap exists")
	}
}

func TestBudgetedTranscriptLeavesShortInputAlone(t *testing.T) {
	a := NewAnalyzer(nil, Options{MaxTranscriptChars: 10_000})
	in := "[00:00] You: short meeting\n"
	if got := a.budgetedTranscript(in); got != in {
		t.Errorf("short transcript was modified: %q", got)
	}
}

func TestKnownPeopleMergesSources(t *testing.T) {
	a := NewAnalyzer(nil, Options{
		Owner:  "Imran Yousuf",
		Roster: []string{"Kevin", "Mona"},
		// People discovered earlier in a run, including a duplicate spelling.
		KnownPeople: func() []string { return []string{"Jeremiah", "kevin", "Carlos"} },
	})

	got := a.knownPeople()
	if len(got) == 0 || got[0] != "Imran Yousuf" {
		t.Errorf("owner should lead the list, got %v", got)
	}

	seen := map[string]int{}
	for _, n := range got {
		seen[NormalizeName(n)]++
	}
	if seen["kevin"] != 1 {
		t.Errorf("Kevin appears %d times, want 1", seen["kevin"])
	}
	for _, want := range []string{"mona", "jeremiah", "carlos"} {
		if seen[want] == 0 {
			t.Errorf("missing %q from known people: %v", want, got)
		}
	}
}

func TestKnownPeopleIsCapped(t *testing.T) {
	many := make([]string, 200)
	for i := range many {
		many[i] = string(rune('A'+i%26)) + "ricson" + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	a := NewAnalyzer(nil, Options{
		Owner:       "Imran Yousuf",
		KnownPeople: func() []string { return many },
	})
	// A list this long stops being a hint and becomes noise in the prompt.
	if got := len(a.knownPeople()); got > 60 {
		t.Errorf("known people = %d, want it capped at 60", got)
	}
}

func TestSanitizeNameRejectsExcludedAndOrdinaryWords(t *testing.T) {
	a := NewAnalyzer(nil, Options{
		Roster:       []string{"Kevin"},
		ExcludeNames: []string{"Opal", "Optimizely"},
	})

	tests := []struct{ in, want string }{
		{"Kevin", "Kevin"},
		// Product names read like names in conversation.
		{"Opal", ""},
		{"Optimizely", ""},
		// Ordinary vocabulary a model might return.
		{"Thanks", ""},
		{"Everyone", ""},
		{"", ""},
		// The roster's spelling wins, so one person does not accumulate a
		// spelling per meeting.
		{"Kevn", "Kevin"},
	}
	for _, tt := range tests {
		if got := a.sanitizeName(tt.in); got != tt.want {
			t.Errorf("sanitizeName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestAnalysisClampsTimesToTheRecording(t *testing.T) {
	a := &Analysis{Topics: []Topic{
		{Name: "late", StartTime: 500, EndTime: 9999},
		{Name: "early", StartTime: -10, EndTime: 100},
		{Name: "inverted", StartTime: 300, EndTime: 200},
	}}
	a.clampTo(600)

	// Ordered by start time, and inside the recording.
	if a.Topics[0].Name != "early" {
		t.Errorf("topics not sorted by start: %s first", a.Topics[0].Name)
	}
	if a.Topics[0].StartTime < 0 {
		t.Errorf("negative start survived: %v", a.Topics[0].StartTime)
	}
	for _, tp := range a.Topics {
		if tp.EndTime > 600 {
			t.Errorf("%s ends at %v, past the recording", tp.Name, tp.EndTime)
		}
		if tp.EndTime < tp.StartTime {
			t.Errorf("%s ends before it starts", tp.Name)
		}
	}
}

func TestResultNameMapRespectsConfidence(t *testing.T) {
	r := &Result{
		Session: build([3]string{"You", SourceMic, "hello"}),
		Identities: []SpeakerIdentity{
			{Label: "You", Name: "Imran", Confidence: 1.0},
			{Label: "Person 1", Name: "Kevin", Confidence: 0.8},
			{Label: "Person 2", Name: "Mona", Confidence: 0.5},
			{Label: "Person 3", Name: "", Confidence: 0.9},
		},
	}
	got := r.NameMap(0.7)
	if len(got) != 2 {
		t.Fatalf("name map = %v, want 2 entries", got)
	}
	if got["Person 2"] != "" {
		t.Error("a low-confidence identity was included")
	}
	if got["Person 3"] != "" {
		t.Error("an empty name was included")
	}
}

func TestNamedTranscriptSubstitutesResolvedNames(t *testing.T) {
	s := build(
		[3]string{"You", SourceMic, "Can you take this one?"},
		[3]string{"Person 1", SourceMonitor, "Yes, I will update the schema."},
		[3]string{"Person 2", SourceMonitor, "I have nothing to add."},
	)
	r := &Result{
		Session: s,
		Identities: []SpeakerIdentity{
			{Label: "You", Name: "Imran", Confidence: 1.0},
			{Label: "Person 1", Name: "Kevin", Confidence: 0.9},
			// Person 2 stays unresolved.
		},
	}

	got := r.NamedTranscript(0.7)
	if !strings.Contains(got, "Kevin: Yes, I will update the schema.") {
		t.Errorf("resolved name not substituted:\n%s", got)
	}
	if !strings.Contains(got, "Imran: Can you take this one?") {
		t.Errorf("owner not substituted:\n%s", got)
	}
	// An unresolved speaker keeps its label rather than being guessed at.
	if !strings.Contains(got, "Person 2: I have nothing to add.") {
		t.Errorf("unresolved speaker should keep its label:\n%s", got)
	}
}

func TestQuoteInTranscript(t *testing.T) {
	s := build(
		[3]string{"You", SourceMic, "Let's ship it on Friday."},
		[3]string{"Person 1", SourceMonitor, "Agreed, Friday works."},
	)

	if !QuoteInTranscript(s, "Let's ship it on Friday.") {
		t.Error("a verbatim quote should be found")
	}
	// Whitespace and case differences are the transcriber's, not the speaker's.
	if !QuoteInTranscript(s, "let's   ship it on friday.") {
		t.Error("a quote differing only in spacing and case should be found")
	}
	if QuoteInTranscript(s, "Let's ship it on Monday.") {
		t.Error("an invented quote must not be reported as found")
	}
	if QuoteInTranscript(s, "   ") {
		t.Error("an empty quote is not a quote")
	}
}
