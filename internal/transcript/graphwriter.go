package transcript

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// Writer projects an enriched session into the knowledge graph.
//
// Every node it creates carries the transcript's path, so re-indexing a
// recording can delete its old nodes and write fresh ones. People and topics
// are deliberately excluded from that: they are global, shared with the rest
// of the graph, and outlive any single meeting.
type Writer struct {
	store    graph.Store
	people   *PersonRegistry
	topics   *TopicRegistry
	opts     WriterOptions
	ensureOn DateLinker
}

// DateLinker attaches a node to the Year/Month/Date hierarchy. It is injected
// rather than imported so that the transcript package does not depend on the
// indexer.
type DateLinker func(ctx context.Context, store graph.Store, t timeLike, nodeID string) error

// timeLike is the time type DateLinker accepts.
type timeLike = interface{ Unix() int64 }

// WriterOptions configures graph projection.
type WriterOptions struct {
	// MinConfidence is the bar an identification must clear before the graph
	// asserts that a speaker is a particular person.
	MinConfidence float64
	// Owner is the person whose microphone made the recordings.
	Owner string
}

// Stats counts what a write produced.
type Stats struct {
	Meetings     int
	Speakers     int
	People       int
	Identified   int
	Unidentified int
	// Background counts speakers judged not to be people at all.
	Background  int
	Topics      int
	Segments    int
	Decisions   int
	ActionItems int
	Edges       int
}

// Add accumulates another session's stats.
func (s *Stats) Add(o Stats) {
	s.Meetings += o.Meetings
	s.Speakers += o.Speakers
	s.People += o.People
	s.Identified += o.Identified
	s.Unidentified += o.Unidentified
	s.Background += o.Background
	s.Topics += o.Topics
	s.Segments += o.Segments
	s.Decisions += o.Decisions
	s.ActionItems += o.ActionItems
	s.Edges += o.Edges
}

// NewWriter creates a Writer. The registry is shared across sessions so that
// people resolve consistently through a batch.
func NewWriter(store graph.Store, people *PersonRegistry, opts WriterOptions) *Writer {
	if opts.MinConfidence <= 0 {
		opts.MinConfidence = defaultMinConfidence
	}
	return &Writer{store: store, people: people, opts: opts}
}

// WithTopics supplies a shared topic registry. A batch passes one so that the
// vocabulary accumulated across meetings is available to all of them; left
// unset, the writer loads its own on first use.
func (w *Writer) WithTopics(tr *TopicRegistry) *Writer {
	w.topics = tr
	return w
}

// topicRegistry returns the registry, loading it on first use.
func (w *Writer) topicRegistry(ctx context.Context) (*TopicRegistry, error) {
	if w.topics == nil {
		tr, err := LoadTopicRegistry(ctx, w.store)
		if err != nil {
			return nil, err
		}
		w.topics = tr
	}
	return w.topics, nil
}

// WithDateLinker attaches the temporal hierarchy to written meetings, so a
// meeting is reachable from "what happened in March 2026?" the same way a
// modified file is.
func (w *Writer) WithDateLinker(fn DateLinker) *Writer {
	w.ensureOn = fn
	return w
}

