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
	"github.com/imyousuf/CodeEagle/internal/search"
	"github.com/imyousuf/CodeEagle/internal/transcript"
	"github.com/imyousuf/CodeEagle/pkg/jev"
	"github.com/imyousuf/CodeEagle/pkg/llm"
)

// BriefVersion is bumped when what a brief is written from, or how, changes,
// so existing briefs are written again.
const BriefVersion = 8

// BriefOptions bound brief writing.
type BriefOptions struct {
	// MaxInputChars bounds the snippets one brief is written from.
	MaxInputChars int
	// LatestMeetings is how many of the most recent linked meetings are
	// read in the speakers' own words; earlier ones contribute only their
	// decisions and follow-ups.
	LatestMeetings int
	// TimelineMeetings is how many linked meetings in all are read in the
	// speakers' own words for the timeline: the latest in full, the rest as
	// the stretch where the document comes up.
	TimelineMeetings int
	// ConflictGate is the decision model's probability at or above which
	// a reported conflict is shown as one.
	ConflictGate float64
	// Settle is how long a document must go unchanged before its brief is
	// rewritten, so a file being edited is not briefed on every save.
	Settle time.Duration
}

// DefaultBriefOptions are the bounds the spec sets.
func DefaultBriefOptions() BriefOptions {
	return BriefOptions{MaxInputChars: 48000, LatestMeetings: 4, TimelineMeetings: 15, ConflictGate: 0.7, Settle: 30 * time.Minute}
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
	// Parts are kept only under the names the document's type allows.
	asset, _ := ClassifyAsset(subj)
	allowed := make(map[string]bool)
	for _, name := range assetParts[asset] {
		allowed[name] = true
	}
	kept := brief.Parts[:0]
	for _, pt := range brief.Parts {
		if allowed[pt.Part] {
			kept = append(kept, pt)
		}
	}
	brief.Parts = kept
	rep.Dropped = dropped
	rep.Unverified = unverified
	if brief.Headline.Text == "" {
		rep.Reason = "nothing in the draft could be verified"
		return rep, nil
	}
	if err := w.confirmConflicts(ctx, brief, evidence, &rep); err != nil {
		return rep, err
	}

	entries := w.ledgerEntries(ctx, rel, brief, evidence, meetings, &rep)
	meetingEntries, meetingEvidence := MeetingDecisions(rel, meetings)
	docEntries, docEvidence := DocDecisions(rel, subj)
	entries = append(append(entries, meetingEntries...), docEntries...)
	old := rel.Evidence
	rel.Brief = brief
	rel.Decisions = MergeDecisions(rel.Decisions, entries, w.now().UTC())
	// Evidence is what this brief and the ledger cite: decisions recorded by
	// earlier rewrites keep their quotes so show can still expand them.
	cited := make(map[string]bool)
	for _, d := range rel.Decisions {
		for _, c := range d.Cites {
			cited[c] = true
		}
	}
	evidence = mergeEvidence(evidence, append(meetingEvidence, docEvidence...))
	var keptEvidence []Evidence
	for _, e := range old {
		if cited[e.ID] {
			keptEvidence = append(keptEvidence, e)
		}
	}
	rel.Evidence = mergeEvidence(evidence, keptEvidence)
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
	terms := subjectTerms(subj, accepted)
	for mi, m := range linkedMeetings {
		date := m.Date.Format("2006-01-02")
		if mi < max(w.opts.LatestMeetings, w.opts.TimelineMeetings) && w.transcripts != nil {
			// The latest meetings in full; earlier ones as the stretch where
			// the document comes up, so every one can have its line in the
			// timeline. Answers lost the most points for missing meetings.
			limit := 3000
			if mi >= w.opts.LatestMeetings {
				limit = timelineChars
			}
			for _, rg := range verdictOf[m].Ranges {
				turns, err := w.transcripts.Span(m, rg.Start, rg.End)
				if err != nil || len(turns) == 0 {
					continue
				}
				if mi >= w.opts.LatestMeetings {
					turns = relevantStretch(turns, terms, limit)
				}
				n++
				add(&snippet{Label: fmt.Sprintf("M%d", n), Kind: EvidenceSaid, meeting: m, turns: turns, date: date,
					Text: w.renderTurns(m, date, turns, limit)})
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
- document: what the document itself says, in three to five lines: its status and date if it states them, what it proposes, and the choices that matter. Quote only D snippets.
- timeline: one line for each meeting snippet (M), newest first, saying what was said or decided there about this document: who reported what, what was demoed, asked, agreed or deferred. Every meeting the snippets show gets its line.
- parts: the parts the document's type calls for, named as the prompt allows, several lines per part where the snippets support them.
- decisions: each decision the snippets record about this document's subject, one line each, with the date it was made (YYYY-MM-DD) when the quoted words or the snippet's date give one, else empty. A decision is something settled: chosen, agreed, ruled out, reversed. Do not list proposals or open questions here.

Rules:
- Every line carries its support: one or more entries, each a snippet label and words copied exactly from that snippet. Copy, do not paraphrase or tidy: quotes are checked character for character against the snippet named beside them, and a line none of whose quotes check out is removed. A conflict's doc support must quote a D snippet and its said support a meeting snippet.
- Say only what the snippets support. If they do not say something, leave it out. An empty field is better than a guess.
- Quote enough to carry the line. Every name, number, date and decision in a line must appear in its quotes or in the header of the snippet they come from; add a second quote rather than a detail the first does not hold.
- Something one person suggested or argued for is "proposed" or "suggested", not "decided", unless the snippet shows the others agreeing or it was recorded as a decision (K).
- Never claim that something did not happen, is not recorded or was never discussed: you see only these snippets, not everything.
- A follow-up's owner is whoever took it on in the snippet. Only call it unassigned when nobody did.
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
	Document []struct {
		Text    string    `json:"text"`
		Support []support `json:"support"`
	} `json:"document"`
	Timeline []struct {
		Date    string    `json:"date"`
		Text    string    `json:"text"`
		Support []support `json:"support"`
	} `json:"timeline"`
	Parts []struct {
		Part    string    `json:"part"`
		Text    string    `json:"text"`
		Support []support `json:"support"`
	} `json:"parts"`
	Decisions []struct {
		Date    string    `json:"date"`
		Text    string    `json:"text"`
		Support []support `json:"support"`
	} `json:"decisions"`
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
			"document":  map[string]any{"type": "array", "items": line},
			"timeline":  map[string]any{"type": "array", "items": llmjson.Object(map[string]any{"date": str, "text": str, "support": sups})},
			"parts":     map[string]any{"type": "array", "items": llmjson.Object(map[string]any{"part": str, "text": str, "support": sups})},
			"decisions": map[string]any{"type": "array", "items": llmjson.Object(map[string]any{"date": str, "text": str, "support": sups})},
		}),
	}
}()

