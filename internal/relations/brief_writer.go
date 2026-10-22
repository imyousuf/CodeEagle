package relations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/imyousuf/CodeEagle/internal/llmjson"
	"github.com/imyousuf/CodeEagle/internal/transcript"
	"github.com/imyousuf/CodeEagle/pkg/jev"
	"github.com/imyousuf/CodeEagle/pkg/llm"
)

// BriefVersion is bumped when what a brief is written from, or how, changes,
// so existing briefs are written again.
const BriefVersion = 1

// BriefOptions bound brief writing.
type BriefOptions struct {
	// MaxInputChars bounds the snippets one brief is written from.
	MaxInputChars int
	// LatestMeetings is how many of the most recent linked meetings are
	// read in the speakers' own words; earlier ones contribute only their
	// decisions and follow-ups.
	LatestMeetings int
	// ConflictGate is the decision model's probability at or above which
	// a reported conflict is shown as one.
	ConflictGate float64
	// Settle is how long a document must go unchanged before its brief is
	// rewritten, so a file being edited is not briefed on every save.
	Settle time.Duration
}

// DefaultBriefOptions are the bounds the spec sets.
func DefaultBriefOptions() BriefOptions {
	return BriefOptions{MaxInputChars: 48000, LatestMeetings: 4, ConflictGate: 0.7, Settle: 30 * time.Minute}
}

// BriefWriter writes relation briefs with a language model, verifies every
// citation, and confirms conflicts with the decision model.
type BriefWriter struct {
	client      llm.Client
	asker       jev.Asker
	transcripts Transcripts
	speakers    SpeakerNamer
	opts        BriefOptions
	now         func() time.Time
}

// NewBriefWriter builds a writer. asker may be nil, in which case no
// conflict is shown as one; speakers may be nil, leaving labels unnamed.
func NewBriefWriter(client llm.Client, asker jev.Asker, transcripts Transcripts, speakers SpeakerNamer, opts BriefOptions, now func() time.Time) *BriefWriter {
	d := DefaultBriefOptions()
	if opts.MaxInputChars <= 0 {
		opts.MaxInputChars = d.MaxInputChars
	}
	if opts.LatestMeetings <= 0 {
		opts.LatestMeetings = d.LatestMeetings
	}
	if opts.ConflictGate <= 0 {
		opts.ConflictGate = d.ConflictGate
	}
	if now == nil {
		now = time.Now
	}
	return &BriefWriter{client: client, asker: asker, transcripts: transcripts, speakers: speakers, opts: opts, now: now}
}

// BriefReport is what writing one brief did and cost.
type BriefReport struct {
	Written          bool
	Reason           string
	InputTokens      int
	OutputTokens     int
	Dropped          int
	ConflictsKept    int
	ConflictsDemoted int
	JudgeRequests    int
	JudgeTokens      int
}

// snippet is one labelled passage the brief may cite.
type snippet struct {
	Label string
	Kind  string
	Text  string
	// Where it came from, to anchor evidence quoted from it.
	path    string
	sec     *Section
	meeting *Meeting
	turns   []Turn
	date    string
}

