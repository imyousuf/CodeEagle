package transcript

import (
	"testing"
)

// TestIsOwnerName covers recognizing the host's name however it is spelled.
func TestIsOwnerName(t *testing.T) {
	a := &Analyzer{opts: Options{
		Owner:        "Imran Yousuf",
		OwnerAliases: []string{"Imron", "Emran"},
	}}

	tests := []struct {
		name string
		want bool
		why  string
	}{
		{"Imran Yousuf", true, "the host's full name"},
		{"Imran", true, "the host's given name, with no other Imran to confuse it with"},
		{"Imron", true, "a configured alias"},
		{"Emran Yousuf", true, "an alias with the surname"},
		// A different person who shares the given name.
		{"Imran Khan", false, "a different surname is a different person"},
		{"Kevin Mitchell", false, "an unrelated name"},
		{"", false, "nothing"},
	}

	for _, tt := range tests {
		t.Run(tt.why, func(t *testing.T) {
			if got := a.isOwnerName(tt.name); got != tt.want {
				t.Errorf("isOwnerName(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

// TestIsOwnerNameWithoutOwner covers a configuration that names no host, where
// nothing should be treated as one.
func TestIsOwnerNameWithoutOwner(t *testing.T) {
	a := &Analyzer{opts: Options{}}
	if a.isOwnerName("Imran Yousuf") {
		t.Error("a name matched the host when no host is configured")
	}
}

// TestOwnerAnchorBeatsAClaimedName covers the rule that matters most here.
//
// The microphone is the host by construction, so a second voice claiming the
// host's name is a different person who shares it — or a mishearing. Accepting
// it would attribute a stranger's words to the host and record the host as
// having attended their own meeting twice.
func TestOwnerAnchorBeatsAClaimedName(t *testing.T) {
	s := build(
		[3]string{"You", SourceMic, "Morning. Let's start with the migration."},
		[3]string{"Person 1", SourceMonitor, "Hi, this is Imran, I'll take the schema work."},
		[3]string{"Person 1", SourceMonitor, "I can have it up by Thursday afternoon."},
	)

	// The model does what a model does: it believes the introduction.
	client := &stubStructuredClient{reply: `{"speakers":[
	    {"label":"Person 1","name":"Imran","confidence":0.95,"evidence":"this is Imran"}
	]}`}

	a := NewAnalyzer(client, Options{Owner: "Imran Yousuf", MinConfidence: 0.7})
	identities, err := a.Identify(t.Context(), s, &Usage{})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}

	byLabel := make(map[string]SpeakerIdentity, len(identities))
	for _, id := range identities {
		byLabel[id.Label] = id
	}

	host, ok := byLabel["You"]
	if !ok {
		t.Fatal("the microphone speaker was not identified")
	}
	if host.Name != "Imran Yousuf" || host.Method != MethodOwnerAnchor {
		t.Errorf("host = %q via %q, want Imran Yousuf via the owner anchor", host.Name, host.Method)
	}

	other, ok := byLabel["Person 1"]
	if !ok {
		t.Fatal("Person 1 was dropped rather than left unidentified")
	}
	if other.Name != "" {
		t.Errorf("Person 1 was identified as %q; the host is already anchored to another label", other.Name)
	}
	if other.Confidence != 0 {
		t.Errorf("Person 1 kept confidence %.2f after its name was rejected", other.Confidence)
	}
}

// TestOwnerAnchorAllowsADifferentPersonOfTheSameGivenName covers not
// over-correcting: a colleague who genuinely shares the host's first name but
// has their own surname is a real person and must still be identified.
func TestOwnerAnchorAllowsADifferentPersonOfTheSameGivenName(t *testing.T) {
	s := build(
		[3]string{"You", SourceMic, "Morning. Over to you."},
		[3]string{"Person 1", SourceMonitor, "Thanks. I'll pick up the schema work."},
		[3]string{"Person 1", SourceMonitor, "It should be ready before the Thursday release."},
	)

	client := &stubStructuredClient{reply: `{"speakers":[
	    {"label":"Person 1","name":"Imran Khan","confidence":0.9,"evidence":"introduced himself"}
	]}`}

	a := NewAnalyzer(client, Options{Owner: "Imran Yousuf", MinConfidence: 0.7})
	identities, err := a.Identify(t.Context(), s, &Usage{})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}

	for _, id := range identities {
		if id.Label == "Person 1" {
			if id.Name != "Imran Khan" {
				t.Errorf("Person 1 = %q, want Imran Khan: a different surname is a different person", id.Name)
			}
			return
		}
	}
	t.Fatal("Person 1 was not in the identities")
}
