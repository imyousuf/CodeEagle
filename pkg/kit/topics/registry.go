package topics

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
)

// Properties a registry keeps on a Topic node.
const (
	// PropTopicUses counts how many times a topic has been resolved, so the
	// vocabulary offered back to a model leads with the labels that have
	// proven reusable.
	PropTopicUses = "uses"

	// PropTopicLevel names a topic's layer: a subject as something named
	// it, or a concept induced over others.
	PropTopicLevel = "topic_level"
	// LevelSubject is a topic as something phrased it.
	LevelSubject = "subject"
	// LevelTheme is a grouping other topics sit under.
	LevelTheme = "theme"
	// PropTopicDepth is how far above the leaves a topic sits: 0 for a
	// subject, 1 for a concept grouping subjects, and so on.
	PropTopicDepth = "topic_depth"
)

// Registry resolves a subject named in one place to a durable topic in the
// graph.
//
// A model names the subject of each document or meeting in its own words, so
// four meetings about the same thing produce "MCP server vs OAuth server
// architecture", "MCP endpoint, GCP hosting and JWT/OAuth server
// abstraction", "SSO server and auth path discussion" and "OBO / user-level
// token concern". Keyed on the exact string, those are four unrelated nodes,
// and "what discussed MCP auth?" has no answer -- the topic layer becomes
// decoration rather than structure.
//
// Collapsing rewordings is what turns HasTopic into an index. The wording
// each use chose is not lost: it is recorded on the topic as another
// wording, and whatever was said stays on the node that said it.
//
// A Registry is safe for concurrent use.
type Registry struct {
	mu    sync.RWMutex
	store graph.Store
	// byNormalized indexes topics by their normalized name and by every
	// alternate wording seen for them.
	byNormalized map[string]*graph.Node
	// topics is the distinct set, used for close matching.
	topics  []*graph.Node
	created int
}

// LoadRegistry reads the topics already in the graph.
func LoadRegistry(ctx context.Context, store graph.Store) (*Registry, error) {
	nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeTopic})
	if err != nil {
		return nil, fmt.Errorf("query topics: %w", err)
	}

	r := &Registry{
		store:        store,
		byNormalized: make(map[string]*graph.Node, len(nodes)*2),
	}
	for _, n := range nodes {
		r.index(n)
	}
	return r, nil
}

func (r *Registry) index(n *graph.Node) {
	key := NormalizeName(n.Name)
	if _, seen := r.byNormalized[key]; !seen {
		r.topics = append(r.topics, n)
	}
	r.byNormalized[key] = n
	for _, alias := range Aliases(n) {
		r.byNormalized[NormalizeName(alias)] = n
	}
}

// Created reports how many new topics this registry added.
func (r *Registry) Created() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.created
}

// Vocabulary lists the topics in use, most-used first.
//
// Feed it back into extraction as a batch goes, so a subject named in
// January is an available label by March. This is what actually makes
// topics converge: matching after the fact can only catch wordings that are
// already close, while offering the existing vocabulary stops the divergence
// happening.
func (r *Registry) Vocabulary(limit int) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	type scored struct {
		name string
		uses int
	}
	list := make([]scored, 0, len(r.topics))
	for _, t := range r.topics {
		uses, _ := strconv.Atoi(t.Properties[PropTopicUses])
		list = append(list, scored{name: t.Name, uses: uses})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].uses != list[j].uses {
			return list[i].uses > list[j].uses
		}
		return list[i].name < list[j].name
	})

	if limit > 0 && len(list) > limit {
		list = list[:limit]
	}
	out := make([]string, 0, len(list))
	for _, s := range list {
		out = append(out, s.name)
	}
	return out
}

// PromoteToTheme records that a topic is a concept others sit under, and
// returns a copy to persist if that changed anything.
//
// The nodes the registry holds are read concurrently: extraction workers
// call Vocabulary while building their prompts while a writer marks levels
// as it places topics. Mutating a node's property map outside the
// registry's lock is a data race on that map, which Go traps and turns into
// an unrecoverable process abort rather than an error the caller could
// survive. The caller is handed a snapshot to write so that serializing it
// does not read the live map either.
func (r *Registry) PromoteToTheme(node *graph.Node) (*graph.Node, bool) {
	return r.markLevel(node, LevelTheme, "1", func(current string) bool {
		return current != LevelTheme
	})
}

// DefaultToSubject records that a topic is a leaf, unless it already has a
// place in the hierarchy.
func (r *Registry) DefaultToSubject(node *graph.Node) (*graph.Node, bool) {
	return r.markLevel(node, LevelSubject, "0", func(current string) bool {
		return current == ""
	})
}

