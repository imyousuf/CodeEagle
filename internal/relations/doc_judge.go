package relations

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/pkg/jev"
)

// Kinds of relation between two documents.
const (
	// DocChanges: one document changes part of what the other specifies,
	// which otherwise still holds (a redesign of its frontend).
	DocChanges = "changes"
	// DocReplaces: the other, as a whole, is no longer the current account.
	DocReplaces = "replaces"
	// DocRecords: one is an account of something that happened (an
	// incident, a review, a meeting, a conversation) involving what the
	// other describes.
	DocRecords = "records"
	// DocCompanion: they concern the same piece of work, with no direction.
	DocCompanion = "companion"
)

// DocLink is an accepted relation between two documents, From to To; a
// companion has no direction and is recorded from the lower path.
type DocLink struct {
	Kind string  `json:"kind"`
	From string  `json:"from"`
	To   string  `json:"to"`
	P    float64 `json:"p"`
}

// DocVerdict is the decision model's answer about a pair of documents,
// stored on the relation of the lower path and kept whether or not anything
// was accepted, so the pair is not asked about again while neither changes.
// Directional probabilities are [lower→higher, higher→lower].
type DocVerdict struct {
	Other     string     `json:"other"`
	PRelated  float64    `json:"p_related"`
	PChanges  [2]float64 `json:"p_changes"`
	PReplaces [2]float64 `json:"p_replaces"`
	PRecords  [2]float64 `json:"p_records"`
	Links     []DocLink  `json:"links,omitempty"`
	Signals   []string   `json:"signals,omitempty"`
	SigA      string     `json:"sig_a"`
	SigB      string     `json:"sig_b"`
	Judge     string     `json:"judge"`
	JudgedAt  time.Time  `json:"judged_at"`
}

// DocGates are the probabilities at or above which each kind is accepted.
type DocGates struct {
	Related, Changes, Replaces, Records float64
}

// DefaultDocGates are provisional until calibrated on labelled pairs, and
// strict where a wrong answer costs most: calling a document replaced hides
// it from a reader who would have needed it.
//
// Measured on 136 pairs two blind labellers agreed on (kappa 0.86 on
// related): related AUC 0.90, precision 0.83 at 0.8 and 0.90 at 0.9; changes
// precision 0.89 at recall 0.47 at 0.75, against 1.00 at recall 0.14 at 0.85.
// No pair labelled replaces scored above 0.62, so at 0.95 replaces is never
// shown and a revision reads as "changed by", which every replacing document
// also is.
func DefaultDocGates() DocGates {
	return DocGates{Related: 0.85, Changes: 0.75, Replaces: 0.95, Records: 0.85}
}

// docCard is what the decision model sees of one document.
type docCardFull struct {
	Path     string       `json:"path"`
	Type     string       `json:"type_of_document"`
	Title    string       `json:"title"`
	Status   string       `json:"status_the_document_states,omitempty"`
	Date     string       `json:"date_the_document_states,omitempty"`
	Outline  []string     `json:"headings_of_the_document"`
	Excerpts []docExcerpt `json:"excerpts_of_the_document"`
}

type docPairState struct {
	A     docCardFull `json:"document_a"`
	B     docCardFull `json:"document_b"`
	Facts []string    `json:"facts_about_the_pair"`
}

// DocJudge decides with the decision model how two documents relate.
type DocJudge struct {
	asker jev.Asker
}

// NewDocJudge builds a document-pair judge.
func NewDocJudge(asker jev.Asker) *DocJudge { return &DocJudge{asker: asker} }

// DocJudgment is the raw answer for one pair.
type DocJudgment struct {
	Candidate *DocCandidate
	Verdict   DocVerdict
}

const docJudgeConcurrency = 4

// Judge asks about each pair in its own request, a few at a time: pairs
// judged together moved each other's probabilities when measured on
// meetings. A pair whose request fails has no verdict and is asked again
// on a later run.
func (j *DocJudge) Judge(ctx context.Context, cands []*DocCandidate) ([]DocJudgment, JudgeReport, error) {
	var (
		mu     sync.Mutex
		out    []DocJudgment
		report JudgeReport
		wg     sync.WaitGroup
		sem    = make(chan struct{}, docJudgeConcurrency)
		errs   []error
	)
	for _, c := range cands {
		wg.Add(1)
		sem <- struct{}{}
		go func(c *DocCandidate) {
			defer wg.Done()
			defer func() { <-sem }()
			v, usage, model, err := j.judgeOne(ctx, c)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				report.Failed++
				errs = append(errs, err)
				return
			}
			report.Requests++
			report.InputTokens += usage
			report.Model = model
			out = append(out, DocJudgment{Candidate: c, Verdict: v})
		}(c)
	}
	wg.Wait()
	if len(out) == 0 && len(errs) > 0 {
		return nil, report, errors.Join(errs...)
	}
	return out, report, nil
}

