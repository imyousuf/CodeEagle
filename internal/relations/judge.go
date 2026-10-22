package relations

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/imyousuf/CodeEagle/internal/search"
	"github.com/imyousuf/CodeEagle/pkg/jev"
)

// Whether a meeting discussed a document is one yes/no question, not a choice
// between kinds of relation. That follows what was measured on topic pairs:
// a six-way choice of relation named the labelled one about half the time,
// while the probability of a plain "related" separated related pairs from
// unrelated ones with an AUC of 0.92. The kind of relation is left to the
// brief, which quotes what was said.

// Judgment is the decision model's probability that a candidate's meeting
// discussed its document.
type Judgment struct {
	Candidate *Candidate
	P         float64
}

// JudgeReport is what a round of judging cost.
type JudgeReport struct {
	Requests    int
	InputTokens int
	Latency     time.Duration
	Model       string
	// Failed counts candidates whose request failed. They get no verdict, so
	// a later run asks again.
	Failed int
}

// Bounds on one request and on how many run at once.
const (
	maxJudgePairs      = 10
	maxDocExcerpts     = 3
	maxDocExcerptChars = 500
	maxOutline         = 20
	maxSaidChars       = 400
	maxSpokenChars     = 400
	maxDecisionChars   = 200
	judgeConcurrency   = 4
)

// Judge decides with the decision model whether meetings discussed documents.
type Judge struct {
	asker       jev.Asker
	transcripts Transcripts
	opts        JudgeOptions
}

// JudgeOptions tune what the decision model is shown. The zero value is the
// measured default.
type JudgeOptions struct {
	// BatchSize is how many pairs about one document share a request.
	BatchSize int
	// RelevantWords shows the stretch of a segment whose words overlap most
	// with the document's, rather than the segment's opening.
	RelevantWords bool
	// SpokenChars bounds the speakers' own words shown per segment.
	SpokenChars int
}

// NewJudge builds a judge. transcripts may be nil, in which case the judge
// sees segment summaries without the speakers' own words.
func NewJudge(asker jev.Asker, transcripts Transcripts) *Judge {
	return NewJudgeWithOptions(asker, transcripts, JudgeOptions{})
}

// NewJudgeWithOptions builds a judge that shows the decision model what opts
// say.
func NewJudgeWithOptions(asker jev.Asker, transcripts Transcripts, opts JudgeOptions) *Judge {
	if opts.BatchSize <= 0 {
		opts.BatchSize = maxJudgePairs
	}
	if opts.SpokenChars <= 0 {
		opts.SpokenChars = maxSpokenChars
	}
	return &Judge{asker: asker, transcripts: transcripts, opts: opts}
}

// docCard is what the model sees of the document. Field names say exactly
// what each value is, because the model believes them.
type docCard struct {
	Path     string       `json:"path"`
	Title    string       `json:"title"`
	Outline  []string     `json:"headings_of_the_document"`
	Excerpts []docExcerpt `json:"excerpts_of_the_document"`
}

type docExcerpt struct {
	Section string `json:"section"`
	Text    string `json:"text"`
}

// segmentCard is what the model sees of one stretch of a meeting.
type segmentCard struct {
	ID            string `json:"id"`
	Meeting       string `json:"meeting_title"`
	Date          string `json:"meeting_date"`
	Topic         string `json:"topic_of_this_stretch_of_the_meeting"`
	WhatWasSaid   string `json:"summary_of_what_was_said,omitempty"`
	SpokenWords   string `json:"verbatim_words_spoken,omitempty"`
	DecisionQuote string `json:"verbatim_quote_from_a_decision_in_this_meeting,omitempty"`
}

// judgePairCard names a pair and the literal facts known about it.
type judgePairCard struct {
	Pair     string   `json:"pair"`
	Segments []string `json:"segment_ids"`
	// Sections are the document's sections nearest in meaning to what was
	// said; a list of headings, not a claim that they match.
	Sections []string `json:"sections_of_the_document_closest_in_meaning,omitempty"`
	// TitleWords are the document's title or headings whose words appear in
	// what the meeting said.
	TitleWords []string `json:"document_titles_or_headings_whose_words_appear_in_the_meeting,omitempty"`
	// IDs are identifiers written in both.
	IDs []string `json:"identifiers_appearing_in_both,omitempty"`
}