// Update rewrites the relation's brief if its inputs changed and the
// document has settled. It returns without writing otherwise, saying why.
func (w *BriefWriter) Update(ctx context.Context, rel *Relation, subj *Subject, meetings map[string]*Meeting) (BriefReport, error) {
	var rep BriefReport
	accepted := rel.Accepted()
	if len(accepted) == 0 {
		rep.Reason = "no meeting discusses it"
		return rep, nil
	}
	hash := InputsHash(rel, subj, w.client.Model())
	if hash == rel.BriefInputsHash {
		rep.Reason = "unchanged"
		return rep, nil
	}
	if w.opts.Settle > 0 && !subj.UpdatedAt.IsZero() && w.now().Sub(subj.UpdatedAt) < w.opts.Settle {
		rep.Reason = "document still changing"
		return rep, nil
	}

	snips := w.snippets(subj, accepted, meetings)
	if len(snips) == 0 {
		rep.Reason = "nothing to read"
		return rep, nil
	}
	var out briefReply
	usage, err := llmjson.ChatJSON(ctx, w.client, briefSystemPrompt, w.userPrompt(rel, subj, snips), briefSchema, &out)
	rep.InputTokens, rep.OutputTokens = usage.InputTokens, usage.OutputTokens
	if err != nil {
		return rep, fmt.Errorf("write brief for %s: %w", subj.Path, err)
	}

	brief, evidence, dropped := ground(out, snips, RelationID(rel.SubjectPath), w.speakers)
	rep.Dropped = dropped
	if brief.Headline.Text == "" {
		rep.Reason = "nothing in the draft could be verified"
		return rep, nil
	}
	if err := w.confirmConflicts(ctx, brief, evidence, &rep); err != nil {
		return rep, err
	}

	rel.Brief = brief
	rel.Evidence = evidence
	rel.BriefInputsHash = hash
	rel.BriefModel = w.client.Model()
	rel.BriefedAt = w.now().UTC()
	rel.Revision++
	rep.Written = true
	return rep, nil
}

// InputsHash identifies everything a brief is written from: the stage version
// and model, the document's sections, and the accepted meetings with the
// signatures they were judged under. A brief is rewritten when it changes.
func InputsHash(rel *Relation, subj *Subject, model string) string {
	parts := []string{fmt.Sprintf("v%d", BriefVersion), model}
	if subj != nil {
		parts = append(parts, subj.Hash)
		for _, sec := range subj.Sections {
			parts = append(parts, sectionKey(sec)+"="+sec.Hash)
		}
	}
	for _, v := range rel.Accepted() {
		parts = append(parts, v.SessionID+"="+v.SigMeeting)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:12])
}

// snippets gathers what the brief is written from, within the budget: the
// sections the meetings were linked on, then the rest of the document's
// outline; the latest meetings in the speakers' own words; and decisions and
// open follow-ups from every linked meeting.
func (w *BriefWriter) snippets(subj *Subject, accepted []*Verdict, meetings map[string]*Meeting) []*snippet {
	budget := w.opts.MaxInputChars
	var out []*snippet
	add := func(s *snippet) bool {
		if len(s.Text) > budget {
			return false
		}
		budget -= len(s.Text)
		out = append(out, s)
		return true
	}

	// Document: linked sections first, most linked first.
	linked := make(map[string]int)
	for _, v := range accepted {
		for _, key := range v.Sections {
			linked[key]++
		}
	}
	secs := append([]*Section(nil), subj.Sections...)
	sort.SliceStable(secs, func(i, j int) bool { return linked[sectionKey(secs[i])] > linked[sectionKey(secs[j])] })
	docBudget := w.opts.MaxInputChars * 2 / 5
	used := 0
	for i, sec := range secs {
		if sec.Text == "" {
			continue
		}
		text := sec.HeadingPath + "\n" + excerpt(sec.Text, 2500)
		if used+len(text) > docBudget && linked[sectionKey(sec)] == 0 {
			break
		}
		if add(&snippet{Label: fmt.Sprintf("D%d", i+1), Kind: EvidenceDoc, Text: text, sec: sec, path: subj.Path}) {
			used += len(text)
		}
	}

	// Meetings, newest first.
	var linkedMeetings []*Meeting
	verdictOf := make(map[*Meeting]*Verdict)
	for _, v := range accepted {
		if m := meetings[v.SessionID]; m != nil {
			linkedMeetings = append(linkedMeetings, m)
			verdictOf[m] = v
		}
	}
	sort.SliceStable(linkedMeetings, func(i, j int) bool { return linkedMeetings[i].Date.After(linkedMeetings[j].Date) })
	n := 0
	for mi, m := range linkedMeetings {
		date := m.Date.Format("2006-01-02")
		if mi < w.opts.LatestMeetings && w.transcripts != nil {
			for _, rg := range verdictOf[m].Ranges {
				turns, err := w.transcripts.Span(m, rg.Start, rg.End)
				if err != nil || len(turns) == 0 {
					continue
				}
				n++
				add(&snippet{Label: fmt.Sprintf("M%d", n), Kind: EvidenceSaid, meeting: m, turns: turns, date: date,
					Text: w.renderTurns(m, date, turns, 3000)})
			}
		}
		for _, d := range m.Decisions {
			if d.Quote == "" {
				continue
			}
			n++
			add(&snippet{Label: fmt.Sprintf("K%d", n), Kind: EvidenceDecided, meeting: m, date: date,
				Text: fmt.Sprintf("[%s] %s, decided: %s\nquote: %s", date, m.Title, d.Text, d.Quote)})
		}
		for _, a := range m.Actions {
			if a.Quote == "" || strings.EqualFold(a.Status, "done") {
				continue
			}
			n++
			who := a.Assignee
			if who == "" {
				who = "unassigned"
			}
			due := ""
			if a.Due != "" {
				due = " (due " + a.Due + ")"
			}
			add(&snippet{Label: fmt.Sprintf("A%d", n), Kind: EvidenceSaid, meeting: m, date: date,
				Text: fmt.Sprintf("[%s] %s, follow-up for %s%s: %s\nquote: %s", date, m.Title, who, due, a.Text, a.Quote)})
		}
	}
	return out
}

