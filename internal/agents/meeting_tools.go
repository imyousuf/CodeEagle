package agents

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// Meetings hold the part of a project's reasoning that never reaches the
// codebase: why a design was chosen, what was ruled out, who owns what next.
// These tools give agents access to that, keyed the same way as the code.

// --- query_meetings ---

type queryMeetingsTool struct {
	store graph.Store
}

func (t *queryMeetingsTool) Name() string { return "query_meetings" }

func (t *queryMeetingsTool) Description() string {
	return "Find meetings by topic, participant, or date. Returns each meeting's title, date, participants, and summary. Use this to learn what was discussed or decided about something, or to find out what a person has been working on."
}

func (t *queryMeetingsTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"topic": map[string]any{
				"type":        "string",
				"description": "Match meetings whose title, summary, or topics mention this text.",
			},
			"person": map[string]any{
				"type":        "string",
				"description": "Match meetings this person attended.",
			},
			"since": map[string]any{
				"type":        "string",
				"description": "Only meetings on or after this date (YYYY-MM-DD).",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum meetings to return (default 10).",
			},
		},
	}
}

func (t *queryMeetingsTool) Execute(ctx context.Context, args map[string]any) (string, bool) {
	topic, _ := args["topic"].(string)
	person, _ := args["person"].(string)
	since, _ := args["since"].(string)
	limit := intArg(args, "limit", 10)

	meetings, err := t.store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeMeeting})
	if err != nil {
		return fmt.Sprintf("Error querying meetings: %v", err), false
	}
	if len(meetings) == 0 {
		return "No meetings are indexed. Run `codeeagle meetings sync` to index transcripts.", false
	}

	var cutoff time.Time
	if since != "" {
		cutoff, err = time.Parse("2006-01-02", since)
		if err != nil {
			return fmt.Sprintf("Error: since must be YYYY-MM-DD, got %q", since), false
		}
	}

	type hit struct {
		node    *graph.Node
		people  []string
		matched string
	}
	var hits []hit

	for _, m := range meetings {
		if !cutoff.IsZero() && m.UpdatedAt.Before(cutoff) {
			continue
		}

		people := t.attendees(ctx, m.ID)
		if person != "" && !containsName(people, person) {
			continue
		}

		matched := ""
		if topic != "" {
			matched = t.topicMatch(ctx, m, topic)
			if matched == "" {
				continue
			}
		}
		hits = append(hits, hit{node: m, people: people, matched: matched})
	}

	sort.Slice(hits, func(i, j int) bool {
		return hits[i].node.UpdatedAt.After(hits[j].node.UpdatedAt)
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	if len(hits) == 0 {
		return "No meetings matched.", false
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d meeting(s):\n\n", len(hits))
	for _, h := range hits {
		fmt.Fprintf(&b, "## %s\n", h.node.Name)
		fmt.Fprintf(&b, "- Date: %s (%s)\n", h.node.UpdatedAt.Format("2006-01-02 15:04"),
			formatDuration(h.node.Properties[graph.PropDuration]))
		fmt.Fprintf(&b, "- Meeting ID: %s\n", h.node.QualifiedName)
		if len(h.people) > 0 {
			fmt.Fprintf(&b, "- Participants: %s\n", strings.Join(h.people, ", "))
		}
		if h.matched != "" {
			fmt.Fprintf(&b, "- Matched on: %s\n", h.matched)
		}
		if s := h.node.Properties[graph.PropSummary]; s != "" {
			fmt.Fprintf(&b, "\n%s\n", s)
		}
		b.WriteString("\n")
	}
	b.WriteString("Use query_meeting_detail with a Meeting ID for topics, decisions, and follow-ups.\n")
	return b.String(), true
}

// topicMatch reports which field matched, so the agent can tell a title hit
// from a hit buried in one topic of a long meeting.
func (t *queryMeetingsTool) topicMatch(ctx context.Context, m *graph.Node, topic string) string {
	needle := strings.ToLower(topic)
	if strings.Contains(strings.ToLower(m.Name), needle) {
		return "title"
	}
	if strings.Contains(strings.ToLower(m.Properties[graph.PropSummary]), needle) {
		return "summary"
	}
	if strings.Contains(strings.ToLower(m.Properties["mentions"]), needle) {
		return "mentioned systems"
	}

	children, err := t.store.GetNeighbors(ctx, m.ID, graph.EdgeContains, graph.Outgoing)
	if err != nil {
		return ""
	}
	for _, c := range children {
		switch c.Type {
		case graph.NodeTopicSegment:
			if strings.Contains(strings.ToLower(c.Name), needle) ||
				strings.Contains(strings.ToLower(c.Properties["keywords"]), needle) ||
				strings.Contains(strings.ToLower(c.Properties[graph.PropSummary]), needle) {
				return "topic: " + c.Name
			}
		case graph.NodeDecision, graph.NodeActionItem:
			if strings.Contains(strings.ToLower(c.Properties[graph.PropSummary]), needle) {
				return strings.ToLower(string(c.Type))
			}
		}
	}
	return ""
}

func (t *queryMeetingsTool) attendees(ctx context.Context, meetingID string) []string {
	people, err := t.store.GetNeighbors(ctx, meetingID, graph.EdgeAttended, graph.Incoming)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(people))
	for _, p := range people {
		names = append(names, p.Name)
	}
	sort.Strings(names)
	return names
}

// --- query_meeting_detail ---

type queryMeetingDetailTool struct {
	store graph.Store
}

func (t *queryMeetingDetailTool) Name() string { return "query_meeting_detail" }

func (t *queryMeetingDetailTool) Description() string {
	return "Get the full record of one meeting: participants with speaking time, topics with per-topic summaries and timestamps, decisions with their supporting quotes, follow-ups with owners, and links to the previous and next meeting with the same people. Takes a Meeting ID from query_meetings."
}

func (t *queryMeetingDetailTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"meeting_id": map[string]any{
				"type":        "string",
				"description": "The Meeting ID, as returned by query_meetings.",
			},
		},
		"required": []string{"meeting_id"},
	}
}