type judgeState struct {
	Document docCard         `json:"document"`
	Segments []segmentCard   `json:"meeting_segments"`
	Pairs    []judgePairCard `json:"pairs"`
}

// Judge returns a judgment per candidate whose request succeeded. Candidates
// are judged in batches per document, a few batches at a time. A batch that
// fails is reported and skipped rather than ending the run: its pairs simply
// have no verdict yet.
func (j *Judge) Judge(ctx context.Context, cands []*Candidate) ([]Judgment, JudgeReport, error) {
	var report JudgeReport
	if j == nil || j.asker == nil || len(cands) == 0 {
		return nil, report, nil
	}

	bySubject := make(map[string][]*Candidate)
	var order []string
	for _, c := range cands {
		if _, ok := bySubject[c.Subject.Path]; !ok {
			order = append(order, c.Subject.Path)
		}
		bySubject[c.Subject.Path] = append(bySubject[c.Subject.Path], c)
	}
	var batches [][]*Candidate
	for _, path := range order {
		group := bySubject[path]
		for start := 0; start < len(group); start += j.opts.BatchSize {
			batches = append(batches, group[start:min(start+j.opts.BatchSize, len(group))])
		}
	}

	var (
		mu    sync.Mutex
		out   []Judgment
		errs  []error
		wg    sync.WaitGroup
		slots = make(chan struct{}, judgeConcurrency)
	)
	for _, batch := range batches {
		wg.Add(1)
		slots <- struct{}{}
		go func(batch []*Candidate) {
			defer wg.Done()
			defer func() { <-slots }()
			var local JudgeReport
			got, err := j.judgeBatch(ctx, batch, &local)
			mu.Lock()
			defer mu.Unlock()
			report.Requests += local.Requests
			report.InputTokens += local.InputTokens
			report.Latency += local.Latency
			if local.Model != "" {
				report.Model = local.Model
			}
			if err != nil {
				report.Failed += len(batch)
				errs = append(errs, err)
				return
			}
			out = append(out, got...)
		}(batch)
	}
	wg.Wait()

	sort.SliceStable(out, func(a, b int) bool { return out[a].Candidate.Key() < out[b].Candidate.Key() })
	return out, report, errors.Join(errs...)
}

func (j *Judge) judgeBatch(ctx context.Context, batch []*Candidate, report *JudgeReport) ([]Judgment, error) {
	state, questions := j.buildState(batch)
	started := time.Now()
	resp, err := j.asker.Ask(ctx, state, questions)
	report.Latency += time.Since(started)
	if err != nil {
		// The size ceiling is the service's, and refusing is how it reports
		// it; halving does not guess at tokens.
		if errors.Is(err, jev.ErrTooLarge) && len(batch) > 1 {
			half := len(batch) / 2
			first, err := j.judgeBatch(ctx, batch[:half], report)
			if err != nil {
				return nil, err
			}
			second, err := j.judgeBatch(ctx, batch[half:], report)
			if err != nil {
				return nil, err
			}
			return append(first, second...), nil
		}
		return nil, fmt.Errorf("judge %s: %w", batch[0].Subject.Path, err)
	}
	report.Requests++
	report.InputTokens += resp.Usage.InputTokens
	report.Model = resp.Model

	out := make([]Judgment, 0, len(batch))
	for i, c := range batch {
		p, err := resp.Answers.Noul(discussesQuestion + judgePairName(i))
		if err != nil {
			return nil, fmt.Errorf("judge %s: %w", c.Subject.Path, err)
		}
		out = append(out, Judgment{Candidate: c, P: p})
	}
	return out, nil
}

const discussesQuestion = "discusses_"

func judgePairName(i int) string { return fmt.Sprintf("p%d", i) }