func (w *BriefWriter) renderTurns(m *Meeting, date string, turns []Turn, limit int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s\n", date, m.Title)
	for _, t := range turns {
		who := t.Speaker
		if w.speakers != nil {
			who = w.speakers.Name(m, t.Speaker)
		}
		line := fmt.Sprintf("%s %s: %s\n", clock(t.Start), who, strings.Join(strings.Fields(t.Text), " "))
		if b.Len()+len(line) > limit {
			b.WriteString("…\n")
			break
		}
		b.WriteString(line)
	}
	return strings.TrimRight(b.String(), "\n")
}

func clock(sec float64) string {
	s := int(sec)
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

const briefSystemPrompt = `You write the brief of a document and the meetings that discussed it, for an engineer or an AI agent who needs its current state at a glance.

You are given labelled snippets: D are sections of the document, M are stretches of meetings in the speakers' own words, K are decisions recorded in meetings, A are open follow-ups. Each snippet is dated where it comes from a meeting.

Write:
- headline: one sentence, the single most important thing about where this stands now. Lead with what was decided, a number, a date or a blocker.
- current: what holds now, at most five lines. Where a later meeting changed something, the later word wins.
- events: dated changes, newest first, at most five.
- unresolved: questions raised and not settled, follow-ups still open, at most five.
- conflicts: only where the document says one thing and a later meeting decided or stated something different. Leave it empty otherwise. Never present vocabulary differences as conflicts.

Rules:
- Every line cites the snippet labels it rests on, and for every label you cite you add an entry to quotes with words copied exactly from that snippet. Copy, do not paraphrase: quotes are checked character for character, and a line whose quotes do not check out is removed.
- Say only what the snippets support. If they do not say something, leave it out. An empty field is better than a guess.
- Use names as the snippets give them. Do not add surnames, titles or roles.
- Dates are YYYY-MM-DD, taken from the snippets.`

// briefReply is the model's draft.
type briefReply struct {
	Headline   Line       `json:"headline"`
	Current    []Line     `json:"current"`
	Events     []Event    `json:"events"`
	Unresolved []Open     `json:"unresolved"`
	Conflicts  []Conflict `json:"conflicts"`
	Quotes     []struct {
		Label string `json:"label"`
		Quote string `json:"quote"`
	} `json:"quotes"`
}

var briefSchema = func() *llm.JSONSchema {
	str := map[string]any{"type": "string"}
	cites := map[string]any{"type": "array", "items": str}
	line := llmjson.Object(map[string]any{"text": str, "cites": cites})
	return &llm.JSONSchema{
		Name:   "relation_brief",
		Strict: true,
		Schema: llmjson.Object(map[string]any{
			"headline":   line,
			"current":    map[string]any{"type": "array", "items": line},
			"events":     map[string]any{"type": "array", "items": llmjson.Object(map[string]any{"date": str, "text": str, "cites": cites})},
			"unresolved": map[string]any{"type": "array", "items": llmjson.Object(map[string]any{"text": str, "raised": str, "cites": cites})},
			"conflicts": map[string]any{"type": "array", "items": llmjson.Object(map[string]any{
				"doc_says": str, "discussion_says": str, "date": str, "doc_cite": str, "said_cite": str,
			})},
			"quotes": map[string]any{"type": "array", "items": llmjson.Object(map[string]any{"label": str, "quote": str})},
		}),
	}
}()

func (w *BriefWriter) userPrompt(rel *Relation, subj *Subject, snips []*snippet) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Document: %s (%s)\n\n", subj.Title, subj.Path)
	for _, s := range snips {
		fmt.Fprintf(&b, "<%s>\n%s\n</%s>\n\n", s.Label, s.Text, s.Label)
	}
	if prev := rel.Brief; prev != nil && (len(prev.Unresolved) > 0 || len(prev.Conflicts) > 0) {
		b.WriteString("The previous brief listed these as unresolved or in conflict; check whether the snippets settle them:\n")
		for _, u := range prev.Unresolved {
			fmt.Fprintf(&b, "- %s\n", u.Text)
		}
		for _, c := range prev.Conflicts {
			fmt.Fprintf(&b, "- document: %s / discussion: %s\n", c.DocSays, c.DiscussionSays)
		}
	}
	return b.String()
}

