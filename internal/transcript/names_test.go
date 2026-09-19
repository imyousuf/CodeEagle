package transcript

import "testing"

func TestPlausibleName(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"Kevin", true},
		{"Mona", true},
		{"O'Brien", true},
		{"Jean-Luc", true},
		{"Imran Yousuf", true},
		// Sentence-initial capitals are the dominant false positive.
		{"Wait", false},
		{"Thanks", false},
		{"Alright", false},
		{"Anyways", false},
		{"Everyone", false},
		{"Guys", false},
		{"I'm", false},
		{"That's", false},
		{"", false},
		{"kevin", false},   // must be capitalized in the source
		{"K", false},       // too short
		{"Kevin1", false},  // digits are not names
		{"A B C D", false}, // too many parts
	}
	for _, tt := range tests {
		if got := PlausibleName(tt.in); got != tt.want {
			t.Errorf("PlausibleName(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestCleanName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"Kevin", "Kevin"},
		// Speech recognition appends unrelated words to names.
		{"Evan Fight", "Evan"},
		// South Asian honorifics are not surnames.
		{"Rupad Bai", "Rupad"},
		{"Imran Bhai", "Imran"},
		{"Nikita Ji", "Nikita"},
		{"Dr Mona", "Mona"},
		{"Mr Kevin", "Kevin"},
		// A bare honorific names nobody.
		{"Bhai", ""},
		{"Sir", ""},
		// Genuine two-part names survive.
		{"Imran Yousuf", "Imran Yousuf"},
		{"Wait", ""},
	}
	for _, tt := range tests {
		if got := CleanName(tt.in); got != tt.want {
			t.Errorf("CleanName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSameName(t *testing.T) {
	same := [][2]string{
		{"Kevin", "kevin"},
		{"Imran", "Imran Yousuf"},
		// The transcriber mishears the same person between recordings.
		{"Imran", "Imron"},
		{"Dimitri", "Dimitro"},
		// Familiar and formal forms.
		{"Mike", "Michael"},
		{"Ben", "Benjamin"},
		{"Bill", "Will"},
	}
	for _, p := range same {
		if !SameName(p[0], p[1]) {
			t.Errorf("SameName(%q, %q) = false, want true", p[0], p[1])
		}
	}

	// Merging two distinct people is the costly error, so short or
	// differently-initialed names must stay apart.
	different := [][2]string{
		{"Jon", "Ron"},
		{"Dan", "Dana"},
		{"Kevin", "Devin"},
		{"Mona", "Nina"},
		{"Alex", "Alice"},
		{"Carlos", "Charles"},
		{"", "Kevin"},
	}
	for _, p := range different {
		if SameName(p[0], p[1]) {
			t.Errorf("SameName(%q, %q) = true, want false", p[0], p[1])
		}
	}
}

func TestNormalizeName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"  Kevin  ", "kevin"},
		{"Jean-Luc", "jean luc"},
		{"O'Brien", "obrien"},
		{"Imran   Yousuf", "imran yousuf"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := NormalizeName(tt.in); got != tt.want {
			t.Errorf("NormalizeName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestJaroWinkler(t *testing.T) {
	if got := jaroWinkler("imran", "imran"); got != 1 {
		t.Errorf("identical strings = %v, want 1", got)
	}
	// A one-vowel substitution mid-name must stay above the variant threshold.
	if got := jaroWinkler("imran", "imron"); got < asrVariantThreshold {
		t.Errorf("jaroWinkler(imran, imron) = %v, want >= %v", got, asrVariantThreshold)
	}
	// Unrelated names must fall well below it.
	if got := jaroWinkler("kevin", "mona"); got >= 0.6 {
		t.Errorf("jaroWinkler(kevin, mona) = %v, want < 0.6", got)
	}
	if got := jaroWinkler("", "kevin"); got != 0 {
		t.Errorf("empty string = %v, want 0", got)
	}
}
