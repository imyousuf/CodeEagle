package relations

import (
	"context"
	"fmt"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// DefaultGate is the probability at or above which a verdict is accepted.
//
// Measured 2026-10-22 on 160 labelled document and meeting pairs (27
// related): the judge ranks them well (AUC 0.97), but the 0.6 measured on
// topic labels admitted wrong pairs four times in ten. At 0.8 precision was
// 0.86 and recall 0.67 to 0.70 across two runs; no gate reached 0.95 without
// recall falling below a quarter. The pairs still wrongly admitted are
// neighbouring subjects (a server spec and a meeting about the same
// product's tokens), where the labels themselves were the least certain.
const DefaultGate = 0.8

// Relater judges candidates and records the verdicts.
type Relater struct {
	store graph.Store
	judge *Judge
	gate  float64
	now   func() time.Time
	log   func(string, ...any)
}

// RelaterOptions configure a Relater.
type RelaterOptions struct {
	Gate float64
	Now  func() time.Time
	Log  func(string, ...any)
}

// NewRelater builds a relater writing through store, which must write the
// relations scope and read the subjects' and meetings' scopes.
func NewRelater(store graph.Store, judge *Judge, opts RelaterOptions) *Relater {
	r := &Relater{store: store, judge: judge, gate: opts.Gate, now: opts.Now, log: opts.Log}
	if r.gate <= 0 {
		r.gate = DefaultGate
	}
	if r.now == nil {
		r.now = time.Now
	}
	if r.log == nil {
		r.log = func(string, ...any) {}
	}
	return r
}

// RunStats is what a run did and cost.
type RunStats struct {
	Judged    int
	Accepted  int
	Relations int
	Judging   JudgeReport
}

// Judge judges the candidates and records every verdict, accepted or not, on
// the subject's relation. existing holds the relations already in the graph
// and is updated in place.
func (r *Relater) Judge(ctx context.Context, cands []*Candidate, existing map[string]*Relation) (RunStats, error) {
	var stats RunStats
	judgments, report, judgeErr := r.judge.Judge(ctx, cands)
	stats.Judging = report
	now := r.now().UTC()

	touched := make(map[string]*Subject)
	for _, jd := range judgments {
		c := jd.Candidate
		rel := existing[c.Subject.Path]
		if rel == nil {
			rel = New(c.Subject.Path)
			existing[c.Subject.Path] = rel
		}
		v := &Verdict{
			SessionID:  c.Meeting.SessionID,
			MeetingID:  c.Meeting.NodeID,
			P:          jd.P,
			Accepted:   jd.P >= r.gate,
			Judge:      report.Model,
			Signals:    c.Signals.Names(),
			Cosine:     c.Cosine,
			SigDoc:     c.SigDoc(),
			SigMeeting: c.SigMeeting(),
			JudgedAt:   now,
		}
		for _, seg := range c.Segments {
			v.Ranges = append(v.Ranges, Range{Start: seg.Start, End: seg.End})
		}
		for _, sec := range c.Sections {
			v.Sections = append(v.Sections, sectionKey(sec))
		}
		rel.Links[c.Meeting.SessionID] = v
		stats.Judged++
		if v.Accepted {
			stats.Accepted++
		}
		touched[c.Subject.Path] = c.Subject
	}

	for path, subj := range touched {
		rel := existing[path]
		rel.Observe(subj, now)
		if err := r.Write(ctx, rel, subj); err != nil {
			return stats, fmt.Errorf("write relation for %s: %w", path, err)
		}
		stats.Relations++
	}
	if judgeErr != nil {
		r.log("warning: some candidates could not be judged and will be retried: %v", judgeErr)
	}
	return stats, nil
}

// Observe records the subject's current state: its hash, and for each
// section, when its present text was first seen. This is the document's
// change history, which the graph does not otherwise keep.
//
// The first observation is a baseline: its sections get a zero FirstSeen,
// since nothing is known about when they were written, and recording the
// moment they were first indexed made "what changed since" report every
// section of every document as new.
func (rel *Relation) Observe(subj *Subject, now time.Time) {
	baseline := len(rel.Sections) == 0
	rel.SubjectHash = subj.Hash
	present := make(map[string]bool, len(subj.Sections))
	for _, sec := range subj.Sections {
		key := sectionKey(sec)
		present[key] = true
		if st, ok := rel.Sections[key]; !ok || st.Hash != sec.Hash {
			seen := now
			if baseline {
				seen = time.Time{}
			}
			rel.Sections[key] = SectionState{Hash: sec.Hash, FirstSeen: seen}
		}
	}
	for key := range rel.Sections {
		if !present[key] {
			delete(rel.Sections, key)
		}
	}
}

// sectionKey names a section within its document. The ordinal tells apart
// two sections under one heading.
func sectionKey(sec *Section) string {
	if sec.Ordinal > 1 {
		return fmt.Sprintf("%s#%d", sec.HeadingPath, sec.Ordinal)
	}
	return sec.HeadingPath
}

// Write stores the relation and derives its edges: About to the document,
// and DiscussedIn to each meeting whose verdict was accepted. Edges of
// verdicts no longer accepted are removed.
func (r *Relater) Write(ctx context.Context, rel *Relation, subj *Subject) error {
	node, err := rel.Node()
	if err != nil {
		return err
	}
	if err := r.store.AddNode(ctx, node); err != nil {
		return err
	}
	if subj != nil && subj.RootID != "" {
		if err := r.store.AddEdge(ctx, &graph.Edge{
			ID:       graph.NewNodeID("edge", node.ID, subj.RootID+":"+string(graph.EdgeAbout)),
			Type:     graph.EdgeAbout,
			SourceID: node.ID,
			TargetID: subj.RootID,
		}); err != nil {
			return err
		}
	}

	want := make(map[string]*Verdict)
	for _, v := range rel.Links {
		if v.Accepted && v.MeetingID != "" {
			want[v.MeetingID] = v
		}
	}
	current, err := r.store.GetEdges(ctx, node.ID, graph.EdgeDiscussedIn)
	if err != nil {
		return err
	}
	for _, e := range current {
		if e.SourceID == node.ID && want[e.TargetID] == nil {
			if err := r.store.DeleteEdge(ctx, e.ID); err != nil {
				return err
			}
		}
	}
	for meetingID, v := range want {
		if err := r.store.AddEdge(ctx, &graph.Edge{
			ID:       graph.NewNodeID("edge", node.ID, meetingID+":"+string(graph.EdgeDiscussedIn)),
			Type:     graph.EdgeDiscussedIn,
			SourceID: node.ID,
			TargetID: meetingID,
			Properties: map[string]string{
				"probability": fmt.Sprintf("%.3f", v.P),
				"judge":       v.Judge,
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

// EnsureRelations gives every subject a relation, so every document has a
// brief whether or not a meeting discussed it: a spec with no confirmed
// meeting had nothing to say about itself, and answers left its content out.
// It returns how many relations it created.
func EnsureRelations(ctx context.Context, store graph.Store, subjects []*Subject, existing map[string]*Relation, now time.Time) (int, error) {
	relater := NewRelater(store, nil, RelaterOptions{})
	created := 0
	for _, s := range subjects {
		if existing[s.Path] != nil {
			continue
		}
		rel := New(s.Path)
		rel.Observe(s, now)
		if err := relater.Write(ctx, rel, s); err != nil {
			return created, fmt.Errorf("create relation for %s: %w", s.Path, err)
		}
		existing[s.Path] = rel
		created++
	}
	return created, nil
}