// Write projects one enriched session into the graph.
func (w *Writer) Write(ctx context.Context, res *Result) (Stats, error) {
	var st Stats
	s := res.Session
	if s == nil {
		return st, fmt.Errorf("result has no session")
	}

	path := s.Path
	// Clear any previous projection of this recording so re-indexing updates
	// rather than duplicates. People and topics are keyed globally and are
	// untouched by this.
	if path != "" {
		if err := w.store.DeleteByFile(ctx, path); err != nil {
			return st, fmt.Errorf("clear previous meeting nodes: %w", err)
		}
	}

	// Written incomplete, then cleared at the end. Projection is several
	// writes with no transaction around them, so a failure partway leaves a
	// meeting that has speakers but no decisions — indistinguishable, to a
	// reader, from a meeting where nothing was decided. It self-heals on the
	// next successful sync, but a recording that fails every time would sit
	// there looking whole. This project records whether a quote checks out
	// rather than assuming it does; the same applies here.
	meeting, err := w.writeMeeting(ctx, res)
	if err != nil {
		return st, err
	}
	st.Meetings++

	speakerStats, err := w.writeSpeakers(ctx, res, meeting)
	if err != nil {
		return st, err
	}
	st.Add(speakerStats)

	if res.Analysis != nil {
		contentStats, err := w.writeAnalysis(ctx, res, meeting)
		if err != nil {
			return st, err
		}
		st.Add(contentStats)
	}

	if w.ensureOn != nil && !s.StartedAt().IsZero() {
		if err := w.ensureOn(ctx, w.store, s.StartedAt(), meeting.ID); err != nil {
			return st, fmt.Errorf("link meeting to date: %w", err)
		}
	}

	if err := w.markComplete(ctx, meeting); err != nil {
		return st, err
	}
	return st, nil
}

// markComplete clears the incomplete marker once everything is written.
func (w *Writer) markComplete(ctx context.Context, meeting *graph.Node) error {
	if meeting.Properties == nil {
		return nil
	}
	if _, marked := meeting.Properties[graph.PropIncomplete]; !marked {
		return nil
	}
	delete(meeting.Properties, graph.PropIncomplete)
	if err := w.store.AddNode(ctx, meeting); err != nil {
		return fmt.Errorf("clear the incomplete marker: %w", err)
	}
	return nil
}

// writeMeeting creates the node representing the recording itself.
func (w *Writer) writeMeeting(ctx context.Context, res *Result) (*graph.Node, error) {
	s := res.Session

	title := s.Title
	summary := ""
	if res.Analysis != nil {
		if res.Analysis.Title != "" {
			title = res.Analysis.Title
		}
		summary = res.Analysis.Summary
	}

	props := map[string]string{
		graph.PropMeetingID: s.ID,
		graph.PropDuration:  strconv.FormatFloat(s.DurationSeconds(), 'f', 0, 64),
		// Cleared once the rest of the projection succeeds. A meeting still
		// carrying this was written partway and then abandoned.
		graph.PropIncomplete: "true",
	}
	if s.Platform != "" {
		props[graph.PropPlatform] = s.Platform
	}
	if summary != "" {
		props[graph.PropSummary] = summary
	}
	if res.Analysis != nil && len(res.Analysis.Mentions) > 0 {
		// Kept as text here; a linker phase resolves these to code entities
		// once the whole graph is available.
		props["mentions"] = strings.Join(res.Analysis.Mentions, ", ")
	}
	if s.AudioPath != "" {
		props["audio_path"] = s.AudioPath
	}
	props["source_title"] = s.Title
	// Recorded as text as well as edges: "that meeting with Kevin and Mona" is
	// how people search, and semantic search reads properties, not edges.
	if people := res.Participants(w.opts.MinConfidence); len(people) > 0 {
		props["participants"] = strings.Join(people, ", ")
	}

	node := &graph.Node{
		ID:            graph.NewNodeID(string(graph.NodeMeeting), s.Path, s.ID),
		Type:          graph.NodeMeeting,
		Name:          title,
		QualifiedName: s.ID,
		FilePath:      s.Path,
		DocComment:    summary,
		Properties:    props,
		UpdatedAt:     s.StartedAt(),
	}
	if err := w.store.AddNode(ctx, node); err != nil {
		return nil, fmt.Errorf("add meeting: %w", err)
	}
	return node, nil
}