func (t *queryMeetingDetailTool) Execute(ctx context.Context, args map[string]any) (string, bool) {
	id, _ := args["meeting_id"].(string)
	if id == "" {
		return "Error: meeting_id is required", false
	}

	meetings, err := t.store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeMeeting})
	if err != nil {
		return fmt.Sprintf("Error querying meetings: %v", err), false
	}
	var m *graph.Node
	for _, cand := range meetings {
		if cand.QualifiedName == id || cand.ID == id {
			m = cand
			break
		}
	}
	if m == nil {
		return fmt.Sprintf("No meeting with ID %q. Use query_meetings to find one.", id), false
	}

	children, err := t.store.GetNeighbors(ctx, m.ID, graph.EdgeContains, graph.Outgoing)
	if err != nil {
		return fmt.Sprintf("Error reading meeting contents: %v", err), false
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", m.Name)
	fmt.Fprintf(&b, "Date: %s · Duration: %s\n\n",
		m.UpdatedAt.Format("2006-01-02 15:04"), formatDuration(m.Properties[graph.PropDuration]))
	if s := m.Properties[graph.PropSummary]; s != "" {
		fmt.Fprintf(&b, "%s\n\n", s)
	}

	var speakers, segments, decisions, actions []*graph.Node
	for _, c := range children {
		switch c.Type {
		case graph.NodeSpeaker:
			speakers = append(speakers, c)
		case graph.NodeTopicSegment:
			segments = append(segments, c)
		case graph.NodeDecision:
			decisions = append(decisions, c)
		case graph.NodeActionItem:
			actions = append(actions, c)
		}
	}

	if len(speakers) > 0 {
		b.WriteString("## Participants\n\n")
		sort.Slice(speakers, func(i, j int) bool {
			return secondsOf(speakers[i]) > secondsOf(speakers[j])
		})
		for _, sp := range speakers {
			name := "unidentified"
			if people, err := t.store.GetNeighbors(ctx, sp.ID, graph.EdgeIdentifiedAs, graph.Outgoing); err == nil && len(people) > 0 {
				name = people[0].Name
			}
			fmt.Fprintf(&b, "- %s (%s, spoke %s)\n", name, sp.Name, formatSecondsFloat(secondsOf(sp)))
		}
		b.WriteString("\n")
	}

	if len(segments) > 0 {
		sortByStart(segments)
		b.WriteString("## Topics\n\n")
		for _, s := range segments {
			fmt.Fprintf(&b, "### %s (%s–%s)\n\n%s\n\n", s.Name,
				formatSecondsFloat(floatProp(s, graph.PropStartTime)),
				formatSecondsFloat(floatProp(s, graph.PropEndTime)),
				s.Properties[graph.PropSummary])
		}
	}

	if len(decisions) > 0 {
		b.WriteString("## Decisions\n\n")
		for _, d := range decisions {
			fmt.Fprintf(&b, "- %s\n", d.Properties[graph.PropSummary])
			if r := d.Properties["rationale"]; r != "" {
				fmt.Fprintf(&b, "  - Rationale: %s\n", r)
			}
			if q := d.Properties[graph.PropQuote]; q != "" {
				verified := "verified against the transcript"
				if d.Properties["quote_verified"] != "true" {
					// Say so plainly: an unverified quote is the one thing here
					// that should not be repeated as fact.
					verified = "NOT found verbatim in the transcript — treat as unverified"
				}
				fmt.Fprintf(&b, "  - Quote (%s): %q\n", verified, q)
			}
		}
		b.WriteString("\n")
	}

	if len(actions) > 0 {
		b.WriteString("## Follow-ups\n\n")
		for _, a := range actions {
			who := a.Properties[graph.PropAssignee]
			if who == "" {
				who = "unassigned"
			}
			fmt.Fprintf(&b, "- %s (owner: %s", a.Properties[graph.PropSummary], who)
			if due := a.Properties[graph.PropDueDate]; due != "" {
				fmt.Fprintf(&b, ", due %s", due)
			}
			b.WriteString(")\n")
		}
		b.WriteString("\n")
	}

	if mentions := m.Properties["mentions"]; mentions != "" {
		fmt.Fprintf(&b, "Systems discussed: %s\n", mentions)
	}

	// A standing meeting is rarely interesting on its own. Pointing at the
	// previous instance lets an agent follow a thread backwards instead of
	// treating each recording as unrelated.
	if prev, err := t.store.GetNeighbors(ctx, m.ID, graph.EdgeFollowsUp, graph.Outgoing); err == nil && len(prev) > 0 {
		fmt.Fprintf(&b, "\nPrevious meeting with these people: %s on %s (ID: %s)\n",
			prev[0].Name, prev[0].UpdatedAt.Format("2006-01-02"), prev[0].QualifiedName)
	}
	if next, err := t.store.GetNeighbors(ctx, m.ID, graph.EdgeFollowsUp, graph.Incoming); err == nil && len(next) > 0 {
		fmt.Fprintf(&b, "Next meeting with these people: %s on %s (ID: %s)\n",
			next[0].Name, next[0].UpdatedAt.Format("2006-01-02"), next[0].QualifiedName)
	}
	return b.String(), true
}

