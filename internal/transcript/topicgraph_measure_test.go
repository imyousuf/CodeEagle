package transcript

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/vectorstore"
	"github.com/imyousuf/CodeEagle/pkg/jev"
)

// These tests measure, against the real corpus, whether topic relatedness can
// be read off embedding similarity alone or needs a judgment per pair. The
// first writes a labelling sheet for a person to fill in; the second scores
// both methods against those labels. Nothing is recorded: the corpus is
// private, and the only artefacts are files in a directory the caller names.
//
//	TOPIC_MEASURE=1 TOPIC_MEASURE_OUT=/tmp/topics \
//	go test -race ./internal/transcript/ -run TestTopicMeasureCandidates -v
//
// then label the sheet as labels.txt ("P01 sibling" per line) and
//
//	TOPIC_MEASURE=1 TOPIC_MEASURE_OUT=/tmp/topics JEV_KEYRING_ACCOUNT=... \
//	go test -race ./internal/transcript/ -run TestTopicMeasureJudge -v
//
// Measured 2026-09-20 over 91 pairs (2 same subject, 7 facet, 22 sibling, 7
// co-occurring, 53 unrelated): cosine separated related from unrelated with
// AUC 0.77 and no usable cut (0.76 gave precision 0.83 at recall 0.50); the
// judge's probability did so with AUC 0.92, put no unrelated pair above
// 0.56, and at a gate of 0.6 admitted 22 pairs, all related.

func measureCorpus(t *testing.T) (*TopicProfiles, func()) {
	t.Helper()
	if os.Getenv("TOPIC_MEASURE") == "" {
		t.Skip("set TOPIC_MEASURE=1 to measure against the real corpus")
	}
	home := envOr("TOPIC_MEASURE_HOME", os.ExpandEnv("$HOME/.CodeEagle"))
	store, err := embedded.NewReadOnlyBranchStore(filepath.Join(home, "graph.db"), "default",
		[]string{"default", embedded.MeetingScope})
	if err != nil {
		t.Skipf("no graph: %v", err)
	}
	vs, err := vectorstore.NewReadOnly(store, nil, "default",
		filepath.Join(home, "vec.idx"), filepath.Join(home, "vec.db"))
	if err != nil {
		store.Close()
		t.Skipf("no vector store: %v", err)
	}
	if loaded, err := vs.Load(); err != nil || !loaded {
		vs.Close()
		store.Close()
		t.Skipf("vector index not loaded: %v", err)
	}
	profiles, err := LoadTopicProfiles(context.Background(), store)
	if err != nil {
		t.Fatalf("profiles: %v", err)
	}
	withVec := profiles.WithVectors(vs.Vector)
	t.Logf("%d meeting topics, %d with vectors", profiles.Len(), withVec)
	return profiles, func() { vs.Close(); store.Close() }
}

type samplePair struct {
	ID      string  `json:"id"`
	A       string  `json:"a"`
	B       string  `json:"b"`
	Stratum string  `json:"stratum"`
	Sources string  `json:"sources"`
	Cosine  float64 `json:"cosine"`
	Rank    int     `json:"rank"`
	Shared  int     `json:"shared"`
}

// sampleLabels are the relations a person labels a pair with, and the
// search breadth each would belong to if the judge could tell them apart.
var sampleLabels = map[string]string{
	"same_subject": "narrow",
	"a_facet_of_b": "default",
	"b_facet_of_a": "default",
	"sibling":      "default",
	"co_occurring": "wide",
	"unrelated":    "none",
}

