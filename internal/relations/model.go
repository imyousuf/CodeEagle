// Package relations discovers relations between artifacts that do not link
// to each other, starting with a document and the meetings that discussed
// it, and keeps a cited brief of where each one stands.
//
// The design is in docs/artifact-relations.md. In short: signals propose
// candidate (document, meeting) pairs cheaply; the decision model judges each
// with a yes/no question, and every verdict is kept so a pair is never asked
// twice; accepted pairs are gathered into one Relation per document, whose
// evidence is anchored to its sources rather than to node IDs that change
// when a meeting is re-enriched.
package relations

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// Property keys of a Relation node.
const (
	propSubjectPath  = "subject_path"
	propSubjectHash  = "subject_hash"
	propSections     = "sections_json"
	propLinks        = "links_json"
	propEvidence     = "evidence_json"
	propBrief        = "brief_json"
	propBriefInputs  = "brief_inputs_hash"
	propBriefModel   = "brief_model"
	propBriefedAt    = "briefed_at"
	propRevision     = "revision"
	propRevisions    = "revisions_json"
	propRelationKind = "kind"
)

// KindDiscussed is the only relation kind so far: a document and the
// meetings that discussed it.
const KindDiscussed = "discussed"

// Relation is everything known about one subject document's relations.
type Relation struct {
	SubjectPath string
	SubjectHash string
	// Sections records each section's hash and when it was first seen with
	// that hash, which is the document's change history: the graph keeps
	// only the current text.
	Sections map[string]SectionState
	// Links is the verdict ledger, one entry per meeting session, accepted
	// or not.
	Links map[string]*Verdict
	// Evidence holds the quotes the brief may cite.
	Evidence []Evidence
	// Brief is the current brief, nil until one is written.
	Brief *Brief

	BriefInputsHash string
	BriefModel      string
	BriefedAt       time.Time
	// Revision increments with every new brief, so a reader can ask what
	// changed since the one it last saw.
	Revision int
	// Revisions records when recent revisions were written, so "since
	// revision N" can be turned into a time.
	Revisions []RevisionStamp
}

// RevisionStamp is when one revision of a brief was written.
type RevisionStamp struct {
	Rev int       `json:"rev"`
	At  time.Time `json:"at"`
}

// maxRevisionStamps bounds the revision history kept.
const maxRevisionStamps = 20

// Stamp records that the current revision was written at t.
func (r *Relation) Stamp(t time.Time) {
	r.Revisions = append(r.Revisions, RevisionStamp{Rev: r.Revision, At: t})
	if len(r.Revisions) > maxRevisionStamps {
		r.Revisions = r.Revisions[len(r.Revisions)-maxRevisionStamps:]
	}
}

// RevisionTime returns when revision rev was written, if it is still known.
func (r *Relation) RevisionTime(rev int) (time.Time, bool) {
	for _, s := range r.Revisions {
		if s.Rev == rev {
			return s.At, true
		}
	}
	return time.Time{}, false
}

// SectionState is a section's hash and when that hash first appeared.
type SectionState struct {
	Hash      string    `json:"hash"`
	FirstSeen time.Time `json:"first_seen"`
}

// Verdict is the decision model's judgment of one (document, meeting) pair.
// Rejected verdicts are kept: a pair is not asked again while neither the
// document's matched sections nor the meeting's transcript has changed.
type Verdict struct {
	SessionID string   `json:"session_id"`
	MeetingID string   `json:"meeting_id,omitempty"`
	P         float64  `json:"p"`
	Accepted  bool     `json:"accepted"`
	Judge     string   `json:"judge"`
	Signals   []string `json:"signals,omitempty"`
	Cosine    float64  `json:"cosine,omitempty"`
	Ranges    []Range  `json:"ranges,omitempty"`
	// Sections are the document sections the pair was proposed on, by
	// heading path: what the brief should read of the document first.
	Sections   []string  `json:"sections,omitempty"`
	SigDoc     string    `json:"sig_doc"`
	SigMeeting string    `json:"sig_meeting"`
	JudgedAt   time.Time `json:"judged_at"`
}

