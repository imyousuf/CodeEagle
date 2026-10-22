package cli

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/indexer"
	"github.com/imyousuf/CodeEagle/internal/relations"
	"github.com/imyousuf/CodeEagle/internal/search"
	"github.com/imyousuf/CodeEagle/internal/vectorstore"
)

// verbs serves find, brief and show, for the commands and for MCP. It opens
// the graph read-only, and opens it again when the database has changed: a
// read-only handle is a snapshot, and a long-running MCP server would
// otherwise never see what the worker writes.
type verbs struct {
	cfg *config.Config

	mu      sync.Mutex
	store   *embedded.BranchStore
	branch  string
	stamp   time.Time
	lib     *relations.Library
	vs      *vectorstore.VectorStore
	vsTried bool
}

func newVerbs(cfg *config.Config) *verbs { return &verbs{cfg: cfg} }

// Close releases what is open.
func (v *verbs) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.closeLocked()
}

func (v *verbs) closeLocked() {
	if v.vs != nil {
		v.vs.Close()
		v.vs = nil
	}
	if v.store != nil {
		v.store.Close()
		v.store = nil
	}
	v.lib = nil
	v.vsTried = false
}

// dbStamp is the newest modification time of any file in the database
// directory: a write anywhere in it, flushed or not, moves it.
func (v *verbs) dbStamp() time.Time {
	dir := v.cfg.ResolveDBPath(dbPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}
	}
	var newest time.Time
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest
}

// open makes sure the store, and the library over it, reflect the database
// as it is now.
func (v *verbs) open(ctx context.Context) (*relations.Library, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	stamp := v.dbStamp()
	if v.store != nil && !stamp.After(v.stamp) {
		return v.lib, nil
	}
	v.closeLocked()
	store, branch, err := openReadOnlyBranchStore(v.cfg)
	if err != nil {
		return nil, err
	}
	roots := repoPaths(v.cfg)
	lib, err := relations.LoadLibrary(ctx, store, func(p string) string { return indexer.ResolveAbsPath(p, roots) })
	if err != nil {
		store.Close()
		return nil, err
	}
	v.store, v.branch, v.stamp, v.lib = store, branch, stamp, lib
	return lib, nil
}

// vectors opens the vector index once, lazily. Nil means none is available
// and searches fall back to keywords.
func (v *verbs) vectors() search.VectorSearcher {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.vsTried {
		if v.vs == nil {
			return noVectors{}
		}
		return v.vs
	}
	v.vsTried = true
	vs, err := openReadOnlyVectorStore(v.cfg, v.store, v.branch, nil)
	if err != nil || vs == nil {
		return noVectors{}
	}
	if loaded, err := vs.Load(); err != nil || !loaded {
		vs.Close()
		return noVectors{}
	}
	v.vs = vs
	return vs
}

// noVectors searches nothing, leaving hybrid search to its keyword half.
type noVectors struct{}

func (noVectors) Search(context.Context, string, int) ([]vectorstore.SearchResult, error) {
	return nil, nil
}
func (noVectors) StaleInLastSearch() int { return 0 }

// What find searches, in two halves searched apart: documents and what is
// known about them, and meetings with their parts. Searched together, a
// subject discussed in many meetings fills every slot with segments and
// decisions and the document never surfaces.
var (
	findDocTypes = map[graph.NodeType]bool{
		graph.NodeDocument: true, graph.NodeAIGuideline: true, graph.NodeRelation: true,
	}
	findMeetingTypes = map[graph.NodeType]bool{
		graph.NodeMeeting: true, graph.NodeTopicSegment: true, graph.NodeDecision: true, graph.NodeActionItem: true,
	}
)

// Bounds on what find returns: at most eight lines, of which documents get
// at least three when there are that many and meetings at most five.
const (
	maxFindLines    = 8
	minFindDocs     = 3
	maxFindMeetings = 5
)

// finding is one folded search hit.
type finding struct {
	line  string
	score float64
	rel   *relations.Relation
	m     *relations.Meeting
}

