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
	// maxTaxonomyDepth is the ceiling on how tall the tree may be built.
	maxTaxonomyDepth = 4
	// DefaultTaxonomyDepth is how many rounds of grouping are applied unless
	// the caller asks for more.
	//
	// Two is the default because a third round measurably made things worse on
	// a real corpus. Grouping 3,637 topics produced 50 concepts and then 35 —
	// "Model routing", "Agent memory", "Tenant isolation", the terms people
	// actually search by. Forcing a further pass over those 35 to reach a
	// handful of top-level areas fused unrelated work: identity management
	// landed under product development while deployment infrastructure landed
	// under security. A model asked to squeeze a good taxonomy into too few
	// headings will do it, and the result reads plausibly and misleads.
	DefaultTaxonomyDepth = 2
	// minTopLevel is the number of roots below which grouping stops early,
	// because a list this short is already browsable.
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

- A concept names ONE thing. Never join two subjects with "and", "&", "/" or a
  comma: "Authentication", not "Authentication & token flows", not "GEO/AEO and
  marketing". If you want to join two words, they are two concepts and belong as
  two separate groups.
- Never create a catch-all. "Unplaced", "Other", "Miscellaneous" and "General"
  are not concepts. A label with no home is left out of your answer entirely —
  that is what "left out" means, and it is a normal outcome.
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
//
// The ceiling has to be generous. A model asked for twenty groups over several
// thousand topics fills those twenty and abandons the rest, so the level
// absorbs a fraction of its input and the tree comes out lopsided — which is
// exactly what happened at 20 over 3,630 topics. Letting the count follow the
// square root lets one level cover its whole input, and the next level reduces
// those groups in turn.
func targetGroups(count int) int {
	n := 1
	for n*n < count {
		n++
	}
	if n < 3 {
		n = 3
	}
	if n > 80 {
		n = 80
	}
	return n
}

const (
	// singlePassLimit is the largest list grouped in one request.
	//
	// The reply has to name every label it places, so output grows with input:
	// a few thousand labels in one call exhausts the token budget before the
	// answer is finished, and a model asked for more groups than it can fill
	// abandons the remainder. Past this size the work is split.
	singlePassLimit = 400

	// proposeSampleSize is how many labels are shown when proposing concepts. A
	// sample is enough to see the shape of a corpus, and keeping it small keeps
	// the proposal cheap.
	proposeSampleSize = 700

	// assignBatchSize is how many labels are placed per request: small enough
	// that the reply always completes, large enough that a corpus of thousands
	// takes a manageable number of calls.
	assignBatchSize = 150
)

// DistillLevel groups one level of topics.
//
// Small lists are grouped in a single request. Larger ones are split in two
// stages — propose the concepts from a sample, then place every label against
// that fixed list in batches. Two reasons: the reply must name each label it
// places, so one call's output grows with the corpus and eventually truncates
// mid-answer; and a single call fills the groups it proposed and abandons the
// remainder, while batched assignment is asked about every label.
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

	if len(items) <= singlePassLimit {
		return a.distillSinglePass(ctx, items, depth, &usage)
	}
	return a.distillBatched(ctx, items, depth, &usage)
}

// distillSinglePass groups a short list in one request.
func (a *Analyzer) distillSinglePass(ctx context.Context, items []TopicUsage, depth int, usage *Usage) (*Taxonomy, Usage, error) {
	var b strings.Builder
	b.WriteString(levelGuidance(depth, len(items)))
	fmt.Fprintf(&b, "\n\nTOPICS (%d), with how many meetings each covers:\n\n", len(items))
	for _, it := range items {
		fmt.Fprintf(&b, "- %s (%d)\n", it.Name, it.Weight)
	}
	b.WriteString("\nCopy each member label exactly as written above.\n")

	var out Taxonomy
	if err := a.chatJSON(ctx, taxonomySystemPrompt, b.String(), taxonomySchema(), &out, usage); err != nil {
		return nil, *usage, err
	}
	return &out, *usage, nil
}

