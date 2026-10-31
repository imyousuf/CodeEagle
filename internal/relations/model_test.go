package relations

import (
	"reflect"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

func TestRelationRoundTripsThroughANode(t *testing.T) {
	r := New("docs/retry.md")
	r.SubjectHash = "sha256:abc"
	r.Revision = 3
	r.Sections["Limits"] = SectionState{Hash: "h1", FirstSeen: day}
	r.Links["s1"] = &Verdict{SessionID: "s1", P: 0.82, Accepted: true, Judge: "jev-1.13.0",
		Signals: []string{"said"}, SigDoc: "d", SigMeeting: "m", JudgedAt: day}
	r.Evidence = []Evidence{{ID: "abc123", Kind: EvidenceDoc, Path: "docs/retry.md", HeadingPath: "Limits", Quote: "three"}}
	r.Brief = &Brief{Headline: Line{Text: "cap raised to five", Cites: []string{"abc123"}}}
	r.BriefedAt = day.Add(time.Hour)

	n, err := r.Node()
	if err != nil {
		t.Fatal(err)
	}
	if n.ID != RelationID("docs/retry.md") || n.Type != graph.NodeRelation {
		t.Errorf("node identity: %s %s", n.ID, n.Type)
	}
	// An empty file path keeps re-indexing the document from deleting the
	// relation along with the document's own nodes.
	if n.FilePath != "" {
		t.Errorf("relation node has file path %q", n.FilePath)
	}
	if n.DocComment != "cap raised to five" {
		t.Errorf("relation text = %q, want its brief", n.DocComment)
	}

	back, err := FromNode(n)
	if err != nil {
		t.Fatal(err)
	}
	if back.SubjectPath != r.SubjectPath || back.Revision != 3 || !back.BriefedAt.Equal(r.BriefedAt) {
		t.Errorf("scalars did not survive: %+v", back)
	}
	if !reflect.DeepEqual(back.Links["s1"], r.Links["s1"]) || !reflect.DeepEqual(back.Evidence, r.Evidence) {
		t.Errorf("ledger or evidence did not survive")
	}
	if back.Brief == nil || back.Brief.Headline.Text != "cap raised to five" {
		t.Errorf("brief did not survive: %+v", back.Brief)
	}
}

// TestEvidenceIDsAreStable covers citations: a brief regenerated from the
// same quote must cite the same ID, so an agent's earlier references stay
// valid.
func TestEvidenceIDsAreStable(t *testing.T) {
	rel := RelationID("docs/retry.md")
	a := Evidence{Kind: EvidenceSaid, SessionID: "s1", Start: 12.5, Quote: "We agreed on  five retries."}
	b := Evidence{Kind: EvidenceSaid, SessionID: "s1", Start: 12.5, Quote: "we agreed on five retries."}
	if EvidenceID(rel, a) != EvidenceID(rel, b) {
		t.Error("the same quote with different spacing or case got different IDs")
	}
	c := Evidence{Kind: EvidenceSaid, SessionID: "s1", Start: 99, Quote: "we agreed on five retries."}
	if EvidenceID(rel, a) == EvidenceID(rel, c) {
		t.Error("the same words at a different moment got the same ID")
	}
	if len(EvidenceID(rel, a)) != 6 {
		t.Errorf("evidence ID %q, want 6 hex characters", EvidenceID(rel, a))
	}
}

func TestJudgedRequiresBothSignaturesUnchanged(t *testing.T) {
	r := New("docs/retry.md")
	r.Links["s1"] = &Verdict{SessionID: "s1", SigDoc: "d1", SigMeeting: "m1"}
	if !r.Judged("s1", "d1", "m1") {
		t.Error("an unchanged pair was not recognised as judged")
	}
	if r.Judged("s1", "d2", "m1") || r.Judged("s1", "d1", "m2") || r.Judged("s2", "d1", "m1") {
		t.Error("a changed or unseen pair was treated as judged")
	}
}