// writeSpeakers creates a Speaker node per participant and links the ones that
// were identified to their Person.
//
// Unidentified speakers are written too, rather than dropped. A meeting where
// three of five voices are known is a fact worth recording: it is what the
// review command lists, and it keeps the participant count honest.
func (w *Writer) writeSpeakers(ctx context.Context, res *Result, meeting *graph.Node) (Stats, error) {
	var st Stats
	s := res.Session

	// Diarization regularly splits one person across several labels — a change
	// of microphone, a gap, a moment of crosstalk. Attendance is therefore
	// accumulated per person and written once at the end: emitting an edge per
	// label would have each overwrite the last, leaving a person credited with
	// only the final fragment of what they said.
	type attendance struct {
		person  *graph.Node
		seconds float64
		labels  []string
	}
	attended := make(map[string]*attendance)

	for _, stat := range s.SubstantiveSpeakers() {
		speaker := &graph.Node{
			ID:            graph.NewNodeID(string(graph.NodeSpeaker), s.Path, stat.Label),
			Type:          graph.NodeSpeaker,
			Name:          stat.Label,
			QualifiedName: s.ID + ":" + stat.Label,
			FilePath:      s.Path,
			UpdatedAt:     s.StartedAt(),
			Properties: map[string]string{
				graph.PropSpeakerLabel:    stat.Label,
				graph.PropSpeakerSource:   stat.Source,
				graph.PropMeetingID:       s.ID,
				graph.PropUtteranceCount:  strconv.Itoa(stat.Utterances),
				graph.PropSpeakingSeconds: strconv.FormatFloat(stat.SpeakingSeconds, 'f', 1, 64),
				graph.PropStartTime:       strconv.FormatFloat(stat.FirstAt, 'f', 1, 64),
				graph.PropEndTime:         strconv.FormatFloat(stat.LastAt, 'f', 1, 64),
			},
			Metrics: map[string]float64{
				"speaking_seconds": stat.SpeakingSeconds,
				"utterances":       float64(stat.Utterances),
				"words":            float64(stat.Words),
			},
		}
		if err := w.store.AddNode(ctx, speaker); err != nil {
			return st, fmt.Errorf("add speaker %q: %w", stat.Label, err)
		}
		st.Speakers++

		if err := w.addEdge(ctx, graph.EdgeContains, meeting.ID, speaker.ID, nil); err != nil {
			return st, err
		}
		st.Edges++

		identity, ok := res.IdentityFor(stat.Label)
		if ok && identity.Method == MethodBackground {
			// Not a person: a television, a demonstrated video. The node stays,
			// so the recording is described honestly and a human can overrule
			// the judgment, but it is never given a name, never linked to
			// anybody, and never counted as an attendee.
			speaker.Properties[graph.PropRole] = MethodBackground
			speaker.Properties[graph.PropConfidence] =
				strconv.FormatFloat(identity.Confidence, 'f', 2, 64)
			if identity.Evidence != "" {
				speaker.Properties[graph.PropEvidence] = truncate(identity.Evidence, 300)
			}
			if err := w.store.AddNode(ctx, speaker); err != nil {
				return st, fmt.Errorf("mark background speaker: %w", err)
			}
			st.Background++
			continue
		}
		if !ok || identity.Name == "" || identity.Confidence < w.opts.MinConfidence {
			st.Unidentified++
			continue
		}

		person, err := w.people.Resolve(ctx, identity.Name)
		if err != nil {
			// An unusable name is not a reason to abandon the meeting.
			st.Unidentified++
			continue
		}
		if stat.IsOwner {
			if err := w.people.MarkOwner(ctx, person); err != nil {
				return st, err
			}
		}
		st.Identified++

		if err := w.addEdge(ctx, graph.EdgeIdentifiedAs, speaker.ID, person.ID, map[string]string{
			graph.PropConfidence: strconv.FormatFloat(identity.Confidence, 'f', 2, 64),
			graph.PropEvidence:   truncate(identity.Evidence, 300),
			graph.PropResolution: identity.Method,
		}); err != nil {
			return st, err
		}
		st.Edges++

		a, ok := attended[person.ID]
		if !ok {
			a = &attendance{person: person}
			attended[person.ID] = a
		}
		a.seconds += stat.SpeakingSeconds
		a.labels = append(a.labels, stat.Label)
	}

	for _, a := range attended {
		if err := w.addEdge(ctx, graph.EdgeAttended, a.person.ID, meeting.ID, map[string]string{
			graph.PropSpeakingSeconds: strconv.FormatFloat(a.seconds, 'f', 1, 64),
			graph.PropSpeakerLabel:    strings.Join(a.labels, ", "),
		}); err != nil {
			return st, err
		}
		st.Edges++
	}
	return st, nil
}

