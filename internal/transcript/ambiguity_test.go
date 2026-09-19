package transcript

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

func newPeopleRegistry(t *testing.T) *PersonRegistry {
	t.Helper()
	store, err := embedded.NewStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	r, err := LoadPersonRegistry(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestResolveRefusesAmbiguousFirstName covers a bare first name that several
// known colleagues answer to.
//
// Names are compared on the surname only when both sides carry one, so a bare
// first name matches every colleague who shares it. Picking one would record a
// coin toss in the graph as a fact — worse, by this project's own rule, than
// leaving the speaker unidentified.
func TestResolveRefusesAmbiguousFirstName(t *testing.T) {
	r := newPeopleRegistry(t)
	ctx := context.Background()

	for _, full := range []string{"Imran Khan", "Imran Sharma"} {
		if _, err := r.Resolve(ctx, full); err != nil {
			t.Fatalf("Resolve(%q): %v", full, err)
		}
	}

	if _, err := r.Resolve(ctx, "Imran"); err == nil {
		t.Fatal("Resolve(\"Imran\") succeeded; want a refusal, since it could be either person")
	}

	// A full name is still unambiguous, and must keep resolving.
	got, err := r.Resolve(ctx, "Imran Khan")
	if err != nil {
		t.Fatalf("Resolve(\"Imran Khan\"): %v", err)
	}
	if got.Name != "Imran Khan" {
		t.Errorf("resolved to %q, want %q", got.Name, "Imran Khan")
	}
}

// TestResolveStillMatchesWhenOnlyOneCandidate covers the case the fuzzy match
// exists for: one known person, and a transcriber's misspelling of them.
func TestResolveStillMatchesWhenOnlyOneCandidate(t *testing.T) {
	r := newPeopleRegistry(t)
	ctx := context.Background()

	original, err := r.Resolve(ctx, "Imran Yousuf")
	if err != nil {
		t.Fatal(err)
	}
	again, err := r.Resolve(ctx, "Imron Yousuf")
	if err != nil {
		t.Fatalf("Resolve of a misspelling: %v", err)
	}
	if again.ID != original.ID {
		t.Errorf("misspelling became a second person: %q vs %q", again.Name, original.Name)
	}
}

// TestNamesAreMostRecentFirst covers the order identification feeds back into
// later meetings. The prompt keeps only the first sixty names, so alphabetical
// order would drop a colleague recognized last week in favour of one whose
// name happens to begin with A.
func TestNamesAreMostRecentFirst(t *testing.T) {
	r := newPeopleRegistry(t)
	ctx := context.Background()

	for _, name := range []string{"Alice Archer", "Zoe Zhang"} {
		if _, err := r.Resolve(ctx, name); err != nil {
			t.Fatal(err)
		}
	}

	names := r.Names()
	if len(names) != 2 || names[0] != "Zoe Zhang" {
		t.Fatalf("Names() = %v, want the most recent first", names)
	}

	// Confirming Alice again moves her to the front.
	if _, err := r.Resolve(ctx, "Alice Archer"); err != nil {
		t.Fatal(err)
	}
	if names = r.Names(); names[0] != "Alice Archer" {
		t.Errorf("Names() = %v, want Alice Archer first after confirming her", names)
	}
}
