package relations

import (
	"sort"
	"testing"
)

func TestWithinOneEdit(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want bool
	}{
		{"budget", "budget", true},
		{"budget", "budgt", true},   // deletion
		{"budget", "budgest", true}, // insertion
		{"budget", "bidget", true},  // substitution
		{"budget", "bidgt", false},  // two edits
		{"budget", "bud", false},
	} {
		if got := withinOneEdit(tt.a, tt.b); got != tt.want {
			t.Errorf("withinOneEdit(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

// TestFuzzyIndexSparesShortWords covers the length floor on tolerance: one
// edit turns a short word into another real word ("cap" and "car").
func TestFuzzyIndexSparesShortWords(t *testing.T) {
	f := newFuzzyIndex()
	for _, w := range []string{"markbench", "cap", "car", "budget"} {
		f.add(w)
	}
	got := f.near("markbinch")
	if len(got) != 1 || got[0] != "markbench" {
		t.Errorf("near(markbinch) = %v, want markbench", got)
	}
	if got := f.near("cat"); len(got) != 0 {
		t.Errorf("a three-letter word matched with an edit: %v", got)
	}
	if got := f.near("car"); len(got) != 1 {
		t.Errorf("an exact short word was not found: %v", got)
	}
}

func TestIdentifiersExtracted(t *testing.T) {
	ids := identifiers("See PLAT-412 and https://example.com/spec, retry_policy.go, PR #1234, RetryBudget, retry_budget_v2.")
	var got []string
	for id := range ids {
		got = append(got, id)
	}
	sort.Strings(got)
	for _, want := range []string{"plat-412", "https://example.com/spec", "retry_policy.go", "#1234", "retrybudget", "retry_budget_v2"} {
		if !ids[want] {
			t.Errorf("missing %q in %v", want, got)
		}
	}
}
