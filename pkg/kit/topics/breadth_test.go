package topics_test

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph/memstore"
	"github.com/imyousuf/CodeEagle/pkg/kit/topics"
)

// breadthEdges are judged pairs, each probability chosen against a gate:
// narrow 0.8, default 0.6, wide 0.5 with a two-edge path's product held to
// 0.5 as well.
var breadthEdges = []struct {
	a, b string
	p    float64
}{
	{"seed", "alpha", 0.85},  // every band
	{"seed", "beta", 0.65},   // default and wide
	{"seed", "gamma", 0.55},  // wide only
	{"seed", "matched", 0.9}, // credited already: never reached
	{"alpha", "delta", 0.70}, // 0.85 x 0.70 = 0.595: a second hop at wide
	{"beta", "eps", 0.70},    // 0.65 x 0.70 = 0.455: under the gate
	{"gamma", "phi", 0.95},   // 0.55 x 0.95 = 0.5225: just clears it
	{"gamma", "chi", 0.90},   // 0.55 x 0.90 = 0.495: just misses
	{"other", "alpha", 0.90}, // a second seed reaches alpha for more credit
	{"half", "iota", 0.95},   // a seed that covers part of the query
}

func breadthStore(t *testing.T) *memstore.Store {
	t.Helper()
	ctx := context.Background()
	store := memstore.New()
	seen := map[string]bool{}
	for _, e := range breadthEdges {
		for _, name := range []string{e.a, e.b} {
			if seen[name] {
				continue
			}
			seen[name] = true
			if err := store.AddNode(ctx, &graph.Node{ID: topicID(name), Type: graph.NodeTopic, Name: name}); err != nil {
				t.Fatal(err)
			}
		}
		a, b := topicID(e.a), topicID(e.b)
		if a > b {
			a, b = b, a
		}
		if err := store.AddEdge(ctx, &graph.Edge{
			ID: graph.NewNodeID("edge", a, b+":"+string(graph.EdgeRelatedTo)), Type: graph.EdgeRelatedTo,
			SourceID: a, TargetID: b,
			Properties: map[string]string{
				topics.PropRelatedProbability: strconv.FormatFloat(e.p, 'f', 2, 64),
				topics.PropSameSubject:        "0.00",
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func TestExpandBands(t *testing.T) {
	ctx := context.Background()
	store := breadthStore(t)
	node := func(name string) *graph.Node {
		n, err := store.GetNode(ctx, topicID(name))
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	seeds := []topics.Seed{
		{Topic: node("seed"), Whole: true},
		{Topic: node("half"), Whole: false},
		{Topic: node("other"), Whole: true},
	}
	credited := map[string]bool{topicID("seed"): true, topicID("half"): true, topicID("other"): true, topicID("matched"): true}

	// Credit as the kit defines it, computed independently.
	hop := func(p float64) float64 { return p * topics.HopDecay }
	want := map[topics.Breadth][]string{
		topics.BreadthNone: nil,
		topics.BreadthNarrow: {
			fmt.Sprintf("seed ~ alpha p=%.4f w=%.6f", 0.85, hop(0.85)),
			fmt.Sprintf("other ~ alpha p=%.4f w=%.6f", 0.90, hop(0.90)),
		},
		topics.BreadthDefault: {
			fmt.Sprintf("seed ~ alpha p=%.4f w=%.6f", 0.85, hop(0.85)),
			fmt.Sprintf("seed ~ beta p=%.4f w=%.6f", 0.65, hop(0.65)),
			fmt.Sprintf("other ~ alpha p=%.4f w=%.6f", 0.90, hop(0.90)),
		},
		topics.BreadthWide: {
			fmt.Sprintf("seed ~ alpha p=%.4f w=%.6f", 0.85, hop(0.85)),
			fmt.Sprintf("seed ~ beta p=%.4f w=%.6f", 0.65, hop(0.65)),
			fmt.Sprintf("seed ~ gamma p=%.4f w=%.6f", 0.55, hop(0.55)),
			fmt.Sprintf("other ~ alpha p=%.4f w=%.6f", 0.90, hop(0.90)),
			fmt.Sprintf("seed ~ alpha ~ delta p=%.4f w=%.6f", 0.85*0.70, hop(0.85)*hop(0.70)),
			fmt.Sprintf("seed ~ gamma ~ phi p=%.4f w=%.6f", 0.55*0.95, hop(0.55)*hop(0.95)),
			// Reached again through the stronger first edge, for more.
			fmt.Sprintf("other ~ alpha ~ delta p=%.4f w=%.6f", 0.90*0.70, hop(0.90)*hop(0.70)),
		},
	}
	for _, b := range topics.Breadths {
		t.Run(string(b), func(t *testing.T) {
			var got []string
			err := topics.Expand(ctx, store, seeds, b, func(id string) bool { return credited[id] }, func(r topics.Reach) {
				if r.Path[0] != seeds[r.Seed].Topic || r.Path[len(r.Path)-1] != r.Topic {
					t.Errorf("path %s does not run from seed %d to the topic", r.Via(), r.Seed)
				}
				got = append(got, fmt.Sprintf("%s p=%.4f w=%.6f", r.Via(), r.Probability, r.Weight))
			})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, "\n") != strings.Join(want[b], "\n") {
				t.Errorf("reached:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want[b], "\n"))
			}
		})
	}
}

// TestExpandCreditIsExact pins credit at full precision: the product of the
// edge probabilities, and of each one times HopDecay.
func TestExpandCreditIsExact(t *testing.T) {
	ctx := context.Background()
	store := breadthStore(t)
	seed, _ := store.GetNode(ctx, topicID("seed"))
	var delta topics.Reach
	err := topics.Expand(ctx, store, []topics.Seed{{Topic: seed, Whole: true}}, topics.BreadthWide, nil, func(r topics.Reach) {
		if r.Topic.Name == "delta" {
			delta = r
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if delta.Probability != 1*0.85*0.70 || delta.Weight != 1*(0.85*0.5)*(0.70*0.5) {
		t.Errorf("delta = p %v w %v", delta.Probability, delta.Weight)
	}
	if math.Abs(delta.Weight-0.14875) > 1e-12 {
		t.Errorf("weight = %v", delta.Weight)
	}
}

func TestBreadthSettings(t *testing.T) {
	for _, tt := range []struct {
		b    topics.Breadth
		gate float64
		hops int
	}{
		{topics.BreadthNone, 0.6, 0},
		{topics.BreadthNarrow, 0.8, 1},
		{topics.BreadthDefault, 0.6, 1},
		{topics.BreadthWide, 0.5, 2},
	} {
		if !tt.b.Valid() || tt.b.MinProbability() != tt.gate || tt.b.Hops() != tt.hops {
			t.Errorf("%s: gate %v hops %d", tt.b, tt.b.MinProbability(), tt.b.Hops())
		}
	}
	if topics.Breadth("everything").Valid() {
		t.Error("an unknown breadth is valid")
	}
}

func TestCovers(t *testing.T) {
	for _, tt := range []struct {
		query, text string
		want        bool
	}{
		{"sticky board", "sticky board redesign", true},
		{"sticky board", "HITL vs sticky notes", false},
		{"sticky board", "Board of sticky things", true},
		{"AGI", "AGI feasibility debate", true},
		{"AGI", "agile ceremonies", false},
		{"migration", "migrations runbook", true},
		{"AGI artificial general intelligence", "AGI timelines", true},
		{"the", "the anything", false},
	} {
		if got := topics.Covers(tt.query, tt.text); got != tt.want {
			t.Errorf("Covers(%q, %q) = %v, want %v", tt.query, tt.text, got, tt.want)
		}
	}
}
