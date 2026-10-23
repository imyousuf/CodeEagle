package relations

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// Library is the read side of relations: the corpus as it is now, the
// relations over it, and what an agent asks of them.
type Library struct {
	Store       graph.Store
	Subjects    map[string]*Subject
	Meetings    map[string]*Meeting
	Relations   map[string]*Relation
	Transcripts Transcripts
	Speakers    SpeakerNamer
	// Resolve turns a graph path into a file on disk.
	Resolve func(graphPath string) string
	Now     func() time.Time
}

// LoadLibrary reads the corpus and the relations from store. resolve turns a
// graph path into a file on disk, for showing a document's text.
func LoadLibrary(ctx context.Context, store graph.Store, resolve func(string) string) (*Library, error) {
	subjects, err := loadSubjects(ctx, store, nil, false)
	if err != nil {
		return nil, fmt.Errorf("load documents: %w", err)
	}
	meetings, err := LoadMeetingHeaders(ctx, store)
	if err != nil {
		return nil, fmt.Errorf("load meetings: %w", err)
	}
	rels, err := LoadRelations(ctx, store)
	if err != nil {
		return nil, fmt.Errorf("load relations: %w", err)
	}
	by := make(map[string]*Subject, len(subjects))
	for _, s := range subjects {
		by[s.Path] = s
	}
	return &Library{
		Store:       store,
		Subjects:    by,
		Meetings:    meetings,
		Relations:   rels,
		Transcripts: NewFileTranscripts(),
		Speakers:    NewStoreSpeakers(ctx, store),
		Resolve:     resolve,
		Now:         time.Now,
	}, nil
}

// Brief renders a relation's brief with its freshness as of now.
func (l *Library) Brief(rel *Relation, budget int, since time.Time, sinceLabel string) string {
	return Render(rel, RenderOptions{
		Budget:      budget,
		Since:       since,
		SinceLabel:  sinceLabel,
		Meetings:    l.Meetings,
		Flags:       Freshness(rel, l.Subjects[rel.SubjectPath], l.Meetings),
		Subject:     l.Subjects[rel.SubjectPath],
		Unconfirmed: l.unconfirmed(rel),
	})
}

// unconfirmed lists the closest meetings below the gate with what was said
// in the part judged, so an agent can weigh them without opening each. With
// few confirmed meetings it reaches further down, since then they are most
// of what is known; an agent given nothing went looking, and spent its
// budget doing so. Without a store it returns nil and titles are listed.
func (l *Library) unconfirmed(rel *Relation) []string {
	if l.Store == nil {
		return nil
	}
	floor, limit := minUnconfirmedP, maxUnconfirmed
	if len(rel.Accepted()) < 3 {
		floor, limit = sparseUnconfirmedP, sparseUnconfirmed
	}
	var near []*Verdict
	for _, v := range rel.Links {
		if !v.Accepted && v.P >= floor && l.Meetings[v.SessionID] != nil {
			near = append(near, v)
		}
	}
	sort.SliceStable(near, func(i, j int) bool { return near[i].P > near[j].P })
	lines := []string{}
	for i, v := range near {
		if i >= limit {
			break
		}
		m := l.Meetings[v.SessionID]
		line := fmt.Sprintf("- %s %s (%s) p=%.2f", m.Date.Format("2006-01-02"), clip(m.Title, maxMeetingTitle), ShortID(m.NodeID), v.P)
		if said := l.saidIn(m, v); said != "" {
			line += ": " + said
		}
		lines = append(lines, line)
	}
	return lines
}