// writeAnalysis creates the topics, decisions, and follow-ups.
func (w *Writer) writeAnalysis(ctx context.Context, res *Result, meeting *graph.Node) (Stats, error) {
	var st Stats
	s := res.Session
	a := res.Analysis

	// Participants, resolved once, so that names appearing in decisions and
	// action items link to the same people who attended.
	attendees := w.attendeeIndex(ctx, res)

	topics, err := w.topicRegistry(ctx)
	if err != nil {
		return st, err
	}

	for _, topic := range a.Topics {
		name := strings.TrimSpace(topic.Name)
		if name == "" {
			continue
		}

		// The subject is resolved through the registry rather than minted per
		// wording. Four meetings that each phrase "MCP authentication"
		// differently must hang off one topic, or HasTopic indexes nothing.
		topicNode, err := topics.Resolve(ctx, name)
		if err != nil {
			// An unusable label costs this topic, not the meeting.
			continue
		}
		st.Topics++

		if err := w.addEdge(ctx, graph.EdgeHasTopic, meeting.ID, topicNode.ID, nil); err != nil {
			return st, err
		}
		st.Edges++

		// Place the subject under the concept the model named, so the
		// hierarchy grows as meetings are indexed rather than only when it is
		// rebuilt in bulk.
		if edges, err := w.linkTopicParent(ctx, topics, topicNode, topic.Parent); err != nil {
			return st, err
		} else {
			st.Edges += edges
		}

		// The segment holds what *this* meeting said about the topic, keeping
		// the shared topic node free of meeting-specific text.
		// The segment keeps the wording this meeting used, so collapsing the
		// subject never loses how it was actually described here.
		segment := &graph.Node{
			ID:            graph.NewNodeID(string(graph.NodeTopicSegment), s.Path, s.ID+":"+name),
			Type:          graph.NodeTopicSegment,
			Name:          name,
			QualifiedName: s.ID + ":" + name,
			FilePath:      s.Path,
			DocComment:    topic.Summary,
			UpdatedAt:     s.StartedAt(),
			Properties: map[string]string{
				graph.PropMeetingID: s.ID,
				graph.PropSummary:   topic.Summary,
				graph.PropStartTime: strconv.FormatFloat(topic.StartTime, 'f', 1, 64),
				graph.PropEndTime:   strconv.FormatFloat(topic.EndTime, 'f', 1, 64),
				"keywords":          strings.Join(topic.Keywords, ", "),
			},
		}
		if err := w.store.AddNode(ctx, segment); err != nil {
			return st, fmt.Errorf("add topic segment %q: %w", name, err)
		}
		st.Segments++

		if err := w.addEdge(ctx, graph.EdgeContains, meeting.ID, segment.ID, nil); err != nil {
			return st, err
		}
		if err := w.addEdge(ctx, graph.EdgeHasTopic, segment.ID, topicNode.ID, nil); err != nil {
			return st, err
		}
		st.Edges += 2

		for _, who := range topic.Participants {
			if personID, ok := attendees[NormalizeName(who)]; ok {
				if err := w.addEdge(ctx, graph.EdgeMentions, segment.ID, personID, nil); err != nil {
					return st, err
				}
				st.Edges++
			}
		}
	}

	for i, d := range a.Decisions {
		text := strings.TrimSpace(d.Text)
		if text == "" {
			continue
		}
		node := &graph.Node{
			ID:            graph.NewNodeID(string(graph.NodeDecision), s.Path, fmt.Sprintf("%s:decision:%d", s.ID, i)),
			Type:          graph.NodeDecision,
			Name:          truncate(text, 120),
			QualifiedName: fmt.Sprintf("%s:decision:%d", s.ID, i),
			FilePath:      s.Path,
			DocComment:    text,
			UpdatedAt:     s.StartedAt(),
			Properties: map[string]string{
				graph.PropMeetingID: s.ID,
				graph.PropSummary:   text,
				graph.PropQuote:     truncate(d.Quote, 400),
				"rationale":         d.Rationale,
				"topic":             d.Topic,
				// Whether the supporting quote is really in the transcript is
				// recorded rather than enforced, so a reader can tell a
				// verified decision from an unverified one.
				"quote_verified": strconv.FormatBool(QuoteInTranscript(s, d.Quote)),
			},
		}
		if err := w.store.AddNode(ctx, node); err != nil {
			return st, fmt.Errorf("add decision: %w", err)
		}
		st.Decisions++

		if err := w.addEdge(ctx, graph.EdgeContains, meeting.ID, node.ID, nil); err != nil {
			return st, err
		}
		st.Edges++

		for _, who := range d.DecidedBy {
			if personID, ok := attendees[NormalizeName(who)]; ok {
				if err := w.addEdge(ctx, graph.EdgeRaisedBy, node.ID, personID, nil); err != nil {
					return st, err
				}
				st.Edges++
			}
		}
		if id, ok := w.topicNodeID(ctx, a, d.Topic); ok {
			if err := w.addEdge(ctx, graph.EdgeHasTopic, node.ID, id, nil); err != nil {
				return st, err
			}
			st.Edges++
		}
	}

	for i, ai := range a.ActionItems {
		text := strings.TrimSpace(ai.Text)
		if text == "" {
			continue
		}
		props := map[string]string{
			graph.PropMeetingID: s.ID,
			graph.PropSummary:   text,
			graph.PropStatus:    "open",
			graph.PropQuote:     truncate(ai.Quote, 400),
			"topic":             ai.Topic,
			"quote_verified":    strconv.FormatBool(QuoteInTranscript(s, ai.Quote)),
		}
		if ai.DueDate != "" {
			props[graph.PropDueDate] = ai.DueDate
		}
		if ai.Assignee != "" {
			props[graph.PropAssignee] = ai.Assignee
		}

		node := &graph.Node{
			ID:            graph.NewNodeID(string(graph.NodeActionItem), s.Path, fmt.Sprintf("%s:action:%d", s.ID, i)),
			Type:          graph.NodeActionItem,
			Name:          truncate(text, 120),
			QualifiedName: fmt.Sprintf("%s:action:%d", s.ID, i),
			FilePath:      s.Path,
			DocComment:    text,
			UpdatedAt:     s.StartedAt(),
			Properties:    props,
		}
		if err := w.store.AddNode(ctx, node); err != nil {
			return st, fmt.Errorf("add action item: %w", err)
		}
		st.ActionItems++

		if err := w.addEdge(ctx, graph.EdgeContains, meeting.ID, node.ID, nil); err != nil {
			return st, err
		}
		st.Edges++

		// An action item is assigned only to someone who was actually in the
		// meeting. A name the model produced that nobody present matches is
		// kept as a property, not asserted as a link to a person.
		if ai.Assignee != "" {
			if personID, ok := attendees[NormalizeName(ai.Assignee)]; ok {
				if err := w.addEdge(ctx, graph.EdgeAssignedTo, node.ID, personID, nil); err != nil {
					return st, err
				}
				st.Edges++
			}
		}
		if id, ok := w.topicNodeID(ctx, a, ai.Topic); ok {
			if err := w.addEdge(ctx, graph.EdgeHasTopic, node.ID, id, nil); err != nil {
				return st, err
			}
			st.Edges++
		}
	}
	return st, nil
}