func (w *BriefWriter) userPrompt(rel *Relation, subj *Subject, snips []*snippet) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Document: %s (%s)\n", subj.Title, subj.Path)
	asset, _ := ClassifyAsset(subj)
	fmt.Fprintf(&b, "Type: %s.", asset)
	if guide := assetGuide[asset]; guide != "" {
		fmt.Fprintf(&b, " %s The allowed part names are: %s.", guide, strings.Join(assetParts[asset], ", "))
	} else {
		b.WriteString(" Leave parts empty.")
	}
	if status, date := DocStatus(subj); status != "" || date != "" {
		fmt.Fprintf(&b, "\nThe document states its status as %q and its date as %q.", status, date)
	}
	b.WriteString("\n\n")
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
	for _, l := range r.Document {
		// What the document says must rest on the document.
		var docOnly []support
		for _, sp := range l.Support {
			if s := byLabel[sp.Label]; s != nil && s.Kind == EvidenceDoc {
				docOnly = append(docOnly, sp)
			}
		}
		if ids := cite(l.Text, docOnly); len(ids) > 0 {
			b.Points = append(b.Points, Line{Text: l.Text, Cites: ids})
		}
	}
	for _, e := range r.Timeline {
		if ids := cite(e.Text, e.Support); len(ids) > 0 {
			b.Timeline = append(b.Timeline, Event{Date: e.Date, Text: e.Text, Cites: ids})
		}
	}
	for _, d := range r.Decisions {
		if ids := cite(d.Text, d.Support); len(ids) > 0 {
			b.Decisions = append(b.Decisions, Event{Date: d.Date, Text: d.Text, Cites: ids})
		}
	}
	for _, pt := range r.Parts {
		if ids := cite(pt.Text, pt.Support); len(ids) > 0 {
			b.Parts = append(b.Parts, Part{Part: strings.ToLower(strings.TrimSpace(pt.Part)), Text: pt.Text, Cites: ids})
		}
	}
	// A headline that did not check out is replaced by the first current
	// line that did, rather than leaving the brief without one.
	if b.Headline.Text == "" && len(b.Current) > 0 {
		b.Headline, b.Current = b.Current[0], b.Current[1:]
	}
	fixDates(b, evidence, snips)
	b.Dropped = dropped
	return b, evidence, dropped, unverified
}

// timelineChars bounds what an earlier meeting contributes to a brief.
const timelineChars = 900

// subjectTerms are the words of a document's title and of the sections its
// meetings were linked on.
func subjectTerms(subj *Subject, accepted []*Verdict) map[string]bool {
	linked := make(map[string]bool)
	for _, v := range accepted {
		for _, key := range v.Sections {
			linked[key] = true
		}
	}
	terms := make(map[string]bool)
	add := func(text string) {
		for _, t := range search.QueryTerms(text) {
			terms[t] = true
		}
	}
	add(subj.Title)
	for _, sec := range subj.Sections {
		if linked[sectionKey(sec)] {
			add(sec.HeadingPath)
			add(excerpt(sec.Text, maxDocExcerptChars))
		}
	}
	return terms
}

