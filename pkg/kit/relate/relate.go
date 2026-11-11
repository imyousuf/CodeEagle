// Package relate decides how two documents relate, and remembers every
// verdict so that a pair is asked about again only when either document's
// content changes.
//
// Four questions are put about a pair: are they about the same piece of
// work, does one change part of what the other specifies, does one replace
// the other as a whole, and is one an account of something that happened
// involving what the other describes. The last three are asked both ways.
// Rules then turn the probabilities into links (Verdict.Decide), and every
// tie falls back to the weaker claim, because a wrong link is worse than a
// missing one: calling a document replaced hides it from a reader who would
// have needed it.
//
// Similarity proposes pairs; a judge decides them. Nothing here asserts a
// relation on its own, and a Relater with no judge asserts none at all, so a
// program without a decision model carries on with no relations rather than
// with relations nobody judged.
//
// Each pair is judged in its own request. Pairs judged together moved each
// other's probabilities when measured: the same related pair cleared a gate
// in one batch and missed it in another, and one pair per request lifted
// precision from 0.86 to 0.95 at the same recall.
package relate

import (
	"path"
	"regexp"
	"strings"
	"time"
)

// Kinds of relation between two documents.
const (
	// Changes: one document changes part of what the other specifies,
	// which otherwise still holds (a redesign of its frontend).
	Changes = "changes"
	// Replaces: the other, as a whole, is no longer the current account.
	Replaces = "replaces"
	// Records: one is an account of something that happened (an incident,
	// a review, a meeting, a conversation) involving what the other
	// describes.
	Records = "records"
	// Companion: they concern the same piece of work, with no direction.
	Companion = "companion"
)

// Doc is what relating knows of one document.
type Doc struct {
	// ID names the document to the judge, in the ledger and in links, and
	// orders a pair. A path serves all three.
	ID string
	// Hash is the document's content. A verdict holds while both
	// documents' hashes are what they were when it was given.
	Hash string
	// Title, Type and Status describe the document to the judge. Type is
	// its kind ("spec", "incident"), and Status what the document says of
	// itself ("Draft"), never a guess.
	Title, Type, Status string
	// Date is the date the document states, written so that later dates
	// sort after earlier ones as text (2026-10-04); empty when it states
	// none. When each of a pair changes or replaces the other above the
	// gate, the later one is taken to, and with no dates neither is.
	Date string
	// Outline is the document's headings, in order, as many as the judge
	// should see.
	Outline []string
	// Record says the document's kind is to record what happened: an
	// incident write-up, notes, a chat, a report. Only such a document is
	// taken as the record of another, and only of one that is not itself a
	// record: asked whether a later review of a pull request records the
	// earlier review, a judge said yes at 0.9, and 19 of 20 records links
	// on labelled pairs were of that kind.
	Record bool
	// Series names the routine whose dated output the document is (see
	// DatedSeries); empty for anything else.
	Series string
	// UpdatedAt orders the documents a run considers, newest first.
	UpdatedAt time.Time
}

// Excerpt is a passage of a document, under the section it comes from.
type Excerpt struct {
	Section string `json:"section"`
	Text    string `json:"text"`
}

// Candidate is a pair of documents proposed for judging. A is the document
// with the lower ID: a verdict is kept on the pair in that order, and its
// directional probabilities are [A→B, B→A]. NewCandidate orders them.
type Candidate struct {
	A, B Doc
	// Signals name what proposed the pair, kept on its verdict.
	Signals []string
	// Facts are what the judge is told about the pair, literally: the
	// sentence in which one names the other, a version-style name.
	Facts []string
	// ExcerptsA and ExcerptsB are what the judge reads of each document:
	// the passages the pair rests on.
	ExcerptsA, ExcerptsB []Excerpt
	// Score ranks candidates, best first, when caps apply.
	Score float64
	// Status is what Select did with the candidate.
	Status string
}

// NewCandidate pairs two documents, the lower ID first.
func NewCandidate(x, y Doc) *Candidate {
	if x.ID > y.ID {
		x, y = y, x
	}
	return &Candidate{A: x, B: y}
}

// Key identifies the pair.
func (c *Candidate) Key() string { return c.A.ID + "\x00" + c.B.ID }

// Link is an accepted relation between two documents, From to To; a
// companion has no direction and is recorded from A to B.
type Link struct {
	Kind string  `json:"kind"`
	From string  `json:"from"`
	To   string  `json:"to"`
	P    float64 `json:"p"`
}

// Verdict is the judge's answer about a pair of documents, kept whether or
// not anything was accepted, so the pair is not asked about again while
// neither document changes. Directional probabilities are [A→B, B→A].
type Verdict struct {
	// Other is B's ID; the verdict is kept with A.
	Other     string     `json:"other"`
	PRelated  float64    `json:"p_related"`
	PChanges  [2]float64 `json:"p_changes"`
	PReplaces [2]float64 `json:"p_replaces"`
	PRecords  [2]float64 `json:"p_records"`
	// Links are what Decide accepted under the gates last applied.
	Links   []Link   `json:"links,omitempty"`
	Signals []string `json:"signals,omitempty"`
	// SigA and SigB are A's and B's hashes when the pair was judged.
	SigA string `json:"sig_a"`
	SigB string `json:"sig_b"`
	// Judge names what gave the verdict.
	Judge    string    `json:"judge"`
	JudgedAt time.Time `json:"judged_at"`
}

