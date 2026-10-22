package relations

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"sort"
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
	subjects, err := LoadSubjects(ctx, store, nil)
	if err != nil {
		return nil, fmt.Errorf("load documents: %w", err)
	}
	meetings, err := LoadMeetings(ctx, store)
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
		Budget:     budget,
		Since:      since,
		SinceLabel: sinceLabel,
		Meetings:   l.Meetings,
		Flags:      Freshness(rel, l.Subjects[rel.SubjectPath], l.Meetings),
	})
}

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
	header := fmt.Sprintf("%s § %s", path, sec.HeadingPath)
	note := ""
	if e.SectionHash != "" && e.SectionHash != sec.Hash {
		note = "\n(this section has changed since it was quoted)"
	}

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
		more = fmt.Sprintf("\n… %d more lines: show %s --from %d", rest, e.ID, from+shown)
	}
	return fmt.Sprintf("%s (lines %d–%d)%s\n\n%s%s", header, sec.Line, sec.EndLine, note,
		strings.TrimRight(b.String(), "\n"), more), nil
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
	if err != nil || len(turns) == 0 {
		return header + "\n\"" + e.Quote + "\"\n(the recording could not be read)", nil
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

// MeetingBrief lists the documents a meeting was judged to discuss, with
// their headlines.
func (l *Library) MeetingBrief(m *Meeting) string {
	header := fmt.Sprintf("%s %s (%s)", m.Date.Format("2006-01-02"), m.Title, ShortID(m.NodeID))
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
	if len(hits) == 0 {
		return header + "\nno document has been judged to be discussed in it"
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].p > hits[j].p })
	lines := []string{header, "documents discussed:"}
	for i, h := range hits {
		if i >= maxDocsForAMeeting {
			lines = append(lines, fmt.Sprintf("… %d more", len(hits)-i))
			break
		}
		lines = append(lines, l.FinderLine(h.rel))
	}
	return strings.Join(lines, "\n")
}

// FinderLine is a relation in one line: its ID, document, headline, how many
// meetings and when, and any freshness flags.
func (l *Library) FinderLine(r *Relation) string {
	head := "no brief yet"
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