// Range is a span of a recording, in seconds from its start.
type Range struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Evidence kinds.
const (
	EvidenceDoc     = "doc"
	EvidenceSaid    = "said"
	EvidenceDecided = "decided"
)

// Evidence is a verbatim quote anchored to a place in its source. Meeting
// child node IDs are not stable across re-enrichment, so evidence never
// refers to them: a document quote is anchored by path and heading, a meeting
// quote by session and time.
type Evidence struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	Path        string  `json:"path,omitempty"`
	HeadingPath string  `json:"heading_path,omitempty"`
	Ordinal     int     `json:"ordinal,omitempty"`
	SectionHash string  `json:"section_hash,omitempty"`
	SessionID   string  `json:"session_id,omitempty"`
	Start       float64 `json:"start,omitempty"`
	End         float64 `json:"end,omitempty"`
	Speaker     string  `json:"speaker,omitempty"`
	Quote       string  `json:"quote"`
	// SourceMissing marks an anchor that no longer resolves. It is kept, so
	// the history is not rewritten, but never cited as current.
	SourceMissing bool `json:"source_missing,omitempty"`
}

// Brief is a relation's current-state summary. Every line cites evidence.
type Brief struct {
	Headline   Line       `json:"headline"`
	Current    []Line     `json:"current,omitempty"`
	Events     []Event    `json:"events,omitempty"`
	Unresolved []Open     `json:"unresolved,omitempty"`
	Conflicts  []Conflict `json:"conflicts,omitempty"`
	// Points are what the document itself says, citing the document.
	Points []Line `json:"document,omitempty"`
	// Timeline has a line per meeting, newest first: what was said or
	// decided there about the document.
	Timeline []Event `json:"timeline,omitempty"`
	// Dropped counts lines the writer produced whose citations did not
	// check out, so a brief that lost most of its lines says so.
	Dropped int `json:"unverified_dropped,omitempty"`
}

// Line is a statement and the evidence it cites.
type Line struct {
	Text  string   `json:"text"`
	Cites []string `json:"cites"`
}

// Event is a dated change.
type Event struct {
	Date  string   `json:"date"`
	Text  string   `json:"text"`
	Cites []string `json:"cites"`
}

// Open is something raised and not settled.
type Open struct {
	Text   string   `json:"text"`
	Raised string   `json:"raised,omitempty"`
	Cites  []string `json:"cites"`
}

// Conflict is where the document and the latest discussion disagree.
type Conflict struct {
	DocSays        string `json:"doc_says"`
	DiscussionSays string `json:"discussion_says"`
	Date           string `json:"date,omitempty"`
	DocCite        string `json:"doc_cite"`
	SaidCite       string `json:"said_cite"`
}

// RelationID is the node ID of a subject document's relation. The file path
// is empty so that re-indexing the document, which deletes the nodes recorded
// under its path, never removes what was discovered about it.
func RelationID(subjectPath string) string {
	return graph.NewNodeID(string(graph.NodeRelation), "", "doc:"+subjectPath)
}

// EvidenceID identifies a quote by where it is and what it says, so a brief
// regenerated from the same evidence cites the same IDs.
func EvidenceID(relationID string, e Evidence) string {
	anchor := e.Path + "|" + e.HeadingPath + "|" + strconv.Itoa(e.Ordinal) + "|" + e.SessionID + "|" +
		strconv.FormatFloat(e.Start, 'f', 1, 64)
	sum := sha256.Sum256([]byte(relationID + "|" + e.Kind + "|" + anchor + "|" +
		strings.Join(strings.Fields(strings.ToLower(e.Quote)), " ")))
	return hex.EncodeToString(sum[:3])
}

// New returns an empty relation for a subject.
func New(subjectPath string) *Relation {
	return &Relation{
		SubjectPath: subjectPath,
		Sections:    make(map[string]SectionState),
		Links:       make(map[string]*Verdict),
	}
}

