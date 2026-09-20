package transcript

import (
	"path/filepath"
	"testing"
	"time"
)

func at(day int) time.Time {
	return time.Date(2026, time.January, day, 9, 0, 0, 0, time.UTC)
}

var twoTurns = [][3]string{
	{"You", "mic", "Shall we begin?"},
	{"Person 1", "system", "Yes, go ahead."},
}

// TestDiscoverSessionsWithOrdersNamedTranscripts covers merging individually
// named transcripts — the ones document indexing marked — into the discovered
// set.
//
// They must be sorted in among the discovered files, not appended after them.
// A batch feeds the people it identified into later meetings, so a recording
// processed out of order loses the names the earlier ones would have taught it,
// and `--limit` would enrich an arbitrary subset rather than the oldest.
func TestDiscoverSessionsWithOrdersNamedTranscripts(t *testing.T) {
	root := t.TempDir()
	watched := filepath.Join(root, "sessions")
	loose := filepath.Join(root, "repo")

	// Deliberately interleaved: the named ones fall either side of the
	// discovered one in time.
	january := writeSession(t, loose, "january", at(15), twoTurns...)
	june := writeSession(t, watched, "june", at(20), twoTurns...)
	september := writeSession(t, loose, "september", at(25), twoTurns...)

	got, err := DiscoverSessionsWith([]string{watched}, []string{september, january})
	if err != nil {
		t.Fatalf("DiscoverSessionsWith: %v", err)
	}

	want := []string{january, june, september}
	if len(got) != len(want) {
		t.Fatalf("got %d paths, want %d: %v", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, filepath.Base(filepath.Dir(got[i])),
				filepath.Base(filepath.Dir(want[i])))
		}
	}
}

// TestDiscoverSessionsWithDeduplicates covers a transcript that is both inside
// a configured directory and named separately, which happens whenever a
// recordings folder is also indexed as a repository. It must be indexed once.
func TestDiscoverSessionsWithDeduplicates(t *testing.T) {
	root := t.TempDir()
	standup := writeSession(t, root, "standup", at(3), twoTurns...)

	got, err := DiscoverSessionsWith([]string{root}, []string{standup, standup, ""})
	if err != nil {
		t.Fatalf("DiscoverSessionsWith: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d paths, want 1: %v", len(got), got)
	}
}

// TestDiscoverSessionsWithIgnoresUnreadable covers a named path that is not a
// transcript. Document marks are a filter, not a guarantee, and one bad entry
// must not lose the rest.
func TestDiscoverSessionsWithIgnoresUnreadable(t *testing.T) {
	root := t.TempDir()
	real := writeSession(t, root, "real", at(4), twoTurns...)
	missing := filepath.Join(root, "gone", "session.json")

	got, err := DiscoverSessionsWith(nil, []string{missing, real})
	if err != nil {
		t.Fatalf("DiscoverSessionsWith: %v", err)
	}
	if len(got) != 1 || got[0] != real {
		t.Fatalf("got %v, want just %v", got, real)
	}
}

// TestDiscoverSessionsInStillWorks covers the no-extras path, which is what
// every caller that only reads configured directories uses.
func TestDiscoverSessionsInStillWorks(t *testing.T) {
	root := t.TempDir()
	first := writeSession(t, root, "first", at(1), twoTurns...)
	second := writeSession(t, root, "second", at(2), twoTurns...)

	got, err := DiscoverSessionsIn([]string{root})
	if err != nil {
		t.Fatalf("DiscoverSessionsIn: %v", err)
	}
	if len(got) != 2 || got[0] != first || got[1] != second {
		t.Fatalf("got %v, want [%v %v]", got, first, second)
	}
}
