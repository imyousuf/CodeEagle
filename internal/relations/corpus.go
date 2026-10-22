package relations

import (
	"context"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// minSubjectText is the least text a document must hold to be a subject.
// Anything shorter is a stub, an index page or a heading with a link, and
// says too little for a meeting to have discussed it.
const minSubjectText = 400

// Subject is a document that relations can be about, with its sections.
type Subject struct {
	Path      string
	Title     string
	RootID    string
	Hash      string
	UpdatedAt time.Time
	Sections  []*Section
	// Topics are the IDs of topics the document was filed under, where its
	// parser or enrichment assigned any.
	Topics []string
	// TextLen is how much text the document holds in all.
	TextLen int
}

// Section is one section of a subject.
type Section struct {
	NodeID      string
	Heading     string
	HeadingPath string
	Ordinal     int
	Line        int
	EndLine     int
	Text        string
	Hash        string
}

// Meeting is a recorded meeting, the discussion side of a relation.
type Meeting struct {
	NodeID       string
	SessionID    string
	Title        string
	Summary      string
	Mentions     string
	Participants string
	ContentHash  string
	FilePath     string
	Date         time.Time
	Segments     []*Segment
	Decisions    []*Decided
	Actions      []*Action
}

// Action is a follow-up recorded in a meeting.
type Action struct {
	Text     string
	Quote    string
	Assignee string
	Due      string
	Status   string
}

// Segment is the part of a meeting about one topic.
type Segment struct {
	NodeID   string
	Name     string
	Summary  string
	Keywords string
	Start    float64
	End      float64
	Topics   []string
	meeting  *Meeting
}

// Meeting returns the meeting the segment belongs to.
func (s *Segment) Meeting() *Meeting { return s.meeting }

// Decided is a decision recorded in a meeting, with its verbatim quote.
type Decided struct {
	Text  string
	Quote string
	Topic string
}

// SubjectFilter decides which document paths may be subjects, beyond the
// built-in exclusions.
type SubjectFilter func(path string) bool

// LoadSubjects reads the markdown documents in the graph that relations may be
// about.
//
// Not subjects: transcripts that are also indexed as documents (each would
// relate to its own meeting), AI guideline files (instructions to an agent,
// not a subject of discussion), duplicates of another file, and documents
// under minSubjectText of text.
func LoadSubjects(ctx context.Context, store graph.Store, keep SubjectFilter) ([]*Subject, error) {
	return loadSubjects(ctx, store, keep, true)
}

// loadSubjects loads the documents, and for proposing also which ones copy
// another file and the topics each was filed under: two edge lookups per
// document that reading a brief has no use for.
func loadSubjects(ctx context.Context, store graph.Store, keep SubjectFilter, forProposing bool) ([]*Subject, error) {
	nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeDocument, Language: markdownLanguage})
	if err != nil {
		return nil, err
	}
	byPath := make(map[string]*Subject)
	var sections []*graph.Node
	for _, n := range nodes {
		switch {
		case n.Name == n.FilePath && n.Properties["kind"] == "":
			if n.Properties[graph.PropIsTranscript] == "true" {
				continue
			}
			if keep != nil && !keep(n.FilePath) {
				continue
			}
			byPath[n.FilePath] = &Subject{
				Path:      n.FilePath,
				Title:     subjectTitle(n),
				RootID:    n.ID,
				Hash:      n.Properties[graph.PropContentHash],
				UpdatedAt: n.UpdatedAt,
				TextLen:   len(n.DocComment),
			}
		case n.Properties["kind"] == "section":
			sections = append(sections, n)
		}
	}
	for _, n := range sections {
		s, ok := byPath[n.FilePath]
		if !ok {
			continue
		}
		ord, _ := strconv.Atoi(n.Properties["ordinal"])
		hp := n.Properties["heading_path"]
		if hp == "" {
			hp = n.Name
		}
		s.Sections = append(s.Sections, &Section{
			NodeID:      n.ID,
			Heading:     n.Name,
			HeadingPath: hp,
			Ordinal:     ord,
			Line:        n.Line,
			EndLine:     n.EndLine,
			Text:        n.DocComment,
			Hash:        n.Properties["section_hash"],
		})
		s.TextLen += len(n.DocComment)
	}

	var out []*Subject
	for _, s := range byPath {
		if s.TextLen < minSubjectText {
			continue
		}
		if forProposing && copiesAnotherFile(ctx, store, s) {
			continue
		}
		if s.Title == "" {
			s.Title = titleFromSections(s)
		}
		sort.Slice(s.Sections, func(i, j int) bool { return s.Sections[i].Line < s.Sections[j].Line })
		if forProposing {
			s.Topics = outgoing(ctx, store, s.RootID, graph.EdgeHasTopic)
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// subjectTitle is the document's front-matter title, else empty.
func subjectTitle(n *graph.Node) string {
	return strings.Trim(n.Properties["frontmatter:title"], `"' `)
}

// titleFromSections is the first top-level heading, else the file name with
// its separators read as spaces.
func titleFromSections(s *Subject) string {
	for _, sec := range s.Sections {
		if !strings.Contains(sec.HeadingPath, " > ") {
			return sec.Heading
		}
	}
	base := strings.TrimSuffix(path.Base(s.Path), path.Ext(s.Path))
	return strings.NewReplacer("-", " ", "_", " ", ".", " ").Replace(base)
}

// LoadMeetings reads every recorded meeting with its segments and decisions,
// keyed by session ID.
func LoadMeetings(ctx context.Context, store graph.Store) (map[string]*Meeting, error) {
	meetings, err := LoadMeetingHeaders(ctx, store)
	if err != nil {
		return nil, err
	}
	return meetings, loadMeetingParts(ctx, store, meetings)
}

// LoadMeetingHeaders loads the meetings without their segments, decisions
// and follow-ups: what reading a brief needs, at a fraction of the cost.
func LoadMeetingHeaders(ctx context.Context, store graph.Store) (map[string]*Meeting, error) {
	nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeMeeting})
	if err != nil {
		return nil, err
	}
	meetings := make(map[string]*Meeting, len(nodes))
	for _, n := range nodes {
		sid := n.Properties[graph.PropMeetingID]
		if sid == "" {
			sid = n.QualifiedName
		}
		if sid == "" || n.Properties[graph.PropIncomplete] == "true" {
			continue
		}
		meetings[sid] = &Meeting{
			NodeID:       n.ID,
			SessionID:    sid,
			Title:        n.Name,
			Summary:      n.Properties[graph.PropSummary],
			Mentions:     n.Properties["mentions"],
			Participants: n.Properties["participants"],
			ContentHash:  n.Properties[graph.PropContentHash],
			FilePath:     n.FilePath,
			Date:         n.UpdatedAt,
		}
	}
	return meetings, nil
}