// distillBatched proposes the concepts once, then places every label against
// that fixed list.
func (a *Analyzer) distillBatched(ctx context.Context, items []TopicUsage, depth int, usage *Usage) (*Taxonomy, Usage, error) {
	concepts, err := a.proposeConcepts(ctx, items, depth, usage)
	if err != nil {
		return nil, *usage, fmt.Errorf("propose concepts: %w", err)
	}
	if len(concepts.Concepts) == 0 {
		return &Taxonomy{}, *usage, nil
	}

	// Index the concepts so an assignment naming something that was never
	// proposed is dropped rather than creating a concept nobody described.
	byName := make(map[string]*ProposedConcept, len(concepts.Concepts))
	members := make(map[string][]string, len(concepts.Concepts))
	for i := range concepts.Concepts {
		c := &concepts.Concepts[i]
		byName[NormalizeName(c.Name)] = c
	}

	for start := 0; start < len(items); start += assignBatchSize {
		end := min(start+assignBatchSize, len(items))
		batch := items[start:end]

		placed, err := a.assignConcepts(ctx, batch, concepts, usage)
		if err != nil {
			// A failed batch costs its own labels, not the level: they stay
			// ungrouped and a later run can place them.
			continue
		}
		for _, p := range placed.Assignments {
			c, ok := byName[NormalizeName(p.Concept)]
			if !ok {
				continue
			}
			key := NormalizeName(c.Name)
			members[key] = append(members[key], p.Topic)
		}
	}

	out := &Taxonomy{}
	for _, c := range concepts.Concepts {
		out.Themes = append(out.Themes, Theme{
			Name:        c.Name,
			Description: c.Description,
			Members:     members[NormalizeName(c.Name)],
		})
	}
	return out, *usage, nil
}

// ProposedConcept is a concept name suggested before any label is placed.
type ProposedConcept struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ConceptProposal is the fixed set of concepts a level will use.
type ConceptProposal struct {
	Concepts []ProposedConcept `json:"concepts"`
}

const proposeSystemPrompt = `You name the concepts that a body of meeting topics is about.

You are shown a sample of the topic labels meetings produced. Propose the set of
concepts these topics are facets of. You are not placing anything yet — only
naming the groups.

- A concept names ONE thing. Never join two subjects with "and", "&", "/" or a
  comma: "Authentication", not "Authentication & token flows".
- Two to four words, a noun phrase. Prefer the term this field actually uses:
  "OAuth", "MCP", "Tenancy", "Rate limiting".
- Cover the range of what you were shown. The sample stands for a longer list,
  so a topic that would fit none of your concepts is a gap.
- Never propose a catch-all. "Unplaced", "Other", "Miscellaneous" and "General"
  are not concepts.
- Concepts must not overlap: a topic should have one obvious home among them.`

// proposeConcepts asks for the concept list a level will use.
func (a *Analyzer) proposeConcepts(ctx context.Context, items []TopicUsage, depth int, usage *Usage) (*ConceptProposal, error) {
	sample := items
	if len(sample) > proposeSampleSize {
		// The list arrives heaviest-first, so an evenly spaced sample covers
		// the long tail as well as the common subjects.
		step := len(sample) / proposeSampleSize
		if step < 1 {
			step = 1
		}
		var spread []TopicUsage
		for i := 0; i < len(sample); i += step {
			spread = append(spread, sample[i])
		}
		sample = spread
	}

	var b strings.Builder
	b.WriteString(levelGuidance(depth, len(items)))
	fmt.Fprintf(&b, "\n\nThere are %d topics in total. Here is a sample of %d:\n\n", len(items), len(sample))
	for _, it := range sample {
		fmt.Fprintf(&b, "- %s\n", it.Name)
	}
	fmt.Fprintf(&b, "\nPropose roughly %d concepts covering this material.\n", targetGroups(len(items)))

	schema := &llm.JSONSchema{
		Name:   "concept_proposal",
		Strict: true,
		Schema: object(map[string]any{
			"concepts": map[string]any{
				"type": "array",
				"items": object(map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "a two-to-four word noun phrase naming ONE concept",
					},
					"description": map[string]any{
						"type":        "string",
						"description": "one sentence saying what belongs under it",
					},
				}, "name", "description"),
			},
		}, "concepts"),
	}

	var out ConceptProposal
	if err := a.chatJSON(ctx, proposeSystemPrompt, b.String(), schema, &out, usage); err != nil {
		return nil, err
	}
	return &out, nil
}

// Assignment places one topic under one concept.
type Assignment struct {
	Topic   string `json:"topic"`
	Concept string `json:"concept"`
}

// AssignmentResult is one batch of placements.
type AssignmentResult struct {
	Assignments []Assignment `json:"assignments"`
}

