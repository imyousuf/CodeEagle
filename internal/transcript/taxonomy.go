package transcript

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/pkg/llm"
)

// A meeting names its subject in whatever words suited that conversation, so
// the topics a corpus produces are specific and numerous: "MCP server vs OAuth
// server architecture", "OBO / user-level token concern", "Token lifetimes",
// "Refresh token revocation". Each accurately describes what was discussed,
// and each appears in one meeting.
//
// Collapsing them into one another is the obvious fix and the wrong one. Merge
// the first two and two genuinely different discussions are silently fused;
// leave them apart and neither is findable. The dilemma exists only because a
// flat vocabulary asks one label to be both the precise description and the
// searchable subject.
//
// A hierarchy removes it. The specific phrases stay exactly as they are, and
// are placed under the concept they are facets of — OAuth — which is itself
// placed under a broader one — Authentication. Nothing is lost, and a subject
// becomes findable by walking up from any of its facets.
//
// The tree is built by repeatedly applying one operation: group what is
// currently ungrouped, then group the groups. Each level is induced from the
// level below, so the taxonomy describes this body of work rather than a
// generic ontology, and it stops when the top is small enough to read.

const (
	// PropTopicLevel names a topic's layer: a subject a meeting produced, or a
	// concept induced over others.
	PropTopicLevel = "topic_level"
	// LevelSubject is a topic as a meeting phrased it.
	LevelSubject = "subject"
	// LevelTheme is an induced grouping.
	LevelTheme = "theme"

	// PropTopicDepth is how far above the leaves a topic sits: 0 for a subject
	// a meeting named, 1 for a concept grouping subjects, and so on.
	PropTopicDepth = "topic_depth"
	// PropTopicReach is how many leaf subjects sit beneath a topic, which is
	// what makes a theme's weight comparable to a subject's usage count.
	PropTopicReach = "topic_reach"
)

const (
	// maxTaxonomyDepth bounds how tall the tree gets. Three levels — subject,
	// concept, area — is as much structure as a corpus of meetings supports;
	// beyond that the top becomes abstractions nobody searches by.
	maxTaxonomyDepth = 3
	// minTopLevel is the number of roots below which grouping stops. A handful
	// of top-level areas is already browsable, and grouping them further only
	// produces headings like "Engineering" that carry no information.
	minTopLevel = 8
	// minThemeMembers is the smallest group worth creating. A group of one is
	// a rename, not a grouping.
	minThemeMembers = 2
)

// Theme is a grouping a model proposed over the labels it was given.
type Theme struct {
	Name string `json:"name"`
	// Description says what belongs under it, which is what makes the grouping
	// checkable rather than a bare label.
	Description string `json:"description"`
	// Members are labels copied exactly from the supplied list.
	Members []string `json:"members"`
}

// Taxonomy is one level of grouping.
type Taxonomy struct {
	Themes []Theme `json:"themes"`
}

// TaxonomyStats reports what building the tree produced.
type TaxonomyStats struct {
	Subjects int
	// Levels counts how many rounds of grouping were applied.
	Levels int
	// ThemesByDepth counts the concepts created at each level.
	ThemesByDepth map[int]int
	Themes        int
	Edges         int
	// Roots is how many topics remain at the top once grouping stops.
	Roots int
	Usage Usage
}

// maxItemsPerPass bounds how many labels go in one request. The default model
// holds a million tokens, so a corpus of this size fits in a single pass and
// the grouping stays globally consistent; splitting it would have each batch
// invent its own concepts for the same material.
const maxItemsPerPass = 2500

const taxonomySystemPrompt = `You organize a list of topics into the concepts they
are facets of.

The topics come from real meetings, so several different labels usually
describe parts of one larger subject. Your job is to name those subjects.

- A concept names ONE thing, not two joined by "and". "Authentication", not
  "Authentication and token flows". If you are tempted to join two words with
  "and", they are two concepts and belong as separate groups.
- Two to four words, a noun phrase. Prefer the term this field actually uses:
  "OAuth", "MCP", "Tenancy", "Rate limiting".
- Every member you list must be copied from the supplied list exactly,
  character for character. Do not reword, shorten, or invent labels.
- Group by what the work is about, not by the kind of meeting it was.
  "Standup" and "Planning" describe formats; "Billing" and "Retrieval" describe
  subjects. Prefer the latter.
- A label that fits nowhere is better left out than forced into a group it does
  not belong to. Leaving it unplaced is a normal outcome.
- Never place one label under two concepts. Pick the best fit.
- A concept needs at least two members. One member is a rename, not a grouping.`