// findings searches and folds hits up to what an agent can act on: a section
// to its document, a document to its relation, a segment, decision or
// follow-up to its meeting.
func (v *verbs) findings(ctx context.Context, query string) ([]finding, error) {
	lib, err := v.open(ctx)
	if err != nil {
		return nil, err
	}
	vec := v.vectors()
	docs, err := search.Hybrid(ctx, v.store, vec, query, search.HybridOptions{Limit: 20, Types: findDocTypes})
	if err != nil {
		return nil, err
	}
	meets, err := search.Hybrid(ctx, v.store, vec, query, search.HybridOptions{Limit: 30, Types: findMeetingTypes})
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	fold := func(results []vectorstore.SearchResult) []finding {
		var out []finding
		for _, r := range results {
			n := r.Node
			var key string
			var f finding
			switch n.Type {
			case graph.NodeRelation:
				rel := lib.Relations[n.Properties["subject_path"]]
				if rel == nil {
					continue
				}
				key, f = "doc:"+rel.SubjectPath, finding{line: lib.FinderLine(rel), rel: rel}
			case graph.NodeDocument, graph.NodeAIGuideline:
				if rel := lib.Relations[n.FilePath]; rel != nil {
					key, f = "doc:"+n.FilePath, finding{line: lib.FinderLine(rel), rel: rel}
					break
				}
				title := n.FilePath
				if s := lib.Subjects[n.FilePath]; s != nil && s.Title != "" {
					title = n.FilePath + ": " + s.Title
				}
				root := graph.NewNodeID(string(graph.NodeDocument), n.FilePath, n.FilePath)
				key, f = "doc:"+n.FilePath, finding{line: fmt.Sprintf("%s doc %s", relations.ShortID(root), title)}
			default:
				m := lib.Meetings[n.Properties[graph.PropMeetingID]]
				if m == nil {
					continue
				}
				key, f = "mtg:"+m.SessionID, finding{m: m,
					line: fmt.Sprintf("%s mtg %s %s", relations.ShortID(m.NodeID), m.Date.Format("2006-01-02"), m.Title)}
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			f.score = r.Score
			out = append(out, f)
		}
		return out
	}
	docHits, meetHits := fold(docs.Results), fold(meets.Results)

	// Merge by score within the bounds.
	var out []finding
	di, mi, meetingsTaken := 0, 0, 0
	for len(out) < maxFindLines && (di < len(docHits) || mi < len(meetHits)) {
		docsNeeded := min(minFindDocs, len(docHits)) - di
		slotsLeft := maxFindLines - len(out)
		takeDoc := di < len(docHits) &&
			(mi >= len(meetHits) || meetingsTaken >= maxFindMeetings || docsNeeded >= slotsLeft ||
				docHits[di].score >= meetHits[mi].score)
		if takeDoc {
			out = append(out, docHits[di])
			di++
			continue
		}
		if mi < len(meetHits) && meetingsTaken < maxFindMeetings {
			out = append(out, meetHits[mi])
			mi++
			meetingsTaken++
			continue
		}
		break
	}
	return out, nil
}

// Find renders the folded hits for a query, one line each.
func (v *verbs) Find(ctx context.Context, query string) (string, error) {
	fs, err := v.findings(ctx, query)
	if err != nil {
		return "", err
	}
	if len(fs) == 0 {
		return "nothing found for " + query, nil
	}
	lines := make([]string, len(fs))
	for i, f := range fs {
		lines[i] = f.line
	}
	return strings.Join(lines, "\n"), nil
}

var hexPrefix = regexp.MustCompile(`^[0-9a-f]{4,24}$`)

// clearMargin is how far the top hit must lead the next for brief to treat a
// query of words as naming it.
const clearMargin = 0.05

// Brief renders the brief of what arg names: an ID prefix of a relation,
// document or meeting, or words that clearly name one. When words match
// several, it lists them instead, in the same call.
func (v *verbs) Brief(ctx context.Context, arg string, budget int, since string) (string, error) {
	lib, err := v.open(ctx)
	if err != nil {
		return "", err
	}
	arg = strings.TrimSpace(arg)
	if hexPrefix.MatchString(arg) {
		if out, ok, err := v.briefByID(ctx, lib, arg, budget, since); ok || err != nil {
			return out, err
		}
	}
	fs, err := v.findings(ctx, arg)
	if err != nil {
		return "", err
	}
	if len(fs) == 0 {
		return "nothing found for " + arg, nil
	}
	top := fs[0]
	if len(fs) == 1 || top.score-fs[1].score >= clearMargin {
		switch {
		case top.rel != nil:
			return v.renderBrief(lib, top.rel, budget, since)
		case top.m != nil:
			return lib.MeetingBrief(top.m), nil
		}
	}
	lines := []string{"several things match; pass an ID to brief one:"}
	for _, f := range fs {
		lines = append(lines, f.line)
	}
	return strings.Join(lines, "\n"), nil
}

func (v *verbs) renderBrief(lib *relations.Library, rel *relations.Relation, budget int, since string) (string, error) {
	t, err := relations.ParseSince(since, rel, time.Now())
	if err != nil {
		return "", err
	}
	return lib.Brief(rel, budget, t, since), nil
}

// briefByID briefs what an ID prefix names, reporting whether it named
// anything.
func (v *verbs) briefByID(ctx context.Context, lib *relations.Library, prefix string, budget int, since string) (string, bool, error) {
	if rel := lib.RelationByID(prefix); rel != nil {
		out, err := v.renderBrief(lib, rel, budget, since)
		return out, true, err
	}
	nodes, err := v.store.ResolvePrefix(ctx, prefix, 10)
	if err != nil || len(nodes) == 0 {
		return "", false, nil
	}
	if len(nodes) > 1 {
		return ambiguous(prefix, nodes), true, nil
	}
	n := nodes[0]
	switch n.Type {
	case graph.NodeDocument, graph.NodeAIGuideline:
		if rel := lib.Relations[n.FilePath]; rel != nil {
			out, err := v.renderBrief(lib, rel, budget, since)
			return out, true, err
		}
		return fmt.Sprintf("%s: no meeting has been judged to discuss it", n.FilePath), true, nil
	case graph.NodeMeeting, graph.NodeTopicSegment, graph.NodeDecision, graph.NodeActionItem:
		if m := lib.Meetings[n.Properties[graph.PropMeetingID]]; m != nil {
			return lib.MeetingBrief(m), true, nil
		}
	}
	return fmt.Sprintf("%s is a %s; brief covers documents and meetings", n.Name, n.Type), true, nil
}

func ambiguous(prefix string, nodes []*graph.Node) string {
	lines := []string{fmt.Sprintf("%q matches %d nodes; give more of the ID:", prefix, len(nodes))}
	for _, n := range nodes {
		lines = append(lines, fmt.Sprintf("%s %s %s", n.ID[:min(10, len(n.ID))], n.Type, n.Name))
	}
	return strings.Join(lines, "\n")
}

// maxShowNodeChars bounds the text show prints for a node.
const maxShowNodeChars = 1500

// Show expands an evidence ID, or a node ID prefix, to its source text,
// bounded so it never costs a whole file.
func (v *verbs) Show(ctx context.Context, arg string, from int) (string, error) {
	lib, err := v.open(ctx)
	if err != nil {
		return "", err
	}
	arg = strings.TrimSpace(arg)
	if rel, e := lib.Evidence(arg); e != nil {
		return lib.ShowEvidence(rel, e, from)
	}
	if !hexPrefix.MatchString(arg) {
		return "", fmt.Errorf("show takes an evidence ID or a node ID prefix (at least %d hex characters)", embedded.MinIDPrefix)
	}
	nodes, err := v.store.ResolvePrefix(ctx, arg, 10)
	if err != nil {
		return "", err
	}
	switch len(nodes) {
	case 0:
		return "", fmt.Errorf("nothing has an ID starting %q", arg)
	case 1:
	default:
		return ambiguous(arg, nodes), nil
	}
	n := nodes[0]
	if n.Type == graph.NodeRelation {
		if rel := lib.Relations[n.Properties["subject_path"]]; rel != nil {
			return lib.Brief(rel, relations.DefaultBriefBudget, time.Time{}, ""), nil
		}
	}
	where := n.FilePath
	if n.Line > 0 {
		where += fmt.Sprintf(":%d", n.Line)
		if n.EndLine > n.Line {
			where += fmt.Sprintf("-%d", n.EndLine)
		}
	}
	text := strings.TrimSpace(n.DocComment)
	if len(text) > maxShowNodeChars {
		text = text[:maxShowNodeChars] + "…"
	}
	return fmt.Sprintf("%s %s %s\n%s\n\n%s", relations.ShortID(n.ID), n.Type, n.Name, where, text), nil
}
