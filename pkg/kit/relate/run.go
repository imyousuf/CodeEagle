package relate

import (
	"context"
	"sort"
	"time"
)

// What Select did with a candidate.
const (
	StatusSelected = "selected"
	StatusJudged   = "judged"
	StatusCut      = "past the caps"
	StatusDeferred = "deferred"
)

// Select sets aside pairs already judged and applies the caps, in the order
// given, which should be best first. Only the per-run cap, maxPairs, defers
// a pair to a later run; the per-document cap, maxPerDoc, keeps a document's
// best few and drops the rest, which is what it exists to do. A cap of zero
// or less is no cap, and judged may be nil. Each candidate's Status says
// what became of it.
func Select(cands []*Candidate, judged func(*Candidate) bool, maxPerDoc, maxPairs int) (selected, deferred []*Candidate) {
	perDoc := make(map[string]int)
	for _, c := range cands {
		if judged != nil && judged(c) {
			c.Status = StatusJudged
			continue
		}
		if maxPerDoc > 0 && (perDoc[c.A.ID] >= maxPerDoc || perDoc[c.B.ID] >= maxPerDoc) {
			c.Status = StatusCut
			continue
		}
		perDoc[c.A.ID]++
		perDoc[c.B.ID]++
		if maxPairs > 0 && len(selected) >= maxPairs {
			c.Status = StatusDeferred
			deferred = append(deferred, c)
			continue
		}
		c.Status = StatusSelected
		selected = append(selected, c)
	}
	return selected, deferred
}

// Stats are what relating some pairs did.
type Stats struct {
	// Judged counts the pairs given a verdict.
	Judged int
	// Accepted counts the links accepted, by kind.
	Accepted map[string]int
	Judging  Report
}

// Relate judges the candidates, decides their links under the Relater's
// gates, and puts each verdict in the ledger, stamped with now. It does not
// save the ledger. The error is returned only when every pair failed; a
// pair that failed has no verdict, so it is asked again later. With no
// judge it puts nothing.
func (r *Relater) Relate(ctx context.Context, cands []*Candidate, l Ledger, now time.Time) (Stats, error) {
	stats := Stats{Accepted: make(map[string]int)}
	if len(cands) == 0 {
		return stats, nil
	}
	judgments, report, err := r.Judge(ctx, cands)
	stats.Judging = report
	if err != nil && len(judgments) == 0 {
		return stats, err
	}
	for _, jd := range judgments {
		v := jd.Verdict
		v.JudgedAt = now
		v.Decide(jd.Candidate.A, jd.Candidate.B, r.gates)
		l.Put(jd.Candidate.A, jd.Candidate.B, &v)
		stats.Judged++
		for _, link := range v.Links {
			stats.Accepted[link.Kind]++
		}
	}
	return stats, nil
}

// Proposer proposes the pairs involving any of the given documents, best
// first. Proposing is the caller's: references, shared topics, nearness in
// meaning, whatever it has.
type Proposer func(ids ...string) []*Candidate

// RunOptions bound one run. A bound of zero or less is no bound.
type RunOptions struct {
	// MaxDocs bounds the documents considered, newest first; the rest wait
	// for a later run.
	MaxDocs int
	// MaxPerDoc keeps a document's best few pairs and drops the rest.
	MaxPerDoc int
	// MaxPairs bounds the pairs judged; the rest are deferred to a later
	// run.
	MaxPairs int
}

// DefaultRunOptions keep one run small: one request per pair, at most sixty
// pairs, from at most fifty documents.
func DefaultRunOptions() RunOptions {
	return RunOptions{MaxDocs: 50, MaxPerDoc: 12, MaxPairs: 60}
}

// RunReport is what a run did.
type RunReport struct {
	// Considered counts the documents whose pairs were considered, and
	// Waiting those left for a later run.
	Considered int
	Waiting    int
	Stats      Stats
	// Deferred counts the pairs left for a later run by MaxPairs.
	Deferred int
}

// Run relates what changed: the documents whose content their pairs were
// not last considered under, newest first and within the bounds. It
// proposes their pairs, judges those not judged under both documents'
// present content, marks considered each document none of whose pairs was
// deferred or failed, and saves the ledger, so a later run takes up the
// rest. With nothing changed it does nothing, and with no judge it
// considers nothing: a document is not marked considered on the strength of
// pairs nobody judged.
func (r *Relater) Run(ctx context.Context, docs []Doc, propose Proposer, l Ledger, o RunOptions, now time.Time) (*RunReport, error) {
	rep := &RunReport{}
	if r.judge == nil {
		return rep, nil
	}
	var pending []Doc
	for _, d := range docs {
		if l.Considered(d.ID) != d.Hash {
			pending = append(pending, d)
		}
	}
	if len(pending) == 0 {
		return rep, nil
	}
	sort.SliceStable(pending, func(i, j int) bool { return pending[i].UpdatedAt.After(pending[j].UpdatedAt) })
	if o.MaxDocs > 0 && len(pending) > o.MaxDocs {
		rep.Waiting = len(pending) - o.MaxDocs
		pending = pending[:o.MaxDocs]
	}
	rep.Considered = len(pending)
	ids := make([]string, len(pending))
	for i, d := range pending {
		ids[i] = d.ID
	}
	judged := func(c *Candidate) bool { return Judged(l, c) }
	selected, deferred := Select(propose(ids...), judged, o.MaxPerDoc, o.MaxPairs)
	stats, err := r.Relate(ctx, selected, l, now)
	rep.Stats, rep.Deferred = stats, len(deferred)
	if err != nil {
		return rep, err
	}
	open := make(map[string]bool)
	for _, c := range deferred {
		open[c.A.ID], open[c.B.ID] = true, true
	}
	for _, c := range selected {
		if !judged(c) {
			open[c.A.ID], open[c.B.ID] = true, true
		}
	}
	for _, d := range pending {
		if !open[d.ID] {
			l.Consider(d.ID, d.Hash)
		}
	}
	return rep, l.Save(ctx)
}