func (j *DocJudge) judgeOne(ctx context.Context, c *DocCandidate) (DocVerdict, int, string, error) {
	state := docPairState{A: docCardOf(c.A, c.SectionsA), B: docCardOf(c.B, c.SectionsB), Facts: c.Facts}
	about := "the work, design or decision"
	q := jev.Questions{
		"related": jev.NoulWithCriteria(
			"Are document A and document B about the same piece of work, so that a reader of one would want to know about the other?",
			"they concern the same feature, system, plan, incident or decision: one builds on, revises, records or accompanies the other",
			"they share vocabulary or a broad area only; a reader of one would gain nothing from the other",
		),
	}
	for _, d := range []struct{ name, x, y string }{{"ab", "A", "B"}, {"ba", "B", "A"}} {
		q["changes_"+d.name] = jev.NoulWithCriteria(
			fmt.Sprintf("Does document %s change part of what document %s specifies?", d.x, d.y),
			fmt.Sprintf("%s revises, renames, re-homes or overrides some of %s in %s, so that part of %s is out of date while the rest may still hold", d.x, about, d.y, d.y),
			fmt.Sprintf("%s leaves what %s specifies as it is: it adds to it, depends on it, records something about it, or is about something else", d.x, d.y),
		)
		q["replaces_"+d.name] = jev.NoulWithCriteria(
			fmt.Sprintf("Does document %s replace document %s as a whole?", d.x, d.y),
			fmt.Sprintf("%s is the current account of everything %s covered, so %s as a whole is no longer the account to follow", d.x, d.y, d.y),
			fmt.Sprintf("%s still holds at least in part: %s changes only some of it, adds to it, or is about something else", d.y, d.x),
		)
		q["records_"+d.name] = jev.NoulWithCriteria(
			fmt.Sprintf("Is document %s an account of something that happened involving what document %s describes?", d.x, d.y),
			fmt.Sprintf("%s reports an incident, outage, investigation, review, meeting, conversation or decision about the system, feature or plan that %s describes", d.x, d.y),
			fmt.Sprintf("%s is itself a design, plan or reference rather than an account of events, or the events it reports concern something else", d.x),
		)
	}
	resp, err := j.asker.Ask(ctx, state, q)
	if err != nil {
		return DocVerdict{}, 0, "", fmt.Errorf("judge %s and %s: %w", c.A.Path, c.B.Path, err)
	}
	get := func(name string) float64 {
		p, _ := resp.Answers.Noul(name)
		return p
	}
	v := DocVerdict{
		Other:     c.B.Path,
		PRelated:  get("related"),
		PChanges:  [2]float64{get("changes_ab"), get("changes_ba")},
		PReplaces: [2]float64{get("replaces_ab"), get("replaces_ba")},
		PRecords:  [2]float64{get("records_ab"), get("records_ba")},
		Signals:   c.Signals,
		SigA:      c.A.Hash,
		SigB:      c.B.Hash,
		Judge:     resp.Model,
	}
	return v, resp.Usage.InputTokens, resp.Model, nil
}

// maxCardOutline and maxCardExcerpt bound a document card.
const (
	maxCardOutline = 15
	maxCardExcerpt = 700
)

func docCardOf(s *Subject, secs []*Section) docCardFull {
	asset, _ := ClassifyAsset(s)
	status, date := DocStatus(s)
	card := docCardFull{Path: s.Path, Type: string(asset), Title: s.Title, Status: status, Date: date}
	for i, sec := range s.Sections {
		if i >= maxCardOutline {
			break
		}
		card.Outline = append(card.Outline, sec.HeadingPath)
	}
	if len(secs) == 0 && len(s.Sections) > 0 {
		secs = s.Sections[:1]
	}
	if s.Preamble != "" {
		card.Excerpts = append(card.Excerpts, docExcerpt{Section: "opening", Text: excerpt(s.Preamble, maxCardExcerpt)})
	}
	for _, sec := range secs {
		if sec.Text != "" {
			card.Excerpts = append(card.Excerpts, docExcerpt{Section: sec.HeadingPath, Text: excerpt(sec.Text, maxCardExcerpt)})
		}
	}
	return card
}