// buildState renders a batch, all about one document, as one state and one
// question per pair.
func (j *Judge) buildState(batch []*Candidate) (*judgeState, jev.Questions) {
	subj := batch[0].Subject
	st := &judgeState{Document: docCard{Path: subj.Path, Title: subj.Title}}
	for i, sec := range subj.Sections {
		if i >= maxOutline {
			break
		}
		st.Document.Outline = append(st.Document.Outline, sec.HeadingPath)
	}
	// The sections the pairs rest on, best first, up to a few.
	seenSec := make(map[*Section]bool)
	for _, c := range batch {
		for _, sec := range c.Sections {
			if len(st.Document.Excerpts) >= maxDocExcerpts || seenSec[sec] || sec.Text == "" {
				continue
			}
			seenSec[sec] = true
			st.Document.Excerpts = append(st.Document.Excerpts, docExcerpt{
				Section: sec.HeadingPath, Text: excerpt(sec.Text, maxDocExcerptChars),
			})
		}
	}

	var terms map[string]bool
	if j.opts.RelevantWords {
		terms = documentTerms(batch)
	}
	segID := make(map[*Segment]string)
	questions := make(jev.Questions, len(batch))
	for i, c := range batch {
		name := judgePairName(i)
		card := judgePairCard{Pair: name, TitleWords: c.Names, IDs: c.IDs}
		for _, sec := range c.Sections {
			card.Sections = append(card.Sections, sec.HeadingPath)
		}
		for _, seg := range c.Segments {
			id, ok := segID[seg]
			if !ok {
				id = fmt.Sprintf("s%d", len(segID))
				segID[seg] = id
				st.Segments = append(st.Segments, j.segmentCard(id, seg, terms))
			}
			card.Segments = append(card.Segments, id)
		}
		st.Pairs = append(st.Pairs, card)
		// Both answers are spelled out as situations: naming the false case
		// is what lets the model decline instead of finding a link because
		// it was asked about one.
		questions[discussesQuestion+name] = jev.NoulWithCriteria(
			fmt.Sprintf("In the pair whose id is %q, do the meeting segments it lists discuss the document?", name),
			"the segments discuss the work, design or decision this document describes, or change, question or report on something in it; a person reading the document would want to know this was said",
			"the segments and the document share vocabulary or a broad area only; what was said would not inform anyone reading this document",
		)
	}
	return st, questions
}

func (j *Judge) segmentCard(id string, seg *Segment, terms map[string]bool) segmentCard {
	m := seg.Meeting()
	card := segmentCard{
		ID:          id,
		Meeting:     m.Title,
		Date:        m.Date.Format("2006-01-02"),
		Topic:       seg.Name,
		WhatWasSaid: excerpt(seg.Summary, maxSaidChars),
	}
	if j.transcripts != nil && seg.End > seg.Start {
		if turns, err := j.transcripts.Span(m, seg.Start, seg.End); err == nil {
			if terms != nil {
				turns = relevantStretch(turns, terms, j.opts.SpokenChars)
			}
			card.SpokenWords = spokenText(turns, j.opts.SpokenChars)
		}
	}
	for _, d := range m.Decisions {
		if d.Quote != "" && strings.EqualFold(d.Topic, seg.Name) {
			card.DecisionQuote = excerpt(d.Quote, maxDecisionChars)
			break
		}
	}
	return card
}

// documentTerms are the words of a document's title, and of the headings and
// text of the sections a batch of pairs rests on.
func documentTerms(batch []*Candidate) map[string]bool {
	terms := make(map[string]bool)
	add := func(text string) {
		for _, t := range search.QueryTerms(text) {
			terms[t] = true
		}
	}
	add(batch[0].Subject.Title)
	for _, c := range batch {
		for _, sec := range c.Sections {
			add(sec.HeadingPath)
			add(excerpt(sec.Text, maxDocExcerptChars))
		}
	}
	return terms
}

// relevantStretch returns the run of consecutive turns, within about n
// characters, that holds the most distinct document terms: the part of a
// long segment where the document comes up, rather than its opening.
func relevantStretch(turns []Turn, terms map[string]bool, n int) []Turn {
	bestStart, bestEnd, best := 0, 0, 0
	for i := range turns {
		seen := make(map[string]bool)
		size := 0
		for k := i; k < len(turns); k++ {
			size += len(turns[k].Text) + 1
			if size > n && k > i {
				break
			}
			for _, t := range search.QueryTerms(turns[k].Text) {
				if terms[t] {
					seen[t] = true
				}
			}
			if len(seen) > best {
				best, bestStart, bestEnd = len(seen), i, k
			}
		}
	}
	if best == 0 {
		return turns
	}
	return turns[bestStart : bestEnd+1]
}