// Current reports whether the verdict was given on a and b as they are now.
func (v *Verdict) Current(a, b Doc) bool {
	return v != nil && v.SigA == a.Hash && v.SigB == b.Hash
}

// Gates are the probabilities at or above which each kind is accepted.
type Gates struct {
	Related, Changes, Replaces, Records float64
}

// DefaultGates are provisional until calibrated on labelled pairs, and
// strict where a wrong answer costs most: calling a document replaced hides
// it from a reader who would have needed it.
//
// They were measured with the Jev decision model and DefaultWording, on 136
// pairs two blind labellers agreed on (kappa 0.86 on related): related AUC
// 0.90, precision 0.83 at 0.8 and 0.90 at 0.9; changes precision 0.89 at
// recall 0.47 at 0.75, against 1.00 at recall 0.14 at 0.85. No pair labelled
// replaces scored above 0.62, so at 0.95 replaces is never shown and a
// revision reads as "changed by", which every replacing document also is.
// Another judge or another wording moves the probabilities: measure gates of
// its own before relying on them.
func DefaultGates() Gates {
	return Gates{Related: 0.85, Changes: 0.75, Replaces: 0.95, Records: 0.85}
}

// Decide turns the probabilities into accepted links, replacing any Decide
// accepted before, so a change of gate or rule takes effect without asking
// the judge again. Every tie falls back to the weaker claim, because a wrong
// link is worse than a missing one:
//   - a direction is kept only if its reverse is below the gate, or, when
//     both are above it, the documents state dates and the later one is
//     the one changing or replacing the earlier;
//   - replacing requires changing, in the same direction;
//   - recording in both directions keeps neither;
//   - related pairs with no directional kind are companions.
func (v *Verdict) Decide(a, b Doc, g Gates) {
	v.Links = nil
	if v.PRelated < g.Related {
		return
	}
	direction := func(p [2]float64, gate float64) int { // 0: a→b, 1: b→a, -1: none
		ab, ba := p[0] >= gate, p[1] >= gate
		switch {
		case ab && !ba:
			return 0
		case ba && !ab:
			return 1
		case ab && ba && a.Date != "" && b.Date != "" && a.Date != b.Date:
			if a.Date > b.Date {
				return 0
			}
			return 1
		}
		return -1
	}
	link := func(kind string, dir int, p [2]float64) {
		from, to := a.ID, b.ID
		if dir == 1 {
			from, to = b.ID, a.ID
		}
		v.Links = append(v.Links, Link{Kind: kind, From: from, To: to, P: p[dir]})
	}
	// Dated outputs of two different routines (a morning brief and an
	// evening recap, a weekly digest and a daily log) share what they report
	// on, but neither revises or recounts the other: seven of eight wrong
	// changes and records links in a blind check were such pairs. Within one
	// series, a later output can change an earlier one.
	if a.Series != "" && b.Series != "" && a.Series != b.Series {
		v.Links = append(v.Links, Link{Kind: Companion, From: a.ID, To: b.ID, P: v.PRelated})
		return
	}
	changes := direction(v.PChanges, g.Changes)
	if changes >= 0 {
		link(Changes, changes, v.PChanges)
		if replaces := direction(v.PReplaces, g.Replaces); replaces == changes {
			link(Replaces, replaces, v.PReplaces)
		}
	}
	// Each might be read as an account of the other; only a record of
	// something that is not itself a record is taken as one, and when that
	// does not tell them apart neither is.
	records := -1
	recordOf := func(from, to Doc) bool { return from.Record && !to.Record }
	ab := v.PRecords[0] >= g.Records && recordOf(a, b)
	ba := v.PRecords[1] >= g.Records && recordOf(b, a)
	switch {
	case ab && !ba:
		records = 0
	case ba && !ab:
		records = 1
	}
	if records >= 0 {
		link(Records, records, v.PRecords)
	}
	if len(v.Links) == 0 {
		v.Links = append(v.Links, Link{Kind: Companion, From: a.ID, To: b.ID, P: v.PRelated})
	}
}

// datedName is a file named by its date, as a routine names its outputs.
var datedName = regexp.MustCompile(`^20\d\d-\d\d-\d\d(?:[_-].*)?$`)

// DatedSeries returns the series a document at p belongs to when it is a
// dated output of a routine (a file named by its date, such as
// vt_daily_brief/2026-10-14.md): its directory. For any other document it
// returns "".
func DatedSeries(p string) string {
	base := path.Base(p)
	if !datedName.MatchString(strings.TrimSuffix(base, path.Ext(base))) {
		return ""
	}
	return path.Dir(p)
}
