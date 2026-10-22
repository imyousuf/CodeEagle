package transcript

import "testing"

func TestQuoteInIgnoresLayoutAndCase(t *testing.T) {
	text := "Limits\n\nAt most   three retries\nper call."
	for _, tt := range []struct {
		quote string
		want  bool
	}{
		{"at most three retries per call", true},
		{"AT MOST THREE", true},
		{"at most five retries", false},
		{"", false},
		{"   ", false},
	} {
		if got := QuoteIn(text, tt.quote); got != tt.want {
			t.Errorf("QuoteIn(%q) = %v, want %v", tt.quote, got, tt.want)
		}
	}
}
