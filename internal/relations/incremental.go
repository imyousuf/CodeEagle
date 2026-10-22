package relations

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// ScanState records, per document and per meeting, the content each was last
// considered for relations under. A document is considered again when its
// hash changes, a meeting when its transcript does, and either one that has
// never been considered is picked up in turn, so the same mechanism handles
// edits, new recordings, recordings embedded after they were indexed, and a
// corpus that existed before relations were turned on.
type ScanState struct {
	Subjects map[string]string `json:"subjects"`
	Meetings map[string]string `json:"meetings"`
}

func scanStateID() string { return graph.NewNodeID(string(graph.NodeRelationScan), "", "state") }

// LoadScanState reads the scan state, empty if there is none yet.
func LoadScanState(ctx context.Context, store graph.Store) (*ScanState, error) {
	st := &ScanState{Subjects: map[string]string{}, Meetings: map[string]string{}}
	n, err := store.GetNode(ctx, scanStateID())
	if err != nil || n == nil {
		return st, nil
	}
	if raw := n.Properties["state_json"]; raw != "" {
		if err := json.Unmarshal([]byte(raw), st); err != nil {
			return nil, fmt.Errorf("decode relation scan state: %w", err)
		}
	}
	if st.Subjects == nil {
		st.Subjects = map[string]string{}
	}
	if st.Meetings == nil {
		st.Meetings = map[string]string{}
	}
	return st, nil
}

// Save writes the scan state.
func (s *ScanState) Save(ctx context.Context, store graph.Store) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return store.AddNode(ctx, &graph.Node{
		ID:         scanStateID(),
		Type:       graph.NodeRelationScan,
		Name:       "relation scan state",
		Properties: map[string]string{"state_json": string(raw)},
	})
}

// IncrementalOptions bound one incremental run.
type IncrementalOptions struct {
	// MaxSubjects and MaxMeetings bound how many documents and meetings are
	// considered per run; the rest wait for the next.
	MaxSubjects int
	MaxMeetings int
	// MaxBriefs bounds the briefs written per run.
	MaxBriefs int
}

// DefaultIncrementalOptions consider enough per run to keep up with a busy
// week of meetings without spending much on any one run.
func DefaultIncrementalOptions() IncrementalOptions {
	return IncrementalOptions{MaxSubjects: 50, MaxMeetings: 25, MaxBriefs: 10}
}

// IncrementalReport is what a run did and cost.
type IncrementalReport struct {
	// NoVectors is set when nothing was considered for want of vectors.
	NoVectors          bool
	SubjectsConsidered int
	MeetingsConsidered int
	SubjectsWaiting    int
	MeetingsWaiting    int
	Proposal           *Proposal
	Judged             RunStats
	BriefsWritten      int
	BriefsPending      int
	BriefTokensIn      int
	BriefTokensOut     int
	Dropped            int
}

// Incremental considers what is new or changed: documents whose content and
// meetings whose transcripts have not been considered under their present
// form, newest first and within the bounds. It judges the candidates, writes
// the briefs whose inputs changed, and marks considered what finished.
//
// Something is marked considered only if none of its candidates were
// deferred by the per-run cap or failed to be judged, so a later run picks
// up where this one stopped. Candidates beyond the best few for a document
// or a meeting are not deferred but dropped: they are what the caps exist to
// leave unjudged. A document or meeting with no vectors yet waits: the meaning
// signal cannot propose for it until it is embedded, and the other signals
// alone were measured to propose nothing useful. With no vectors at all,
// nothing is considered.
func Incremental(ctx context.Context, store graph.Store, vec Vectors, judge *Judge, writer *BriefWriter,
	keep SubjectFilter, popts Options, gate float64, o IncrementalOptions) (*IncrementalReport, error) {
	rep := &IncrementalReport{}
	subjects, err := LoadSubjects(ctx, store, keep)
	if err != nil {
		return rep, err
	}
	meetings, err := LoadMeetings(ctx, store)
	if err != nil {
		return rep, err
	}
	existing, err := LoadRelations(ctx, store)
	if err != nil {
		return rep, err
	}
	state, err := LoadScanState(ctx, store)
	if err != nil {
		return rep, err
	}
	p := NewProposer(subjects, meetings, vec, popts)
	return incremental(ctx, store, p, subjects, meetings, existing, state, judge, writer, gate, o, rep)
}

