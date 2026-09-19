package transcript

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

// TestPreferOneExportReportsWhatItDropped covers the export that loses.
//
// One meeting often arrives twice — a caption file and a Word document of the
// same call. Keeping the richer one is right; dropping the other silently is
// not. Nothing visits that path again, so whatever was already indexed from it
// stays in the graph forever while the winner is enriched from scratch, and
// the meeting appears twice in a listing.
func TestPreferOneExportReportsWhatItDropped(t *testing.T) {
	at := func(h int) time.Time {
		return time.Date(2026, time.July, 10, h, 0, 0, 0, time.UTC)
	}

	tests := []struct {
		name           string
		exports        []export
		wantKept       int
		wantSuperseded []string
	}{
		{
			// Captions outrank a Word export: they timestamp every cue,
			// while the document records only when each utterance began.
			name: "the weaker export loses, whichever is found first",
			exports: []export{
				{path: "/rec/Standup.vtt", started: at(9)},
				{path: "/rec/Standup.docx", started: at(9)},
			},
			wantKept:       1,
			wantSuperseded: []string{"/rec/Standup.docx"},
		},
		{
			name: "and loses just the same when it is found first",
			exports: []export{
				{path: "/rec/Standup.docx", started: at(9)},
				{path: "/rec/Standup.vtt", started: at(9)},
			},
			wantKept:       1,
			wantSuperseded: []string{"/rec/Standup.docx"},
		},
		{
			name: "a recurring meeting keeps every instance",
			exports: []export{
				{path: "/rec/Standup.vtt", started: at(9)},
				{path: "/other/Standup.vtt", started: at(9).AddDate(0, 0, 60)},
			},
			wantKept: 2,
		},
		{
			name: "unrelated meetings are untouched",
			exports: []export{
				{path: "/rec/Standup.vtt", started: at(9)},
				{path: "/rec/Retro.vtt", started: at(11)},
			},
			wantKept: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kept, superseded := preferOneExportPerMeeting(tt.exports)
			if len(kept) != tt.wantKept {
				t.Errorf("kept %v, want %d of them", kept, tt.wantKept)
			}
			if len(superseded) != len(tt.wantSuperseded) {
				t.Fatalf("superseded %v, want %v", superseded, tt.wantSuperseded)
			}
			for i := range superseded {
				if superseded[i] != tt.wantSuperseded[i] {
					t.Errorf("superseded[%d] = %q, want %q", i, superseded[i], tt.wantSuperseded[i])
				}
			}
		})
	}
}

// TestSupersededExportIsClearedFromTheGraph covers the end of that story: the
// nodes indexed from the losing export are removed, so the meeting is
// represented once rather than once per format it was exported in.
func TestSupersededExportIsClearedFromTheGraph(t *testing.T) {
	ctx := t.Context()
	store, err := embedded.NewStore(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Stand in for an earlier run that indexed the caption export.
	captions := filepath.Join(t.TempDir(), "Standup.vtt")
	if err := os.WriteFile(captions, []byte(zoomVTT), 0o644); err != nil {
		t.Fatal(err)
	}
	session, err := Load(captions)
	if err != nil {
		t.Fatalf("load captions: %v", err)
	}

	people, err := LoadPersonRegistry(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	writer := NewWriter(store, people, WriterOptions{MinConfidence: 0.7, Owner: "Imran Yousuf"})
	if _, err := writer.Write(ctx, &Result{Session: session}); err != nil {
		t.Fatalf("write captions: %v", err)
	}
	if got := meetingCount(ctx, t, store); got != 1 {
		t.Fatalf("setup left %d meetings, want 1", got)
	}

	// A better export of the same meeting has now won, so this one is stale.
	ix := NewIndexer(store, nil, writer, IndexOptions{})
	if n := ix.clearSuperseded(ctx, []string{captions}); n != 1 {
		t.Errorf("cleared %d superseded exports, want 1", n)
	}
	if got := meetingCount(ctx, t, store); got != 0 {
		t.Errorf("%d meetings survived the clear, want 0", got)
	}
}

// TestClearSupersededIgnoresUnindexedPaths covers the common case: the losing
// export was never indexed, so there is nothing to clear and no reason to
// report one.
func TestClearSupersededIgnoresUnindexedPaths(t *testing.T) {
	store, err := embedded.NewStore(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ix := NewIndexer(store, nil, nil, IndexOptions{})
	if n := ix.clearSuperseded(t.Context(), []string{"/nowhere/Standup.vtt"}); n != 0 {
		t.Errorf("cleared %d, want 0 for a path that was never indexed", n)
	}
}

func meetingCount(ctx context.Context, t *testing.T, store graph.Store) int {
	t.Helper()
	nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeMeeting})
	if err != nil {
		t.Fatalf("query meetings: %v", err)
	}
	return len(nodes)
}