func TestTopicMeasureCandidates(t *testing.T) {
	profiles, closeAll := measureCorpus(t)
	defer closeAll()
	outDir := envOr("TOPIC_MEASURE_OUT", t.TempDir())
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}

	pairs := profiles.Candidates(DefaultNearestK)
	bySource := make(map[string]int)
	var nearestCos []float64
	singleUse := 0
	for _, p := range profiles.All() {
		if len(p.Meetings) == 1 {
			singleUse++
		}
	}
	for _, p := range pairs {
		bySource[p.Sources.String()]++
		if p.Sources&SourceNearest != 0 && !math.IsNaN(p.Cosine) {
			nearestCos = append(nearestCos, p.Cosine)
		}
	}
	t.Logf("topics used by one meeting: %d of %d", singleUse, profiles.Len())
	t.Logf("%d candidate pairs", len(pairs))
	for _, k := range sortedKeys(bySource) {
		t.Logf("  %-45s %d", k, bySource[k])
	}
	sort.Float64s(nearestCos)
	if n := len(nearestCos); n > 0 {
		t.Logf("cosine among nearest-%d pairs: min %.2f p10 %.2f p50 %.2f p90 %.2f max %.2f",
			DefaultNearestK, nearestCos[0], nearestCos[n/10], nearestCos[n/2], nearestCos[n*9/10], nearestCos[n-1])
	}

	// Strata, drawn with a fixed seed so the sheet is reproducible.
	rng := rand.New(rand.NewSource(20260920))
	var sample []*TopicPair
	strata := make(map[pairKey]string)
	take := func(name string, from []*TopicPair, n int) {
		rng.Shuffle(len(from), func(i, j int) { from[i], from[j] = from[j], from[i] })
		for _, p := range from {
			if n == 0 {
				break
			}
			if _, dup := strata[p.Key()]; dup {
				continue
			}
			strata[p.Key()] = name
			sample = append(sample, p)
			n--
		}
	}
	filter := func(keep func(*TopicPair) bool) []*TopicPair {
		var out []*TopicPair
		for _, p := range pairs {
			if keep(p) {
				out = append(out, p)
			}
		}
		return out
	}
	nearestOnly := func(p *TopicPair) bool { return p.Sources&SourceNearest != 0 }
	notNearest := func(p *TopicPair) bool { return p.Sources&SourceNearest == 0 }

	// The worked example: everything about AGI, whichever label it wore.
	agi := regexp.MustCompile(`(?i)\bAGI\b|superintelligence|recursive self`)
	var agiTopics []*TopicProfile
	for _, p := range profiles.All() {
		if agi.MatchString(p.Node.Name) {
			agiTopics = append(agiTopics, p)
		}
	}
	t.Logf("AGI-labelled topics: %d", len(agiTopics))
	var agiPairs []*TopicPair
	for i := range agiTopics {
		for j := i + 1; j < len(agiTopics); j++ {
			agiPairs = append(agiPairs, profiles.pair(agiTopics[i], agiTopics[j]))
		}
	}
	take("agi", agiPairs, 12)
	take("nearest-top", filter(func(p *TopicPair) bool { return nearestOnly(p) && p.NearestRank == 1 }), 15)
	take("nearest-mid", filter(func(p *TopicPair) bool { return nearestOnly(p) && p.NearestRank >= 3 && p.NearestRank <= 6 }), 15)
	take("nearest-tail", filter(func(p *TopicPair) bool { return nearestOnly(p) && p.NearestRank >= 9 }), 10)
	take("same-meeting-only", filter(func(p *TopicPair) bool { return notNearest(p) && p.Sources&SourceSameMeeting != 0 }), 15)
	take("adjacent-only", filter(func(p *TopicPair) bool { return notNearest(p) && p.Sources&SourceAdjacent != 0 }), 8)
	take("shared-parent-only", filter(func(p *TopicPair) bool { return notNearest(p) && p.Sources&SourceSharedParent != 0 }), 10)
	all := profiles.All()
	var random []*TopicPair
	for len(random) < 60 {
		a, b := all[rng.Intn(len(all))], all[rng.Intn(len(all))]
		if a == b {
			continue
		}
		random = append(random, profiles.pair(a, b))
	}
	take("random", random, 15)

	// The sheet hides everything a labeller should not be swayed by: which
	// signal proposed the pair and how close the embeddings are.
	order := make([]int, len(sample))
	for i := range order {
		order[i] = i
	}
	rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })

	var rows []samplePair
	sheet, err := os.Create(filepath.Join(outDir, "sheet.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer sheet.Close()
	w := bufio.NewWriter(sheet)
	fmt.Fprintf(w, "Label each pair with one of: %s\n\n", strings.Join(sortedKeys(sampleLabels), " "))
	for n, idx := range order {
		p := sample[idx]
		id := fmt.Sprintf("P%02d", n+1)
		rows = append(rows, samplePair{
			ID: id, A: p.A.ID(), B: p.B.ID(), Stratum: strata[p.Key()],
			Sources: p.Sources.String(), Cosine: p.Cosine, Rank: p.NearestRank, Shared: p.SharedMeetings,
		})
		fmt.Fprintf(w, "=== %s   (meetings filed under both: %d)\n", id, p.SharedMeetings)
		writeTopic(w, "A", p.A)
		writeTopic(w, "B", p.B)
		fmt.Fprintf(w, "%s label: \n\n", id)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(rows, "", " ")
	if err := os.WriteFile(filepath.Join(outDir, "pairs.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d pairs to %s", len(rows), outDir)
}

func writeTopic(w *bufio.Writer, side string, p *TopicProfile) {
	fmt.Fprintf(w, "%s: %s\n", side, p.Node.Name)
	if al := aliasesOf(p.Node); len(al) > 0 {
		fmt.Fprintf(w, "   also worded: %s\n", strings.Join(al, " | "))
	}
	fmt.Fprintf(w, "   meetings: %d\n", len(p.Meetings))
	for i, s := range p.Summaries {
		if i >= 2 {
			break
		}
		fmt.Fprintf(w, "   said: %s\n", clipRunes(strings.Join(strings.Fields(s), " "), 260))
	}
}

func TestTopicMeasureJudge(t *testing.T) {
	profiles, closeAll := measureCorpus(t)
	defer closeAll()
	outDir := envOr("TOPIC_MEASURE_OUT", "")
	if outDir == "" {
		t.Skip("set TOPIC_MEASURE_OUT to the directory holding pairs.json and labels.txt")
	}
	var rows []samplePair
	data, err := os.ReadFile(filepath.Join(outDir, "pairs.json"))
	if err != nil {
		t.Skipf("no pairs: %v", err)
	}
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}
	labels := readLabels(t, filepath.Join(outDir, "labels.txt"))

	var pairs []*TopicPair
	var truth []string
	var kept []samplePair
	for _, r := range rows {
		want, ok := labels[r.ID]
		if !ok {
			continue
		}
		a, b := profiles.Get(r.A), profiles.Get(r.B)
		if a == nil || b == nil {
			t.Fatalf("%s: topic missing", r.ID)
		}
		pairs = append(pairs, profiles.pair(a, b))
		truth = append(truth, want)
		kept = append(kept, r)
	}
	t.Logf("%d labelled pairs", len(pairs))
	isRelated := func(l string) bool { return l != "unrelated" }

	// (a) Cosine alone: can one cut separate related from unrelated?
	byLabel := make(map[string][]float64)
	for i, p := range pairs {
		if !math.IsNaN(p.Cosine) {
			byLabel[truth[i]] = append(byLabel[truth[i]], p.Cosine)
		}
	}
	for _, k := range sortedKeys(byLabel) {
		v := byLabel[k]
		sort.Float64s(v)
		t.Logf("cosine | %-13s n=%2d  min %.2f  p25 %.2f  med %.2f  p75 %.2f  max %.2f",
			k, len(v), v[0], v[len(v)/4], v[len(v)/2], v[len(v)*3/4], v[len(v)-1])
	}
	var relCos, unrelCos []float64
	for i, p := range pairs {
		if isRelated(truth[i]) {
			relCos = append(relCos, p.Cosine)
		} else {
			unrelCos = append(unrelCos, p.Cosine)
		}
	}
	t.Logf("cosine AUC related-vs-unrelated: %.2f", auc(relCos, unrelCos))
	best, bestAcc := 0.0, -1.0
	for cut := 0.20; cut <= 0.99; cut += 0.01 {
		right := 0
		for i, p := range pairs {
			if (p.Cosine >= cut) == isRelated(truth[i]) {
				right++
			}
		}
		if acc := float64(right) / float64(len(pairs)); acc > bestAcc {
			best, bestAcc = cut, acc
		}
	}
	gateReport(t, "cosine", best, func(i int) float64 { return pairs[i].Cosine }, truth, isRelated)
	t.Logf("cosine best cut %.2f: accuracy %.2f", best, bestAcc)

	// Which generator proposed the pairs a person calls related.
	found := make(map[string]map[string]int)
	for i, r := range kept {
		band := sampleLabels[truth[i]]
		if found[band] == nil {
			found[band] = make(map[string]int)
		}
		found[band][r.Sources]++
	}
	for _, band := range []string{"narrow", "default", "wide", "none"} {
		for _, src := range sortedKeys(found[band]) {
			t.Logf("labelled %-8s proposed by %-40s %d", band, src, found[band][src])
		}
	}

	// (b) A judgment per pair.
	key := os.Getenv("JEV_API_KEY")
	if key == "" {
		key = jevKey(t)
	}
	client, err := jev.New(key)
	if err != nil {
		t.Fatal(err)
	}
	judge := NewTopicJudge(client)
	ctx := context.Background()
	verdicts, report, err := judge.Judge(ctx, pairs)
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	t.Logf("jev: %d requests, %d input tokens, %s, model %s", report.Requests, report.InputTokens, report.Latency, report.Model)

	var rel, unrel []float64
	for i, v := range verdicts {
		if isRelated(truth[i]) {
			rel = append(rel, v.Related)
		} else {
			unrel = append(unrel, v.Related)
		}
	}
	sort.Float64s(rel)
	sort.Float64s(unrel)
	t.Logf("P(related) | related   n=%d min %.2f p25 %.2f med %.2f p75 %.2f max %.2f", len(rel), rel[0], rel[len(rel)/4], rel[len(rel)/2], rel[len(rel)*3/4], rel[len(rel)-1])
	t.Logf("P(related) | unrelated n=%d min %.2f p25 %.2f med %.2f p75 %.2f max %.2f", len(unrel), unrel[0], unrel[len(unrel)/4], unrel[len(unrel)/2], unrel[len(unrel)*3/4], unrel[len(unrel)-1])
	t.Logf("jev AUC related-vs-unrelated: %.2f", auc(rel, unrel))
	for _, gate := range []float64{WideRelatedGate, DefaultRelatedGate, 0.7, NarrowRelatedGate, 0.9} {
		gateReport(t, "jev", gate, func(i int) float64 { return verdicts[i].Related }, truth, isRelated)
	}
	// Same-subject: few positives exist, so report them individually.
	for i, v := range verdicts {
		if truth[i] == "same_subject" || v.Same >= 0.5 {
			t.Logf("same: %s label=%-13s P(same)=%.2f P(related)=%.2f | %s || %s",
				kept[i].ID, truth[i], v.Same, v.Related, v.Pair.A.Node.Name, v.Pair.B.Node.Name)
		}
	}
	// Drift between identical requests, on the first batch.
	if len(pairs) >= maxPairsPerRequest {
		again, _, err := judge.Judge(ctx, pairs[:maxPairsPerRequest])
		if err == nil {
			maxDelta := 0.0
			for i, v := range again {
				maxDelta = math.Max(maxDelta, math.Abs(v.Related-verdicts[i].Related))
			}
			t.Logf("max probability drift between identical requests: %.2f", maxDelta)
		}
	}

	type result struct {
		samplePair
		Label   string  `json:"label"`
		ALabel  string  `json:"a_label"`
		BLabel  string  `json:"b_label"`
		Related float64 `json:"related"`
		Same    float64 `json:"same"`
	}
	var results []result
	for i, v := range verdicts {
		results = append(results, result{samplePair: kept[i], Label: truth[i],
			ALabel: v.Pair.A.Node.Name, BLabel: v.Pair.B.Node.Name, Related: v.Related, Same: v.Same})
	}
	out, _ := json.MarshalIndent(results, "", " ")
	if err := os.WriteFile(filepath.Join(outDir, "jev_results.json"), out, 0o644); err != nil {
		t.Fatal(err)
	}
}

// gateReport logs what admitting pairs at or above a score does.
func gateReport(t *testing.T, method string, gate float64, score func(int) float64, truth []string, isRelated func(string) bool) {
	t.Helper()
	kept, tp, positives := 0, 0, 0
	for i := range truth {
		if isRelated(truth[i]) {
			positives++
		}
		if score(i) >= gate {
			kept++
			if isRelated(truth[i]) {
				tp++
			}
		}
	}
	t.Logf("%s gate >=%.2f: kept %2d precision %.2f recall %.2f", method, gate, kept, ratio(tp, kept), ratio(tp, positives))
}

// auc is the probability that a random related pair scores above a random
// unrelated one.
func auc(pos, neg []float64) float64 {
	if len(pos) == 0 || len(neg) == 0 {
		return 0
	}
	above := 0.0
	for _, p := range pos {
		for _, n := range neg {
			switch {
			case p > n:
				above++
			case p == n:
				above += 0.5
			}
		}
	}
	return above / float64(len(pos)*len(neg))
}

func readLabels(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("no labels: %v", err)
	}
	defer f.Close()
	out := make(map[string]string)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if _, ok := sampleLabels[fields[1]]; !ok {
			t.Fatalf("label %q for %s is not one of %s", fields[1], fields[0], strings.Join(sortedKeys(sampleLabels), " "))
		}
		out[fields[0]] = fields[1]
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}