// saidIn is the summary of the part of a meeting a verdict judged.
func (l *Library) saidIn(m *Meeting, v *Verdict) string {
	if len(v.Ranges) == 0 {
		return ""
	}
	children, err := l.Store.GetNeighbors(context.Background(), m.NodeID, graph.EdgeContains, graph.Outgoing)
	if err != nil {
		return ""
	}
	rg := v.Ranges[0]
	for _, c := range children {
		if c.Type != graph.NodeTopicSegment {
			continue
		}
		if startOf(c) <= rg.End && endOf(c) >= rg.Start {
			return clip(strings.Join(strings.Fields(c.Properties[graph.PropSummary]), " "), maxUnconfirmedSaid)
		}
	}
	return ""
}

// Bounds on the unconfirmed meetings a brief names.
const (
	sparseUnconfirmedP = 0.3
	sparseUnconfirmed  = 6
	maxUnconfirmedSaid = 180
)

// RelationByID returns the relation whose node ID starts with prefix.
func (l *Library) RelationByID(prefix string) *Relation {
	for _, r := range l.Relations {
		if strings.HasPrefix(RelationID(r.SubjectPath), prefix) {
			return r
		}
	}
	return nil
}

// Evidence finds a piece of evidence by ID across all relations.
func (l *Library) Evidence(id string) (*Relation, *Evidence) {
	for _, r := range l.Relations {
		for i := range r.Evidence {
			if r.Evidence[i].ID == id {
				return r, &r.Evidence[i]
			}
		}
	}
	return nil, nil
}

// Bounds on what show prints, so expanding a citation never costs a file.
const (
	maxShowLines        = 60
	maxShowChars        = 2500
	maxShowSaidChars    = 1500
	showContextSeconds  = 15
	showTurnsBefore     = 2
	maxDocsForAMeeting  = 8
	maxHeadlineInFinder = 140
)

// ShowEvidence expands a piece of evidence: a document quote to its section
// as it is on disk now, a meeting quote to the turns around it. from skips
// that many lines into a long section.
func (l *Library) ShowEvidence(rel *Relation, e *Evidence, from int) (string, error) {
	switch e.Kind {
	case EvidenceDoc:
		return l.showSection(rel, e, from)
	default:
		return l.showSaid(e)
	}
}

func (l *Library) showSection(rel *Relation, e *Evidence, from int) (string, error) {
	path := e.Path
	if path == "" {
		path = rel.SubjectPath
	}
	subj := l.Subjects[path]
	if subj == nil {
		return "", fmt.Errorf("%s is no longer indexed", path)
	}
	var sec *Section
	for _, s := range subj.Sections {
		if s.HeadingPath == e.HeadingPath && (e.Ordinal <= 1 || s.Ordinal == e.Ordinal) {
			sec = s
			break
		}
	}
	if sec == nil {
		return "", fmt.Errorf("section %q of %s is gone", e.HeadingPath, path)
	}
	note := ""
	if e.SectionHash != "" && e.SectionHash != sec.Hash {
		note = "\n(this section has changed since it was quoted)"
	}
	return l.renderSection(path, sec, note, from, e.ID), nil
}

// ShowSection prints a section as it is on disk now, bounded, continuing
// from a line into a long one.
func (l *Library) ShowSection(path string, sec *Section, from int) string {
	return l.renderSection(path, sec, "", from, ShortID(sec.NodeID))
}

func (l *Library) renderSection(path string, sec *Section, note string, from int, id string) string {
	header := fmt.Sprintf("%s § %s", path, sec.HeadingPath)
	var lines []string
	if l.Resolve != nil {
		if abs := l.Resolve(path); abs != "" {
			lines = readLines(abs, sec.Line, sec.EndLine)
		}
	}
	if len(lines) == 0 {
		// The file cannot be read; the indexed text of the section is
		// still the section.
		lines = strings.Split(sec.Text, "\n")
	}
	if from > 0 && from < len(lines) {
		lines = lines[from:]
	}
	var b strings.Builder
	shown := 0
	for _, ln := range lines {
		if shown >= maxShowLines || b.Len()+len(ln) > maxShowChars {
			break
		}
		b.WriteString(ln)
		b.WriteString("\n")
		shown++
	}
	more := ""
	if rest := len(lines) - shown; rest > 0 {
		more = fmt.Sprintf("\n… %d more lines: show %s --from %d", rest, id, from+shown)
	}
	return fmt.Sprintf("%s (lines %d–%d)%s\n\n%s%s", header, sec.Line, sec.EndLine, note,
		strings.TrimRight(b.String(), "\n"), more)
}

