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
const BriefVersion = 3

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
	Written      bool
	Reason       string
	InputTokens  int
	OutputTokens int
	Dropped      int
	// Unverified describes each quote that failed its check, for looking
	// at why lines were dropped.
	Unverified       []string
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

	brief, evidence, dropped, unverified := ground(out, snips, RelationID(rel.SubjectPath), w.speakers)
	rep.Dropped = dropped
	rep.Unverified = unverified
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
	rel.Stamp(rel.BriefedAt)
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
- Every line carries its support: one or more entries, each a snippet label and words copied exactly from that snippet. Copy, do not paraphrase or tidy: quotes are checked character for character against the snippet named beside them, and a line none of whose quotes check out is removed. A conflict's doc support must quote a D snippet and its said support a meeting snippet.
- Say only what the snippets support. If they do not say something, leave it out. An empty field is better than a guess.
- Use names as the snippets give them. Do not add surnames, titles or roles.
- Dates are YYYY-MM-DD, taken from the snippets.`

// support is one citation in the draft: a snippet label and words copied
// from that snippet.
type support struct {
	Label string `json:"label"`
	Quote string `json:"quote"`
}

// briefReply is the model's draft. Every line carries its own support, so a
// line cannot cite a snippet without quoting it.
type briefReply struct {
	Headline struct {
		Text    string    `json:"text"`
		Support []support `json:"support"`
	} `json:"headline"`
	Current []struct {
		Text    string    `json:"text"`
		Support []support `json:"support"`
	} `json:"current"`
	Events []struct {
		Date    string    `json:"date"`
		Text    string    `json:"text"`
		Support []support `json:"support"`
	} `json:"events"`
	Unresolved []struct {
		Text    string    `json:"text"`
		Raised  string    `json:"raised"`
		Support []support `json:"support"`
	} `json:"unresolved"`
	Conflicts []struct {
		DocSays        string  `json:"doc_says"`
		DiscussionSays string  `json:"discussion_says"`
		Date           string  `json:"date"`
		Doc            support `json:"doc"`
		Said           support `json:"said"`
	} `json:"conflicts"`
}

var briefSchema = func() *llm.JSONSchema {
	str := map[string]any{"type": "string"}
	sup := llmjson.Object(map[string]any{"label": str, "quote": str})
	sups := map[string]any{"type": "array", "items": sup}
	line := llmjson.Object(map[string]any{"text": str, "support": sups})
	return &llm.JSONSchema{
		Name:   "relation_brief",
		Strict: true,
		Schema: llmjson.Object(map[string]any{
			"headline":   line,
			"current":    map[string]any{"type": "array", "items": line},
			"events":     map[string]any{"type": "array", "items": llmjson.Object(map[string]any{"date": str, "text": str, "support": sups})},
			"unresolved": map[string]any{"type": "array", "items": llmjson.Object(map[string]any{"text": str, "raised": str, "support": sups})},
			"conflicts": map[string]any{"type": "array", "items": llmjson.Object(map[string]any{
				"doc_says": str, "discussion_says": str, "date": str, "doc": sup, "said": sup,
			})},
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
// in the snippet named beside it; a line counts only if one of its quotes
// does. What survives is re-cited by evidence ID.
func ground(r briefReply, snips []*snippet, relID string, speakers SpeakerNamer) (*Brief, []Evidence, int, []string) {
	byLabel := make(map[string]*snippet, len(snips))
	for _, s := range snips {
		byLabel[s.Label] = s
	}
	var (
		evidence   []Evidence
		idOf       = make(map[string]string) // label + quote -> evidence ID
		unverified []string
		dropped    int
	)
	// verify returns the evidence ID for a quote, or "" if it does not
	// check out against its own snippet.
	verify := func(sp support) string {
		key := sp.Label + "\x00" + strings.Join(strings.Fields(strings.ToLower(sp.Quote)), " ")
		if id, ok := idOf[key]; ok {
			return id
		}
		s := byLabel[sp.Label]
		switch {
		case s == nil:
			unverified = append(unverified, fmt.Sprintf("%s: no such snippet: %q", sp.Label, sp.Quote))
		case !transcript.QuoteIn(s.Text, sp.Quote):
			unverified = append(unverified, fmt.Sprintf("%s: not in the snippet: %q", sp.Label, sp.Quote))
		default:
			e := anchor(s, sp.Quote, speakers)
			e.ID = EvidenceID(relID, e)
			idOf[key] = e.ID
			if !containsEvidence(evidence, e.ID) {
				evidence = append(evidence, e)
			}
			return e.ID
		}
		idOf[key] = ""
		return ""
	}
	cite := func(text string, sups []support) []string {
		var ids []string
		for _, sp := range sups {
			if id := verify(sp); id != "" && !contains(ids, id) {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 && text != "" {
			dropped++
			var labels []string
			for _, sp := range sups {
				labels = append(labels, sp.Label)
			}
			unverified = append(unverified, fmt.Sprintf("dropped %q: no verified quote among %v", text, labels))
		}
		return ids
	}

	b := &Brief{}
	if ids := cite(r.Headline.Text, r.Headline.Support); len(ids) > 0 {
		b.Headline = Line{Text: r.Headline.Text, Cites: ids}
	}
	for _, l := range r.Current {
		if ids := cite(l.Text, l.Support); len(ids) > 0 {
			b.Current = append(b.Current, Line{Text: l.Text, Cites: ids})
		}
	}
	for _, e := range r.Events {
		if ids := cite(e.Text, e.Support); len(ids) > 0 {
			b.Events = append(b.Events, Event{Date: e.Date, Text: e.Text, Cites: ids})
		}
	}
	for _, u := range r.Unresolved {
		if ids := cite(u.Text, u.Support); len(ids) > 0 {
			b.Unresolved = append(b.Unresolved, Open{Text: u.Text, Raised: u.Raised, Cites: ids})
		}
	}
	for _, c := range r.Conflicts {
		docSnip, saidSnip := byLabel[c.Doc.Label], byLabel[c.Said.Label]
		docID, saidID := verify(c.Doc), verify(c.Said)
		if docID == "" || saidID == "" || docSnip == nil || docSnip.Kind != EvidenceDoc ||
			saidSnip == nil || saidSnip.Kind == EvidenceDoc {
			dropped++
			unverified = append(unverified, fmt.Sprintf("dropped conflict %q: doc %s / said %s did not both check out",
				c.DiscussionSays, c.Doc.Label, c.Said.Label))
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
	return b, evidence, dropped, unverified
}

func containsEvidence(list []Evidence, id string) bool {
	for _, e := range list {
		if e.ID == id {
			return true
		}
	}
	return false
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
		if i, j, ok := LocateQuote(s.turns, quote); ok {
			e.Start, e.End = s.turns[i].Start, s.turns[j].End
			e.Speaker = s.turns[i].Speaker
			if speakers != nil {
				e.Speaker = speakers.Name(s.meeting, s.turns[i].Speaker)
			}
		} else if len(s.turns) > 0 {
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
			// Only the two verbatim passages: the drafting model's summary of
			// the change is a claim, and the decision model believes what its
			// state asserts.
			state := map[string]string{
				"what_the_document_says":     quoteOf[c.DocCite],
				"what_was_said_in_a_meeting": quoteOf[c.SaidCite],
				"date_of_the_meeting":        c.Date,
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