func loadMeetingParts(ctx context.Context, store graph.Store, meetings map[string]*Meeting) error {
	segs, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeTopicSegment})
	if err != nil {
		return err
	}
	for _, n := range segs {
		m, ok := meetings[n.Properties[graph.PropMeetingID]]
		if !ok {
			continue
		}
		start, _ := strconv.ParseFloat(n.Properties[graph.PropStartTime], 64)
		end, _ := strconv.ParseFloat(n.Properties[graph.PropEndTime], 64)
		seg := &Segment{
			NodeID:   n.ID,
			Name:     n.Name,
			Summary:  n.Properties[graph.PropSummary],
			Keywords: n.Properties["keywords"],
			Start:    start,
			End:      end,
			meeting:  m,
		}
		seg.Topics = outgoing(ctx, store, n.ID, graph.EdgeHasTopic)
		m.Segments = append(m.Segments, seg)
	}

	decs, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeDecision})
	if err != nil {
		return err
	}
	for _, n := range decs {
		m, ok := meetings[n.Properties[graph.PropMeetingID]]
		if !ok {
			continue
		}
		m.Decisions = append(m.Decisions, &Decided{
			Text:  n.Properties[graph.PropSummary],
			Quote: n.Properties[graph.PropQuote],
			Topic: n.Properties["topic"],
		})
	}
	acts, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeActionItem})
	if err != nil {
		return err
	}
	for _, n := range acts {
		m, ok := meetings[n.Properties[graph.PropMeetingID]]
		if !ok {
			continue
		}
		m.Actions = append(m.Actions, &Action{
			Text:     n.Properties[graph.PropSummary],
			Quote:    n.Properties[graph.PropQuote],
			Assignee: n.Properties[graph.PropAssignee],
			Due:      n.Properties[graph.PropDueDate],
			Status:   n.Properties[graph.PropStatus],
		})
	}
	for _, m := range meetings {
		sort.Slice(m.Segments, func(i, j int) bool { return m.Segments[i].Start < m.Segments[j].Start })
	}
	return nil
}

// SpeakerNamer names the speaker behind a recording's diarization label.
type SpeakerNamer interface {
	Name(m *Meeting, label string) string
}

// StoreSpeakers names speakers from the identities meeting indexing recorded.
// A label nobody was identified behind keeps its label: a wrong name is worse
// than none.
type StoreSpeakers struct {
	store graph.Store
	ctx   context.Context
	mu    sync.Mutex
	cache map[string]string
}

// NewStoreSpeakers names speakers from store.
func NewStoreSpeakers(ctx context.Context, store graph.Store) *StoreSpeakers {
	return &StoreSpeakers{store: store, ctx: ctx, cache: make(map[string]string)}
}

// Name returns who spoke under label in m, or the label itself.
func (s *StoreSpeakers) Name(m *Meeting, label string) string {
	id := graph.NewNodeID(string(graph.NodeSpeaker), m.FilePath, label)
	s.mu.Lock()
	name, ok := s.cache[id]
	s.mu.Unlock()
	if ok {
		return name
	}
	name = label
	for _, target := range outgoing(s.ctx, s.store, id, graph.EdgeIdentifiedAs) {
		if p, err := s.store.GetNode(s.ctx, target); err == nil && p.Name != "" {
			name = p.Name
			break
		}
	}
	s.mu.Lock()
	s.cache[id] = name
	s.mu.Unlock()
	return name
}

// copiesAnotherFile reports whether the document is a duplicate of another
// file. GetEdges returns edges in both directions, so only outgoing ones
// count, and an edge into the document's own file does not: an earlier
// linker marked a markdown file a duplicate of its own section.
func copiesAnotherFile(ctx context.Context, store graph.Store, s *Subject) bool {
	for _, target := range outgoing(ctx, store, s.RootID, graph.EdgeDuplicateOf) {
		if n, err := store.GetNode(ctx, target); err == nil && n.FilePath != s.Path {
			return true
		}
	}
	return false
}

// outgoing returns the targets of a node's outgoing edges of one type.
func outgoing(ctx context.Context, store graph.Store, id string, t graph.EdgeType) []string {
	edges, err := store.GetEdges(ctx, id, t)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range edges {
		if e.SourceID == id {
			out = append(out, e.TargetID)
		}
	}
	return out
}

// markdownLanguage is parser.LangMarkdown.
const markdownLanguage = "markdown"