// --- query_person_activity ---

type queryPersonActivityTool struct {
	store graph.Store
}

func (t *queryPersonActivityTool) Name() string { return "query_person_activity" }

func (t *queryPersonActivityTool) Description() string {
	return "Summarize what a person has been involved in: the meetings they attended, the topics they spoke on, the decisions they made, and the follow-ups they own. Use this to understand someone's area of ownership before assigning work or asking who to consult."
}

func (t *queryPersonActivityTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "The person's name. Alternate spellings recorded for them also match.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum meetings to list (default 15).",
			},
		},
		"required": []string{"name"},
	}
}

func (t *queryPersonActivityTool) Execute(ctx context.Context, args map[string]any) (string, bool) {
	name, _ := args["name"].(string)
	if name == "" {
		return "Error: name is required", false
	}
	limit := intArg(args, "limit", 15)

	people, err := t.store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodePerson})
	if err != nil {
		return fmt.Sprintf("Error querying people: %v", err), false
	}

	var person *graph.Node
	for _, p := range people {
		if strings.EqualFold(p.Name, name) {
			person = p
			break
		}
		for _, alias := range strings.Split(p.Properties[graph.PropAliases], ",") {
			if strings.EqualFold(strings.TrimSpace(alias), name) {
				person = p
				break
			}
		}
		if person != nil {
			break
		}
	}
	if person == nil {
		var known []string
		for _, p := range people {
			known = append(known, p.Name)
		}
		sort.Strings(known)
		if len(known) > 30 {
			known = known[:30]
		}
		return fmt.Sprintf("No person named %q. Known people: %s", name, strings.Join(known, ", ")), false
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", person.Name)
	if aliases := person.Properties[graph.PropAliases]; aliases != "" {
		fmt.Fprintf(&b, "Also transcribed as: %s\n", aliases)
	}
	if person.Properties[graph.PropIsOwner] == "true" {
		b.WriteString("This is the person whose recordings these are.\n")
	}
	b.WriteString("\n")

	meetings, err := t.store.GetNeighbors(ctx, person.ID, graph.EdgeAttended, graph.Outgoing)
	if err != nil {
		return fmt.Sprintf("Error reading attendance: %v", err), false
	}
	sort.Slice(meetings, func(i, j int) bool { return meetings[i].UpdatedAt.After(meetings[j].UpdatedAt) })

	fmt.Fprintf(&b, "## Meetings attended (%d)\n\n", len(meetings))
	shown := meetings
	if len(shown) > limit {
		shown = shown[:limit]
	}
	for _, m := range shown {
		fmt.Fprintf(&b, "- %s — %s (ID: %s)\n", m.UpdatedAt.Format("2006-01-02"), m.Name, m.QualifiedName)
	}
	if len(meetings) > len(shown) {
		fmt.Fprintf(&b, "- ... and %d more\n", len(meetings)-len(shown))
	}
	b.WriteString("\n")

	if owned := t.incoming(ctx, person.ID, graph.EdgeAssignedTo); len(owned) > 0 {
		fmt.Fprintf(&b, "## Follow-ups owned (%d)\n\n", len(owned))
		for _, a := range owned {
			fmt.Fprintf(&b, "- %s", a.Properties[graph.PropSummary])
			if due := a.Properties[graph.PropDueDate]; due != "" {
				fmt.Fprintf(&b, " (due %s)", due)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if raised := t.incoming(ctx, person.ID, graph.EdgeRaisedBy); len(raised) > 0 {
		var decisions []*graph.Node
		for _, n := range raised {
			if n.Type == graph.NodeDecision {
				decisions = append(decisions, n)
			}
		}
		if len(decisions) > 0 {
			fmt.Fprintf(&b, "## Decisions made (%d)\n\n", len(decisions))
			for _, d := range decisions {
				fmt.Fprintf(&b, "- %s\n", d.Properties[graph.PropSummary])
			}
			b.WriteString("\n")
		}
	}

	if len(meetings) == 0 {
		b.WriteString("No meeting activity recorded. This person may be known only from images.\n")
	}
	return b.String(), true
}

// incoming returns nodes pointing at id along an edge type.
func (t *queryPersonActivityTool) incoming(ctx context.Context, id string, edge graph.EdgeType) []*graph.Node {
	nodes, err := t.store.GetNeighbors(ctx, id, edge, graph.Incoming)
	if err != nil {
		return nil
	}
	return nodes
}

// --- query_action_items ---

type queryActionItemsTool struct {
	store graph.Store
}

func (t *queryActionItemsTool) Name() string { return "query_action_items" }

func (t *queryActionItemsTool) Description() string {
	return "List follow-ups and TODOs captured from meetings, optionally filtered by owner or by whether anyone owns them. Use this to find outstanding commitments."
}

func (t *queryActionItemsTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"person": map[string]any{
				"type":        "string",
				"description": "Only follow-ups owned by this person.",
			},
			"unassigned": map[string]any{
				"type":        "boolean",
				"description": "Only follow-ups with no owner.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum follow-ups to return (default 25).",
			},
		},
	}
}

func (t *queryActionItemsTool) Execute(ctx context.Context, args map[string]any) (string, bool) {
	person, _ := args["person"].(string)
	unassigned, _ := args["unassigned"].(bool)
	limit := intArg(args, "limit", 25)

	actions, err := t.store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeActionItem})
	if err != nil {
		return fmt.Sprintf("Error querying action items: %v", err), false
	}

	type row struct {
		node  *graph.Node
		owner string
	}
	var rows []row
	for _, a := range actions {
		owner := a.Properties[graph.PropAssignee]
		if people, err := t.store.GetNeighbors(ctx, a.ID, graph.EdgeAssignedTo, graph.Outgoing); err == nil && len(people) > 0 {
			owner = people[0].Name
		}
		if person != "" && !strings.EqualFold(owner, person) {
			continue
		}
		if unassigned && owner != "" {
			continue
		}
		rows = append(rows, row{node: a, owner: owner})
	}
	if len(rows) == 0 {
		return "No follow-ups matched.", false
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].node.UpdatedAt.After(rows[j].node.UpdatedAt)
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d follow-up(s):\n\n", len(rows))
	for _, r := range rows {
		owner := r.owner
		if owner == "" {
			owner = "unassigned"
		}
		fmt.Fprintf(&b, "- %s\n  - Owner: %s", r.node.Properties[graph.PropSummary], owner)
		if due := r.node.Properties[graph.PropDueDate]; due != "" {
			fmt.Fprintf(&b, ", due %s", due)
		}
		fmt.Fprintf(&b, "\n  - From meeting on %s (ID: %s)\n",
			r.node.UpdatedAt.Format("2006-01-02"), r.node.Properties[graph.PropMeetingID])
	}
	return b.String(), true
}

