package topics_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/kit/decide"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph/memstore"
	"github.com/imyousuf/CodeEagle/pkg/kit/topics"
)

// cardState is the default card as a judge reads it.
type cardState struct {
	Topics []struct {
		ID          string   `json:"id"`
		Label       string   `json:"label"`
		TimesFiled  int      `json:"times_filed_under"`
		WhatWasSaid []string `json:"what_was_said_under_it"`
	} `json:"topics"`
	Pairs []struct {
		Pair   string `json:"pair"`
		A      string `json:"a"`
		B      string `json:"b"`
		Shared int    `json:"times_filed_under_both"`
	} `json:"pairs"`
}

// scriptedJudge answers from a table keyed by the labels of each pair, the
// way an LLM judge would score them, and can refuse a batch as too large.
type scriptedJudge struct {
	related map[string]float64
	// refuseOver refuses, as too large, any batch of more pairs than this.
	refuseOver int
	// meter, when set, reports this many input tokens per pair and a
	// model name, as a judge that knows its cost does.
	meter bool
	// drop leaves this question out of every reply.
	drop string

	mu      sync.Mutex
	calls   int
	batches []string
	states  []cardState
}

func labels(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

func (j *scriptedJudge) Decide(ctx context.Context, state any, questions map[string]decide.Question) (map[string]decide.Verdict, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	var st cardState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.calls++
	j.states = append(j.states, st)
	if j.refuseOver > 0 && len(st.Pairs) > j.refuseOver {
		return nil, decide.ErrTooLarge
	}
	labelOf := map[string]string{}
	for _, c := range st.Topics {
		labelOf[c.ID] = c.Label
	}
	var batch []string
	out := map[string]decide.Verdict{}
	for _, p := range st.Pairs {
		key := labels(labelOf[p.A], labelOf[p.B])
		batch = append(batch, key)
		for _, q := range []string{topics.RelatedQuestion + p.Pair, topics.SameQuestion + p.Pair} {
			if _, asked := questions[q]; !asked {
				return nil, fmt.Errorf("no question %q", q)
			}
			if !questions[q].IsYesNo() || questions[q].WhenTrue == "" || questions[q].WhenFalse == "" {
				return nil, fmt.Errorf("question %q is not a yes/no question with both cases", q)
			}
		}
		r, ok := j.related[key]
		if !ok {
			r = 0.1
		}
		out[topics.RelatedQuestion+p.Pair] = decide.YesNoVerdict(r)
		out[topics.SameQuestion+p.Pair] = decide.YesNoVerdict(0.05)
	}
	delete(out, j.drop)
	j.batches = append(j.batches, strings.Join(batch, ", "))
	if j.meter {
		topics.MeterFrom(ctx).Record(100*len(st.Pairs), "scripted-1")
	}
	return out, nil
}

func (j *scriptedJudge) Name() string { return "scripted" }

func topicID(name string) string { return graph.NewNodeID(string(graph.NodeTopic), "", name) }

// notesGraph files documents under topics the way extraction does: each
// document node carries a summary and HasTopic edges to its labels.
func notesGraph(t *testing.T, docs map[string][]string) *memstore.Store {
	t.Helper()
	ctx := context.Background()
	store := memstore.New()
	for doc, labels := range docs {
		if err := store.AddNode(ctx, &graph.Node{
			ID: doc, Type: graph.NodeDocument, Name: doc,
			Properties: map[string]string{graph.PropSummary: "What " + doc + " says."},
		}); err != nil {
			t.Fatal(err)
		}
		nodes, edges := topics.NewNodes(labels, doc)
		for _, n := range nodes {
			if err := store.AddNode(ctx, n); err != nil {
				t.Fatal(err)
			}
		}
		for _, e := range edges {
			if err := store.AddEdge(ctx, e); err != nil {
				t.Fatal(err)
			}
		}
	}
	return store
}

// relatedEdges lists every RelatedTo edge as "a|b" by label, with its
// properties.
func relatedEdges(t *testing.T, store graph.Store) map[string]map[string]string {
	t.Helper()
	ctx := context.Background()
	nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeTopic})
	if err != nil {
		t.Fatal(err)
	}
	name := map[string]string{}
	for _, n := range nodes {
		name[n.ID] = n.Name
	}
	out := map[string]map[string]string{}
	for _, n := range nodes {
		edges, err := store.GetEdges(ctx, n.ID, graph.EdgeRelatedTo)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range edges {
			if e.SourceID > e.TargetID {
				t.Errorf("edge %s not in canonical order", e.ID)
			}
			out[labels(name[e.SourceID], name[e.TargetID])] = e.Properties
		}
	}
	return out
}