// maxOutlineSections bounds an outline.
const maxOutlineSections = 40

// Outline lists a document's sections, each with the ID show expands, so an
// agent can read the part it needs rather than the file.
func (l *Library) Outline(subj *Subject) string {
	last := 0
	for _, sec := range subj.Sections {
		last = max(last, sec.EndLine)
	}
	lines := []string{fmt.Sprintf("%s: %s (%s, lines 1–%d)", subj.Path, subj.Title, plural(len(subj.Sections), "section"), last)}
	for i, sec := range subj.Sections {
		if i >= maxOutlineSections {
			lines = append(lines, fmt.Sprintf("… %d more sections", len(subj.Sections)-i))
			break
		}
		lines = append(lines, fmt.Sprintf("%s § %s (lines %d–%d)", ShortID(sec.NodeID), sec.HeadingPath, sec.Line, sec.EndLine))
	}
	lines = append(lines, "expand: show <id>")
	return strings.Join(lines, "\n")
}

// SectionByID finds a section by its node ID.
func (l *Library) SectionByID(id string) (*Subject, *Section) {
	for _, subj := range l.Subjects {
		for _, sec := range subj.Sections {
			if sec.NodeID == id {
				return subj, sec
			}
		}
	}
	return nil, nil
}

func (l *Library) showSaid(e *Evidence) (string, error) {
	m := l.Meetings[e.SessionID]
	if m == nil {
		return "", fmt.Errorf("meeting %s is no longer indexed", e.SessionID)
	}
	header := fmt.Sprintf("%s %s (%s)", m.Date.Format("2006-01-02"), m.Title, ShortID(m.NodeID))
	if l.Transcripts == nil {
		return header + "\n" + e.Quote, nil
	}
	turns, err := l.Transcripts.Span(m, e.Start-showContextSeconds, e.End+showContextSeconds)
	first, _, found := LocateQuote(turns, e.Quote)
	if err == nil && !found {
		// The anchor is only as good as the quote's match when the brief
		// was written, and a decision's or follow-up's quote has no time at
		// all; the recording itself says where the words are.
		if all, err := l.Transcripts.Span(m, 0, math.MaxFloat64); err == nil {
			if i, _, ok := LocateQuote(all, e.Quote); ok {
				turns, first, found = all, i, true
			}
		}
	}
	if err != nil || len(turns) == 0 {
		return header + "\n\"" + e.Quote + "\"\n(the recording could not be read)", nil
	}
	if found {
		// Start a couple of turns before the quote, so it is never cut off
		// by the bound on what is shown.
		turns = turns[max(0, first-showTurnsBefore):]
	}
	var b strings.Builder
	for _, t := range turns {
		who := t.Speaker
		if l.Speakers != nil {
			who = l.Speakers.Name(m, t.Speaker)
		}
		line := fmt.Sprintf("%s %s: %s\n", clock(t.Start), who, strings.Join(strings.Fields(t.Text), " "))
		if b.Len()+len(line) > maxShowSaidChars {
			b.WriteString("…\n")
			break
		}
		b.WriteString(line)
	}
	return header + "\n\n" + strings.TrimRight(b.String(), "\n"), nil
}

// readLines returns lines first..last (1-based, inclusive) of a file.
func readLines(path string, first, last int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	n := 0
	for sc.Scan() {
		n++
		if n < first {
			continue
		}
		if last > 0 && n > last {
			break
		}
		out = append(out, sc.Text())
	}
	return out
}

