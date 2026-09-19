package transcript

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// PersonRegistry resolves a name spoken in one meeting to a durable person in
// the graph.
//
// This is where identity stops being per-meeting. Each recording yields names
// as the transcriber heard them that day, so the same colleague arrives as
// "Imran" from one meeting and "Imron" from another. Without a registry the
// graph accumulates a person per spelling, and the question "what has Imran
// committed to?" silently returns a fraction of the answer.
//
// Person nodes are global — keyed on name with no file path — which is the
// same convention face recognition uses. A person identified by voice in a
// meeting and by face in a photograph therefore converge on one node.
type PersonRegistry struct {
	store graph.Store
	// byNormalized indexes people by their normalized name and by every alias
	// recorded for them.
	byNormalized map[string]*graph.Node
	// people is the distinct set, used for fuzzy matching.
	people []*graph.Node
	// created counts people added during this run.
	created int
}

// LoadPersonRegistry reads the people already in the graph.
func LoadPersonRegistry(ctx context.Context, store graph.Store) (*PersonRegistry, error) {
	nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodePerson})
	if err != nil {
		return nil, fmt.Errorf("query people: %w", err)
	}

	r := &PersonRegistry{
		store:        store,
		byNormalized: make(map[string]*graph.Node, len(nodes)*2),
	}
	for _, n := range nodes {
		r.index(n)
	}
	return r, nil
}

// index records a person under their name and all known aliases.
func (r *PersonRegistry) index(n *graph.Node) {
	if _, seen := r.byNormalized[NormalizeName(n.Name)]; !seen {
		r.people = append(r.people, n)
	}
	r.byNormalized[NormalizeName(n.Name)] = n
	for _, alias := range aliasesOf(n) {
		r.byNormalized[NormalizeName(alias)] = n
	}
}

// aliasesOf returns the alternate spellings recorded for a person.
func aliasesOf(n *graph.Node) []string {
	if n.Properties == nil {
		return nil
	}
	raw := n.Properties[graph.PropAliases]
	if raw == "" {
		return nil
	}
	var out []string
	for _, a := range strings.Split(raw, ",") {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// Created reports how many new people this registry added.
func (r *PersonRegistry) Created() int { return r.created }

// Resolve returns the person a name refers to, creating them if they are new.
//
// A spelling that differs from the one on file is recorded as an alias rather
// than becoming a second person, so later meetings resolve it directly.
func (r *PersonRegistry) Resolve(ctx context.Context, name string) (*graph.Node, error) {
	cleaned := CleanName(name)
	if cleaned == "" {
		return nil, fmt.Errorf("not a usable person name: %q", name)
	}
	norm := NormalizeName(cleaned)

	// Exact match on a name or a known alias.
	if n, ok := r.byNormalized[norm]; ok {
		return n, nil
	}

	// A transcription variant of someone already known.
	if existing := r.fuzzyMatch(cleaned); existing != nil {
		if err := r.addAlias(ctx, existing, cleaned); err != nil {
			return nil, err
		}
		return existing, nil
	}

	node := &graph.Node{
		ID:            graph.NewNodeID(string(graph.NodePerson), "", cleaned),
		Type:          graph.NodePerson,
		Name:          cleaned,
		QualifiedName: cleaned,
	}
	if err := r.store.AddNode(ctx, node); err != nil {
		return nil, fmt.Errorf("add person %q: %w", cleaned, err)
	}
	r.index(node)
	r.created++
	return node, nil
}

// fuzzyMatch finds an existing person whose name is the same as this one, up
// to the edits a transcriber makes.
//
// Where several people match — which real name sets make possible — the
// longest-established spelling wins, so resolution stays stable between runs
// rather than depending on map iteration order.
func (r *PersonRegistry) fuzzyMatch(name string) *graph.Node {
	var matches []*graph.Node
	for _, p := range r.people {
		if SameName(p.Name, name) {
			matches = append(matches, p)
		}
		for _, alias := range aliasesOf(p) {
			if SameName(alias, name) {
				matches = append(matches, p)
				break
			}
		}
	}
	if len(matches) == 0 {
		return nil
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Name < matches[j].Name })
	return matches[0]
}

// addAlias records an alternate spelling on a person.
func (r *PersonRegistry) addAlias(ctx context.Context, n *graph.Node, alias string) error {
	if NormalizeName(n.Name) == NormalizeName(alias) {
		return nil
	}
	for _, existing := range aliasesOf(n) {
		if NormalizeName(existing) == NormalizeName(alias) {
			return nil
		}
	}

	aliases := append(aliasesOf(n), alias)
	sort.Strings(aliases)
	if n.Properties == nil {
		n.Properties = make(map[string]string)
	}
	n.Properties[graph.PropAliases] = strings.Join(aliases, ",")

	if err := r.store.UpdateNode(ctx, n); err != nil {
		return fmt.Errorf("record alias %q for %q: %w", alias, n.Name, err)
	}
	r.byNormalized[NormalizeName(alias)] = n
	return nil
}

// MarkOwner flags a person as the one whose microphone made the recordings.
func (r *PersonRegistry) MarkOwner(ctx context.Context, n *graph.Node) error {
	if n.Properties != nil && n.Properties[graph.PropIsOwner] == "true" {
		return nil
	}
	if n.Properties == nil {
		n.Properties = make(map[string]string)
	}
	n.Properties[graph.PropIsOwner] = "true"
	if err := r.store.UpdateNode(ctx, n); err != nil {
		return fmt.Errorf("mark owner %q: %w", n.Name, err)
	}
	return nil
}