// ground keeps only what the snippets support. A quote counts only if it is
// in the snippet it is filed under; a line counts only if one of its labels
// has such a quote. What survives is re-cited by evidence ID.
func ground(r briefReply, snips []*snippet, relID string, speakers SpeakerNamer) (*Brief, []Evidence, int) {
	byLabel := make(map[string]*snippet, len(snips))
	for _, s := range snips {
		byLabel[s.Label] = s
	}
	evidenceFor := make(map[string]string) // label -> evidence ID
	var evidence []Evidence
	for _, q := range r.Quotes {
		s := byLabel[q.Label]
		if s == nil || evidenceFor[q.Label] != "" || !transcript.QuoteIn(s.Text, q.Quote) {
			continue
		}
		e := anchor(s, q.Quote, speakers)
		e.ID = EvidenceID(relID, e)
		evidenceFor[q.Label] = e.ID
		evidence = append(evidence, e)
	}

	dropped := 0
	recite := func(labels []string) []string {
		var ids []string
		for _, l := range labels {
			if id := evidenceFor[l]; id != "" && !contains(ids, id) {
				ids = append(ids, id)
			}
		}
		return ids
	}
	b := &Brief{}
	if ids := recite(r.Headline.Cites); len(ids) > 0 && r.Headline.Text != "" {
		b.Headline = Line{Text: r.Headline.Text, Cites: ids}
	} else if r.Headline.Text != "" {
		dropped++
	}
	for _, l := range r.Current {
		if ids := recite(l.Cites); len(ids) > 0 {
			b.Current = append(b.Current, Line{Text: l.Text, Cites: ids})
		} else {
			dropped++
		}
	}
	for _, e := range r.Events {
		if ids := recite(e.Cites); len(ids) > 0 {
			b.Events = append(b.Events, Event{Date: e.Date, Text: e.Text, Cites: ids})
		} else {
			dropped++
		}
	}
	for _, u := range r.Unresolved {
		if ids := recite(u.Cites); len(ids) > 0 {
			b.Unresolved = append(b.Unresolved, Open{Text: u.Text, Raised: u.Raised, Cites: ids})
		} else {
			dropped++
		}
	}
	for _, c := range r.Conflicts {
		docID, saidID := evidenceFor[c.DocCite], evidenceFor[c.SaidCite]
		docSnip := byLabel[c.DocCite]
		if docID == "" || saidID == "" || docSnip == nil || docSnip.Kind != EvidenceDoc {
			dropped++
			continue
		}
		b.Conflicts = append(b.Conflicts, Conflict{
			DocSays: c.DocSays, DiscussionSays: c.DiscussionSays, Date: c.Date, DocCite: docID, SaidCite: saidID,
		})
	}
	// A headline that did not check out is replaced by the first current
	// line that did, rather than leaving the brief without one.
	if b.Headline.Text == "" && len(b.Current) > 0 {
		b.Headline, b.Current = b.Current[0], b.Current[1:]
	}
	b.Dropped = dropped
	return b, evidence, dropped
}