// levelGuidance tells the model how abstract this round should be, since the
// same operation is applied to raw subjects and then to the concepts it just
// produced.
func levelGuidance(depth, count int) string {
	switch depth {
	case 1:
		return fmt.Sprintf(`These are topics as meetings phrased them. Name the concept each
group is about — the term someone would search for. Aim for roughly %d groups.`,
			targetGroups(count))
	default:
		return fmt.Sprintf(`These are concepts produced by an earlier round of grouping, so they
are already abstract. Group them into broader areas only where a genuine parent
exists — "OAuth" and "SSO" both sit under "Authentication". Do not invent a
parent just to have one: leaving a concept at the top is correct when it has no
natural home. Aim for roughly %d groups.`, targetGroups(count))
	}
}

// targetGroups suggests how many groups a level should produce. Roughly the
// square root keeps each group small enough to read while still reducing the
// list meaningfully.
func targetGroups(count int) int {
	n := 1
	for n*n < count {
		n++
	}
	if n < 3 {
		n = 3
	}
	if n > 20 {
		n = 20
	}
	return n
}

// DistillLevel groups one level of topics.
//
// It reads topic labels rather than transcripts, so it costs one request per
// level regardless of how many meetings were indexed.
func (a *Analyzer) DistillLevel(ctx context.Context, items []TopicUsage, depth int) (*Taxonomy, Usage, error) {
	var usage Usage
	if len(items) == 0 {
		return &Taxonomy{}, usage, nil
	}
	if len(items) > maxItemsPerPass {
		// Keep the weightiest labels: a long tail used once each adds length
		// without adding structure.
		sort.Slice(items, func(i, j int) bool { return items[i].Weight > items[j].Weight })
		items = items[:maxItemsPerPass]
	}

	var b strings.Builder
	b.WriteString(levelGuidance(depth, len(items)))
	fmt.Fprintf(&b, "\n\nTOPICS (%d), with how many meetings each covers:\n\n", len(items))
	for _, it := range items {
		fmt.Fprintf(&b, "- %s (%d)\n", it.Name, it.Weight)
	}
	b.WriteString("\nCopy each member label exactly as written above.\n")

	var out Taxonomy
	if err := a.chatJSON(ctx, taxonomySystemPrompt, b.String(), taxonomySchema(), &out, &usage); err != nil {
		return nil, usage, err
	}
	return &out, usage, nil
}

func taxonomySchema() *llm.JSONSchema {
	return &llm.JSONSchema{
		Name:   "topic_taxonomy",
		Strict: true,
		Schema: object(map[string]any{
			"themes": map[string]any{
				"type": "array",
				"items": object(map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "a two-to-four word noun phrase naming ONE concept, never two joined by 'and'",
					},
					"description": map[string]any{
						"type":        "string",
						"description": "one sentence saying what belongs under this concept; must not merely repeat the name",
					},
					"members": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "labels copied exactly from the supplied list, at least two",
					},
				}, "name", "description", "members"),
			},
		}, "themes"),
	}
}

// TopicUsage is a topic and how much of the corpus sits beneath it.
type TopicUsage struct {
	Node *graph.Node
	Name string
	// Weight is meetings for a subject, and leaves beneath it for a concept,
	// so the two are comparable when a level mixes them.
	Weight int
}