func (r *Registry) markLevel(node *graph.Node, level, depth string, when func(string) bool) (*graph.Node, bool) {
	if node == nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if node.Properties == nil {
		node.Properties = make(map[string]string)
	}
	if !when(node.Properties[PropTopicLevel]) {
		return nil, false
	}
	node.Properties[PropTopicLevel] = level
	node.Properties[PropTopicDepth] = depth

	snapshot := *node
	snapshot.Properties = make(map[string]string, len(node.Properties))
	for k, v := range node.Properties {
		snapshot.Properties[k] = v
	}
	return &snapshot, true
}

// Resolve returns the topic a subject refers to, creating it if new, and
// counts one use of it. An alternate wording is recorded on the topic rather
// than becoming a second one, so the next use that phrases it that way
// resolves directly.
func (r *Registry) Resolve(ctx context.Context, name string) (*graph.Node, error) {
	clean := CleanTopic(name)
	if clean == "" {
		return nil, fmt.Errorf("not a usable topic: %q", name)
	}
	key := NormalizeName(clean)

	r.mu.Lock()
	defer r.mu.Unlock()

	if n, ok := r.byNormalized[key]; ok {
		r.noteUse(ctx, n)
		return n, nil
	}
	if existing := r.closeMatch(clean); existing != nil {
		if err := r.addWording(ctx, existing, clean); err != nil {
			return nil, err
		}
		r.noteUse(ctx, existing)
		return existing, nil
	}

	node := &graph.Node{
		ID:            graph.NewNodeID(string(graph.NodeTopic), "", clean),
		Type:          graph.NodeTopic,
		Name:          clean,
		QualifiedName: clean,
		Properties:    map[string]string{PropTopicUses: "1"},
	}
	if err := r.store.AddNode(ctx, node); err != nil {
		return nil, fmt.Errorf("add topic %q: %w", clean, err)
	}
	r.index(node)
	r.created++
	return node, nil
}

// Attach files the node nodeID under the topic a label names, resolving the
// label as Resolve does, and writes the HasTopic edge. Any node can carry
// topics: a document, a section, a meeting, a fact. Each call counts a use,
// so attach a node to a label once per time it is indexed.
func (r *Registry) Attach(ctx context.Context, nodeID, label string) (*graph.Node, error) {
	topic, err := r.Resolve(ctx, label)
	if err != nil {
		return nil, err
	}
	if err := r.store.AddEdge(ctx, hasTopicEdge(nodeID, topic.ID)); err != nil {
		return nil, fmt.Errorf("add HasTopic edge: %w", err)
	}
	return topic, nil
}

// noteUse increments how many times a topic has been used.
func (r *Registry) noteUse(ctx context.Context, n *graph.Node) {
	if n.Properties == nil {
		n.Properties = make(map[string]string)
	}
	uses, _ := strconv.Atoi(n.Properties[PropTopicUses])
	n.Properties[PropTopicUses] = strconv.Itoa(uses + 1)
	// A failed counter update is not worth failing an index for; it only
	// affects the order topics are offered back in.
	_ = r.store.UpdateNode(ctx, n)
}

// closeMatch finds an existing topic that means the same thing.
func (r *Registry) closeMatch(name string) *graph.Node {
	var matches []*graph.Node
	for _, t := range r.topics {
		if SameTopic(t.Name, name) {
			matches = append(matches, t)
			continue
		}
		for _, alias := range Aliases(t) {
			if SameTopic(alias, name) {
				matches = append(matches, t)
				break
			}
		}
	}
	if len(matches) == 0 {
		return nil
	}
	// Prefer the most-used label, so the vocabulary converges on the wording
	// that has already proven reusable rather than on whichever was seen first.
	sort.Slice(matches, func(i, j int) bool {
		ui, _ := strconv.Atoi(matches[i].Properties[PropTopicUses])
		uj, _ := strconv.Atoi(matches[j].Properties[PropTopicUses])
		if ui != uj {
			return ui > uj
		}
		return matches[i].Name < matches[j].Name
	})
	return matches[0]
}

// addWording records an alternate phrasing on a topic.
func (r *Registry) addWording(ctx context.Context, n *graph.Node, wording string) error {
	if NormalizeName(n.Name) == NormalizeName(wording) {
		return nil
	}
	for _, existing := range Aliases(n) {
		if NormalizeName(existing) == NormalizeName(wording) {
			return nil
		}
	}

	wordings := append(Aliases(n), wording)
	sort.Strings(wordings)
	if n.Properties == nil {
		n.Properties = make(map[string]string)
	}
	n.Properties[graph.PropAliases] = strings.Join(wordings, ",")

	if err := r.store.UpdateNode(ctx, n); err != nil {
		return fmt.Errorf("record wording %q for topic %q: %w", wording, n.Name, err)
	}
	r.byNormalized[NormalizeName(wording)] = n
	return nil
}

