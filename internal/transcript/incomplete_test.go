package transcript

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

// failingStore fails the nth write, to interrupt a projection partway.
type failingStore struct {
	graph.Store
	failOn int
	writes int
}

func (f *failingStore) AddNode(ctx context.Context, n *graph.Node) error {
	f.writes++
	if f.writes == f.failOn {
		return errors.New("storage unavailable")
	}
	return f.Store.AddNode(ctx, n)
}

// TestIncompleteMeetingIsMarked covers a projection that fails partway.
//
// Writing a meeting is several writes with no transaction around them, so a
// failure leaves a meeting with speakers but no decisions — which reads
// exactly like a meeting where nothing was decided. It self-heals on the next
// successful sync, but a recording that fails every time would sit in the
// graph looking whole.
func TestIncompleteMeetingIsMarked(t *testing.T) {
	ctx := context.Background()
	inner, err := embedded.NewStore(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()

	// Fail after the meeting node itself is written.
	store := &failingStore{Store: inner, failOn: 2}

	people, err := LoadPersonRegistry(ctx, inner)
	if err != nil {
		t.Fatal(err)
	}
	writer := NewWriter(store, people, WriterOptions{MinConfidence: 0.7, Owner: "Imran Yousuf"})

	s := build(
		[3]string{"You", SourceMic, "Shall we begin the review of the deployment?"},
		[3]string{"Person 1", SourceMonitor, "Yes, the migration is ready for Thursday."},
	)
	if _, err := writer.Write(ctx, &Result{Session: s}); err == nil {
		t.Fatal("Write succeeded against a failing store")
	}

	meetings, err := inner.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeMeeting})
	if err != nil {
		t.Fatal(err)
	}
	if len(meetings) != 1 {
		t.Fatalf("got %d meetings, want the partially-written one", len(meetings))
	}
	if meetings[0].Properties[graph.PropIncomplete] != "true" {
		t.Error("a meeting abandoned partway is not marked incomplete")
	}
}

// TestCompleteMeetingIsNotMarked covers the ordinary case: the marker is
// cleared once everything is written, so it means something when present.
func TestCompleteMeetingIsNotMarked(t *testing.T) {
	ctx := context.Background()
	store, err := embedded.NewStore(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	people, err := LoadPersonRegistry(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	writer := NewWriter(store, people, WriterOptions{MinConfidence: 0.7, Owner: "Imran Yousuf"})

	s := build(
		[3]string{"You", SourceMic, "Shall we begin the review of the deployment?"},
		[3]string{"Person 1", SourceMonitor, "Yes, the migration is ready for Thursday."},
	)
	if _, err := writer.Write(ctx, &Result{Session: s}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	meetings, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeMeeting})
	if err != nil || len(meetings) != 1 {
		t.Fatalf("got %d meetings, %v", len(meetings), err)
	}
	if got, marked := meetings[0].Properties[graph.PropIncomplete]; marked {
		t.Errorf("a fully written meeting is marked incomplete (%q)", got)
	}
}

// TestIncompleteMarkerClearsOnReindex covers the self-healing path: a meeting
// that failed once and then succeeded must not keep the mark.
func TestIncompleteMarkerClearsOnReindex(t *testing.T) {
	ctx := context.Background()
	inner, err := embedded.NewStore(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()

	people, err := LoadPersonRegistry(ctx, inner)
	if err != nil {
		t.Fatal(err)
	}
	s := build(
		[3]string{"You", SourceMic, "Shall we begin the review of the deployment?"},
		[3]string{"Person 1", SourceMonitor, "Yes, the migration is ready for Thursday."},
	)

	failing := &failingStore{Store: inner, failOn: 2}
	w1 := NewWriter(failing, people, WriterOptions{MinConfidence: 0.7, Owner: "Imran Yousuf"})
	if _, err := w1.Write(ctx, &Result{Session: s}); err == nil {
		t.Fatal("the first write should have failed")
	}

	w2 := NewWriter(inner, people, WriterOptions{MinConfidence: 0.7, Owner: "Imran Yousuf"})
	if _, err := w2.Write(ctx, &Result{Session: s}); err != nil {
		t.Fatalf("the retry failed: %v", err)
	}

	meetings, err := inner.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeMeeting})
	if err != nil || len(meetings) != 1 {
		t.Fatalf("got %d meetings, %v", len(meetings), err)
	}
	if _, marked := meetings[0].Properties[graph.PropIncomplete]; marked {
		t.Error("the marker survived a successful re-index")
	}
}