// fixDates sets the date of each conflict, and of each event that cites a
// meeting under a date none of its meetings has, from the meetings its
// evidence is anchored to. The drafting model copied the wrong meeting's
// date onto a conflict often enough to matter, and the anchor knows.
func fixDates(b *Brief, evidence []Evidence, snips []*snippet) {
	dateOf := make(map[string]string) // session -> date
	for _, s := range snips {
		if s.meeting != nil && s.date != "" {
			dateOf[s.meeting.SessionID] = s.date
		}
	}
	meetingDate := make(map[string]string, len(evidence)) // evidence ID -> date
	for _, e := range evidence {
		if d := dateOf[e.SessionID]; e.SessionID != "" && d != "" {
			meetingDate[e.ID] = d
		}
	}
	for i := range b.Conflicts {
		if d := meetingDate[b.Conflicts[i].SaidCite]; d != "" {
			b.Conflicts[i].Date = d
		}
	}
	for _, list := range [][]Event{b.Events, b.Timeline} {
		for i := range list {
			var dates []string
			for _, c := range list[i].Cites {
				if d := meetingDate[c]; d != "" {
					dates = append(dates, d)
				}
			}
			if len(dates) > 0 && !contains(dates, list[i].Date) {
				sort.Strings(dates)
				list[i].Date = dates[len(dates)-1]
			}
		}
	}
	sort.SliceStable(b.Timeline, func(i, j int) bool { return b.Timeline[i].Date > b.Timeline[j].Date })
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
					"the meeting decided or settled something that cannot be true at the same time as the document's passage, so one of them must be out of date",
					"the two are compatible: they say the same thing, one adds detail, or they are about different things; or the meeting only proposed, asked or argued for something without settling it",
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

// settledGate is the decision model's probability at or above which a
// decision drawn from what was said is recorded as decided; below it, as
// proposed. maxSettledChecks bounds the checks one brief makes.
const (
	settledGate      = 0.7
	maxSettledChecks = 8
)

// ledgerEntries turns the writer's decisions into ledger entries: dated by
// the meeting they cite, or by a date written in the cited words of the
// document, else undated; and, when they rest on what was said rather than
// on a recorded decision or the document, checked with the decision model
// for whether it was settled. The writer called proposals decisions often
// enough to matter when measured.
func (w *BriefWriter) ledgerEntries(ctx context.Context, rel *Relation, b *Brief, evidence []Evidence,
	meetings map[string]*Meeting, rep *BriefReport) []DecisionEntry {
	byID := make(map[string]Evidence, len(evidence))
	for _, e := range evidence {
		byID[e.ID] = e
	}
	relID := RelationID(rel.SubjectPath)
	checks := 0
	var out []DecisionEntry
	for _, d := range b.Decisions {
		entry := DecisionEntry{ID: decisionID(relID, d.Cites), Text: d.Text, Status: StatusDecided, Source: "writer",
			Cites: d.Cites, DateBasis: DateNone}
		said, recorded := "", false
		for _, c := range d.Cites {
			e := byID[c]
			switch e.Kind {
			case EvidenceDecided:
				recorded = true
				if m := meetings[e.SessionID]; m != nil {
					entry.Date, entry.DateBasis = m.Date.Format("2006-01-02"), DateMeeting
				}
			case EvidenceSaid:
				if said == "" {
					said = e.Quote
				}
				if m := meetings[e.SessionID]; m != nil && entry.DateBasis == DateNone {
					entry.Date, entry.DateBasis = m.Date.Format("2006-01-02"), DateMeeting
				}
			case EvidenceDoc:
				if d.Date != "" && strings.Contains(e.Quote, d.Date) && entry.DateBasis == DateNone {
					entry.Date, entry.DateBasis = d.Date, DateStated
				}
			}
		}
		if said != "" && !recorded {
			entry.Status = StatusProposed
			if w.asker != nil && checks < maxSettledChecks {
				checks++
				if p, ok := w.settled(ctx, d.Text, said, rep); ok && p >= settledGate {
					entry.Status = StatusDecided
				}
			}
		}
		out = append(out, entry)
	}
	return out
}

// settled asks whether what was said settled the decision, rather than
// proposing or arguing for it.
func (w *BriefWriter) settled(ctx context.Context, decision, said string, rep *BriefReport) (float64, bool) {
	resp, err := w.asker.Ask(ctx, map[string]string{
		"what_was_said_in_a_meeting":   said,
		"decision_it_is_claimed_to_be": decision,
	}, jev.Questions{
		"settled": jev.NoulWithCriteria(
			"Does what was said show the decision being made or settled?",
			"the words show it chosen, agreed, ruled out or reversed: someone with the standing to decide says it is so, or the others agree",
			"the words only propose, suggest, argue for, ask about or plan to consider it, with nobody shown settling it",
		),
	})
	if err != nil {
		return 0, false
	}
	rep.JudgeRequests++
	rep.JudgeTokens += resp.Usage.InputTokens
	p, err := resp.Answers.Noul("settled")
	return p, err == nil
}