// Bounds on a meeting's brief.
const (
	maxMeetingSummary   = 500
	maxPartSummary      = 180
	maxMeetingQuote     = 200
	maxTopicsInBrief    = 12
	maxDecisionsInBrief = 8
	maxActionsInBrief   = 8
)

// MeetingBrief is what a meeting settled, in a budget: its summary, the
// decisions with their quotes, its topics with the IDs show expands to what
// was said, its follow-ups, and the documents it was judged to discuss.
// Without a store, while a sync holds the graph, it is the documents only.
func (l *Library) MeetingBrief(ctx context.Context, m *Meeting, budget int) string {
	if budget <= 0 {
		budget = DefaultBriefBudget
	}
	budget = min(budget, MaxBriefBudget)
	header := fmt.Sprintf("%s %s (%s)", m.Date.Format("2006-01-02"), m.Title, ShortID(m.NodeID))
	parts := []*briefPart{}
	summary := briefPart{keep: -1}
	if m.Summary != "" {
		summary.lines = []string{clip(m.Summary, maxMeetingSummary)}
	}
	decided := briefPart{title: "decided:", keep: 3}
	topics := briefPart{title: "topics (show an ID for what was said):", keep: 4}
	actions := briefPart{title: "follow-ups:", keep: 2}
	if l.Store != nil {
		children, _ := l.Store.GetNeighbors(ctx, m.NodeID, graph.EdgeContains, graph.Outgoing)
		var segs []*graph.Node
		for _, c := range children {
			switch c.Type {
			case graph.NodeTopicSegment:
				segs = append(segs, c)
			case graph.NodeDecision:
				if len(decided.lines) >= maxDecisionsInBrief {
					continue
				}
				line := "- " + clip(c.Properties[graph.PropSummary], maxPartSummary)
				if q := c.Properties[graph.PropQuote]; q != "" {
					line += fmt.Sprintf(" — %q", clip(q, maxMeetingQuote))
					if c.Properties["quote_verified"] != "true" {
						line += " (quote not found in the transcript)"
					}
				}
				decided.lines = append(decided.lines, line+" ["+ShortID(c.ID)+"]")
			case graph.NodeActionItem:
				if len(actions.lines) >= maxActionsInBrief || strings.EqualFold(c.Properties["status"], "done") {
					continue
				}
				who := c.Properties[graph.PropAssignee]
				if who == "" {
					who = "unassigned"
				}
				due := ""
				if d := c.Properties[graph.PropDueDate]; d != "" {
					due = ", due " + d
				}
				actions.lines = append(actions.lines, fmt.Sprintf("- %s (%s%s) [%s]",
					clip(c.Properties[graph.PropSummary], maxPartSummary), who, due, ShortID(c.ID)))
			}
		}
		sort.Slice(segs, func(i, j int) bool { return startOf(segs[i]) < startOf(segs[j]) })
		for i, sg := range segs {
			if i >= maxTopicsInBrief {
				topics.lines = append(topics.lines, fmt.Sprintf("… %d more", len(segs)-i))
				break
			}
			topics.lines = append(topics.lines, fmt.Sprintf("- %s %s–%s %s: %s", ShortID(sg.ID),
				clock(startOf(sg)), clock(endOf(sg)), sg.Name, clip(sg.Properties[graph.PropSummary], maxPartSummary)))
		}
	}
	docs := briefPart{title: "documents discussed:", keep: 2}
	type hit struct {
		rel *Relation
		p   float64
	}
	var hits []hit
	for _, r := range l.Relations {
		if v, ok := r.Links[m.SessionID]; ok && v.Accepted {
			hits = append(hits, hit{r, v.P})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].p > hits[j].p })
	for i, h := range hits {
		if i >= maxDocsForAMeeting {
			docs.lines = append(docs.lines, fmt.Sprintf("… %d more", len(hits)-i))
			break
		}
		docs.lines = append(docs.lines, "- "+l.FinderLine(h.rel))
	}
	parts = append(parts, &summary, &decided, &topics, &actions, &docs)
	render := func() string {
		out := []string{header}
		for _, p := range parts {
			if len(p.lines) == 0 {
				continue
			}
			if p.title != "" {
				out = append(out, p.title)
			}
			out = append(out, p.lines...)
		}
		if len(out) == 1 {
			out = append(out, "nothing recorded about it yet")
		}
		return strings.Join(out, "\n")
	}
	for _, trim := range []struct {
		p    *briefPart
		keep int
	}{{&docs, 2}, {&actions, 2}, {&topics, 4}, {&decided, 3}, {&docs, 0}, {&actions, 0}, {&topics, 2}} {
		if EstimateTokens(render()) <= budget {
			break
		}
		if len(trim.p.lines) > trim.keep {
			trim.p.lines = trim.p.lines[:trim.keep]
		}
	}
	return render()
}

