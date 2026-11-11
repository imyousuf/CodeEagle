package transcript

// Characterization of how far a meeting search reaches through the topic
// adjacency graph at each breadth, before that expansion moves out of this
// package. The golden under testdata/golden holds, per query and breadth,
// every hit in order with its score, and every match with the path it came
// by and the credit it carried, at full precision; regenerate it only for an
// intended change, with:
//
//	UPDATE_GOLDEN=1 go test -race -count=1 -run Golden ./internal/transcript/
//
// The table in TestFindMeetingsBreadthGolden states the same outcomes in
// code, so a careless regeneration still fails.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
)

// breadthMeetings are the meetings of the fixture: title, then the topics it
// is filed under. Each is a day later than the one before, so recency
// breaks ties the same way every run.
var breadthMeetings = []struct {
	title  string
	topics []string
}{
	{"AGI feasibility debate with the platform team", []string{"AGI feasibility debate"}},
	{"Alignment review", []string{"AI alignment risk"}},
	{"Self-improvement reading group", []string{"recursive self improvement"}},
	{"Regression postmortem", []string{"model regression"}},
	{"Capacity planning review", []string{"capacity planning"}},
	{"Roadmap offsite", []string{"AI roadmap"}},
	{"GPU budget sync", []string{"GPU budget"}},
	{"Hiring sync", []string{"hiring plan"}},
	{"Sticky board kickoff", []string{"sticky board redesign"}},
	{"Review tooling", []string{"HITL vs sticky notes"}},
	{"Review queue triage", []string{"HITL review queue"}},
	{"Whiteboard tools demo", []string{"whiteboard tooling"}},
	{"Robotics lab tour", []string{"robotics lab"}},
	{"Quarterly planning, AGI mentioned", []string{"quarterly planning"}},
	{"Timelines chat", []string{"AGI timelines"}},
	{"Board meeting prep", []string{"board governance"}},
}

// breadthTheme groups two subjects under a concept no meeting is filed
// under directly.
const breadthTheme = "Machine intelligence"

// breadthEdges are the judged pairs. Each probability is chosen against a
// gate: narrow 0.8, default 0.6, wide 0.5 with the product of a two-edge
// path held to 0.5 as well.
var breadthEdges = []struct {
	a, b string
	p    float64
	same float64
}{
	// From the AGI seeds: one edge each band admits, one only default and
	// wide admit, one only wide admits.
	{"AGI feasibility debate", "AI alignment risk", 0.85, 0.10},
	{"AGI feasibility debate", "recursive self improvement", 0.65, 0.05},
	{"AGI feasibility debate", "AI roadmap", 0.55, 0.02},
	// A second seed reaches the same neighbour with a higher probability,
	// after the first seed has credited it; and reaches another neighbour
	// at exactly the default gate.
	{"AGI timelines", "AI alignment risk", 0.90, 0.20},
	{"AGI timelines", "model regression", 0.60, 0.00},
	// Second hops: 0.85 x 0.70 = 0.595 is followed at wide; 0.65 x 0.70 =
	// 0.455 is not, though each edge clears the gate.
	{"AI alignment risk", "model regression", 0.70, 0.01},
	{"recursive self improvement", "capacity planning", 0.70, 0.03},
	// A hub, reached at 0.55: 0.55 x 0.90 = 0.495 falls just under the
	// gate, 0.55 x 0.95 = 0.5225 just clears it, and its edge back to an
	// already credited neighbour earns less than that neighbour has.
	{"AI roadmap", "GPU budget", 0.90, 0.00},
	{"AI roadmap", "hiring plan", 0.95, 0.00},
	{"AI roadmap", "model regression", 0.92, 0.00},
	{"AI roadmap", "AI alignment risk", 0.60, 0.00},
	// A seed that covers only part of "sticky board" is never expanded,
	// however strong its edges.
	{"HITL vs sticky notes", "HITL review queue", 0.90, 0.40},
	{"board governance", "hiring plan", 0.95, 0.00},
	// The full seed, an edge back to a label the words matched, and a
	// two-hop path at 0.7 x 0.8 = 0.56.
	{"sticky board redesign", "HITL vs sticky notes", 0.80, 0.15},
	{"sticky board redesign", "whiteboard tooling", 0.70, 0.25},
	{"whiteboard tooling", "robotics lab", 0.80, 0.00},
	// From the theme: exactly the narrow gate, the default band, exactly
	// the wide gate.
	{breadthTheme, "hiring plan", 0.80, 0.00},
	{breadthTheme, "robotics lab", 0.75, 0.00},
	{breadthTheme, "capacity planning", 0.50, 0.00},
}

