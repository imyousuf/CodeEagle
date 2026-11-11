package relate

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/imyousuf/CodeEagle/pkg/kit/decide"
)

// The names the questions are asked under. They reach the judge, so they
// are part of what it is asked: keep them stable.
const (
	qRelated    = "related"
	qChangesAB  = "changes_ab"
	qChangesBA  = "changes_ba"
	qReplacesAB = "replaces_ab"
	qReplacesBA = "replaces_ba"
	qRecordsAB  = "records_ab"
	qRecordsBA  = "records_ba"
)

// Wording is how the questions are put to the judge. Each directional
// question is asked both ways: x is the document it asks about and y the
// other, "A" and "B" as the default state names them (document_a and
// document_b).
type Wording struct {
	Related  decide.Question
	Changes  func(x, y string) decide.Question
	Replaces func(x, y string) decide.Question
	Records  func(x, y string) decide.Question
}

// DefaultWording is the wording DefaultGates were measured with. Each
// question spells out what yes and no mean, which sharpens a judgment
// measurably over the bare question.
func DefaultWording() Wording {
	about := "the work, design or decision"
	return Wording{
		Related: decide.YesNo(
			"Are document A and document B about the same piece of work, so that a reader of one would want to know about the other?",
			"they concern the same feature, system, plan, incident or decision: one builds on, revises, records or accompanies the other",
			"they share vocabulary or a broad area only; a reader of one would gain nothing from the other",
		),
		Changes: func(x, y string) decide.Question {
			return decide.YesNo(
				fmt.Sprintf("Does document %s change part of what document %s specifies?", x, y),
				fmt.Sprintf("%s revises, renames, re-homes or overrides some of %s in %s, so that part of %s is out of date while the rest may still hold", x, about, y, y),
				fmt.Sprintf("%s leaves what %s specifies as it is: it adds to it, depends on it, records something about it, or is about something else", x, y),
			)
		},
		Replaces: func(x, y string) decide.Question {
			return decide.YesNo(
				fmt.Sprintf("Does document %s replace document %s as a whole?", x, y),
				fmt.Sprintf("%s is the current account of everything %s covered, so %s as a whole is no longer the account to follow", x, y, y),
				fmt.Sprintf("%s still holds at least in part: %s changes only some of it, adds to it, or is about something else", y, x),
			)
		},
		Records: func(x, y string) decide.Question {
			return decide.YesNo(
				fmt.Sprintf("Is document %s an account of something that happened involving what document %s describes?", x, y),
				fmt.Sprintf("%s reports an incident, outage, investigation, review, meeting, conversation or decision about the system, feature or plan that %s describes", x, y),
				fmt.Sprintf("%s is itself a design, plan or reference rather than an account of events, or the events it reports concern something else", x),
			)
		},
	}
}

// questions are the seven questions asked about every pair.
func (w Wording) questions() map[string]decide.Question {
	return map[string]decide.Question{
		qRelated:    w.Related,
		qChangesAB:  w.Changes("A", "B"),
		qChangesBA:  w.Changes("B", "A"),
		qReplacesAB: w.Replaces("A", "B"),
		qReplacesBA: w.Replaces("B", "A"),
		qRecordsAB:  w.Records("A", "B"),
		qRecordsBA:  w.Records("B", "A"),
	}
}

// Card is what the judge sees of one document in the default state.
type Card struct {
	Path     string    `json:"path"`
	Type     string    `json:"type_of_document"`
	Title    string    `json:"title"`
	Status   string    `json:"status_the_document_states,omitempty"`
	Date     string    `json:"date_the_document_states,omitempty"`
	Outline  []string  `json:"headings_of_the_document"`
	Excerpts []Excerpt `json:"excerpts_of_the_document"`
}

// PairState is the default state a pair is judged on. A judge reads it as
// text, so the keys say what each part is.
type PairState struct {
	A     Card     `json:"document_a"`
	B     Card     `json:"document_b"`
	Facts []string `json:"facts_about_the_pair"`
}

// CardOf is the card of a document, with the excerpts the pair rests on.
func CardOf(d Doc, excerpts []Excerpt) Card {
	return Card{Path: d.ID, Type: d.Type, Title: d.Title, Status: d.Status, Date: d.Date,
		Outline: d.Outline, Excerpts: excerpts}
}

// DefaultState is the PairState of a candidate: each document's card and
// what is known about the pair.
func DefaultState(c *Candidate) any {
	return PairState{A: CardOf(c.A, c.ExcerptsA), B: CardOf(c.B, c.ExcerptsB), Facts: c.Facts}
}

// DefaultConcurrency is how many pairs are judged at once.
const DefaultConcurrency = 4

// Relater judges pairs of documents and keeps what it decides.
type Relater struct {
	judge       decide.Judge
	gates       Gates
	questions   map[string]decide.Question
	state       func(*Candidate) any
	concurrency int
	name        func() string
}

// Option configures a Relater.
type Option func(*Relater)

