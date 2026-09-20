package transcript

import (
	"context"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/pkg/jev"
)

// TestTopicMeasureCoverage asks two questions of a graph that has been
// related: whether the generators have a structural blind spot — pairs
// related through a third topic that no generator proposed directly — and
// whether the children of families too large for the shared-parent
// generator are reached by the other generators anyway.
//
// The blind spot is measured by judging a sample of the pairs reachable
// only at two hops, so it costs a few cents and writes nothing.
//
//	TOPIC_MEASURE=1 TOPIC_MEASURE_DB=/path/to/graph.db JEV_KEYRING_ACCOUNT=... \
//	go test -race ./internal/transcript/ -run TestTopicMeasureCoverage -v
func TestTopicMeasureCoverage(t *testing.T) {
	if os.Getenv("TOPIC_MEASURE") == "" {
		t.Skip("set TOPIC_MEASURE=1 to measure against a real graph")
	}
	dbPath := envOr("TOPIC_MEASURE_DB", os.ExpandEnv("$HOME/.CodeEagle/graph.db"))
	store, err := embedded.NewReadOnlyBranchStore(dbPath, "default", []string{"default", embedded.MeetingScope})
	if err != nil {
		t.Skipf("no graph: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	profiles, err := LoadTopicProfiles(ctx, store)
	if err != nil {
		t.Fatal(err)
	}

	// Every judged pair, and the related ones as an adjacency list.
	judged := make(map[pairKey]float64)
	related := make(map[string][]string)
	for _, p := range profiles.All() {
		edges, err := store.GetEdges(ctx, p.ID(), graph.EdgeRelatedTo)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range edges {
			key := keyOf(e.SourceID, e.TargetID)
			if _, seen := judged[key]; seen {
				continue
			}
			prob, err := strconv.ParseFloat(e.Properties[PropRelatedProbability], 64)
			if err != nil {
				continue
			}
			judged[key] = prob
			if prob >= DefaultRelatedGate {
				related[e.SourceID] = append(related[e.SourceID], e.TargetID)
				related[e.TargetID] = append(related[e.TargetID], e.SourceID)
			}
		}
	}
	t.Logf("%d judged pairs, %d topics with a related neighbour at >=%.1f", len(judged), len(related), DefaultRelatedGate)

	// 1. Families the shared-parent generator skipped: are their children
	// reached by the other generators?
	families := profiles.families()
	var bigChildren, bigJudged, bigRelated, smallChildren, smallJudged, smallRelated int
	var bigPairs, bigPairsJudged, bigPairsRelated int
	for _, members := range families {
		big := len(members) > maxSiblingFamily
		for i, m := range members {
			hasJudged, hasRelated := false, false
			for key := range judged {
				if key.a == m.ID() || key.b == m.ID() {
					hasJudged = true
					break
				}
			}
			if len(related[m.ID()]) > 0 {
				hasRelated = true
			}
			if big {
				bigChildren++
				if hasJudged {
					bigJudged++
				}
				if hasRelated {
					bigRelated++
				}
				for j := i + 1; j < len(members); j++ {
					bigPairs++
					if p, ok := judged[keyOf(m.ID(), members[j].ID())]; ok {
						bigPairsJudged++
						if p >= DefaultRelatedGate {
							bigPairsRelated++
						}
					}
				}
			} else {
				smallChildren++
				if hasJudged {
					smallJudged++
				}
				if hasRelated {
					smallRelated++
				}
			}
		}
	}
	t.Logf("children of families over %d: %d, of which %d judged at all, %d with a related neighbour",
		maxSiblingFamily, bigChildren, bigJudged, bigRelated)
	t.Logf("children of families of at most %d: %d, of which %d judged at all, %d with a related neighbour",
		maxSiblingFamily, smallChildren, smallJudged, smallRelated)
	t.Logf("pairs within big families: %d, of which %d proposed by other generators, %d related",
		bigPairs, bigPairsJudged, bigPairsRelated)

	// 2. Pairs reachable only at two hops: a~b and b~c related, a~c never
	// proposed. How many are there, and how many of them are related?
	twoHop := make(map[pairKey]bool)
	for a, bs := range related {
		for _, b := range bs {
			for _, c := range related[b] {
				if c == a {
					continue
				}
				key := keyOf(a, c)
				if _, seen := judged[key]; !seen {
					twoHop[key] = true
				}
			}
		}
	}
	t.Logf("pairs reachable only at two hops: %d", len(twoHop))
	if len(twoHop) == 0 {
		return
	}

	key := os.Getenv("JEV_API_KEY")
	if key == "" {
		key = jevKey(t)
	}
	client, err := jev.New(key)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]pairKey, 0, len(twoHop))
	for k := range twoHop {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].a != keys[j].a {
			return keys[i].a < keys[j].a
		}
		return keys[i].b < keys[j].b
	})
	rng := rand.New(rand.NewSource(20260920))
	rng.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
	sampleSize, _ := strconv.Atoi(envOr("TOPIC_MEASURE_N", "200"))
	if len(keys) > sampleSize {
		keys = keys[:sampleSize]
	}
	var pairs []*TopicPair
	for _, k := range keys {
		a, b := profiles.Get(k.a), profiles.Get(k.b)
		if a == nil || b == nil {
			continue
		}
		pairs = append(pairs, profiles.pair(a, b))
	}
	verdicts, report, err := NewTopicJudge(client).Judge(ctx, pairs)
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	var wide, def, narrow int
	for _, v := range verdicts {
		if v.Related >= WideRelatedGate {
			wide++
		}
		if v.Related >= DefaultRelatedGate {
			def++
		}
		if v.Related >= NarrowRelatedGate {
			narrow++
		}
	}
	t.Logf("sampled %d two-hop-only pairs (%d requests, %d tokens): related at >=0.5: %d, >=0.6: %d, >=0.8: %d",
		len(verdicts), report.Requests, report.InputTokens, wide, def, narrow)
	shown := 0
	for _, v := range verdicts {
		if v.Related >= DefaultRelatedGate && shown < 12 {
			shown++
			t.Logf("   %.2f  %s || %s", v.Related, v.Pair.A.Node.Name, v.Pair.B.Node.Name)
		}
	}
}
