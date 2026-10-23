package relations

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Budgets for a rendered brief, in estimated tokens.
const (
	DefaultBriefBudget = 1200
	MaxBriefBudget     = 2000
)

// EstimateTokens is a cheap count of the tokens text costs a reader. Bytes
// over 3.5 is close for English prose and identifiers, and errs high.
func EstimateTokens(text string) int {
	return int(float64(len(text))/3.5) + 1
}

// ShortID is the prefix of an ID an agent is shown and can pass back.
func ShortID(id string) string {
	if len(id) > 6 {
		return id[:6]
	}
	return id
}

// RenderOptions shape a rendered brief.
type RenderOptions struct {
	// Budget is the most estimated tokens the brief may take.
	Budget int
	// Since, when set, adds what changed after it and puts that first.
	Since time.Time
	// SinceLabel is how Since was given, to repeat back.
	SinceLabel string
	// Meetings supplies meeting dates and titles.
	Meetings map[string]*Meeting
	// Flags are the freshness flags to show.
	Flags []string
	// Subject is the document as it is now, for its opening.
	Subject *Subject
	// Unconfirmed, when set, are lines for the closest meetings below the
	// gate, with what was said there; otherwise they are listed by title.
	Unconfirmed []string
}

// Bounds on the parts of a brief that come from the graph rather than the
// writer.
const (
	maxDocumentLine   = 320
	maxMeetingTitle   = 70
	maxUnconfirmed    = 3
	minUnconfirmedP   = 0.5
	keepMeetingsLines = 5
	keepTimelineLines = 8
)

// briefPart is one block of a rendered brief, with how much of it may be
// dropped under a tight budget.
type briefPart struct {
	title string
	lines []string
	// keep is how many lines survive trimming; -1 means the whole part is
	// never dropped.
	keep int
}