// Accepted returns the accepted verdicts, oldest meeting first by judge time.
func (r *Relation) Accepted() []*Verdict {
	var out []*Verdict
	for _, v := range r.Links {
		if v.Accepted {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out
}

// Judged reports whether the pair was judged under the same signatures, in
// which case it must not be asked again.
func (r *Relation) Judged(sessionID, sigDoc, sigMeeting string) bool {
	v, ok := r.Links[sessionID]
	return ok && v.SigDoc == sigDoc && v.SigMeeting == sigMeeting
}

// Node encodes the relation as a graph node.
func (r *Relation) Node() (*graph.Node, error) {
	props := map[string]string{
		propRelationKind: KindDiscussed,
		propSubjectPath:  r.SubjectPath,
		propSubjectHash:  r.SubjectHash,
		propRevision:     strconv.Itoa(r.Revision),
	}
	for key, v := range map[string]any{
		propSections:  r.Sections,
		propLinks:     r.Links,
		propEvidence:  r.Evidence,
		propRevisions: r.Revisions,
	} {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("encode %s: %w", key, err)
		}
		props[key] = string(b)
	}
	doc := ""
	if r.Brief != nil {
		b, err := json.Marshal(r.Brief)
		if err != nil {
			return nil, fmt.Errorf("encode brief: %w", err)
		}
		props[propBrief] = string(b)
		props[propBriefInputs] = r.BriefInputsHash
		props[propBriefModel] = r.BriefModel
		props[propBriefedAt] = r.BriefedAt.UTC().Format(time.RFC3339)
		doc = r.Brief.Text()
	}
	return &graph.Node{
		ID:            RelationID(r.SubjectPath),
		Type:          graph.NodeRelation,
		Name:          r.SubjectPath,
		QualifiedName: "relation:" + r.SubjectPath,
		DocComment:    doc,
		Properties:    props,
		UpdatedAt:     r.BriefedAt,
	}, nil
}

// FromNode decodes a relation from its graph node.
func FromNode(n *graph.Node) (*Relation, error) {
	if n == nil || n.Type != graph.NodeRelation {
		return nil, fmt.Errorf("not a relation node")
	}
	r := New(n.Properties[propSubjectPath])
	r.SubjectHash = n.Properties[propSubjectHash]
	r.Revision, _ = strconv.Atoi(n.Properties[propRevision])
	r.BriefInputsHash = n.Properties[propBriefInputs]
	r.BriefModel = n.Properties[propBriefModel]
	if t, err := time.Parse(time.RFC3339, n.Properties[propBriefedAt]); err == nil {
		r.BriefedAt = t
	}
	decode := func(key string, into any) error {
		if raw := n.Properties[key]; raw != "" {
			if err := json.Unmarshal([]byte(raw), into); err != nil {
				return fmt.Errorf("decode %s of %s: %w", key, r.SubjectPath, err)
			}
		}
		return nil
	}
	if err := decode(propSections, &r.Sections); err != nil {
		return nil, err
	}
	if err := decode(propLinks, &r.Links); err != nil {
		return nil, err
	}
	if err := decode(propEvidence, &r.Evidence); err != nil {
		return nil, err
	}
	if err := decode(propRevisions, &r.Revisions); err != nil {
		return nil, err
	}
	if raw := n.Properties[propBrief]; raw != "" {
		r.Brief = &Brief{}
		if err := json.Unmarshal([]byte(raw), r.Brief); err != nil {
			return nil, fmt.Errorf("decode brief of %s: %w", r.SubjectPath, err)
		}
	}
	if r.Sections == nil {
		r.Sections = make(map[string]SectionState)
	}
	if r.Links == nil {
		r.Links = make(map[string]*Verdict)
	}
	return r, nil
}

// Text is the brief as plain text: its headline and current lines. It is what
// a relation node embeds, so search lands on what the relation says.
func (b *Brief) Text() string {
	if b == nil {
		return ""
	}
	parts := []string{b.Headline.Text}
	for _, l := range b.Current {
		parts = append(parts, l.Text)
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}