func TestRelaterJudgesEachPairOnceAndStoresEveryVerdict(t *testing.T) {
	ctx := context.Background()
	store := notesGraph(t, map[string][]string{
		"doc-1": {"schema migration", "client rollout"},
		"doc-2": {"client rollout", "release notes"},
		"doc-3": {"hiring plan"},
	})
	judge := &scriptedJudge{related: map[string]float64{labels("schema migration", "client rollout"): 0.9}}
	r := topics.NewRelater(store, judge)

	if n, err := r.Pending(ctx); err != nil || n != 2 {
		t.Fatalf("pending = %d (%v), want the two pairs that share a document", n, err)
	}
	st, err := r.RelateAll(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if st.Pairs != 2 || st.Related != 1 || st.Judging.Requests != 1 || st.Judging.Model != "scripted" {
		t.Errorf("stats = %+v", st)
	}
	edges := relatedEdges(t, store)
	want := map[string]map[string]string{
		labels("schema migration", "client rollout"): {
			topics.PropRelatedProbability: "0.90", topics.PropSameSubject: "0.05",
			topics.PropRelatedSources: "shared-use", topics.PropRelatedJudge: "scripted",
		},
		labels("client rollout", "release notes"): {
			topics.PropRelatedProbability: "0.10", topics.PropSameSubject: "0.05",
			topics.PropRelatedSources: "shared-use", topics.PropRelatedJudge: "scripted",
		},
	}
	if fmt.Sprint(edges) != fmt.Sprint(want) {
		t.Errorf("edges =\n%v\nwant\n%v", edges, want)
	}

	// The card shows what each document said under its topics, and how
	// often each was used.
	if got := judge.states[0].Topics; len(got) != 3 || got[0].TimesFiled == 0 || len(got[0].WhatWasSaid) == 0 {
		t.Errorf("card topics = %+v", got)
	}

	// Asked once: neither this relater nor a new one reading the edges
	// asks again.
	if _, err := r.RelateAll(ctx, 0); err != nil || judge.calls != 1 {
		t.Errorf("second pass made %d further calls (%v)", judge.calls-1, err)
	}
	again := topics.NewRelater(store, judge)
	if n, _ := again.Pending(ctx); n != 0 {
		t.Errorf("a new relater found %d unjudged pairs", n)
	}

	related, err := topics.RelatedTopics(ctx, store, topicID("client rollout"), topics.DefaultRelatedGate)
	if err != nil || len(related) != 1 || related[0].Topic.Name != "schema migration" || related[0].Probability != 0.9 {
		t.Errorf("related = %+v (%v)", related, err)
	}
	if all, _ := topics.RelatedTopics(ctx, store, topicID("client rollout"), 0); len(all) != 2 {
		t.Errorf("every verdict is stored, related or not: got %d", len(all))
	}
}

func TestRelaterRelatesANewUseIncrementally(t *testing.T) {
	ctx := context.Background()
	store := notesGraph(t, map[string][]string{"doc-1": {"schema migration", "client rollout"}})
	judge := &scriptedJudge{}
	vectors := map[string][]float32{
		topicID("schema migration"): {1, 0, 0},
		topicID("client rollout"):   {0, 1, 0},
		topicID("release notes"):    {0.9, 0.1, 0},
	}
	r := topics.NewRelater(store, judge, topics.WithVectors(func(id string) ([]float32, bool) {
		v, ok := vectors[id]
		return v, ok
	}), topics.WithGenerators(topics.NearestLabels(1), topics.SharedUse()))

	// A section, newly written, filed under two topics, one of them new.
	section := &graph.Node{ID: "sec-1", Type: "Section", Name: "Release", Properties: map[string]string{graph.PropSummary: "  Notes for the release.  "}}
	nodes, _ := topics.NewNodes([]string{"client rollout", "release notes"}, section.ID)
	for _, n := range nodes {
		if err := store.AddNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	st, err := r.Relate(ctx, section.ID, nodes, map[string]*graph.Node{nodes[1].ID: section})
	if err != nil {
		t.Fatal(err)
	}
	// Its own pair, and the new topic's nearest label; the old pair is not
	// a new use's business, being already known to the graph but unjudged.
	if st.Pairs != 2 || judge.calls != 1 {
		t.Fatalf("judged %d pairs in %d calls", st.Pairs, judge.calls)
	}
	if got := judge.batches[0]; got != "release notes|schema migration, client rollout|release notes" &&
		got != "client rollout|release notes, release notes|schema migration" {
		t.Errorf("asked about %s", got)
	}
	edges := relatedEdges(t, store)
	if p := edges[labels("release notes", "schema migration")]; p[topics.PropRelatedSources] != "nearest" || p[topics.PropRelatedCosine] != "0.99" {
		t.Errorf("nearest edge = %v", p)
	}
	for _, c := range judge.states[0].Topics {
		if c.Label == "release notes" && (len(c.WhatWasSaid) != 1 || c.WhatWasSaid[0] != "Notes for the release.") {
			t.Errorf("what was said under the new topic = %q", c.WhatWasSaid)
		}
	}

	// The same use written again proposes nothing new.
	if st, err := r.Relate(ctx, section.ID, nodes, nil); err != nil || st.Pairs != 0 || judge.calls != 1 {
		t.Errorf("rewrite judged %d pairs (%v)", st.Pairs, err)
	}
}

func TestRelaterWithoutAJudgeAssertsNothing(t *testing.T) {
	ctx := context.Background()
	store := notesGraph(t, map[string][]string{"doc-1": {"a topic", "b topic"}})
	r := topics.NewRelater(store, nil)
	if n, err := r.Pending(ctx); err != nil || n != 1 {
		t.Fatalf("pending = %d (%v)", n, err)
	}
	st, err := r.RelateAll(ctx, 0)
	if err != nil || st.Pairs != 0 {
		t.Errorf("stats = %+v (%v)", st, err)
	}
	if edges := relatedEdges(t, store); len(edges) != 0 {
		t.Errorf("a relater without a judge wrote %v", edges)
	}
	if _, err := r.Relate(ctx, "doc-2", nil, nil); err != nil {
		t.Errorf("Relate without a judge: %v", err)
	}
	verdicts, report, err := topics.NewPairJudge(nil).Judge(ctx, []*topics.Pair{{}})
	if verdicts != nil || report != (topics.Judging{}) || err != nil {
		t.Errorf("nil pair judge = %v, %+v, %v", verdicts, report, err)
	}
}

// pairsOf builds n standalone pairs, labelled "a00|b00" and so on.
func pairsOf(n int) []*topics.Pair {
	ps := topics.NewProfiles()
	var out []*topics.Pair
	for i := range n {
		a := ps.Ensure(&graph.Node{ID: topicID(fmt.Sprintf("a%02d", i)), Type: graph.NodeTopic, Name: fmt.Sprintf("a%02d", i)})
		b := ps.Ensure(&graph.Node{ID: topicID(fmt.Sprintf("b%02d", i)), Type: graph.NodeTopic, Name: fmt.Sprintf("b%02d", i)})
		out = append(out, ps.Pair(a, b))
	}
	return out
}

func TestPairJudgeBatchesAndHalvesWhatIsTooLarge(t *testing.T) {
	ctx := context.Background()
	related := map[string]float64{}
	pairs := pairsOf(5)
	for i, p := range pairs {
		related[labels(p.A.Node.Name, p.B.Node.Name)] = float64(i) / 10
	}
	judge := &scriptedJudge{related: related, refuseOver: 2, meter: true}
	verdicts, report, err := topics.NewPairJudge(judge, topics.WithBatchSize(10)).Judge(ctx, pairs)
	if err != nil {
		t.Fatal(err)
	}
	// 5 refused, 2 answered, 3 refused, 1 and 2 answered.
	var sizes []int
	for _, st := range judge.states {
		sizes = append(sizes, len(st.Pairs))
	}
	if fmt.Sprint(sizes) != "[5 2 3 1 2]" {
		t.Errorf("batch sizes = %v", sizes)
	}
	if report.Requests != 3 || report.InputTokens != 500 || report.Model != "scripted-1" {
		t.Errorf("report = %+v; want three answered calls and the metered cost", report)
	}
	if len(verdicts) != 5 {
		t.Fatalf("%d verdicts", len(verdicts))
	}
	for i, v := range verdicts {
		if v.Pair != pairs[i] || v.Related != float64(i)/10 || v.Same != 0.05 {
			t.Errorf("verdict %d = %+v, want its own pair at %v", i, v, float64(i)/10)
		}
	}

	// One pair too large is an error, still recognisable as the refusal.
	if _, _, err := topics.NewPairJudge(refusingJudge{}).Judge(ctx, pairsOf(1)); !errors.Is(err, decide.ErrTooLarge) ||
		!strings.HasPrefix(err.Error(), "relate topics: ") {
		t.Errorf("error = %v, want ErrTooLarge wrapped", err)
	}

	// Batches of a given size, each naming its pairs from p0.
	judge = &scriptedJudge{related: related}
	if _, report, err := topics.NewPairJudge(judge, topics.WithBatchSize(2)).Judge(ctx, pairs); err != nil || report.Requests != 3 {
		t.Errorf("batched: %+v, %v", report, err)
	}
	if got := judge.states[2].Pairs; len(got) != 1 || got[0].Pair != "p0" {
		t.Errorf("last batch = %+v", got)
	}
}

type refusingJudge struct{}

func (refusingJudge) Decide(context.Context, any, map[string]decide.Question) (map[string]decide.Verdict, error) {
	return nil, decide.ErrTooLarge
}
func (refusingJudge) Name() string { return "refusing" }

func TestPairJudgeRefusesToFillInAMissingAnswer(t *testing.T) {
	judge := &scriptedJudge{drop: topics.SameQuestion + "p1"}
	verdicts, report, err := topics.NewPairJudge(judge).Judge(context.Background(), pairsOf(3))
	if err == nil || !strings.Contains(err.Error(), `no answer to "same_p1"`) || len(verdicts) != 0 {
		t.Errorf("got %v, %v; want an error naming the missing answer", verdicts, err)
	}
	if report.Requests != 1 {
		t.Errorf("requests = %d, want the answered call counted", report.Requests)
	}
}

func TestMeteredCostReachesStatsAndEdges(t *testing.T) {
	ctx := context.Background()
	store := notesGraph(t, map[string][]string{"doc-1": {"a topic", "b topic", "c topic"}})
	r := topics.NewRelater(store, &scriptedJudge{meter: true}, topics.WithGate(0.05))
	st, err := r.RelateAll(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if st.Pairs != 3 || st.Related != 3 || st.Judging.InputTokens != 300 || st.Judging.Model != "scripted-1" {
		t.Errorf("stats = %+v", st)
	}
	if total := r.Stats(); total != st {
		t.Errorf("Stats() = %+v, want %+v", total, st)
	}
	for k, p := range relatedEdges(t, store) {
		if p[topics.PropRelatedJudge] != "scripted-1" {
			t.Errorf("edge %s judge = %q, want the metered model", k, p[topics.PropRelatedJudge])
		}
	}
}

func TestCardQuestionsAndSourceNamesAreOptions(t *testing.T) {
	ctx := context.Background()
	store := notesGraph(t, map[string][]string{"doc-1": {"a topic", "b topic"}})
	var seenState any
	var seenAsk string
	judge := judgeFunc(func(_ context.Context, state any, qs map[string]decide.Question) (map[string]decide.Verdict, error) {
		seenState = state
		seenAsk = qs["related_p0"].Ask
		return map[string]decide.Verdict{"related_p0": decide.YesNoVerdict(0.7), "same_p0": decide.YesNoVerdict(0.2)}, nil
	})
	q := topics.Questions{
		Related: func(p string) decide.Question { return decide.YesNo("related "+p+"?", "yes", "no") },
		Same:    func(p string) decide.Question { return decide.YesNo("same "+p+"?", "yes", "no") },
	}
	r := topics.NewRelater(store, judge,
		topics.WithCard(func(pairs []*topics.Pair) any { return fmt.Sprintf("%d pairs", len(pairs)) }),
		topics.WithQuestions(q),
		topics.WithSourceNames(map[topics.Source]string{topics.SourceSharedUse: "same-file"}),
	)
	if _, err := r.RelateAll(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if seenState != "1 pairs" || seenAsk != "related p0?" {
		t.Errorf("judge saw %v, %q", seenState, seenAsk)
	}
	for _, p := range relatedEdges(t, store) {
		if p[topics.PropRelatedSources] != "same-file" || p[topics.PropRelatedProbability] != "0.70" {
			t.Errorf("edge = %v", p)
		}
	}

	state, questions := topics.NewPairJudge(nil, topics.WithQuestions(q)).Request(pairsOf(2))
	if state == nil || len(questions) != 4 || questions["same_p1"].Ask != "same p1?" {
		t.Errorf("Request = %v, %v", state, questions)
	}
}

type judgeFunc func(ctx context.Context, state any, qs map[string]decide.Question) (map[string]decide.Verdict, error)

func (f judgeFunc) Decide(ctx context.Context, state any, qs map[string]decide.Question) (map[string]decide.Verdict, error) {
	return f(ctx, state, qs)
}
func (judgeFunc) Name() string { return "func" }

// profileSet builds profiles by hand: vectors, said embeddings, parents.
func profileSet() (*topics.Profiles, map[string]*topics.Profile) {
	ps := topics.NewProfiles()
	by := map[string]*topics.Profile{}
	add := func(name string, vec []float32, parents ...string) *topics.Profile {
		p := ps.Ensure(&graph.Node{ID: topicID(name), Type: graph.NodeTopic, Name: name})
		p.Vector, p.Parents = vec, parents
		by[name] = p
		return p
	}
	add("a", []float32{1, 0, 0}, "small")
	add("b", []float32{0.95, 0.05, 0}, "small")
	add("c", []float32{0, 1, 0}, "big")
	add("d", []float32{0, 0.9, 0.1}, "big")
	add("e", nil, "big")
	by["c"].Said = [][]float32{{1, 1}}
	by["e"].Said = [][]float32{{1, 0.9}}
	ps.CountUse("doc-1", []*topics.Profile{by["a"], by["c"]})
	ps.AddAdjacent(by["b"].ID(), by["e"].ID())
	return ps, by
}

func pairNames(pairs []*topics.Pair) string {
	var out []string
	for _, p := range pairs {
		names := []string{p.A.Node.Name, p.B.Node.Name}
		sort.Strings(names)
		out = append(out, fmt.Sprintf("%s%s:%s/%d", names[0], names[1], p.Sources, p.NearestRank))
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

func TestGeneratorsAreOptions(t *testing.T) {
	for _, tt := range []struct {
		name string
		gens []topics.Generator
		want string
	}{
		{"nearest label", []topics.Generator{topics.NearestLabels(1)}, "ab:nearest/1 cd:nearest/1"},
		{"nearest said", []topics.Generator{topics.NearestSaid(1)}, "ce:said/0"},
		{"shared use", []topics.Generator{topics.SharedUse()}, "ac:shared-use/0"},
		{"adjacency", []topics.Generator{topics.Adjacency()}, "be:adjacent/0"},
		{"small families", []topics.Generator{topics.SharedParent(2)}, "ab:shared-parent/0"},
		{"families of three", []topics.Generator{topics.SharedParent(3)}, "ab:shared-parent/0 cd:shared-parent/0 ce:shared-parent/0 de:shared-parent/0"},
		{"union", topics.DefaultGenerators(1), "ab:nearest+shared-parent/1 ac:shared-use/0 be:adjacent/0 cd:nearest+shared-parent/1 ce:said+shared-parent/0 de:shared-parent/0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ps, _ := profileSet()
			if got := pairNames(ps.Candidates(tt.gens...)); got != tt.want {
				t.Errorf("candidates = %s\nwant         %s", got, tt.want)
			}
		})
	}

	ps, by := profileSet()
	p := ps.Pair(by["c"], by["a"])
	if p.A.ID() > p.B.ID() || p.Shared != 1 || p.Cosine != 0 {
		t.Errorf("pair = %+v", p)
	}
	if sib := ps.Siblings(by["c"], 3); len(sib) != 2 {
		t.Errorf("siblings of c = %d", len(sib))
	}
	if s := (topics.SourceSaid | topics.SourceNearest | topics.SourceSharedUse).Label(map[topics.Source]string{topics.SourceSharedUse: "same-meeting"}); s != "nearest+said+same-meeting" {
		t.Errorf("label = %s", s)
	}
	if topics.Source(0).String() != "none" {
		t.Error("an empty source set is not named none")
	}
}

func TestProfilesCountEachUseOnce(t *testing.T) {
	ps, by := profileSet()
	ps.NoteUse("doc-1", []*topics.Profile{by["a"], by["c"]})
	ps.NoteUse("doc-2", []*topics.Profile{by["a"], by["c"]})
	ps.NoteUse("doc-3", []*topics.Profile{by["d"], by["d"]})
	if got := ps.Shared(by["a"].ID(), by["c"].ID()); got != 2 {
		t.Errorf("shared = %d, want doc-1 counted once and doc-2 once", got)
	}
	if got := ps.Shared(by["d"].ID(), by["d"].ID()); got != 0 {
		t.Errorf("a topic shares %d uses with itself", got)
	}
	if got := ps.Adjacent(by["e"].ID(), by["b"].ID()); got != 1 {
		t.Errorf("adjacent = %d", got)
	}
}

func TestLoadProfilesReadsWhatIsFiledUnderTopics(t *testing.T) {
	ctx := context.Background()
	store := notesGraph(t, map[string][]string{
		"doc-1": {"auth", "billing"},
		"doc-2": {"auth"},
	})
	parent := &graph.Node{ID: topicID("platform"), Type: graph.NodeTopic, Name: "platform"}
	if err := store.AddNode(ctx, parent); err != nil {
		t.Fatal(err)
	}
	if err := store.AddEdge(ctx, &graph.Edge{ID: "c1", Type: graph.EdgeContains, SourceID: parent.ID, TargetID: topicID("auth")}); err != nil {
		t.Fatal(err)
	}
	ps, err := topics.LoadProfiles(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	// The parent has nothing filed under it, so it is not a profile.
	if ps.Len() != 2 {
		t.Fatalf("%d profiles", ps.Len())
	}
	auth := ps.Get(topicID("auth"))
	if len(auth.Uses) != 2 || len(auth.Summaries) != 2 || auth.Summaries[0] != "What doc-1 says." || auth.SaidIDs[1] != "doc-2" {
		t.Errorf("auth = %+v", auth)
	}
	if len(auth.Parents) != 1 || auth.Parents[0] != parent.ID {
		t.Errorf("parents = %v", auth.Parents)
	}
	if ps.Shared(topicID("auth"), topicID("billing")) != 1 {
		t.Error("shared use not counted")
	}
}

func TestNoGeneratorsProposeNothing(t *testing.T) {
	store := notesGraph(t, map[string][]string{"doc-1": {"a topic", "b topic"}})
	r := topics.NewRelater(store, &scriptedJudge{}, topics.WithGenerators())
	if n, err := r.Pending(context.Background()); err != nil || n != 0 {
		t.Errorf("pending = %d (%v), want nothing proposed", n, err)
	}
}

// TestRelaterOptionsReachTheirSeams drives an incremental relate through a
// caller's own loader, embedder, embed text and said text: a new topic is
// embedded from the text the caller chose, a failure to embed another is
// logged rather than fatal, and siblings under a small parent are proposed.
func TestRelaterOptionsReachTheirSeams(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	add := func(n *graph.Node) {
		if err := store.AddNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	parent := &graph.Node{ID: topicID("billing"), Type: graph.NodeTopic, Name: "billing"}
	old := &graph.Node{ID: topicID("invoices"), Type: graph.NodeTopic, Name: "invoices"}
	fresh := &graph.Node{ID: topicID("refunds"), Type: graph.NodeTopic, Name: "refunds", DocComment: "money back"}
	broken := &graph.Node{ID: topicID("chargebacks"), Type: graph.NodeTopic, Name: "chargebacks"}
	for _, n := range []*graph.Node{parent, old, fresh, broken} {
		add(n)
	}
	for _, child := range []string{old.ID, fresh.ID} {
		if err := store.AddEdge(ctx, &graph.Edge{ID: "c-" + child, Type: graph.EdgeContains, SourceID: parent.ID, TargetID: child}); err != nil {
			t.Fatal(err)
		}
	}

	loaded := false
	var embedded []string
	var logged []string
	judge := &scriptedJudge{}
	r := topics.NewRelater(store, judge,
		topics.WithProfiles(func(ctx context.Context, s graph.Store) (*topics.Profiles, error) {
			loaded = true
			ps := topics.NewProfiles()
			p := ps.Ensure(old)
			p.Parents = []string{parent.ID}
			p.Vector = []float32{1, 0}
			return ps, nil
		}),
		topics.WithGenerators(topics.NearestLabels(5), topics.SharedParent(0)),
		topics.WithConcurrency(2),
		topics.WithEmbedText(func(n *graph.Node) string { return "label: " + n.Name }),
		topics.WithEmbed(func(_ context.Context, text string) ([]float32, error) {
			embedded = append(embedded, text)
			if strings.Contains(text, "chargebacks") {
				return nil, errors.New("embedder down")
			}
			return []float32{0.9, 0.1}, nil
		}),
		topics.WithSaidText(func(n *graph.Node) string { return n.DocComment }),
		topics.WithLog(func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }),
	)
	note := &graph.Node{ID: "note-1", Type: "Note", Name: "note", DocComment: "We refund within a week."}
	st, err := r.Relate(ctx, note.ID, []*graph.Node{fresh, broken}, map[string]*graph.Node{fresh.ID: note})
	if err != nil {
		t.Fatal(err)
	}
	if !loaded {
		t.Error("the caller's loader was not used")
	}
	// What the note said is placed too, as well as each new label.
	if strings.Join(embedded, ";") != "label: note;label: refunds;label: chargebacks" {
		t.Errorf("embedded %q", embedded)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "could not embed") {
		t.Errorf("logged %q", logged)
	}
	// Refunds' nearest label and its sibling, one pair proposed twice.
	// Chargebacks has no vector and no parent, and without SharedUse among
	// the generators the note's own pair is not proposed.
	if st.Pairs != 1 {
		t.Errorf("judged %d pairs: %v", st.Pairs, judge.batches)
	}
	edges := relatedEdges(t, store)
	if p := edges[labels("invoices", "refunds")]; p[topics.PropRelatedSources] != "nearest+shared-parent" {
		t.Errorf("sibling edge = %v", p)
	}
	for _, c := range judge.states[0].Topics {
		if c.Label == "refunds" && (len(c.WhatWasSaid) != 1 || c.WhatWasSaid[0] != "We refund within a week.") {
			t.Errorf("what was said = %q", c.WhatWasSaid)
		}
	}
}