// Render writes the brief as compact text: header, headline, current,
// conflicts, unresolved, delta, freshness and how to expand. Every line ends
// with the evidence IDs it rests on. Under a tight budget, delta goes first,
// then unresolved beyond two, current beyond three and conflicts beyond two;
// the header, headline and freshness always stay. With Since, the delta moves
// up to follow the headline.
func Render(rel *Relation, o RenderOptions) string {
	if o.Budget <= 0 {
		o.Budget = DefaultBriefBudget
	}
	if o.Budget > MaxBriefBudget {
		o.Budget = MaxBriefBudget
	}
	accepted := rel.Accepted()
	var last time.Time
	for _, v := range accepted {
		if m := o.Meetings[v.SessionID]; m != nil && m.Date.After(last) {
			last = m.Date
		}
	}
	header := fmt.Sprintf("%s · %s %s", rel.SubjectPath, plural(len(accepted), "meeting"), lastSeen(last))
	if rel.Revision > 0 {
		header += fmt.Sprintf(" · @r%d", rel.Revision)
	}

	// What the graph knows without the writer: the document's opening,
	// every meeting confirmed to discuss it, and the closest ones that were
	// not. Answers lost most points for missing meetings and leaving the
	// document out, and an agent told nothing about the unconfirmed ones
	// reported that no meeting discussed the document.
	docLine := ""
	if o.Subject != nil && o.Subject.Preamble != "" {
		docLine = "document: " + clip(strings.Join(strings.Fields(o.Subject.Preamble), " "), maxDocumentLine)
	}
	meetingsPart := briefPart{title: "meetings that discussed it, newest first:", keep: keepMeetingsLines}
	byDate := append([]*Verdict(nil), accepted...)
	sort.SliceStable(byDate, func(i, j int) bool {
		return meetingDate(o.Meetings, byDate[i]).After(meetingDate(o.Meetings, byDate[j]))
	})
	for _, v := range byDate {
		if m := o.Meetings[v.SessionID]; m != nil {
			meetingsPart.lines = append(meetingsPart.lines, fmt.Sprintf("- %s %s (%s)", m.Date.Format("2006-01-02"), clip(m.Title, maxMeetingTitle), ShortID(m.NodeID)))
		}
	}
	unconfirmed := briefPart{title: "possibly also, below the gate and not confirmed:", keep: 2}
	var near []*Verdict
	for _, v := range rel.Links {
		if !v.Accepted && v.P >= minUnconfirmedP && o.Meetings[v.SessionID] != nil {
			near = append(near, v)
		}
	}
	sort.SliceStable(near, func(i, j int) bool { return near[i].P > near[j].P })
	for i, v := range near {
		if i >= maxUnconfirmed {
			break
		}
		m := o.Meetings[v.SessionID]
		unconfirmed.lines = append(unconfirmed.lines, fmt.Sprintf("- %s %s (%s) p=%.2f", m.Date.Format("2006-01-02"), clip(m.Title, maxMeetingTitle), ShortID(m.NodeID), v.P))
	}
	if o.Unconfirmed != nil {
		unconfirmed.lines = o.Unconfirmed
	}

	b := rel.Brief
	if b == nil {
		lines := []string{header}
		if docLine != "" {
			lines = append(lines, docLine)
		}
		switch {
		case len(meetingsPart.lines) > 0:
			lines = append(lines, "no brief written yet")
			lines = append(lines, meetingsPart.title)
			lines = append(lines, meetingsPart.lines...)
		default:
			lines = append(lines, "no meeting confirmed to discuss it; that does not mean none did")
		}
		if len(unconfirmed.lines) > 0 {
			lines = append(lines, unconfirmed.title)
			lines = append(lines, unconfirmed.lines...)
		}
		if len(o.Flags) > 0 {
			lines = append(lines, "fresh: "+strings.Join(o.Flags, ", "))
		}
		return strings.Join(lines, "\n")
	}

	headline := b.Headline.Text + cites(b.Headline.Cites)
	points := briefPart{title: "the document says:", keep: 3}
	for _, l := range b.Points {
		points.lines = append(points.lines, "- "+l.Text+cites(l.Cites))
	}
	// With a timeline, each meeting is a line of what was said there; the
	// meetings it does not cover are still listed by title.
	if len(b.Timeline) > 0 {
		sessionOf := make(map[string]string, len(rel.Evidence))
		for _, e := range rel.Evidence {
			sessionOf[e.ID] = e.SessionID
		}
		covered := make(map[string]bool)
		var lines []string
		for _, e := range b.Timeline {
			for _, c := range e.Cites {
				covered[sessionOf[c]] = true
			}
			lines = append(lines, fmt.Sprintf("- %s %s%s", e.Date, e.Text, cites(e.Cites)))
		}
		for _, v := range byDate {
			if m := o.Meetings[v.SessionID]; m != nil && !covered[v.SessionID] {
				lines = append(lines, fmt.Sprintf("- %s %s (%s)", m.Date.Format("2006-01-02"), clip(m.Title, maxMeetingTitle), ShortID(m.NodeID)))
			}
		}
		meetingsPart.lines = lines
		meetingsPart.keep = keepTimelineLines
	}
	current := briefPart{title: "current:", keep: 3}
	for _, l := range b.Current {
		current.lines = append(current.lines, "- "+l.Text+cites(l.Cites))
	}
	// A conflict is shown in the two passages' own words where they are
	// recorded. The drafting model's paraphrase of each side can attribute
	// the meeting's position to the document and the reverse, and the
	// contradiction check, being symmetric, cannot notice; the verified
	// quotes cannot be swapped.
	quoteOf := make(map[string]string, len(rel.Evidence))
	for _, e := range rel.Evidence {
		quoteOf[e.ID] = e.Quote
	}
	side := func(cite, paraphrase string) string {
		if q := quoteOf[cite]; q != "" {
			return "\"" + clip(q, maxConflictQuote) + "\""
		}
		return paraphrase
	}
	conflicts := briefPart{title: "conflicts:", keep: 2}
	for _, c := range b.Conflicts {
		date := ""
		if c.Date != "" {
			date = c.Date + " "
		}
		conflicts.lines = append(conflicts.lines, fmt.Sprintf("- document: %s / %smeeting: %s%s",
			side(c.DocCite, c.DocSays), date, side(c.SaidCite, c.DiscussionSays), cites([]string{c.DocCite, c.SaidCite})))
	}
	unresolved := briefPart{title: "unresolved:", keep: 2}
	for _, u := range b.Unresolved {
		raised := ""
		if u.Raised != "" {
			raised = " (raised " + u.Raised + ")"
		}
		unresolved.lines = append(unresolved.lines, "- "+u.Text+raised+cites(u.Cites))
	}
	delta := briefPart{title: "changes:", keep: 0}
	if !o.Since.IsZero() {
		delta.title = "changes since " + o.SinceLabel + ":"
		delta.keep = -1
	}
	for _, e := range b.Events {
		if !o.Since.IsZero() && !dateAfter(e.Date, o.Since) {
			continue
		}
		delta.lines = append(delta.lines, fmt.Sprintf("- %s %s%s", e.Date, e.Text, cites(e.Cites)))
	}
	if !o.Since.IsZero() {
		for _, key := range sortedKeys(rel.Sections) {
			if st := rel.Sections[key]; st.FirstSeen.After(o.Since) {
				delta.lines = append(delta.lines, fmt.Sprintf("- %s document section %q changed", st.FirstSeen.Format("2006-01-02"), key))
			}
		}
		if len(delta.lines) == 0 {
			delta.lines = []string{"- nothing"}
		}
	}

	tail := []string{}
	if len(o.Flags) > 0 {
		tail = append(tail, "fresh: "+strings.Join(o.Flags, ", "))
	}
	expand := "expand: "
	if ids := allCites(b); len(ids) > 0 {
		expand += "show " + ids[0] + " · "
	}
	expand += fmt.Sprintf("brief %s --since @r%d", ShortID(RelationID(rel.SubjectPath)), rel.Revision)
	tail = append(tail, expand)

	parts := []*briefPart{&points, &current, &conflicts, &unresolved, &meetingsPart, &unconfirmed, &delta}
	if !o.Since.IsZero() {
		parts = []*briefPart{&delta, &points, &current, &conflicts, &unresolved, &meetingsPart, &unconfirmed}
	}
	render := func() string {
		out := []string{header, headline}
		if docLine != "" {
			out = append(out, docLine)
		}
		for _, p := range parts {
			if len(p.lines) == 0 {
				continue
			}
			out = append(out, p.title)
			out = append(out, p.lines...)
		}
		out = append(out, tail...)
		return strings.Join(out, "\n")
	}
	// Trim in priority order until the brief fits.
	for _, trim := range []struct {
		p    *briefPart
		keep int
	}{{&delta, 0}, {&unconfirmed, 2}, {&unresolved, 2}, {&meetingsPart, meetingsPart.keep}, {&points, 3}, {&current, 3},
		{&conflicts, 2}, {&unresolved, 0}, {&unconfirmed, 0}, {&meetingsPart, 3}, {&points, 1}, {&current, 1}} {
		if EstimateTokens(render()) <= o.Budget {
			break
		}
		if trim.p.keep == -1 {
			continue
		}
		if len(trim.p.lines) > trim.keep {
			trim.p.lines = trim.p.lines[:trim.keep]
		}
	}
	return render()
}