// TopicRoots returns the topics not yet grouped under anything — the ones the
// next round of grouping operates on.
//
// A subject left unplaced by an earlier round stays a root, so it is offered
// again alongside the concepts and eventually finds a home.
func TopicRoots(ctx context.Context, store graph.Store) ([]TopicUsage, error) {
	nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeTopic})
	if err != nil {
		return nil, fmt.Errorf("query topics: %w", err)
	}

	out := make([]TopicUsage, 0, len(nodes))
	for _, n := range nodes {
		parents, err := store.GetNeighbors(ctx, n.ID, graph.EdgeContains, graph.Incoming)
		if err != nil {
			continue
		}
		grouped := false
		for _, p := range parents {
			if p.Type == graph.NodeTopic {
				grouped = true
				break
			}
		}
		if grouped {
			continue
		}
		out = append(out, TopicUsage{Node: n, Name: n.Name, Weight: topicWeight(n)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Weight != out[j].Weight {
			return out[i].Weight > out[j].Weight
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// topicWeight is how much of the corpus a topic accounts for.
func topicWeight(n *graph.Node) int {
	if reach, err := strconv.Atoi(n.Properties[PropTopicReach]); err == nil && reach > 0 {
		return reach
	}
	uses, _ := strconv.Atoi(n.Properties[propTopicUses])
	if uses < 1 {
		uses = 1
	}
	return uses
}

// BuildTaxonomy groups topics, then groups the groups, until the top is small
// enough to browse or no further grouping is possible.
func (a *Analyzer) BuildTaxonomy(ctx context.Context, store graph.Store, log func(string, ...any)) (TaxonomyStats, error) {
	if log == nil {
		log = func(string, ...any) {}
	}
	st := TaxonomyStats{ThemesByDepth: map[int]int{}}

	subjects, err := TopicRoots(ctx, store)
	if err != nil {
		return st, err
	}
	st.Subjects = len(subjects)

	for depth := 1; depth <= maxTaxonomyDepth; depth++ {
		roots, err := TopicRoots(ctx, store)
		if err != nil {
			return st, err
		}
		st.Roots = len(roots)
		if len(roots) <= minTopLevel {
			log("  Level %d: %d topics at the top already — small enough to browse, stopping.", depth, len(roots))
			break
		}

		log("  Level %d: grouping %d topics...", depth, len(roots))
		tax, usage, err := a.DistillLevel(ctx, roots, depth)
		if err != nil {
			return st, fmt.Errorf("group level %d: %w", depth, err)
		}
		st.Usage.InputTokens += usage.InputTokens
		st.Usage.OutputTokens += usage.OutputTokens
		st.Usage.Requests += usage.Requests

		created, edges, err := applyLevel(ctx, store, roots, tax, depth)
		if err != nil {
			return st, err
		}
		if created == 0 {
			// Nothing grouped: another round would produce the same answer.
			log("  Level %d: no groups formed, stopping.", depth)
			break
		}
		st.ThemesByDepth[depth] = created
		st.Themes += created
		st.Edges += edges
		st.Levels = depth
		log("  Level %d: created %d concepts over %d topics.", depth, created, edges)
	}

	roots, err := TopicRoots(ctx, store)
	if err == nil {
		st.Roots = len(roots)
	}
	return st, nil
}

// applyLevel writes one level of grouping into the graph.
func applyLevel(ctx context.Context, store graph.Store, items []TopicUsage, tax *Taxonomy, depth int) (created, edges int, err error) {
	// Index the real labels, so a group naming something that was not in the
	// list — which a model occasionally does — is dropped rather than creating
	// a concept over a topic that does not exist.
	byName := make(map[string]*graph.Node, len(items))
	for _, it := range items {
		byName[NormalizeName(it.Name)] = it.Node
	}

	claimed := make(map[string]bool)
	for _, theme := range tax.Themes {
		name := CleanTopic(theme.Name)
		if name == "" {
			continue
		}

		members := make([]*graph.Node, 0, len(theme.Members))
		for _, raw := range theme.Members {
			node, ok := byName[NormalizeName(raw)]
			if !ok {
				continue
			}
			// One topic gets one parent; a second claim is ignored rather than
			// turning the tree into a graph.
			if claimed[node.ID] {
				continue
			}
			// A concept must not be made its own parent.
			if NormalizeName(node.Name) == NormalizeName(name) {
				continue
			}
			members = append(members, node)
		}
		if len(members) < minThemeMembers {
			continue
		}

		reach := 0
		for _, m := range members {
			reach += topicWeight(m)
		}

		description := strings.TrimSpace(theme.Description)
		if NormalizeName(description) == NormalizeName(name) {
			// A description that only repeats the name tells a reader nothing.
			description = ""
		}

		themeNode := &graph.Node{
			ID:            graph.NewNodeID(string(graph.NodeTopic), "", name),
			Type:          graph.NodeTopic,
			Name:          name,
			QualifiedName: name,
			DocComment:    description,
			Properties: map[string]string{
				PropTopicLevel: LevelTheme,
				PropTopicDepth: strconv.Itoa(depth),
				PropTopicReach: strconv.Itoa(reach),
				"member_count": strconv.Itoa(len(members)),
			},
		}
		if description != "" {
			themeNode.Properties[graph.PropSummary] = description
		}
		if err := store.AddNode(ctx, themeNode); err != nil {
			return created, edges, fmt.Errorf("add concept %q: %w", name, err)
		}
		created++

		for _, m := range members {
			claimed[m.ID] = true
			edge := &graph.Edge{
				ID:       graph.NewNodeID("edge", themeNode.ID, m.ID+":"+string(graph.EdgeContains)),
				Type:     graph.EdgeContains,
				SourceID: themeNode.ID,
				TargetID: m.ID,
			}
			if err := store.AddEdge(ctx, edge); err != nil {
				return created, edges, fmt.Errorf("link concept %q: %w", name, err)
			}
			edges++

			if m.Properties == nil {
				m.Properties = make(map[string]string)
			}
			if m.Properties[PropTopicLevel] == "" {
				m.Properties[PropTopicLevel] = LevelSubject
			}
			if m.Properties[PropTopicDepth] == "" {
				m.Properties[PropTopicDepth] = "0"
			}
			if err := store.UpdateNode(ctx, m); err != nil {
				return created, edges, fmt.Errorf("mark topic %q: %w", m.Name, err)
			}
		}
	}
	return created, edges, nil
}

// RenderTaxonomy draws the current hierarchy as indented text, for showing a
// model the structure it is adding to.
//
// A model given a flat list of prior topics can only match against them; given
// the tree, it can also say where something new belongs. That is the
// difference between a vocabulary that stops growing and a structure that
// keeps its shape as the corpus grows.
func RenderTaxonomy(ctx context.Context, store graph.Store, maxLines int) (string, error) {
	roots, err := TopicRoots(ctx, store)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	lines := 0
	var walk func(n *graph.Node, depth int)
	walk = func(n *graph.Node, depth int) {
		if lines >= maxLines || depth > maxTaxonomyDepth {
			return
		}
		fmt.Fprintf(&b, "%s%s\n", strings.Repeat("  ", depth), n.Name)
		lines++

		children, err := TopicChildren(ctx, store, n.ID)
		if err != nil {
			return
		}
		for _, c := range children {
			walk(c, depth+1)
		}
	}
	for _, r := range roots {
		// Only the grouped part of the tree is worth showing: an ungrouped
		// subject is already offered through the flat vocabulary.
		if TopicDepth(r.Node) == 0 {
			continue
		}
		walk(r.Node, 0)
	}
	return b.String(), nil
}

// TopicMeetingCount returns how many distinct meetings a topic covers,
// counting the subjects beneath it as well as the topic itself.
//
// This is derived from the graph rather than from a counter kept while
// indexing. A counter records how many times a label was looked up, which is
// not the same thing: a single meeting resolves the same topic once for the
// segment and again for each decision and action item under it, so the counter
// drifts above the number of meetings and makes a parent look smaller than its
// children.
func TopicMeetingCount(ctx context.Context, store graph.Store, n *graph.Node) int {
	seen := make(map[string]bool)
	visited := make(map[string]bool)

	var walk func(node *graph.Node, depth int)
	walk = func(node *graph.Node, depth int) {
		if node == nil || depth > maxTaxonomyDepth+1 || visited[node.ID] {
			return
		}
		visited[node.ID] = true

		if edges, err := store.GetEdges(ctx, node.ID, graph.EdgeHasTopic); err == nil {
			for _, e := range edges {
				if e.TargetID != node.ID {
					continue
				}
				src, err := store.GetNode(ctx, e.SourceID)
				if err != nil || src == nil {
					continue
				}
				// Key on the session id in both cases. A meeting is reachable
				// both directly and through the segments and decisions inside
				// it, and keying those on different identifiers would count one
				// meeting twice.
				if mid := src.Properties[graph.PropMeetingID]; mid != "" {
					seen[mid] = true
				} else if src.Type == graph.NodeMeeting {
					seen[src.QualifiedName] = true
				}
			}
		}

		children, err := TopicChildren(ctx, store, node.ID)
		if err != nil {
			return
		}
		for _, c := range children {
			walk(c, depth+1)
		}
	}
	walk(n, 0)
	return len(seen)
}

// TopicChildren returns the topics directly beneath one, heaviest first.
func TopicChildren(ctx context.Context, store graph.Store, id string) ([]*graph.Node, error) {
	all, err := store.GetNeighbors(ctx, id, graph.EdgeContains, graph.Outgoing)
	if err != nil {
		return nil, err
	}
	out := make([]*graph.Node, 0, len(all))
	for _, n := range all {
		if n.Type == graph.NodeTopic {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		wi, wj := topicWeight(out[i]), topicWeight(out[j])
		if wi != wj {
			return wi > wj
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// TopicDepth reports how far above the leaves a topic sits.
func TopicDepth(n *graph.Node) int {
	d, _ := strconv.Atoi(n.Properties[PropTopicDepth])
	return d
}

// TopicReach reports how many meeting-level subjects sit beneath a topic.
func TopicReach(n *graph.Node) int { return topicWeight(n) }