// anchor turns a verified quote into evidence anchored to its source.
func anchor(s *snippet, quote string, speakers SpeakerNamer) Evidence {
	e := Evidence{Kind: s.Kind, Quote: strings.Join(strings.Fields(quote), " ")}
	switch {
	case s.sec != nil:
		e.Path = s.path
		e.HeadingPath = s.sec.HeadingPath
		e.Ordinal = s.sec.Ordinal
		e.SectionHash = s.sec.Hash
	case s.meeting != nil:
		e.SessionID = s.meeting.SessionID
		for _, t := range s.turns {
			if transcript.QuoteIn(t.Text, quote) {
				e.Start, e.End = t.Start, t.End
				e.Speaker = t.Speaker
				if speakers != nil {
					e.Speaker = speakers.Name(s.meeting, t.Speaker)
				}
				break
			}
		}
		if e.Start == 0 && len(s.turns) > 0 {
			e.Start, e.End = s.turns[0].Start, s.turns[len(s.turns)-1].End
		}
	}
	return e
}

// confirmConflicts asks the decision model whether each conflict's two
// passages really disagree. One it does not confirm is kept as an event:
// a reported difference that is not a contradiction is still news, but
// calling it a conflict would send a reader to fix a document that is right.
func (w *BriefWriter) confirmConflicts(ctx context.Context, b *Brief, evidence []Evidence, rep *BriefReport) error {
	if len(b.Conflicts) == 0 {
		return nil
	}
	quoteOf := make(map[string]string, len(evidence))
	for _, e := range evidence {
		quoteOf[e.ID] = e.Quote
	}
	var kept []Conflict
	for i, c := range b.Conflicts {
		p := 0.0
		if w.asker != nil {
			state := map[string]string{
				"what_the_document_says":        quoteOf[c.DocCite],
				"what_was_said_in_a_meeting":    quoteOf[c.SaidCite],
				"date_of_the_meeting":           c.Date,
				"summary_of_the_claimed_change": c.DiscussionSays,
			}
			name := fmt.Sprintf("contradicts_%d", i)
			resp, err := w.asker.Ask(ctx, state, jev.Questions{
				name: jev.NoulWithCriteria(
					"Does what was said in the meeting contradict what the document says?",
					"the meeting decided or stated something that cannot be true at the same time as the document's passage; one of them must be out of date",
					"the two are compatible: they say the same thing, one adds detail, or they are about different things",
				),
			})
			if err != nil {
				return fmt.Errorf("confirm conflict: %w", err)
			}
			rep.JudgeRequests++
			rep.JudgeTokens += resp.Usage.InputTokens
			if p, err = resp.Answers.Noul(name); err != nil {
				return fmt.Errorf("confirm conflict: %w", err)
			}
		}
		if p >= w.opts.ConflictGate {
			kept = append(kept, c)
			rep.ConflictsKept++
			continue
		}
		rep.ConflictsDemoted++
		b.Events = append([]Event{{Date: c.Date, Text: c.DiscussionSays, Cites: []string{c.SaidCite}}}, b.Events...)
	}
	b.Conflicts = kept
	return nil
}