// --- topic text handling ---

// maxTopicWords bounds a usable topic label. A longer phrase is a
// description of one use rather than a subject, and nothing else will ever
// phrase it the same way.
const maxTopicWords = 6

// CleanTopic normalizes a topic label for use as a shared subject.
//
// Models given a free-text field write summaries into it -- "Standup: Kamur
// updates on remember/recall tools and code execution DNS issue". Trimming
// the leading clause off such a label recovers the subject often enough to
// be worth doing, and the full wording stays wherever the caller keeps it.
func CleanTopic(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	// A descriptive label usually front-loads its subject and then explains:
	// keep the part before the first separator.
	for _, sep := range []string{": ", " — ", " – ", " - ", "; "} {
		if i := strings.Index(s, sep); i > 0 {
			s = s[:i]
			break
		}
	}
	// A list of subjects is not one subject; keep the first.
	if i := strings.Index(s, ", "); i > 0 && len(strings.Fields(s)) > maxTopicWords {
		s = s[:i]
	}

	s = strings.Trim(s, " .,:;-–—")
	if s == "" {
		return ""
	}
	if len(strings.Fields(s)) > maxTopicWords {
		s = strings.Join(strings.Fields(s)[:maxTopicWords], " ")
	}
	return s
}

// topicStopWords are words that carry no subject meaning, so two labels that
// differ only by them are the same subject.
var topicStopWords = map[string]bool{
	"the": true, "a": true, "an": true, "and": true, "or": true, "of": true,
	"for": true, "to": true, "in": true, "on": true, "with": true, "vs": true,
	"versus": true, "about": true, "discussion": true, "update": true,
	"updates": true, "status": true, "review": true, "planning": true,
	"topic": true, "general": true, "misc": true, "other": true,
}

// topicKey reduces a label to its meaningful words, singularized and sorted,
// so wording and order stop mattering.
func topicKey(s string) string {
	words := strings.Fields(NormalizeName(s))
	kept := make([]string, 0, len(words))
	for _, w := range words {
		if topicStopWords[w] {
			continue
		}
		kept = append(kept, singularize(w))
	}
	if len(kept) == 0 {
		return NormalizeName(s)
	}
	sort.Strings(kept)
	return strings.Join(kept, " ")
}

// singularize strips a trailing plural "s", which is the only inflection that
// separates topic labels often enough to be worth handling.
func singularize(w string) string {
	if len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
		return strings.TrimSuffix(w, "s")
	}
	return w
}

// SameTopicThreshold is the similarity above which two labels are one
// subject. It is high because over-merging is the worse error: two distinct
// subjects fused together silently mix unrelated things under one heading,
// while two labels for one subject stay visible and correctable.
const SameTopicThreshold = 0.93

// SameTopic reports whether two labels name the same subject: the same
// meaningful words in any order and either number, one abbreviating the
// other word for word, or a Jaro-Winkler similarity of those words at or
// above SameTopicThreshold.
func SameTopic(a, b string) bool {
	na, nb := NormalizeName(a), NormalizeName(b)
	if na == "" || nb == "" {
		return false
	}
	if na == nb {
		return true
	}
	// Same meaningful words, in any order and either number.
	if topicKey(a) == topicKey(b) {
		return true
	}
	// One label abbreviating the other ("mcp auth" / "mcp authentication").
	if abbreviates(topicKey(a), topicKey(b)) || abbreviates(topicKey(b), topicKey(a)) {
		return true
	}
	return JaroWinkler(topicKey(a), topicKey(b)) >= SameTopicThreshold
}

// abbreviates reports whether every word of short is a prefix of the matching
// word in long, with the same number of words.
func abbreviates(short, long string) bool {
	sw, lw := strings.Fields(short), strings.Fields(long)
	if len(sw) == 0 || len(sw) != len(lw) {
		return false
	}
	sawShortening := false
	for i := range sw {
		if sw[i] == lw[i] {
			continue
		}
		// An abbreviation is a real prefix of at least three characters.
		if len(sw[i]) >= 3 && strings.HasPrefix(lw[i], sw[i]) {
			sawShortening = true
			continue
		}
		return false
	}
	return sawShortening
}
