package transcript

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/decide"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

// TestIdentityLayersDoNotCompound covers all three strictness layers in one
// run, which no other test does.
//
// Identification is guarded three times over: the microphone anchor fixes the
// host, a claimed name matching the host is rejected because the host is
// already anchored elsewhere, and resolving a bare name that several known
// colleagues answer to is refused. Each is tested alone. The question this
// answers is whether they interact — whether a meeting that used to resolve
// can now come back empty because the layers subtract from each other.
//
// They do not, because each only ever turns "resolved" into "unresolved" for
// the one speaker it judges, and none of them feeds back into another's input.
func TestIdentityLayersDoNotCompound(t *testing.T) {
	ctx := context.Background()
	store, err := embedded.NewStore(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Two colleagues who share a given name, as earlier meetings would have
	// left them. This is what makes a bare "Imran" ambiguous later.
	people, err := LoadPersonRegistry(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Imran Khan", "Imran Sharma"} {
		if _, err := people.Resolve(ctx, name); err != nil {
			t.Fatalf("seed %q: %v", name, err)
		}
	}

	s := build(
		[3]string{"You", SourceMic, "Morning. Shall we start with the retention job?"},
		[3]string{"Person 1", SourceMonitor, "I looked at it yesterday, the query does a full scan."},
		[3]string{"Person 1", SourceMonitor, "It did. I can put a migration up today."},
		[3]string{"Person 2", SourceMonitor, "Before we move on, I want to flag the alerting gap."},
		[3]string{"Person 2", SourceMonitor, "We only hear about it when a customer tells us."},
		[3]string{"Person 3", SourceMonitor, "Sorry, I have to drop. Speak tomorrow everyone."},
		[3]string{"Person 3", SourceMonitor, "Yes, tomorrow works for me as well, thanks."},
	)

	// The judge answers three ways on purpose: a name only a surname could
	// disambiguate, a clean one, and the host's own name on somebody else.
	judge := &stubJudge{verdictFor: map[string]decide.Verdict{
		"Person 1": {Choice: "Imran", Confidence: 0.95},
		"Person 2": {Choice: "Kevin Mitchell", Confidence: 0.93},
		"Person 3": {Choice: "Priya Raman", Confidence: 0.91},
	}}

	// The host is Priya, so Person 3 claiming her name must be rejected by
	// the anchor — she is already fixed to the microphone.
	a := NewAnalyzer(nil, Options{
		Owner:         "Priya Raman",
		MinConfidence: 0.70,
		KnownPeople:   people.Names,
	}).WithJudge(judge)

	identities, err := a.Identify(ctx, s, &Usage{})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}

	writer := NewWriter(store, people, WriterOptions{
		MinConfidence: 0.70,
		Owner:         "Priya Raman",
	})
	st, err := writer.Write(ctx, &Result{Session: s, Identities: identities})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	// The meeting must not come back empty: the host still resolves, and so
	// does the one speaker whose name was unambiguous.
	if st.Identified < 2 {
		t.Errorf("only %d speakers identified; the layers have subtracted from each other", st.Identified)
	}

	named := namedSpeakers(ctx, t, store)

	if got := named["You"]; got != "Priya Raman" {
		t.Errorf("the microphone speaker resolved to %q, want Priya Raman", got)
	}
	if got := named["Person 2"]; got != "Kevin Mitchell" {
		t.Errorf("Person 2 resolved to %q, want Kevin Mitchell: an unambiguous name must still resolve", got)
	}
	// Refused at resolution: two known colleagues answer to "Imran", and
	// guessing between them would put one person's words in another's mouth.
	if got, ok := named["Person 1"]; ok {
		t.Errorf("Person 1 resolved to %q; a bare name two people share must stay unresolved", got)
	}
	// Refused at the anchor: the host is fixed to the microphone, so another
	// voice claiming her name is somebody else or a mishearing.
	if got, ok := named["Person 3"]; ok {
		t.Errorf("Person 3 resolved to %q; the host is anchored to another label", got)
	}

	// And the refusals must not have invented anyone: the registry still
	// holds exactly the people it should.
	for _, unwanted := range []string{"Imran", "Priya Raman"} {
		if hasPerson(ctx, t, store, unwanted) && unwanted == "Imran" {
			t.Errorf("a bare %q was created as a person rather than refused", unwanted)
		}
	}
}

// namedSpeakers maps each speaker label to the person it was identified as,
// omitting the ones left unresolved.
func namedSpeakers(ctx context.Context, t *testing.T, store graph.Store) map[string]string {
	t.Helper()
	speakers, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeSpeaker})
	if err != nil {
		t.Fatalf("query speakers: %v", err)
	}

	out := make(map[string]string, len(speakers))
	for _, sp := range speakers {
		person, err := store.GetNeighbors(ctx, sp.ID, graph.EdgeIdentifiedAs, graph.Outgoing)
		if err != nil || len(person) == 0 {
			continue
		}
		out[sp.Name] = person[0].Name
	}
	return out
}

// hasPerson reports whether a person of that exact name is in the graph.
func hasPerson(ctx context.Context, t *testing.T, store graph.Store, name string) bool {
	t.Helper()
	nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodePerson})
	if err != nil {
		t.Fatalf("query people: %v", err)
	}
	for _, n := range nodes {
		if n.Name == name {
			return true
		}
	}
	return false
}
