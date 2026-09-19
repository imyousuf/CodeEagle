package linker

import (
	"context"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// linkMeetingMentions connects meetings to the code they discussed.
//
// Enrichment records what a meeting referred to — "the schema service", "the
// MCP proxy" — as plain text, because at that point only the transcript is in
// view. Resolving those names against the indexed codebase is what turns a
// pile of meeting notes into part of the code graph, so that "which meetings
// discussed the auth service?" and "what did we decide about this package?"
// have answers.
//
// Matching is exact on a normalized name rather than fuzzy. A spurious link
// between a meeting and a package is worse than a missing one: it is invisible
// in the graph and quietly misleads anything that traverses it.
func (l *Linker) linkMeetingMentions(ctx context.Context) (int, error) {
	meetings, err := l.store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeMeeting})
	if err != nil {
		return 0, err
	}
	if len(meetings) == 0 {
		return 0, nil
	}

	index, err := l.buildMentionIndex(ctx)
	if err != nil {
		return 0, err
	}
	if len(index) == 0 {
		return 0, nil
	}

	linked := 0
	for _, m := range meetings {
		for _, mention := range splitMentions(m.Properties["mentions"]) {
			for _, targetID := range index[mention] {
				if targetID == m.ID {
					continue
				}
				edge := &graph.Edge{
					ID:       graph.NewNodeID("edge", m.ID, targetID+":"+string(graph.EdgeMentions)),
					Type:     graph.EdgeMentions,
					SourceID: m.ID,
					TargetID: targetID,
					Properties: map[string]string{
						// Keep the phrase that produced the link, so a reader
						// can judge whether it is really the same thing.
						"mention": mention,
					},
				}
				if err := l.store.AddEdge(ctx, edge); err != nil {
					if l.verbose {
						l.log("  Warning: add meeting mention edge: %v", err)
					}
					continue
				}
				linked++
			}
		}
	}

	segmentLinks, err := l.linkTopicSegmentMentions(ctx, index)
	if err != nil {
		return linked, err
	}
	return linked + segmentLinks, nil
}

// linkTopicSegmentMentions links individual topic segments to code, using the
// keywords enrichment produced for them. This is finer-grained than the
// meeting-level link: it says which *part* of a meeting concerned a service.
func (l *Linker) linkTopicSegmentMentions(ctx context.Context, index map[string][]string) (int, error) {
	segments, err := l.store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeTopicSegment})
	if err != nil {
		return 0, err
	}

	linked := 0
	for _, seg := range segments {
		terms := splitMentions(seg.Properties["keywords"])
		// The topic's own name is as good a search term as its keywords.
		terms = append(terms, normalizeMention(seg.Name))

		seen := make(map[string]bool)
		for _, term := range terms {
			for _, targetID := range index[term] {
				if targetID == seg.ID || seen[targetID] {
					continue
				}
				seen[targetID] = true
				edge := &graph.Edge{
					ID:       graph.NewNodeID("edge", seg.ID, targetID+":"+string(graph.EdgeMentions)),
					Type:     graph.EdgeMentions,
					SourceID: seg.ID,
					TargetID: targetID,
					Properties: map[string]string{
						"mention": term,
					},
				}
				if err := l.store.AddEdge(ctx, edge); err != nil {
					continue
				}
				linked++
			}
		}
	}
	return linked, nil
}

// mentionableTypes are the code entities a meeting plausibly discusses by
// name. Functions and methods are excluded: people say "the handler" or "that
// function", not an identifier, and matching on short symbol names produces
// far more noise than signal.
var mentionableTypes = []graph.NodeType{
	graph.NodeRepository,
	graph.NodeService,
	graph.NodeModule,
	graph.NodePackage,
	graph.NodeDependency,
	graph.NodeAPIEndpoint,
	graph.NodeDBModel,
}

// minMentionLength is the shortest phrase allowed to create a link. Short
// tokens ("api", "db", "ui") appear in every conversation and match many
// unrelated entities.
const minMentionLength = 4

// buildMentionIndex maps normalized entity names to the nodes they name.
func (l *Linker) buildMentionIndex(ctx context.Context) (map[string][]string, error) {
	index := make(map[string][]string)
	for _, nt := range mentionableTypes {
		nodes, err := l.store.QueryNodes(ctx, graph.NodeFilter{Type: nt})
		if err != nil {
			continue
		}
		for _, n := range nodes {
			for _, key := range mentionKeys(n.Name) {
				index[key] = append(index[key], n.ID)
			}
		}
	}

	// A name shared by many entities identifies none of them.
	for key, ids := range index {
		if len(ids) > 3 {
			delete(index, key)
		}
	}
	return index, nil
}

// mentionKeys returns the forms under which an entity name may be spoken.
// "schema-service" is said as "schema service" and often just as "schema".
func mentionKeys(name string) []string {
	base := normalizeMention(name)
	if len(base) < minMentionLength {
		return nil
	}
	keys := []string{base}

	// Drop a role suffix people usually omit in conversation.
	for _, suffix := range []string{" service", " server", " api", " client", " lib", " sdk"} {
		if strings.HasSuffix(base, suffix) {
			if trimmed := strings.TrimSuffix(base, suffix); len(trimmed) >= minMentionLength {
				keys = append(keys, trimmed)
			}
			break
		}
	}
	return keys
}

// normalizeMention reduces a phrase to a comparison key: lowercase, with
// separators turned into single spaces.
func normalizeMention(s string) string {
	var b strings.Builder
	prevSpace := true
	for _, r := range strings.TrimSpace(strings.ToLower(s)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevSpace = false
		case r == ' ' || r == '-' || r == '_' || r == '.' || r == '/':
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// splitMentions parses a comma-separated property into normalized keys.
func splitMentions(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		key := normalizeMention(part)
		if len(key) >= minMentionLength {
			out = append(out, key)
		}
	}
	return out
}