// WithGates sets the probabilities at or above which each kind is accepted;
// DefaultGates otherwise.
func WithGates(g Gates) Option { return func(r *Relater) { r.gates = g } }

// WithWording sets how the questions are put; DefaultWording otherwise.
func WithWording(w Wording) Option {
	return func(r *Relater) { r.questions = w.questions() }
}

// WithState sets what the judge is shown of a pair; DefaultState otherwise.
// The state names the documents A and B, as the wording does.
func WithState(fn func(*Candidate) any) Option { return func(r *Relater) { r.state = fn } }

// WithConcurrency sets how many pairs are judged at once, each in its own
// request; DefaultConcurrency otherwise.
func WithConcurrency(n int) Option {
	return func(r *Relater) {
		if n > 0 {
			r.concurrency = n
		}
	}
}

// WithJudgeName sets what a verdict records as its judge, asked once per
// verdict; the judge's Name otherwise. A caller that knows which model
// answered, which a decide.Judge does not say, names it here.
func WithJudgeName(fn func() string) Option { return func(r *Relater) { r.name = fn } }

// New builds a Relater that asks judge. A nil judge is allowed: such a
// Relater judges nothing and so relates nothing, which is what a program
// without a decision model should get, rather than relations asserted on
// similarity alone.
func New(judge decide.Judge, opts ...Option) *Relater {
	r := &Relater{
		judge:       judge,
		gates:       DefaultGates(),
		questions:   DefaultWording().questions(),
		state:       DefaultState,
		concurrency: DefaultConcurrency,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Gates returns the gates the Relater decides links under.
func (r *Relater) Gates() Gates { return r.gates }

func (r *Relater) judgeName() string {
	if r.name != nil {
		return r.name()
	}
	return r.judge.Name()
}

// Judgment is the raw answer for one pair: probabilities, with no links
// decided and no time recorded.
type Judgment struct {
	Candidate *Candidate
	Verdict   Verdict
}

// Report is what judging cost.
type Report struct {
	// Requests counts the pairs judged, one request each.
	Requests int
	// InputTokens and OutputTokens are what the judge spent, where it says
	// (decide.JevJudge and decide.LLMJudge do). A judge shared with other
	// work at the same time is counted with it.
	InputTokens  int
	OutputTokens int
	// Judge names what answered, once anything has.
	Judge string
	// Failed counts pairs whose request failed. They get no verdict, so a
	// later run asks again.
	Failed int
}

// Judge asks about each pair in its own request, a few at a time, and
// returns the answers in the order of cands. A pair whose request fails has
// no verdict and is reported in Failed; the error is returned only when
// every pair failed. With no judge it returns nothing.
func (r *Relater) Judge(ctx context.Context, cands []*Candidate) ([]Judgment, Report, error) {
	var report Report
	if r.judge == nil || len(cands) == 0 {
		return nil, report, nil
	}
	before := spent(r.judge)
	var (
		wg       sync.WaitGroup
		sem      = make(chan struct{}, r.concurrency)
		verdicts = make([]*Verdict, len(cands))
		errs     = make([]error, len(cands))
	)
	for i, c := range cands {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			v, err := r.judgeOne(ctx, c)
			if err != nil {
				errs[i] = err
				return
			}
			verdicts[i] = &v
		}()
	}
	wg.Wait()

	var out []Judgment
	var failed []error
	for i, v := range verdicts {
		if v == nil {
			report.Failed++
			failed = append(failed, errs[i])
			continue
		}
		report.Requests++
		out = append(out, Judgment{Candidate: cands[i], Verdict: *v})
	}
	after := spent(r.judge)
	report.InputTokens = after.InputTokens - before.InputTokens
	report.OutputTokens = after.OutputTokens - before.OutputTokens
	if report.Requests > 0 {
		report.Judge = r.judgeName()
	}
	if len(out) == 0 && len(failed) > 0 {
		return nil, report, errors.Join(failed...)
	}
	return out, report, nil
}

func (r *Relater) judgeOne(ctx context.Context, c *Candidate) (Verdict, error) {
	answers, err := r.judge.Decide(ctx, r.state(c), r.questions)
	if err != nil {
		return Verdict{}, fmt.Errorf("judge %s and %s: %w", c.A.ID, c.B.ID, err)
	}
	p := func(name string) float64 { return answers[name].P }
	return Verdict{
		Other:     c.B.ID,
		PRelated:  p(qRelated),
		PChanges:  [2]float64{p(qChangesAB), p(qChangesBA)},
		PReplaces: [2]float64{p(qReplacesAB), p(qReplacesBA)},
		PRecords:  [2]float64{p(qRecordsAB), p(qRecordsBA)},
		Signals:   c.Signals,
		SigA:      c.A.Hash,
		SigB:      c.B.Hash,
		Judge:     r.judgeName(),
	}, nil
}

// spent is what a judge has spent so far, where it says.
func spent(j decide.Judge) decide.Usage {
	if u, ok := j.(interface{ Usage() decide.Usage }); ok {
		return u.Usage()
	}
	return decide.Usage{}
}
