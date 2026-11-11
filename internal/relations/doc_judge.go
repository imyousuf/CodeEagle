package relations

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/imyousuf/CodeEagle/pkg/jev"
	"github.com/imyousuf/CodeEagle/pkg/kit/decide"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
	"github.com/imyousuf/CodeEagle/pkg/kit/relate"
)

// Kinds of relation between two documents, as pkg/kit/relate decides them.
const (
	// DocChanges: one document changes part of what the other specifies,
	// which otherwise still holds (a redesign of its frontend).
	DocChanges = relate.Changes
	// DocReplaces: the other, as a whole, is no longer the current account.
	DocReplaces = relate.Replaces
	// DocRecords: one is an account of something that happened (an
	// incident, a review, a meeting, a conversation) involving what the
	// other describes.
	DocRecords = relate.Records
	// DocCompanion: they concern the same piece of work, with no direction.
	DocCompanion = relate.Companion
)

// DocLink is an accepted relation between two documents, From to To; a
// companion has no direction and is recorded from the lower path.
type DocLink = relate.Link

// DocVerdict is the decision model's answer about a pair of documents,
// stored on the relation of the lower path and kept whether or not anything
// was accepted, so the pair is not asked about again while neither changes.
// Directional probabilities are [lower→higher, higher→lower].
//
// It is the kit's verdict, stored as it always was; its own type only so
// that Decide can take subjects.
type DocVerdict relate.Verdict

// DocGates are the probabilities at or above which each kind is accepted.
type DocGates = relate.Gates

// DefaultDocGates are the kit's default gates, which were measured with Jev
// on this package's documents and wording; see relate.DefaultGates.
func DefaultDocGates() DocGates { return relate.DefaultGates() }

// DocJudge decides with the decision model how two documents relate.
type DocJudge struct {
	judge decide.Judge // nil without a decision model
	asker *answered
}

// NewDocJudge builds a document-pair judge over the decision model. It asks
// in the kit's default wording, which is the wording DefaultDocGates were
// measured with.
func NewDocJudge(asker jev.Asker) *DocJudge {
	if decide.Optional(asker) == nil {
		return &DocJudge{}
	}
	a := &answered{Asker: asker}
	return &DocJudge{judge: decide.NewJevJudge(a), asker: a}
}

// relater is the kit's relater over this judge, deciding under g.
func (j *DocJudge) relater(g DocGates) *relate.Relater {
	return relate.New(j.judge, relate.WithGates(g), relate.WithJudgeName(j.model),
		relate.WithConcurrency(docJudgeConcurrency))
}

// model names the model that answered, as a verdict records it.
func (j *DocJudge) model() string {
	if j.asker == nil {
		return ""
	}
	return j.asker.lastModel()
}

// answered is the decision model as the judge asks it, remembering which
// model answered: a verdict names it, and a decide.Judge does not say. Every
// request goes to the one model the client pins, so the last to answer names
// them all.
type answered struct {
	jev.Asker
	mu   sync.Mutex
	last string
}

func (a *answered) Ask(ctx context.Context, state any, q jev.Questions) (*jev.Response, error) {
	resp, err := a.Asker.Ask(ctx, state, q)
	if err == nil && resp != nil {
		a.mu.Lock()
		a.last = resp.Model
		a.mu.Unlock()
	}
	return resp, err
}

// Model is the model the client asks for, so the judge's Name stays true.
func (a *answered) Model() string {
	if m, ok := a.Asker.(interface{ Model() string }); ok {
		return m.Model()
	}
	return ""
}

func (a *answered) lastModel() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.last
}

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
	pairs, orig := kitCandidates(cands)
	judgments, report, err := j.relater(DefaultDocGates()).Judge(ctx, pairs)
	if err != nil {
		return nil, judgeReport(report), err
	}
	var out []DocJudgment
	for _, jd := range judgments {
		out = append(out, DocJudgment{Candidate: orig[jd.Candidate], Verdict: DocVerdict(jd.Verdict)})
	}
	return out, judgeReport(report), nil
}