// Decide turns the probabilities into accepted links. Every tie falls back
// to the weaker claim, because a wrong link is worse than a missing one:
//   - a direction is kept only if its reverse is below the gate, or, when
//     both are above it, the documents state dates and the later one is
//     the one changing or replacing the earlier;
//   - replacing requires changing;
//   - recording in both directions keeps neither;
//   - related pairs with no directional kind are companions.
func (v *DocVerdict) Decide(a, b *Subject, g DocGates) {
	v.Links = nil
	if v.PRelated < g.Related {
		return
	}
	_, dateA := DocStatus(a)
	_, dateB := DocStatus(b)
	direction := func(p [2]float64, gate float64) int { // 0: a→b, 1: b→a, -1: none
		ab, ba := p[0] >= gate, p[1] >= gate
		switch {
		case ab && !ba:
			return 0
		case ba && !ab:
			return 1
		case ab && ba && dateA != "" && dateB != "" && dateA != dateB:
			if dateA > dateB {
				return 0
			}
			return 1
		}
		return -1
	}
	link := func(kind string, dir int, p [2]float64) {
		from, to := a.Path, b.Path
		if dir == 1 {
			from, to = b.Path, a.Path
		}
		v.Links = append(v.Links, DocLink{Kind: kind, From: from, To: to, P: p[dir]})
	}
	changes := direction(v.PChanges, g.Changes)
	if changes >= 0 {
		link(DocChanges, changes, v.PChanges)
		if replaces := direction(v.PReplaces, g.Replaces); replaces == changes {
			link(DocReplaces, replaces, v.PReplaces)
		}
	}
	// Each might be read as an account of the other; only a document whose
	// kind is to record things (an incident write-up, notes, a chat) is
	// taken as the record, and when that does not tell them apart neither
	// is.
	//
	// A record is of something that is not itself a record: asked whether a
	// later review of a pull request records the earlier review, the model
	// said yes at 0.9, and 19 of 20 records links on labelled pairs were of
	// that kind. So the record side must be record-like and the other side
	// must not be.
	records := -1
	recordOf := func(from, to *Subject) bool { return recordLike(from) && !recordLike(to) }
	ab := v.PRecords[0] >= g.Records && recordOf(a, b)
	ba := v.PRecords[1] >= g.Records && recordOf(b, a)
	switch {
	case ab && !ba:
		records = 0
	case ba && !ab:
		records = 1
	}
	if records >= 0 {
		link(DocRecords, records, v.PRecords)
	}
	if len(v.Links) == 0 {
		v.Links = append(v.Links, DocLink{Kind: DocCompanion, From: a.Path, To: b.Path, P: v.PRelated})
	}
}

// recordLike reports whether a document's kind is to record what happened.
func recordLike(s *Subject) bool {
	switch asset, _ := ClassifyAsset(s); asset {
	case AssetIncident, AssetMeetingNotes, AssetNotes, AssetChat, AssetReport:
		return true
	}
	return false
}

// JudgedDoc reports, for a pair, whether a verdict exists under the same
// content of both documents.
func JudgedDoc(rels map[string]*Relation) func(*DocCandidate) bool {
	return func(c *DocCandidate) bool {
		rel := rels[c.A.Path]
		if rel == nil {
			return false
		}
		v := rel.DocLinks[c.B.Path]
		return v != nil && v.SigA == c.A.Hash && v.SigB == c.B.Hash
	}
}

// DocRunStats are what judging document pairs did.
type DocRunStats struct {
	Judged   int
	Accepted map[string]int // by kind
	Judging  JudgeReport
}

// JudgeDocPairs judges the candidates, decides their links with the gates,
// stores each verdict on the relation of the lower path, and writes it with
// its edges.
func JudgeDocPairs(ctx context.Context, store graph.Store, judge *DocJudge, cands []*DocCandidate,
	rels map[string]*Relation, gates DocGates, now time.Time) (DocRunStats, error) {
	stats := DocRunStats{Accepted: make(map[string]int)}
	if len(cands) == 0 {
		return stats, nil
	}
	judgments, report, err := judge.Judge(ctx, cands)
	stats.Judging = report
	if err != nil && len(judgments) == 0 {
		return stats, err
	}
	relater := NewRelater(store, nil, RelaterOptions{})
	touched := make(map[string]*Subject)
	for _, jd := range judgments {
		c := jd.Candidate
		v := jd.Verdict
		v.JudgedAt = now
		v.Decide(c.A, c.B, gates)
		rel := rels[c.A.Path]
		if rel == nil {
			rel = New(c.A.Path)
			rel.Observe(c.A, now)
			rels[c.A.Path] = rel
		}
		if rel.DocLinks == nil {
			rel.DocLinks = make(map[string]*DocVerdict)
		}
		vv := v
		rel.DocLinks[c.B.Path] = &vv
		touched[c.A.Path] = c.A
		stats.Judged++
		for _, l := range v.Links {
			stats.Accepted[l.Kind]++
		}
	}
	for path, subj := range touched {
		if err := relater.Write(ctx, rels[path], subj); err != nil {
			return stats, fmt.Errorf("store document verdicts of %s: %w", path, err)
		}
	}
	return stats, nil
}
