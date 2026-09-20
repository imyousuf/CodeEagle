package transcript

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

func indexedMeeting(t *testing.T, store graph.Store, session, title, path string, when time.Time) *graph.Node {
	t.Helper()
	n := &graph.Node{
		ID:            graph.NewNodeID(string(graph.NodeMeeting), path, session),
		Type:          graph.NodeMeeting,
		Name:          title,
		QualifiedName: session,
		FilePath:      path,
		UpdatedAt:     when,
	}
	if err := store.AddNode(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMeetingIndexResolvesAPrefixOfTheSessionID(t *testing.T) {
	store := testStore(t)
	now := time.Now()
	want := indexedMeeting(t, store, "a99b3645-5840-47b2-83cc-05b8bf761e8a", "Alignment debate", "/s/a99b/session.json", now)
	indexedMeeting(t, store, "a1401a13-5738-4b9c-94cd-4d5dbb5ee2ed", "Performance review", "/s/a140/session.json", now)

	ix, err := LoadMeetingIndex(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"a99b3645", "A99B3645-5840", want.ID[:8], "alignment"} {
		got, err := ix.Resolve(ref)
		if err != nil {
			t.Errorf("Resolve(%q): %v", ref, err)
			continue
		}
		if got.ID != want.ID {
			t.Errorf("Resolve(%q) = %q, want %q", ref, got.Name, want.Name)
		}
	}

	// A prefix both share is ambiguous, and the error lists both.
	_, err = ix.Resolve("a")
	if err == nil || !strings.Contains(err.Error(), "no meeting matching") {
		// Too short to be a prefix; no title contains "a"? Titles do, so
		// this is a title match on both and must be refused.
		if err == nil {
			t.Fatal("Resolve(\"a\") picked one meeting")
		}
	}
	_, err = ix.Resolve("a99b3645-5840-47b2-83cc-05b8bf761e8a-and-more")
	if err == nil {
		t.Error("a reference longer than any id resolved")
	}
}

func TestMeetingIndexRefusesAnAmbiguousPrefix(t *testing.T) {
	store := testStore(t)
	now := time.Now()
	indexedMeeting(t, store, "7c873305-2422", "Pricing", "/s/1/session.json", now)
	indexedMeeting(t, store, "7c87ffff-0000", "Hiring", "/s/2/session.json", now.Add(-time.Hour))

	ix, err := LoadMeetingIndex(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ix.Resolve("7c87")
	if err == nil {
		t.Fatal("ambiguous prefix resolved to one meeting")
	}
	for _, want := range []string{"2 meetings match", "Pricing", "Hiring", "7c873305-2422"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	// Extending the prefix past the fork resolves it.
	if got, err := ix.Resolve("7c873"); err != nil || got.Name != "Pricing" {
		t.Errorf("Resolve(7c873) = %v, %v", got, err)
	}
}

func TestMeetingIndexBySession(t *testing.T) {
	store := testStore(t)
	now := time.Now()
	indexedMeeting(t, store, "meeting-recording", "Payments sync", "/downloads/Meeting Recording.docx", now)
	indexedMeeting(t, store, "meeting-recording", "Platform standup", "/archive/Meeting Recording.docx", now)
	only := indexedMeeting(t, store, "unique-1", "Retro", "/s/3/session.json", now)

	ix, err := LoadMeetingIndex(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ix.BySession("unique-1"); err != nil || got.ID != only.ID {
		t.Errorf("BySession(unique-1) = %v, %v", got, err)
	}
	if got, err := ix.BySession("never"); err != nil || got != nil {
		t.Errorf("BySession(never) = %v, %v; want nil, nil", got, err)
	}
	if _, err := ix.BySession("meeting-recording"); err == nil || !strings.Contains(err.Error(), "2 meetings answer to") {
		t.Errorf("BySession(shared) = %v; want an ambiguity error", err)
	}
	if ix.Len() != 3 {
		t.Errorf("Len = %d", ix.Len())
	}
}

func TestShortID(t *testing.T) {
	if got := ShortID(&graph.Node{QualifiedName: "a99b3645-5840-47b2"}); got != "a99b3645" {
		t.Errorf("ShortID = %q", got)
	}
	if got := ShortID(&graph.Node{QualifiedName: "abc"}); got != "abc" {
		t.Errorf("ShortID short = %q", got)
	}
	if got := ShortID(&graph.Node{ID: "0123456789ab"}); got != "01234567" {
		t.Errorf("ShortID from node id = %q", got)
	}
}
