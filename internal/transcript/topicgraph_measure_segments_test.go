package transcript

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/vectorstore"
)

// TestTopicMeasureSegmentNeighbours asks whether comparing what was said —
// the segment summaries, which the index embeds in full — proposes the AGI
// pairs that comparing the labels does not.
//
// Measured 2026-09-20: "AGI feasibility debate" and "AI alignment and
// superintelligence risk" are each other's nearest segment at 0.63, so the
// pair the labels never proposed is found this way; over the whole corpus
// the signal proposed 4,131 pairs of which 43% were judged related at 0.6
// or above, the highest yield of the four generators.
func TestTopicMeasureSegmentNeighbours(t *testing.T) {
	if os.Getenv("TOPIC_MEASURE") == "" {
		t.Skip("set TOPIC_MEASURE=1 to measure against the real corpus")
	}
	home := envOr("TOPIC_MEASURE_HOME", os.ExpandEnv("$HOME/.CodeEagle"))
	store, err := embedded.NewReadOnlyBranchStore(filepath.Join(home, "graph.db"), "default",
		[]string{"default", embedded.MeetingScope})
	if err != nil {
		t.Skipf("no graph: %v", err)
	}
	defer store.Close()
	vs, err := vectorstore.NewReadOnly(store, nil, "default",
		filepath.Join(home, "vec.idx"), filepath.Join(home, "vec.db"))
	if err != nil {
		t.Skipf("no vector store: %v", err)
	}
	defer vs.Close()
	if loaded, err := vs.Load(); err != nil || !loaded {
		t.Skipf("vector index not loaded: %v", err)
	}
	ctx := context.Background()

	segments, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeTopicSegment})
	if err != nil {
		t.Fatal(err)
	}
	type seg struct {
		node  *graph.Node
		topic string
		vec   []float32
	}
	var all []seg
	for _, s := range segments {
		v, ok := vs.Vector(s.ID)
		if !ok {
			continue
		}
		topic := ""
		if ts, err := store.GetNeighbors(ctx, s.ID, graph.EdgeHasTopic, graph.Outgoing); err == nil {
			for _, tn := range ts {
				if tn.Type == graph.NodeTopic {
					topic = tn.Name
					break
				}
			}
		}
		all = append(all, seg{s, topic, v})
	}
	t.Logf("%d segments with vectors", len(all))

	agi := regexp.MustCompile(`(?i)\bAGI\b|superintelligence|recursive self`)
	for _, s := range all {
		if !agi.MatchString(s.topic) {
			continue
		}
		type near struct {
			topic string
			cos   float64
		}
		var out []near
		for _, o := range all {
			if o.node.ID == s.node.ID {
				continue
			}
			out = append(out, near{o.topic, cosine(s.vec, o.vec)})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].cos > out[j].cos })
		t.Logf("segment %q (topic %q)", s.node.Name, s.topic)
		for i, n := range out[:12] {
			mark := ""
			if agi.MatchString(n.topic) {
				mark = "  <== AGI"
			}
			t.Logf("   %2d  %.2f  %s%s", i+1, n.cos, n.topic, mark)
		}
	}
}
