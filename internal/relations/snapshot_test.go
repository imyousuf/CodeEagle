package relations

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSnapshotRoundTripsUnderItsStamp(t *testing.T) {
	store, _, _ := incrementalStores(t)
	ctx := context.Background()
	subjects, err := LoadSubjects(ctx, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	rels := map[string]*Relation{}
	if _, err := EnsureRelations(ctx, store, subjects, rels, day); err != nil {
		t.Fatal(err)
	}
	lib, err := LoadLibrary(ctx, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "library.snap")
	stamp := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	if err := lib.SaveSnapshot(path, stamp); err != nil {
		t.Fatal(err)
	}

	got, ok := LoadSnapshot(path, stamp)
	if !ok {
		t.Fatal("snapshot not read back")
	}
	subj, want := got.Subjects["docs/retry.md"], lib.Subjects["docs/retry.md"]
	if subj == nil || len(subj.Sections) != len(want.Sections) {
		t.Fatalf("subject after a round trip = %+v", subj)
	}
	for i, sec := range subj.Sections {
		if sec.Text != want.Sections[i].Text || sec.Hash != want.Sections[i].Hash || sec.HeadingPath != want.Sections[i].HeadingPath {
			t.Errorf("section %d after a round trip = %+v, want %+v", i, sec, want.Sections[i])
		}
	}
	if got.Relations["docs/retry.md"] == nil || len(got.Meetings) != len(lib.Meetings) {
		t.Errorf("relations %d, meetings %d; want %d and %d", len(got.Relations), len(got.Meetings), len(lib.Relations), len(lib.Meetings))
	}
	if _, ok := LoadSnapshot(path, stamp.Add(time.Second)); ok {
		t.Error("a snapshot taken under another database state was used")
	}
	if _, ok := LoadSnapshot(filepath.Join(t.TempDir(), "none"), stamp); ok {
		t.Error("a missing snapshot was reported read")
	}
}