// judgeReport is the kit's report as this package reports judging.
func judgeReport(r relate.Report) JudgeReport {
	return JudgeReport{Requests: r.Requests, InputTokens: r.InputTokens, Model: r.Judge, Failed: r.Failed}
}

// maxCardOutline and maxCardExcerpt bound a document card.
const (
	maxCardOutline = 15
	maxCardExcerpt = 700
)

// docOf is a subject as the kit relates it: what the decision model is told
// of it, and what the direction rules read.
func docOf(s *Subject) relate.Doc {
	asset, _ := ClassifyAsset(s)
	status, date := DocStatus(s)
	d := relate.Doc{ID: s.Path, Hash: s.Hash, Title: s.Title, Type: string(asset), Status: status, Date: date,
		Record: recordKind(asset), Series: relate.DatedSeries(s.Path), UpdatedAt: s.UpdatedAt}
	for i, sec := range s.Sections {
		if i >= maxCardOutline {
			break
		}
		d.Outline = append(d.Outline, sec.HeadingPath)
	}
	return d
}

// docExcerptsOf is what the decision model reads of a subject for a pair:
// its opening, then the sections the pair rests on, or its first section
// when it rests on none.
func docExcerptsOf(s *Subject, secs []*Section) []relate.Excerpt {
	var out []relate.Excerpt
	if len(secs) == 0 && len(s.Sections) > 0 {
		secs = s.Sections[:1]
	}
	if s.Preamble != "" {
		out = append(out, relate.Excerpt{Section: "opening", Text: excerpt(s.Preamble, maxCardExcerpt)})
	}
	for _, sec := range secs {
		if sec.Text != "" {
			out = append(out, relate.Excerpt{Section: sec.HeadingPath, Text: excerpt(sec.Text, maxCardExcerpt)})
		}
	}
	return out
}

// kit is the candidate as the kit judges it.
func (c *DocCandidate) kit() *relate.Candidate {
	return &relate.Candidate{A: docOf(c.A), B: docOf(c.B), Signals: c.Signals, Facts: c.Facts,
		ExcerptsA: docExcerptsOf(c.A, c.SectionsA), ExcerptsB: docExcerptsOf(c.B, c.SectionsB), Score: c.Score}
}

// kitCandidates converts candidates for the kit, with the way back.
func kitCandidates(cands []*DocCandidate) ([]*relate.Candidate, map[*relate.Candidate]*DocCandidate) {
	pairs := make([]*relate.Candidate, len(cands))
	orig := make(map[*relate.Candidate]*DocCandidate, len(cands))
	for i, c := range cands {
		pairs[i] = c.kit()
		orig[pairs[i]] = c
	}
	return pairs, orig
}

// Decide turns the probabilities into accepted links under the kit's rules
// (relate.Verdict.Decide): every tie falls back to the weaker claim, a
// direction holds only when its reverse does not or the later-dated
// document takes it, replacing requires changing, only a record-like
// document records another, and dated outputs of two routines are only
// companions.
func (v *DocVerdict) Decide(a, b *Subject, g DocGates) {
	(*relate.Verdict)(v).Decide(docOf(a), docOf(b), g)
}

// RedecideDocs applies the gates and rules to every stored document verdict
// again and rewrites the links and edges that follow from them, without
// asking the decision model anything: the probabilities are kept, so a change
// of gate or rule takes effect at no cost. It returns how many links changed.
func RedecideDocs(ctx context.Context, store graph.Store, rels map[string]*Relation, subjects []*Subject, g DocGates) (int, error) {
	by := make(map[string]*Subject, len(subjects))
	for _, s := range subjects {
		by[s.Path] = s
	}
	relater := NewRelater(store, nil, RelaterOptions{})
	changed := 0
	for p, rel := range rels {
		touched := false
		for other, v := range rel.DocLinks {
			a, b := by[p], by[other]
			if a == nil || b == nil {
				continue
			}
			before := fmt.Sprint(v.Links)
			v.Decide(a, b, g)
			if fmt.Sprint(v.Links) != before {
				changed++
				touched = true
			}
		}
		if touched {
			if err := relater.Write(ctx, rel, by[p]); err != nil {
				return changed, fmt.Errorf("store document verdicts of %s: %w", p, err)
			}
		}
	}
	return changed, nil
}