const assignSystemPrompt = `You place each topic under the concept it belongs to.

You are given a fixed list of concepts and a list of topics. For every topic,
name the one concept it best belongs under.

- Use only the concepts given. Do not invent new ones.
- Copy each topic label exactly as written, character for character.
- A topic that fits none of the concepts is left out of your answer. Do not
  force it, and do not invent a catch-all for it.
- One concept per topic. Pick the best fit.`

// assignConcepts places one batch of labels against the proposed concepts.
func (a *Analyzer) assignConcepts(ctx context.Context, batch []TopicUsage, concepts *ConceptProposal, usage *Usage) (*AssignmentResult, error) {
	var b strings.Builder
	b.WriteString("CONCEPTS:\n")
	for _, c := range concepts.Concepts {
		if c.Description != "" {
			fmt.Fprintf(&b, "- %s — %s\n", c.Name, c.Description)
			continue
		}
		fmt.Fprintf(&b, "- %s\n", c.Name)
	}
	fmt.Fprintf(&b, "\nTOPICS (%d):\n", len(batch))
	for _, it := range batch {
		fmt.Fprintf(&b, "- %s\n", it.Name)
	}
	b.WriteString("\nPlace every topic you can under one of the concepts above.\n")

	schema := &llm.JSONSchema{
		Name:   "concept_assignments",
		Strict: true,
		Schema: object(map[string]any{
			"assignments": map[string]any{
				"type": "array",
				"items": object(map[string]any{
					"topic": map[string]any{
						"type":        "string",
						"description": "the topic label, copied exactly",
					},
					"concept": map[string]any{
						"type":        "string",
						"description": "the concept it belongs under, from the list given",
					},
				}, "topic", "concept"),
			},
		}, "assignments"),
	}

	var out AssignmentResult
	if err := a.chatJSON(ctx, assignSystemPrompt, b.String(), schema, &out, usage); err != nil {
		return nil, err
	}
	return &out, nil
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
func (a *Analyzer) BuildTaxonomy(ctx context.Context, store graph.Store, maxDepth int, log func(string, ...any)) (TaxonomyStats, error) {
	if log == nil {
		log = func(string, ...any) {}
	}
	if maxDepth <= 0 {
		maxDepth = DefaultTaxonomyDepth
	}
	if maxDepth > maxTaxonomyDepth {
		maxDepth = maxTaxonomyDepth
	}
	st := TaxonomyStats{ThemesByDepth: map[int]int{}}

	subjects, err := TopicRoots(ctx, store)
	if err != nil {
		return st, err
	}
	st.Subjects = len(subjects)

	for depth := 1; depth <= maxDepth; depth++ {
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
		// A catch-all is worse than leaving topics ungrouped: it looks like
		// structure while telling a reader nothing, and it absorbs exactly the
		// topics that most needed a real home.
		if name == "" || isCatchAll(name) {
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

// catchAllNames are labels that pretend to be concepts. A model reaches for
// them when asked to place everything, which is why the prompt says not to and
// this check enforces it.
var catchAllNames = map[string]bool{
	"unplaced": true, "unsorted": true, "uncategorized": true, "unassigned": true,
	"other": true, "others": true, "miscellaneous": true, "misc": true,
	"general": true, "various": true, "assorted": true, "ungrouped": true,
	"unknown": true, "everything else": true, "remaining": true, "leftover": true,
	"topics": true, "subjects": true, "concepts": true,
}

// isCatchAll reports whether a proposed concept is really a bucket for
// everything the model could not place.
func isCatchAll(name string) bool {
	return catchAllNames[NormalizeName(name)]
}

// ClearTaxonomy removes every induced concept and the links beneath it,
// leaving the subjects meetings produced untouched.
//
// Rebuilding rather than extending is sometimes the right call: the grouping
// reflects the corpus it was induced from, and after a corpus doubles the old
// concepts can carve it up worse than a fresh pass would.
func ClearTaxonomy(ctx context.Context, store graph.Store) (int, error) {
	topics, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeTopic})
	if err != nil {
		return 0, fmt.Errorf("query topics: %w", err)
	}

	removed := 0
	for _, t := range topics {
		if t.Properties[PropTopicLevel] != LevelTheme {
			continue
		}
		// Deleting the node takes its edges with it, so the subjects beneath
		// simply become roots again.
		if err := store.DeleteNode(ctx, t.ID); err != nil {
			return removed, fmt.Errorf("remove concept %q: %w", t.Name, err)
		}
		removed++
	}
	return removed, nil
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
