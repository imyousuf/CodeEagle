package transcript

import (
	"context"
	"os"
	"sort"
	"strconv"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

// TestTopicMeasureFamilies reports how many children each parent in the tree
// has, which decides whether "shared parent" can propose pairs at all: a
// parent with three hundred children proposes forty-five thousand.
//
// Measured 2026-09-20: 198 parents, 55,691 pairs; "Opal platform" alone has
// 190 children. Families of at most 30 give 2,994 pairs.
func TestTopicMeasureFamilies(t *testing.T) {
	profiles, closeAll := measureCorpus(t)
	defer closeAll()
	families := profiles.families()
	type fam struct {
		name string
		n    int
	}
	var list []fam
	pairs := 0
	for id, members := range families {
		name := id
		if p := profiles.Get(id); p != nil {
			name = p.Node.Name
		}
		list = append(list, fam{name, len(members)})
		pairs += len(members) * (len(members) - 1) / 2
	}
	sort.Slice(list, func(i, j int) bool { return list[i].n > list[j].n })
	t.Logf("%d parents, %d shared-parent pairs uncapped", len(list), pairs)
	for i, f := range list {
		if i >= 15 {
			break
		}
		t.Logf("  %4d  %s", f.n, f.name)
	}
	for _, cap := range []int{10, 20, maxSiblingFamily, 50} {
		capped := 0
		for _, f := range list {
			if f.n <= cap {
				capped += f.n * (f.n - 1) / 2
			}
		}
		t.Logf("families of at most %d children: %d pairs", cap, capped)
	}
	t.Logf("candidates at k=%d: %d", DefaultNearestK, len(profiles.Candidates(DefaultNearestK)))
}

// TestTopicMeasureEdges reports what a relate run over a graph produced: how
// the probabilities are distributed, and per proposing signal how many pairs
// cleared each gate — which is the yield of each generator.
//
//	TOPIC_MEASURE=1 TOPIC_MEASURE_DB=/path/to/graph.db \
//	go test -race ./internal/transcript/ -run TestTopicMeasureEdges -v
func TestTopicMeasureEdges(t *testing.T) {
	if os.Getenv("TOPIC_MEASURE") == "" {
		t.Skip("set TOPIC_MEASURE=1 to measure against a real graph")
	}
	dbPath := envOr("TOPIC_MEASURE_DB", os.ExpandEnv("$HOME/.CodeEagle/graph.db"))
	store, err := embedded.NewReadOnlyBranchStore(dbPath, embedded.MeetingScope, []string{embedded.MeetingScope})
	if err != nil {
		t.Skipf("no graph: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	topics, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeTopic})
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	buckets := make([]int, 10)
	type yield struct{ total, wide, def, narrow int }
	bySource := make(map[string]*yield)
	total := 0
	for _, tp := range topics {
		edges, err := store.GetEdges(ctx, tp.ID, graph.EdgeRelatedTo)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range edges {
			if seen[e.ID] {
				continue
			}
			seen[e.ID] = true
			p, err := strconv.ParseFloat(e.Properties[PropRelatedProbability], 64)
			if err != nil {
				continue
			}
			total++
			buckets[min(int(p*10), 9)]++
			src := e.Properties[propRelatedSources]
			y := bySource[src]
			if y == nil {
				y = &yield{}
				bySource[src] = y
			}
			y.total++
			if p >= WideRelatedGate {
				y.wide++
			}
			if p >= DefaultRelatedGate {
				y.def++
			}
			if p >= NarrowRelatedGate {
				y.narrow++
			}
		}
	}
	t.Logf("%d judged pairs", total)
	for i, n := range buckets {
		t.Logf("  p in [%.1f, %.1f): %6d", float64(i)/10, float64(i+1)/10, n)
	}
	t.Logf("%-50s %7s %7s %7s %7s", "proposed by", "judged", ">=0.5", ">=0.6", ">=0.8")
	for _, src := range sortedKeys(bySource) {
		y := bySource[src]
		t.Logf("%-50s %7d %7d %7d %7d", src, y.total, y.wide, y.def, y.narrow)
	}
}