// maxConflictQuote bounds each side of a conflict as shown.
const maxConflictQuote = 240

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := strings.LastIndex(s[:n], " ")
	if cut < n/2 {
		cut = n
	}
	return s[:cut] + "…"
}

func meetingDate(meetings map[string]*Meeting, v *Verdict) time.Time {
	if m := meetings[v.SessionID]; m != nil {
		return m.Date
	}
	return time.Time{}
}

func cites(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return " [" + strings.Join(ids, ",") + "]"
}

func allCites(b *Brief) []string {
	var out []string
	seen := make(map[string]bool)
	add := func(ids []string) {
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	add(b.Headline.Cites)
	for _, l := range b.Current {
		add(l.Cites)
	}
	for _, c := range b.Conflicts {
		add([]string{c.DocCite, c.SaidCite})
	}
	for _, u := range b.Unresolved {
		add(u.Cites)
	}
	for _, e := range b.Events {
		add(e.Cites)
	}
	for _, l := range b.Points {
		add(l.Cites)
	}
	for _, e := range b.Timeline {
		add(e.Cites)
	}
	return out
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func lastSeen(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return "(last " + t.Format("2006-01-02") + ")"
}

func dateAfter(date string, t time.Time) bool {
	d, err := time.Parse("2006-01-02", date)
	return err == nil && !d.Before(t.Truncate(24*time.Hour))
}

func sortedKeys(m map[string]SectionState) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var relativeRe = regexp.MustCompile(`^(\d+)([dhw])$`)

// ParseSince turns a --since value into a time: a date, an RFC 3339 time, a
// duration back from now (7d, 36h, 2w), or a revision of rel (@r3).
func ParseSince(value string, rel *Relation, now time.Time) (time.Time, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return time.Time{}, nil
	}
	if strings.HasPrefix(v, "@r") {
		n, err := strconv.Atoi(v[2:])
		if err != nil {
			return time.Time{}, fmt.Errorf("revision %q: want @r followed by a number", v)
		}
		if rel != nil {
			if t, ok := rel.RevisionTime(n); ok {
				return t, nil
			}
		}
		return time.Time{}, fmt.Errorf("revision %d is no longer recorded; use a date", n)
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return t, nil
	}
	if m := relativeRe.FindStringSubmatch(v); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := map[string]time.Duration{"h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour}[m[2]]
		return now.Add(-time.Duration(n) * unit), nil
	}
	return time.Time{}, fmt.Errorf("--since %q: want a date, an RFC 3339 time, 7d/36h/2w, or @rN", v)
}
