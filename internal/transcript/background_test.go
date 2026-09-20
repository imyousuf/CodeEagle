package transcript

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/decide"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

// broadcastSession has a voice nobody ever answers — the shape of a television
// left running — alongside two people talking to each other.
func broadcastSession() *Session {
	return build(
		[3]string{"You", SourceMic, "Morning. Shall we start with the retention job?"},
		[3]string{"Person 1", SourceMonitor, "I looked at it yesterday, the query does a full scan."},
		[3]string{"You", SourceMic, "Right. Kevin, can you put a migration up today?"},
		[3]string{"Person 1", SourceMonitor, "Yes, it will be ready before the Thursday release."},
		[3]string{"Person 7", SourceMonitor, "Are you familiar with this chili? The scorpion pepper."},
		[3]string{"Person 7", SourceMonitor, "It has a fruity heat that builds at the back of the throat."},
		[3]string{"Person 7", SourceMonitor, "Now this next sauce is the one that people fear the most."},
	)
}

// TestBackgroundCandidatesVetoPeople covers the deterministic filter that runs
// before any model call.
//
// A voice that was addressed and answered, or that traded turns with several
// others, is taking part in a conversation. A television does neither. Both of
// the known model mistakes fail this veto, which is why it exists.
func TestBackgroundCandidatesVetoPeople(t *testing.T) {
	s := broadcastSession()
	a := NewAnalyzer(nil, Options{Owner: "Imran Yousuf"})

	candidates := a.backgroundCandidates(s, ExtractHints(s))

	byLabel := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		byLabel[c.Stat.Label] = true
	}

	if !byLabel["Person 7"] {
		t.Error("the voice nobody answered was not put forward")
	}
	if byLabel["Person 1"] {
		t.Error("a voice that was addressed and answered was put forward")
	}
	if byLabel["You"] {
		t.Error("the host was put forward; the microphone is a person by construction")
	}
}

// TestScreenBackgroundMarksAndGates covers the judgment and the bar it has to
// clear.
func TestScreenBackgroundMarksAndGates(t *testing.T) {
	tests := []struct {
		name       string
		confidence float64
		choice     string
		wantMarked bool
	}{
		{"a confident judgment marks it", 0.92, optionBackground, true},
		{"just under the bar does not", 0.84, optionBackground, false},
		{"exactly at the bar does", 0.85, optionBackground, true},
		// A label holding both meeting speech and bleed keeps its real words.
		{"mixed is kept as a participant", 0.95, optionMixed, false},
		{"a participant is kept", 0.95, optionParticipant, false},
		{"not knowing is not a verdict", 0.95, optionCannotTell, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			judge := &stubJudge{verdictFor: map[string]decide.Verdict{
				"Person 7": {Choice: tt.choice, Confidence: tt.confidence},
			}}
			a := NewAnalyzer(nil, Options{Owner: "Imran Yousuf"}).WithJudge(judge)

			marks, err := a.screenBackground(context.Background(), broadcastSession())
			if err != nil {
				t.Fatalf("screenBackground: %v", err)
			}
			if _, marked := marks["Person 7"]; marked != tt.wantMarked {
				t.Errorf("marked = %v, want %v", marked, tt.wantMarked)
			}
		})
	}
}

// TestScreenBackgroundSkipsNamedTranscripts covers a conferencing export,
// which lists the people who were in the call. There is nothing to screen, and
// nothing to pay for.
func TestScreenBackgroundSkipsNamedTranscripts(t *testing.T) {
	s := broadcastSession()
	s.NamedSpeakers = true

	judge := &stubJudge{}
	a := NewAnalyzer(nil, Options{Owner: "Imran Yousuf"}).WithJudge(judge)

	if _, err := a.screenBackground(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if judge.calls != 0 {
		t.Errorf("screened a named transcript, costing %d calls", judge.calls)
	}
}

// TestScreenBackgroundInertWithoutJudge covers the default: no decision model
// configured means nothing changes at all.
func TestScreenBackgroundInertWithoutJudge(t *testing.T) {
	a := NewAnalyzer(nil, Options{Owner: "Imran Yousuf"})
	marks, err := a.screenBackground(context.Background(), broadcastSession())
	if err != nil {
		t.Fatal(err)
	}
	if len(marks) != 0 {
		t.Errorf("marked %d voices with no decision model configured", len(marks))
	}
}

// TestBackgroundSpeakerIsNeverLinked covers what reaches the graph: the node
// stays, so the recording is described honestly and a human can overrule the
// judgment, but it is never named and never counted as an attendee.
func TestBackgroundSpeakerIsNeverLinked(t *testing.T) {
	ctx := context.Background()
	store, err := embedded.NewStore(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	judge := &stubJudge{verdictFor: map[string]decide.Verdict{
		"Person 7": {Choice: optionBackground, Confidence: 0.92},
		"Person 1": {Choice: "Kevin Mitchell", Confidence: 0.95},
	}}
	a := NewAnalyzer(nil, Options{Owner: "Imran Yousuf", MinConfidence: 0.70}).WithJudge(judge)

	s := broadcastSession()
	identities, err := a.Identify(ctx, s, &Usage{})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}

	people, err := LoadPersonRegistry(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	writer := NewWriter(store, people, WriterOptions{MinConfidence: 0.70, Owner: "Imran Yousuf"})
	st, err := writer.Write(ctx, &Result{Session: s, Identities: identities})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if st.Background != 1 {
		t.Errorf("counted %d background voices, want 1", st.Background)
	}

	speakers, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeSpeaker})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, sp := range speakers {
		if sp.Name != "Person 7" {
			continue
		}
		found = true
		if sp.Properties[graph.PropRole] != MethodBackground {
			t.Errorf("Person 7 is not marked as background (%q)", sp.Properties[graph.PropRole])
		}
		linked, err := store.GetNeighbors(ctx, sp.ID, graph.EdgeIdentifiedAs, graph.Outgoing)
		if err != nil {
			t.Fatal(err)
		}
		if len(linked) != 0 {
			t.Errorf("a television was identified as %q", linked[0].Name)
		}
	}
	if !found {
		t.Error("the background voice was deleted rather than marked")
	}

	// And nobody was invented for it.
	persons, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodePerson})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range persons {
		if p.Name == "Person 7" {
			t.Error("a person was created for background audio")
		}
	}
}
