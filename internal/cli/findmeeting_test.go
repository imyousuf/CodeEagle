package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

// meetingNode builds a meeting whose qualified name is its session identifier,
// which is how the writer records it.
func meetingNode(t *testing.T, store graph.Store, sessionID, title, path string) *graph.Node {
	t.Helper()
	n := &graph.Node{
		ID:            graph.NewNodeID(string(graph.NodeMeeting), path, sessionID),
		Type:          graph.NodeMeeting,
		Name:          title,
		QualifiedName: sessionID,
		FilePath:      path,
	}
	if err := store.AddNode(context.Background(), n); err != nil {
		t.Fatalf("add meeting: %v", err)
	}
	return n
}

func meetingStore(t *testing.T) graph.Store {
	t.Helper()
	store, err := embedded.NewStore(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// TestFindMeetingRefusesAnAmbiguousReference covers two unrelated recordings
// answering to the same identifier.
//
// Formats that carry no identifier of their own get one derived from the
// filename, so two exports named alike — which conferencing tools produce,
// reusing a generic name per recurring meeting — collide. Showing whichever
// came back first would show the wrong meeting without saying so.
func TestFindMeetingRefusesAnAmbiguousReference(t *testing.T) {
	store := meetingStore(t)
	ctx := context.Background()

	first := meetingNode(t, store, "meeting-recording", "Payments sync", "/downloads/Meeting Recording.docx")
	meetingNode(t, store, "meeting-recording", "Platform standup", "/archive/Meeting Recording.docx")

	_, err := findMeeting(ctx, store, "meeting-recording")
	if err == nil {
		t.Fatal("findMeeting picked one of two meetings sharing an identifier")
	}
	for _, want := range []string{"2 meetings", "Payments sync", "Platform standup"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}

	// The full node id hashes the file path, so it stays unambiguous and must
	// still resolve — that is what the error tells the reader to use.
	got, err := findMeeting(ctx, store, first.ID)
	if err != nil {
		t.Fatalf("findMeeting by node id: %v", err)
	}
	if got.Name != "Payments sync" {
		t.Errorf("by node id = %q, want Payments sync", got.Name)
	}
}

// TestFindMeetingResolvesAUniqueReference covers the ordinary case, which must
// keep working.
func TestFindMeetingResolvesAUniqueReference(t *testing.T) {
	store := meetingStore(t)
	ctx := context.Background()

	meetingNode(t, store, "abc-123", "Retention job", "/rec/abc-123/session.json")
	meetingNode(t, store, "def-456", "Platform standup", "/rec/def-456/session.json")

	got, err := findMeeting(ctx, store, "abc-123")
	if err != nil {
		t.Fatalf("findMeeting: %v", err)
	}
	if got.Name != "Retention job" {
		t.Errorf("got %q, want Retention job", got.Name)
	}

	// Partial matching on the title is unchanged.
	if got, err = findMeeting(ctx, store, "standup"); err != nil || got.Name != "Platform standup" {
		t.Errorf("partial match = %v, %v; want Platform standup", got, err)
	}
	if _, err := findMeeting(ctx, store, "nothing like this"); err == nil {
		t.Error("findMeeting resolved a reference matching nothing")
	}
}

// TestFindMeetingResolvesAPrefix: session directories are named by the
// identifier, so its first characters are what people copy from a listing.
func TestFindMeetingResolvesAPrefix(t *testing.T) {
	store := meetingStore(t)
	ctx := context.Background()

	meetingNode(t, store, "a99b3645-5840-47b2-83cc-05b8bf761e8a", "Alignment debate", "/rec/a99b/session.json")
	meetingNode(t, store, "7c873305-2422-4fd7-9368-2292f58d2e51", "Pricing and AGI", "/rec/7c87/session.json")

	got, err := findMeeting(ctx, store, "a99b3645")
	if err != nil {
		t.Fatalf("prefix: %v", err)
	}
	if got.Name != "Alignment debate" {
		t.Errorf("prefix resolved to %q", got.Name)
	}
	// Too short to be an identifier and matching no title.
	if _, err := findMeeting(ctx, store, "a99"); err == nil {
		t.Error("a three-character reference resolved")
	}
}

// TestFindMeetingByIDRefusesAnAmbiguousReference covers the series threading
// path. Attaching a series to the wrong meeting is worse than not attaching
// it, so an ambiguous identifier is reported rather than resolved.
func TestFindMeetingByIDRefusesAnAmbiguousReference(t *testing.T) {
	store := meetingStore(t)
	ctx := context.Background()

	meetingNode(t, store, "meeting-recording", "Payments sync", "/downloads/Meeting Recording.docx")
	meetingNode(t, store, "meeting-recording", "Platform standup", "/archive/Meeting Recording.docx")

	if _, err := findMeetingByID(ctx, store, "meeting-recording"); err == nil {
		t.Fatal("findMeetingByID picked one of two meetings sharing an identifier")
	}

	// A session identifier nobody has is not an error; it means no previous
	// instance of this series.
	got, err := findMeetingByID(ctx, store, "never-seen")
	if err != nil || got != nil {
		t.Errorf("unknown id = %v, %v; want nil, nil", got, err)
	}
}