// breadthFixture writes the meetings through the real writer, adds the
// theme above two of their topics, and records the judged pairs the way the
// relater does.
func breadthFixture(t *testing.T) graph.Store {
	t.Helper()
	ctx := context.Background()
	store := testStore(t)
	people, err := LoadPersonRegistry(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter(store, people, WriterOptions{MinConfidence: 0.7, Owner: "Imran Yousuf"})
	start := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	for i, m := range breadthMeetings {
		res := meetingFiledUnder(fmt.Sprintf("/tmp/breadth/%02d/session.json", i), fmt.Sprintf("session-%02d", i), m.title, m.topics...)
		res.Session.CreatedAt = start.AddDate(0, 0, i)
		if _, err := w.Write(ctx, res); err != nil {
			t.Fatalf("write %q: %v", m.title, err)
		}
	}
	for _, m := range breadthMeetings {
		for _, name := range m.topics {
			if n, err := store.GetNode(ctx, topicID(name)); err != nil || n == nil {
				t.Fatalf("topic %q was not written under its own label (merged into another?)", name)
			}
		}
	}

	theme := &graph.Node{
		ID: topicID(breadthTheme), Type: graph.NodeTopic, Name: breadthTheme, QualifiedName: breadthTheme,
		Properties: map[string]string{PropTopicLevel: LevelTheme, PropTopicDepth: "1", PropTopicReach: "2"},
	}
	if err := store.AddNode(ctx, theme); err != nil {
		t.Fatal(err)
	}
	for _, child := range []string{"AGI feasibility debate", "AI alignment risk"} {
		err := store.AddEdge(ctx, &graph.Edge{
			ID:   graph.NewNodeID("edge", theme.ID, topicID(child)+":"+string(graph.EdgeContains)),
			Type: graph.EdgeContains, SourceID: theme.ID, TargetID: topicID(child),
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	for _, e := range breadthEdges {
		a, b := topicID(e.a), topicID(e.b)
		if a > b {
			a, b = b, a
		}
		err := store.AddEdge(ctx, &graph.Edge{
			ID:       graph.NewNodeID("edge", a, b+":"+string(graph.EdgeRelatedTo)),
			Type:     graph.EdgeRelatedTo,
			SourceID: a, TargetID: b,
			Properties: map[string]string{
				PropRelatedProbability: strconv.FormatFloat(e.p, 'f', 2, 64),
				PropSameSubject:        strconv.FormatFloat(e.same, 'f', 2, 64),
				propRelatedSources:     "nearest",
				propRelatedJudge:       "jev-1.13.0",
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return store
}

// breadthMatch is a match as the golden records it.
type breadthMatch struct {
	Kind        MatchKind `json:"kind"`
	Label       string    `json:"label"`
	Node        string    `json:"node,omitempty"`
	Terms       []string  `json:"terms"`
	Phrase      bool      `json:"phrase"`
	Via         string    `json:"via,omitempty"`
	Hops        int       `json:"hops"`
	Probability float64   `json:"probability"`
	Weight      float64   `json:"weight"`
}

type breadthHit struct {
	Meeting     string         `json:"meeting"`
	Session     string         `json:"session"`
	Date        string         `json:"date"`
	Score       float64        `json:"score"`
	Phrase      bool           `json:"phrase"`
	Terms       []string       `json:"terms"`
	CoversAll   bool           `json:"covers_all"`
	OnlyRelated bool           `json:"only_related"`
	Matches     []breadthMatch `json:"matches"`
}

type breadthResult struct {
	Query      string       `json:"query"`
	Breadth    Breadth      `json:"breadth"`
	Terms      []string     `json:"terms"`
	Total      int          `json:"total"`
	Complete   int          `json:"complete"`
	Considered int          `json:"considered"`
	Expanded   int          `json:"expanded"`
	Hits       []breadthHit `json:"hits"`
}

func breadthRecord(q string, b Breadth, f *Found) breadthResult {
	out := breadthResult{Query: q, Breadth: b, Terms: f.Terms, Total: f.Total, Complete: f.Complete,
		Considered: f.Considered, Expanded: f.Expanded}
	for _, h := range f.Hits {
		hit := breadthHit{
			Meeting: h.Meeting.Name, Session: h.Meeting.QualifiedName, Date: h.Meeting.UpdatedAt.Format("2006-01-02"),
			Score: h.Score, Phrase: h.Phrase, Terms: h.Terms, CoversAll: h.CoversAll(f.Terms), OnlyRelated: h.OnlyRelated(),
		}
		for _, m := range h.Matches {
			bm := breadthMatch{Kind: m.Kind, Label: m.Label, Terms: m.Terms, Phrase: m.Phrase, Via: m.Via,
				Probability: m.Probability, Weight: m.Weight}
			if m.Node != nil {
				bm.Node = string(m.Node.Type) + ": " + m.Node.Name
			}
			if m.Via != "" {
				bm.Hops = strings.Count(m.Via, " ~ ")
			}
			hit.Matches = append(hit.Matches, bm)
		}
		out.Hits = append(out.Hits, hit)
	}
	return out
}

// breadthReach summarizes a hit for the table: its title, and for one the
// words never matched, each path that reached it.
func breadthReach(h *Hit) string {
	if !h.OnlyRelated() {
		return h.Meeting.Name
	}
	var vias []string
	for _, m := range h.Matches {
		vias = append(vias, m.Via)
	}
	return h.Meeting.Name + " <- " + strings.Join(vias, " | ")
}

func TestFindMeetingsBreadthGolden(t *testing.T) {
	store := breadthFixture(t)
	ctx := context.Background()

	// Hits in order. A hit the words never matched is followed by every
	// path that reached it. Hits the words matched equally well are ordered
	// most recent first.
	agiWords := []string{
		"Timelines chat",
		"Quarterly planning, AGI mentioned",
		"AGI feasibility debate with the platform team",
	}
	// Credited by the first seed, then again by the second at a higher
	// weight: the meeting carries both matches.
	alignment := "Alignment review <- AGI feasibility debate ~ AI alignment risk | AGI timelines ~ AI alignment risk"
	stickyWords := []string{"Sticky board kickoff", "Board meeting prep", "Review tooling"}
	// The theme credits the meetings under both of its subjects in full.
	themeWords := []string{"Alignment review", "AGI feasibility debate with the platform team"}
	with := func(base []string, more ...string) []string { return append(append([]string(nil), base...), more...) }
	want := map[string]map[Breadth][]string{
		"AGI": {
			BreadthNone:   agiWords,
			BreadthNarrow: with(agiWords, alignment),
			BreadthDefault: with(agiWords, alignment,
				"Self-improvement reading group <- AGI feasibility debate ~ recursive self improvement",
				"Regression postmortem <- AGI timelines ~ model regression"),
			// Second hops reach the hiring plan through the hub (0.5225), but
			// not the GPU budget (0.495) or capacity planning (0.455); the
			// regression meeting keeps its one-hop credit, which beats both
			// two-hop paths to it.
			BreadthWide: with(agiWords, alignment,
				"Self-improvement reading group <- AGI feasibility debate ~ recursive self improvement",
				"Regression postmortem <- AGI timelines ~ model regression",
				"Roadmap offsite <- AGI feasibility debate ~ AI roadmap",
				"Hiring sync <- AGI feasibility debate ~ AI roadmap ~ hiring plan"),
		},
		"sticky board": {
			// "HITL vs sticky notes" and "board governance" each match one
			// word, so their strong edges are never followed.
			BreadthNone:    stickyWords,
			BreadthNarrow:  stickyWords,
			BreadthDefault: with(stickyWords, "Whiteboard tools demo <- sticky board redesign ~ whiteboard tooling"),
			BreadthWide: with(stickyWords,
				"Whiteboard tools demo <- sticky board redesign ~ whiteboard tooling",
				"Robotics lab tour <- sticky board redesign ~ whiteboard tooling ~ robotics lab"),
		},
		"intelligence": {
			BreadthNone:    themeWords,
			BreadthNarrow:  with(themeWords, "Hiring sync <- Machine intelligence ~ hiring plan"),
			BreadthDefault: with(themeWords, "Hiring sync <- Machine intelligence ~ hiring plan", "Robotics lab tour <- Machine intelligence ~ robotics lab"),
			BreadthWide: with(themeWords,
				"Hiring sync <- Machine intelligence ~ hiring plan",
				"Robotics lab tour <- Machine intelligence ~ robotics lab",
				"Capacity planning review <- Machine intelligence ~ capacity planning",
				"Board meeting prep <- Machine intelligence ~ hiring plan ~ board governance",
				"Roadmap offsite <- Machine intelligence ~ hiring plan ~ AI roadmap",
				"Whiteboard tools demo <- Machine intelligence ~ robotics lab ~ whiteboard tooling"),
		},
	}

	for _, q := range []string{"AGI", "sticky board", "intelligence"} {
		var results []breadthResult
		for _, b := range Breadths {
			found, err := FindMeetings(ctx, store, Query{Text: q, Breadth: b})
			if err != nil {
				t.Fatalf("%q at %s: %v", q, b, err)
			}
			results = append(results, breadthRecord(q, b, found))

			var got []string
			for _, h := range found.Hits {
				got = append(got, breadthReach(h))
			}
			if g, w := strings.Join(got, "\n"), strings.Join(want[q][b], "\n"); g != w {
				t.Errorf("%q at %s:\n%s\nwant\n%s", q, b, g, w)
			}
			// A meeting reached through an edge never outranks one the
			// words matched.
			seenRelated := false
			for _, h := range found.Hits {
				if h.OnlyRelated() {
					seenRelated = true
				} else if seenRelated {
					t.Errorf("%q at %s: %q, matched by the words, ranks below a meeting reached only through edges", q, b, h.Meeting.Name)
				}
			}
		}
		name := strings.ReplaceAll(q, " ", "_")
		checkJevGolden(t, "find_breadth_"+name+".json", jevGoldenJSON(t, results))
	}
}

// TestRelatedTopicsGolden pins the neighbour lists expansion reads: every
// judged pair at a gate of zero, most probable first, and each band's gate
// admitting a probability exactly at it.
func TestRelatedTopicsGolden(t *testing.T) {
	store := breadthFixture(t)
	ctx := context.Background()

	type neighbour struct {
		Topic       string  `json:"topic"`
		Probability float64 `json:"probability"`
		Same        float64 `json:"same"`
	}
	type entry struct {
		Topic      string                 `json:"topic"`
		Neighbours map[string][]neighbour `json:"neighbours_at_gate"`
	}
	names := []string{breadthTheme}
	for _, m := range breadthMeetings {
		names = append(names, m.topics...)
	}
	var out []entry
	for _, name := range names {
		e := entry{Topic: name, Neighbours: make(map[string][]neighbour)}
		for _, gate := range []float64{0, WideRelatedGate, DefaultRelatedGate, NarrowRelatedGate} {
			related, err := RelatedTopics(ctx, store, topicID(name), gate)
			if err != nil {
				t.Fatal(err)
			}
			key := strconv.FormatFloat(gate, 'f', 1, 64)
			e.Neighbours[key] = []neighbour{}
			for _, r := range related {
				e.Neighbours[key] = append(e.Neighbours[key], neighbour{r.Topic.Name, r.Probability, r.Same})
			}
		}
		out = append(out, e)
	}
	checkJevGolden(t, "related_topics.json", jevGoldenJSON(t, out))

	theme, err := RelatedTopics(ctx, store, topicID(breadthTheme), NarrowRelatedGate)
	if err != nil {
		t.Fatal(err)
	}
	if len(theme) != 1 || theme[0].Topic.Name != "hiring plan" || theme[0].Probability != 0.8 {
		t.Errorf("at the narrow gate the theme's neighbours are %+v, want hiring plan at exactly 0.8", theme)
	}
	hub, err := RelatedTopics(ctx, store, topicID("AI roadmap"), 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range hub {
		got = append(got, fmt.Sprintf("%s %.2f/%.2f", r.Topic.Name, r.Probability, r.Same))
	}
	if g, w := strings.Join(got, ", "), "hiring plan 0.95/0.00, model regression 0.92/0.00, GPU budget 0.90/0.00, AI alignment risk 0.60/0.00, AGI feasibility debate 0.55/0.02"; g != w {
		t.Errorf("hub neighbours = %s\nwant              %s", g, w)
	}
}