// attendeeIndex maps normalized participant names — and their aliases — to
// person node IDs.
func (w *Writer) attendeeIndex(ctx context.Context, res *Result) map[string]string {
	idx := make(map[string]string)
	for _, id := range res.Identities {
		if id.Name == "" || id.Confidence < w.opts.MinConfidence {
			continue
		}
		person, err := w.people.Resolve(ctx, id.Name)
		if err != nil {
			continue
		}
		idx[NormalizeName(person.Name)] = person.ID
		idx[NormalizeName(id.Name)] = person.ID
		for _, alias := range aliasesOf(person) {
			idx[NormalizeName(alias)] = person.ID
		}
	}
	return idx
}

// topicNodeID resolves a topic name the model referenced to its node, but only
// when that topic is one the analysis actually produced. Resolution goes
// through the registry so the id matches the topic the segment was linked to,
// rather than one derived from the raw wording.
func (w *Writer) topicNodeID(ctx context.Context, a *Analysis, name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	topics, err := w.topicRegistry(ctx)
	if err != nil {
		return "", false
	}
	for _, t := range a.Topics {
		if !strings.EqualFold(strings.TrimSpace(t.Name), name) {
			continue
		}
		node, err := topics.Resolve(ctx, t.Name)
		if err != nil {
			return "", false
		}
		return node.ID, true
	}
	return "", false
}