func incremental(ctx context.Context, store graph.Store, p *Proposer, subjects []*Subject, meetings map[string]*Meeting,
	existing map[string]*Relation, state *ScanState, judge *Judge, writer *BriefWriter, gate float64,
	o IncrementalOptions, rep *IncrementalReport) (*IncrementalReport, error) {
	if o.MaxSubjects <= 0 || o.MaxMeetings <= 0 {
		d := DefaultIncrementalOptions()
		if o.MaxSubjects <= 0 {
			o.MaxSubjects = d.MaxSubjects
		}
		if o.MaxMeetings <= 0 {
			o.MaxMeetings = d.MaxMeetings
		}
	}
	if !p.HasVectors() {
		rep.NoVectors = true
		return rep, nil
	}

	// What is pending, newest first.
	var pendingSubjects []*Subject
	for _, s := range subjects {
		if state.Subjects[s.Path] != s.Hash && p.SubjectHasVectors(s.Path) {
			pendingSubjects = append(pendingSubjects, s)
		}
	}
	sort.SliceStable(pendingSubjects, func(i, j int) bool { return pendingSubjects[i].UpdatedAt.After(pendingSubjects[j].UpdatedAt) })
	var pendingMeetings []*Meeting
	for _, m := range meetings {
		if state.Meetings[m.SessionID] != meetingSig(m) && p.MeetingHasVectors(m.SessionID) {
			pendingMeetings = append(pendingMeetings, m)
		}
	}
	sort.SliceStable(pendingMeetings, func(i, j int) bool { return pendingMeetings[i].Date.After(pendingMeetings[j].Date) })

	if len(pendingSubjects) > o.MaxSubjects {
		rep.SubjectsWaiting = len(pendingSubjects) - o.MaxSubjects
		pendingSubjects = pendingSubjects[:o.MaxSubjects]
	}
	if len(pendingMeetings) > o.MaxMeetings {
		rep.MeetingsWaiting = len(pendingMeetings) - o.MaxMeetings
		pendingMeetings = pendingMeetings[:o.MaxMeetings]
	}
	rep.SubjectsConsidered, rep.MeetingsConsidered = len(pendingSubjects), len(pendingMeetings)

	// Each candidate is owed to whichever pending side proposed it, so a
	// pair deferred for a document does not hold back the meeting it names.
	var cands []*Candidate
	forSubject, forMeeting := make(map[string]bool), make(map[string]bool)
	if len(pendingSubjects) > 0 {
		paths := make([]string, len(pendingSubjects))
		for i, s := range pendingSubjects {
			paths[i] = s.Path
		}
		for _, c := range p.ForSubjects(paths...) {
			forSubject[c.Key()] = true
			cands = append(cands, c)
		}
	}
	if len(pendingMeetings) > 0 {
		ids := make([]string, len(pendingMeetings))
		for i, m := range pendingMeetings {
			ids[i] = m.SessionID
		}
		for _, c := range p.ForMeetings(ids...) {
			forMeeting[c.Key()] = true
			cands = append(cands, c)
		}
	}
	cands = dedupe(cands)
	prop := selectFairly(p, cands, forMeeting, JudgedBy(existing))
	rep.Proposal = prop

	// Judge what was selected.
	unfinished := make(map[string]bool) // subject path or "m:"+session
	owe := func(c *Candidate) {
		if forSubject[c.Key()] {
			unfinished[c.Subject.Path] = true
		}
		if forMeeting[c.Key()] {
			unfinished["m:"+c.Meeting.SessionID] = true
		}
	}
	for _, c := range prop.Deferred {
		owe(c)
	}
	if len(prop.Candidates) > 0 && judge != nil {
		relater := NewRelater(store, judge, RelaterOptions{Gate: gate})
		stats, err := relater.Judge(ctx, prop.Candidates, existing)
		rep.Judged = stats
		if err != nil {
			return rep, err
		}
		for _, c := range prop.Candidates {
			if v, ok := existing[c.Subject.Path]; !ok || !v.Judged(c.Meeting.SessionID, c.SigDoc(), c.SigMeeting()) {
				owe(c)
			}
		}
	} else {
		for _, c := range prop.Candidates {
			owe(c)
		}
	}

	// Mark considered what finished.
	for _, s := range pendingSubjects {
		if !unfinished[s.Path] {
			state.Subjects[s.Path] = s.Hash
		}
	}
	for _, m := range pendingMeetings {
		if !unfinished["m:"+m.SessionID] {
			state.Meetings[m.SessionID] = meetingSig(m)
		}
	}
	if err := state.Save(ctx, store); err != nil {
		return rep, fmt.Errorf("save relation scan state: %w", err)
	}

	// Briefs whose inputs changed, up to the bound.
	if writer != nil {
		by := make(map[string]*Subject, len(subjects))
		for _, s := range subjects {
			by[s.Path] = s
		}
		relater := NewRelater(store, nil, RelaterOptions{})
		paths := make([]string, 0, len(existing))
		for path := range existing {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		maxBriefs := o.MaxBriefs
		if maxBriefs <= 0 {
			maxBriefs = DefaultIncrementalOptions().MaxBriefs
		}
		for _, path := range paths {
			rel, subj := existing[path], by[path]
			if subj == nil || len(rel.Accepted()) == 0 {
				continue
			}
			if rep.BriefsWritten >= maxBriefs {
				if InputsHash(rel, subj, writer.client.Model()) != rel.BriefInputsHash {
					rep.BriefsPending++
				}
				continue
			}
			br, err := writer.Update(ctx, rel, subj, meetings)
			rep.BriefTokensIn += br.InputTokens
			rep.BriefTokensOut += br.OutputTokens
			if err != nil || !br.Written {
				continue
			}
			rep.Dropped += br.Dropped
			if err := relater.Write(ctx, rel, subj); err != nil {
				return rep, fmt.Errorf("store brief for %s: %w", path, err)
			}
			rep.BriefsWritten++
		}
	}
	return rep, nil
}

// selectFairly splits the per-run cap between the candidates pending
// meetings proposed and those only pending documents did, each side getting
// at least half of it and either side what the other leaves. Selected as one,
// the side that scores higher would take the whole cap on every run while the
// other waited behind a window that never empties.
func selectFairly(p *Proposer, cands []*Candidate, forMeeting map[string]bool, judged func(*Candidate) bool) *Proposal {
	budget := p.opts.MaxPairs
	if budget <= 0 {
		return p.Select(cands, judged)
	}
	var meetingSide, subjectSide []*Candidate
	for _, c := range cands {
		if forMeeting[c.Key()] {
			meetingSide = append(meetingSide, c)
		} else {
			subjectSide = append(subjectSide, c)
		}
	}
	clone := func(cs []*Candidate) []*Candidate { return append([]*Candidate(nil), cs...) }
	subjectNeed := len(p.SelectWithin(clone(subjectSide), judged, -1).Candidates)
	m := p.SelectWithin(clone(meetingSide), judged, budget-min(subjectNeed, budget/2))
	sp := p.SelectWithin(clone(subjectSide), judged, budget-len(m.Candidates))

	for k, v := range sp.BySignal {
		m.BySignal[k] += v
	}
	m.Candidates = append(m.Candidates, sp.Candidates...)
	m.Deferred = append(m.Deferred, sp.Deferred...)
	m.Proposed += sp.Proposed
	m.AlreadyJudged += sp.AlreadyJudged
	m.Unsupported += sp.Unsupported
	m.CutByCaps += sp.CutByCaps
	return m
}

// meetingSig is what a meeting is considered under: its transcript's hash.
func meetingSig(m *Meeting) string {
	if m.ContentHash != "" {
		return m.ContentHash
	}
	return m.NodeID
}

func dedupe(cands []*Candidate) []*Candidate {
	seen := make(map[string]*Candidate, len(cands))
	var out []*Candidate
	for _, c := range cands {
		if prev, ok := seen[c.Key()]; ok {
			prev.Signals |= c.Signals
			if c.Cosine > prev.Cosine {
				prev.Cosine = c.Cosine
			}
			continue
		}
		seen[c.Key()] = c
		out = append(out, c)
	}
	return out
}

// Summary is the run's one-line cost report.
func (r *IncrementalReport) Summary() string {
	if r.NoVectors {
		return "relations: waiting for documents and meetings to be embedded"
	}
	if r.Proposal == nil {
		return "relations: nothing to consider"
	}
	j := r.Judged.Judging
	out := fmt.Sprintf("relations: considered %d documents, %d meetings · proposed %d, judged %d, accepted %d (%d requests, %dk tokens)",
		r.SubjectsConsidered, r.MeetingsConsidered, r.Proposal.Proposed, r.Judged.Judged, r.Judged.Accepted,
		j.Requests, (j.InputTokens+999)/1000)
	if n := len(r.Proposal.Deferred); n > 0 {
		out += fmt.Sprintf(" · %d deferred to later runs", n)
	}
	out += fmt.Sprintf(" · briefs %d written (%dk in / %dk out)", r.BriefsWritten, (r.BriefTokensIn+999)/1000, (r.BriefTokensOut+999)/1000)
	if r.BriefsPending > 0 {
		out += fmt.Sprintf(", %d pending", r.BriefsPending)
	}
	if r.SubjectsWaiting > 0 || r.MeetingsWaiting > 0 {
		out += fmt.Sprintf(" · %d documents and %d meetings wait for later runs", r.SubjectsWaiting, r.MeetingsWaiting)
	}
	return out
}