func startOf(n *graph.Node) float64 {
	v, _ := strconv.ParseFloat(n.Properties[graph.PropStartTime], 64)
	return v
}

func endOf(n *graph.Node) float64 {
	v, _ := strconv.ParseFloat(n.Properties[graph.PropEndTime], 64)
	return v
}

// ShowSpan prints what was said in a meeting between two times, bounded,
// from the given turn on, naming where to continue.
func (l *Library) ShowSpan(m *Meeting, start, end float64, from int, id string) (string, error) {
	header := fmt.Sprintf("%s %s (%s) %s–%s", m.Date.Format("2006-01-02"), m.Title, ShortID(m.NodeID), clock(start), clock(end))
	if l.Transcripts == nil {
		return header + "\n(the recording is not available)", nil
	}
	turns, err := l.Transcripts.Span(m, start, end)
	if err != nil || len(turns) == 0 {
		return header + "\n(the recording could not be read)", nil
	}
	if from > 0 && from < len(turns) {
		turns = turns[from:]
	}
	var b strings.Builder
	shown := 0
	for _, t := range turns {
		who := t.Speaker
		if l.Speakers != nil {
			who = l.Speakers.Name(m, t.Speaker)
		}
		line := fmt.Sprintf("%s %s: %s\n", clock(t.Start), who, strings.Join(strings.Fields(t.Text), " "))
		if b.Len()+len(line) > maxShowChars {
			break
		}
		b.WriteString(line)
		shown++
	}
	more := ""
	if rest := len(turns) - shown; rest > 0 {
		more = fmt.Sprintf("\n… %d more turns: show %s --from %d", rest, id, from+shown)
	}
	return header + "\n\n" + strings.TrimRight(b.String(), "\n") + more, nil
}

// FinderLine is a relation in one line: its ID, document, headline, how many
// meetings and when, and any freshness flags.
func (l *Library) FinderLine(r *Relation) string {
	head := "no brief yet"
	if len(r.Accepted()) == 0 {
		head = "no meeting confirmed"
	}
	if r.Brief != nil && r.Brief.Headline.Text != "" {
		head = r.Brief.Headline.Text
	}
	if len(head) > maxHeadlineInFinder {
		head = head[:maxHeadlineInFinder] + "…"
	}
	var last time.Time
	for _, v := range r.Accepted() {
		if m := l.Meetings[v.SessionID]; m != nil && m.Date.After(last) {
			last = m.Date
		}
	}
	line := fmt.Sprintf("%s doc %s: %s (%s %s)", ShortID(RelationID(r.SubjectPath)), r.SubjectPath, head,
		plural(len(r.Accepted()), "mtg"), strings.TrimSuffix(strings.TrimPrefix(lastSeen(last), "("), ")"))
	if flags := Freshness(r, l.Subjects[r.SubjectPath], l.Meetings); len(flags) > 0 {
		line += " !" + strings.Join(flags, " !")
	}
	return line
}