// addEdge writes an edge with a deterministic id, so repeated runs update
// rather than duplicate.
func (w *Writer) addEdge(ctx context.Context, t graph.EdgeType, from, to string, props map[string]string) error {
	edge := &graph.Edge{
		ID:         graph.NewNodeID("edge", from, to+":"+string(t)),
		Type:       t,
		SourceID:   from,
		TargetID:   to,
		Properties: props,
	}
	if err := w.store.AddEdge(ctx, edge); err != nil {
		return fmt.Errorf("add %s edge: %w", t, err)
	}
	return nil
}

// SortedTopics returns topic names in a stable order, for display.
func SortedTopics(a *Analysis) []string {
	if a == nil {
		return nil
	}
	out := make([]string, 0, len(a.Topics))
	for _, t := range a.Topics {
		out = append(out, t.Name)
	}
	sort.Strings(out)
	return out
}

// linkTopicParent places a subject under the broader concept a meeting named.
//
// The parent is resolved through the same registry as subjects, so a concept
// named slightly differently by two meetings does not fork into two branches.
func (w *Writer) linkTopicParent(ctx context.Context, topics *TopicRegistry, child *graph.Node, parent string) (int, error) {
	name := CleanTopic(parent)
	if name == "" || SameTopic(name, child.Name) {
		return 0, nil
	}

	// A subject already placed keeps its parent: re-parenting on every mention
	// would have the last meeting to run decide the shape of the tree.
	existing, err := w.store.GetNeighbors(ctx, child.ID, graph.EdgeContains, graph.Incoming)
	if err == nil {
		for _, p := range existing {
			if p.Type == graph.NodeTopic {
				return 0, nil
			}
		}
	}

	parentNode, err := topics.Resolve(ctx, name)
	if err != nil {
		return 0, nil
	}
	if parentNode.ID == child.ID {
		return 0, nil
	}

	if snapshot, changed := topics.PromoteToTheme(parentNode); changed {
		if err := w.store.UpdateNode(ctx, snapshot); err != nil {
			return 0, fmt.Errorf("mark concept %q: %w", name, err)
		}
	}
	if snapshot, changed := topics.DefaultToSubject(child); changed {
		if err := w.store.UpdateNode(ctx, snapshot); err != nil {
			return 0, fmt.Errorf("mark subject %q: %w", child.Name, err)
		}
	}

	if err := w.addEdge(ctx, graph.EdgeContains, parentNode.ID, child.ID, nil); err != nil {
		return 0, err
	}
	return 1, nil
}
