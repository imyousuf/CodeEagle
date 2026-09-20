package transcript

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// MeetingIndex holds every indexed meeting so that lookups by session
// identifier, node id, prefix or title cost nothing after the first load.
//
// Commands that resolve a meeting per row — one per follow-up, one per
// unidentified speaker — used to rescan the whole meeting list each time,
// which turned a listing of 1,500 follow-ups into a 25-second wait.
type MeetingIndex struct {
	all       []*graph.Node
	byID      map[string]*graph.Node
	bySession map[string][]*graph.Node
}

// LoadMeetingIndex reads the meetings from the graph.
func LoadMeetingIndex(ctx context.Context, store graph.Store) (*MeetingIndex, error) {
	meetings, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeMeeting})
	if err != nil {
		return nil, fmt.Errorf("query meetings: %w", err)
	}
	ix := &MeetingIndex{
		all:       meetings,
		byID:      make(map[string]*graph.Node, len(meetings)),
		bySession: make(map[string][]*graph.Node, len(meetings)),
	}
	for _, m := range meetings {
		ix.byID[m.ID] = m
		ix.bySession[m.QualifiedName] = append(ix.bySession[m.QualifiedName], m)
	}
	return ix, nil
}

// All returns every meeting, in no particular order.
func (ix *MeetingIndex) All() []*graph.Node { return ix.all }

// Len reports how many meetings are indexed.
func (ix *MeetingIndex) Len() int { return len(ix.all) }

// BySession returns the meeting recorded under a session identifier: nil when
// there is none, and an error when several recordings share it.
//
// A session identifier is the recording's own, which for formats that carry
// none is derived from the filename. Two unrelated exports named alike
// therefore answer to one identifier, and threading a series or a follow-up
// onto whichever came first would attach it to the wrong meeting silently.
func (ix *MeetingIndex) BySession(id string) (*graph.Node, error) {
	if id == "" {
		return nil, nil
	}
	switch matches := ix.bySession[id]; len(matches) {
	case 0:
		return nil, nil
	case 1:
		return matches[0], nil
	default:
		return nil, ambiguousMeetings(id, matches)
	}
}

// minPrefixLen is the shortest reference treated as the start of an
// identifier. Below it, almost any string is a prefix of something.
const minPrefixLen = 4

// Resolve finds one meeting from a reference a person would type: a node id,
// a session identifier, the first characters of either — session directories
// are named by the identifier, so its prefix is what people copy — or a
// fragment of the title.
//
// Nothing is ever picked silently. A reference that several meetings answer
// to is reported with enough of each to tell them apart.
func (ix *MeetingIndex) Resolve(ref string) (*graph.Node, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errors.New("no meeting reference given")
	}
	if m, ok := ix.byID[ref]; ok {
		return m, nil
	}
	if exact := ix.bySession[ref]; len(exact) == 1 {
		return exact[0], nil
	} else if len(exact) > 1 {
		return nil, ambiguousMeetings(ref, exact)
	}

	lower := strings.ToLower(ref)
	var candidates []*graph.Node
	seen := make(map[string]bool)
	add := func(m *graph.Node) {
		if !seen[m.ID] {
			seen[m.ID] = true
			candidates = append(candidates, m)
		}
	}
	if len(ref) >= minPrefixLen {
		for _, m := range ix.all {
			if strings.HasPrefix(strings.ToLower(m.QualifiedName), lower) ||
				strings.HasPrefix(strings.ToLower(m.ID), lower) {
				add(m)
			}
		}
	}
	for _, m := range ix.all {
		if strings.Contains(strings.ToLower(m.Name), lower) {
			add(m)
		}
	}

	switch len(candidates) {
	case 0:
		return nil, fmt.Errorf("no meeting matching %q", ref)
	case 1:
		return candidates[0], nil
	default:
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].UpdatedAt.After(candidates[j].UpdatedAt)
		})
		var b strings.Builder
		fmt.Fprintf(&b, "%d meetings match %q:", len(candidates), ref)
		for i, m := range candidates {
			if i >= 10 {
				fmt.Fprintf(&b, "\n  ... and %d more", len(candidates)-10)
				break
			}
			fmt.Fprintf(&b, "\n  %s  %s  %s", m.UpdatedAt.Format("2006-01-02"), m.QualifiedName, m.Name)
		}
		return nil, errors.New(b.String())
	}
}

// ambiguousMeetings reports several meetings answering to one identifier,
// listing enough of each to tell them apart.
func ambiguousMeetings(ref string, matches []*graph.Node) error {
	sorted := append([]*graph.Node(nil), matches...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].FilePath < sorted[j].FilePath })

	var b strings.Builder
	fmt.Fprintf(&b, "%d meetings answer to %q; use the full id to choose one:", len(sorted), ref)
	for _, m := range sorted {
		fmt.Fprintf(&b, "\n  %s  %s", m.ID, m.Name)
		if m.FilePath != "" {
			fmt.Fprintf(&b, "  (%s)", m.FilePath)
		}
	}
	return errors.New(b.String())
}

// ShortID is the part of a session identifier a person needs to type: the
// first eight characters, which for the UUIDs recorders assign is the name
// of the session directory as it appears in a listing.
func ShortID(m *graph.Node) string {
	id := m.QualifiedName
	if id == "" {
		id = m.ID
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// Attendees returns the names of the people identified in a meeting, sorted.
func Attendees(ctx context.Context, store graph.Store, meetingID string) ([]string, error) {
	people, err := store.GetNeighbors(ctx, meetingID, graph.EdgeAttended, graph.Incoming)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(people))
	for _, p := range people {
		names = append(names, p.Name)
	}
	sort.Strings(names)
	return names, nil
}

// Unidentified counts the voices in a meeting that were people but could not
// be named. Background audio is not a person and is not counted.
func Unidentified(ctx context.Context, store graph.Store, meetingID string) int {
	children, err := store.GetNeighbors(ctx, meetingID, graph.EdgeContains, graph.Outgoing)
	if err != nil {
		return 0
	}
	n := 0
	for _, c := range children {
		if c.Type != graph.NodeSpeaker || c.Properties[graph.PropRole] == MethodBackground {
			continue
		}
		people, err := store.GetNeighbors(ctx, c.ID, graph.EdgeIdentifiedAs, graph.Outgoing)
		if err == nil && len(people) == 0 {
			n++
		}
	}
	return n
}