// --- shared helpers ---

// NewMeetingTools returns the meeting query tools, or nil when no meetings are
// indexed. Offering a tool that can only answer "nothing is indexed" wastes an
// agent's turns, so they are registered only when there is something to query.
func NewMeetingTools(ctx context.Context, store graph.Store) []Tool {
	if store == nil {
		return nil
	}
	meetings, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeMeeting})
	if err != nil || len(meetings) == 0 {
		return nil
	}
	return []Tool{
		&queryMeetingsTool{store: store},
		&queryMeetingDetailTool{store: store},
		&queryPersonActivityTool{store: store},
		&queryActionItemsTool{store: store},
	}
}

func intArg(args map[string]any, key string, def int) int {
	switch v := args[key].(type) {
	case float64:
		if v > 0 {
			return int(v)
		}
	case int:
		if v > 0 {
			return v
		}
	case string:
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if strings.EqualFold(n, want) || strings.Contains(strings.ToLower(n), strings.ToLower(want)) {
			return true
		}
	}
	return false
}

func floatProp(n *graph.Node, key string) float64 {
	v, _ := strconv.ParseFloat(n.Properties[key], 64)
	return v
}

func secondsOf(n *graph.Node) float64 {
	return floatProp(n, graph.PropSpeakingSeconds)
}

func sortByStart(nodes []*graph.Node) {
	sort.Slice(nodes, func(i, j int) bool {
		return floatProp(nodes[i], graph.PropStartTime) < floatProp(nodes[j], graph.PropStartTime)
	})
}

func formatDuration(v string) string {
	secs, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return "unknown"
	}
	return formatSecondsFloat(secs)
}

func formatSecondsFloat(secs float64) string {
	if secs < 0 {
		secs = 0
	}
	total := int(secs)
	h, m, s := total/3600, (total%3600)/60, total%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}