// recordKind reports whether a document of this kind is there to record
// what happened.
func recordKind(asset AssetType) bool {
	switch asset {
	case AssetIncident, AssetMeetingNotes, AssetNotes, AssetChat, AssetReport:
		return true
	}
	return false
}

// JudgedDoc reports, for a pair, whether a verdict exists under the same
// content of both documents.
func JudgedDoc(rels map[string]*Relation) func(*DocCandidate) bool {
	l := &docLedger{rels: rels}
	return func(c *DocCandidate) bool {
		return l.Verdict(c.A.Path, c.B.Path).Current(relate.Doc{Hash: c.A.Hash}, relate.Doc{Hash: c.B.Hash})
	}
}

// DocRunStats are what judging document pairs did.
type DocRunStats struct {
	Judged   int
	Accepted map[string]int // by kind
	Judging  JudgeReport
}

// docRunStats is the kit's stats as this package reports them.
func docRunStats(s relate.Stats) DocRunStats {
	return DocRunStats{Judged: s.Judged, Accepted: s.Accepted, Judging: judgeReport(s.Judging)}
}

// JudgeDocPairs judges the candidates, decides their links with the gates,
// stores each verdict on the relation of the lower path, and writes it with
// its edges.
func JudgeDocPairs(ctx context.Context, store graph.Store, judge *DocJudge, cands []*DocCandidate,
	rels map[string]*Relation, gates DocGates, now time.Time) (DocRunStats, error) {
	if len(cands) == 0 {
		return DocRunStats{Accepted: make(map[string]int)}, nil
	}
	subjects := make(map[string]*Subject, 2*len(cands))
	for _, c := range cands {
		subjects[c.A.Path], subjects[c.B.Path] = c.A, c.B
	}
	pairs, _ := kitCandidates(cands)
	ledger := &docLedger{store: store, rels: rels, subjects: subjects}
	stats, err := judge.relater(gates).Relate(ctx, pairs, ledger, now)
	if err != nil {
		return docRunStats(stats), err
	}
	return docRunStats(stats), ledger.Save(ctx)
}

// docLedger is the kit's ledger kept where this package keeps it: each
// verdict in the DocLinks of the relation of the pair's lower path, written
// with its edges, and what was considered in the scan state, when there is
// one.
type docLedger struct {
	store    graph.Store
	rels     map[string]*Relation
	subjects map[string]*Subject
	state    *ScanState
	touched  map[string]bool
}

func (l *docLedger) Verdict(a, b string) *relate.Verdict {
	rel := l.rels[a]
	if rel == nil {
		return nil
	}
	return (*relate.Verdict)(rel.DocLinks[b])
}

func (l *docLedger) Put(a, b relate.Doc, v *relate.Verdict) {
	rel := l.rels[a.ID]
	if rel == nil {
		rel = New(a.ID)
		rel.Observe(l.subjects[a.ID], v.JudgedAt)
		l.rels[a.ID] = rel
	}
	if rel.DocLinks == nil {
		rel.DocLinks = make(map[string]*DocVerdict)
	}
	rel.DocLinks[b.ID] = (*DocVerdict)(v)
	if l.touched == nil {
		l.touched = make(map[string]bool)
	}
	l.touched[a.ID] = true
}

func (l *docLedger) Considered(doc string) string {
	if l.state == nil {
		return ""
	}
	return l.state.DocPairs[doc]
}

func (l *docLedger) Consider(doc, hash string) {
	if l.state != nil {
		l.state.DocPairs[doc] = hash
	}
}

// Save writes the relations given verdicts, then the scan state.
func (l *docLedger) Save(ctx context.Context) error {
	relater := NewRelater(l.store, nil, RelaterOptions{})
	for path := range l.touched {
		if err := relater.Write(ctx, l.rels[path], l.subjects[path]); err != nil {
			return fmt.Errorf("store document verdicts of %s: %w", path, err)
		}
		delete(l.touched, path)
	}
	if l.state == nil {
		return nil
	}
	return l.state.Save(ctx, l.store)
}
