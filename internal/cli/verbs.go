package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
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

// verbs serves find, brief and show, for the commands and for MCP.
//
// A call holds the graph open only while it runs. A read-only Badger handle
// takes a shared lock that keeps every writer out while it is open, so a
// handle kept for an agent's whole session would stop the worker syncing
// until the session ended, and the briefs could never be brought up to date
// under it. The library over the graph is kept between calls and reloaded
// when the database has changed; the vector index is loaded once and
// detached from its database for the same reason.
type verbs struct {
	cfg *config.Config

	mu      sync.Mutex
	stamp   time.Time
	lib     *relations.Library
	vs      *vectorstore.VectorStore
	vsTried bool
	// store is open for the duration of a call, and nil between calls or
	// when a sync holds the database.
	store *embedded.BranchStore
}

func newVerbs(cfg *config.Config) *verbs { return &verbs{cfg: cfg} }

// Close releases what is open.
func (v *verbs) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.vs != nil {
		v.vs.Close()
		v.vs = nil
	}
	v.lib = nil
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

// begin opens the graph for one call and brings the library up to date with
// it. The returned function closes the graph again. Calls are serialized.
//
// When a sync holds the database, the call goes ahead on the library last
// read, with no store: briefs and evidence can still be shown, a little
// behind, but nothing can be searched.
func (v *verbs) begin(ctx context.Context) (*relations.Library, func(), error) {
	v.mu.Lock()
	var branch string
	store, err := embedded.OpenWaiting(func() (*embedded.BranchStore, error) {
		s, b, err := openReadOnlyBranchStore(v.cfg)
		branch = b
		return s, err
	})
	if err != nil {
		if errors.Is(err, embedded.ErrBusy) && v.lib != nil {
			v.lib.Speakers = nil
			return v.lib, v.mu.Unlock, nil
		}
		v.mu.Unlock()
		return nil, nil, err
	}
	release := func() {
		v.store = nil
		if v.lib != nil {
			v.lib.Store, v.lib.Speakers = nil, nil
		}
		store.Close()
		v.mu.Unlock()
	}
	if stamp := v.dbStamp(); v.lib == nil || stamp.After(v.stamp) {
		roots := repoPaths(v.cfg)
		lib, err := relations.LoadLibrary(ctx, store, func(p string) string { return indexer.ResolveAbsPath(p, roots) })
		if err != nil {
			release()
			return nil, nil, err
		}
		v.lib, v.stamp = lib, stamp
	}
	v.store = store
	v.lib.Store = store
	v.lib.Speakers = relations.NewStoreSpeakers(ctx, store)
	v.attachVectors(store, branch)
	return v.lib, release, nil
}

// attachVectors loads the vector index on first use, detached from its
// database, and points it at this call's graph.
func (v *verbs) attachVectors(store *embedded.BranchStore, branch string) {
	if v.vs != nil {
		v.vs.UseGraph(store)
		return
	}
	if v.vsTried {
		return
	}
	vs, err := openReadOnlyVectorStore(v.cfg, store, branch, nil)
	if embedded.IsLocked(err) {
		return // the worker is writing vectors; try again next call
	}
	v.vsTried = true
	if err != nil || vs == nil {
		return
	}
	if loaded, err := vs.Load(); err != nil || !loaded {
		vs.Close()
		return
	}
	if err := vs.Detach(); err != nil {
		vs.Close()
		return
	}
	v.vs = vs
}

// vectors is the index to search during a call, or none.
func (v *verbs) vectors() search.VectorSearcher {
	if v.vs == nil {
		return noVectors{}
	}
	return v.vs
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
func (v *verbs) findings(ctx context.Context, lib *relations.Library, query string) ([]finding, error) {
	if v.store == nil {
		return nil, embedded.ErrBusy
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
	lib, done, err := v.begin(ctx)
	if err != nil {
		return "", err
	}
	defer done()
	fs, err := v.findings(ctx, lib, query)
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
	return v.brief(ctx, arg, func(lib *relations.Library, rel *relations.Relation) (string, error) {
		return v.renderBrief(lib, rel, budget, since)
	})
}

// BriefJSON is Brief for a program: the relation's whole brief, the evidence
// each line cites with its quote and anchor, the meetings judged to discuss
// the document, and its freshness. Words that match several things, or a
// meeting, still answer in text.
func (v *verbs) BriefJSON(ctx context.Context, arg string) (string, error) {
	return v.brief(ctx, arg, briefJSON)
}

// renderFunc turns a relation into what brief prints.
type renderFunc func(lib *relations.Library, rel *relations.Relation) (string, error)

func (v *verbs) brief(ctx context.Context, arg string, render renderFunc) (string, error) {
	lib, done, err := v.begin(ctx)
	if err != nil {
		return "", err
	}
	defer done()
	arg = strings.TrimSpace(arg)
	if hexPrefix.MatchString(arg) {
		if out, ok, err := v.briefByID(ctx, lib, arg, render); ok || err != nil {
			return out, err
		}
	}
	fs, err := v.findings(ctx, lib, arg)
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
			return render(lib, top.rel)
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
func (v *verbs) briefByID(ctx context.Context, lib *relations.Library, prefix string, render renderFunc) (string, bool, error) {
	if rel := lib.RelationByID(prefix); rel != nil {
		out, err := render(lib, rel)
		return out, true, err
	}
	if v.store == nil {
		return "", false, nil
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
			out, err := render(lib, rel)
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

// briefJSON is a relation as a program reads it.
func briefJSON(lib *relations.Library, rel *relations.Relation) (string, error) {
	type meeting struct {
		Session string  `json:"session_id"`
		ID      string  `json:"id"`
		Date    string  `json:"date"`
		Title   string  `json:"title"`
		P       float64 `json:"p"`
	}
	out := struct {
		ID        string               `json:"id"`
		Subject   string               `json:"subject"`
		Revision  int                  `json:"revision"`
		BriefedAt time.Time            `json:"briefed_at,omitzero"`
		Model     string               `json:"model,omitempty"`
		Fresh     []string             `json:"fresh,omitempty"`
		Meetings  []meeting            `json:"meetings"`
		Brief     *relations.Brief     `json:"brief"`
		Evidence  []relations.Evidence `json:"evidence"`
	}{
		ID: relations.ShortID(relations.RelationID(rel.SubjectPath)), Subject: rel.SubjectPath,
		Revision: rel.Revision, BriefedAt: rel.BriefedAt, Model: rel.BriefModel,
		Fresh: relations.Freshness(rel, lib.Subjects[rel.SubjectPath], lib.Meetings),
		Brief: rel.Brief, Evidence: rel.Evidence,
	}
	for _, vd := range rel.Accepted() {
		m := meeting{Session: vd.SessionID, P: vd.P}
		if mt := lib.Meetings[vd.SessionID]; mt != nil {
			m.ID, m.Date, m.Title = relations.ShortID(mt.NodeID), mt.Date.Format("2006-01-02"), mt.Title
		}
		out.Meetings = append(out.Meetings, m)
	}
	sort.Slice(out.Meetings, func(i, j int) bool { return out.Meetings[i].Date > out.Meetings[j].Date })
	b, err := json.MarshalIndent(out, "", "  ")
	return string(b), err
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
	lib, done, err := v.begin(ctx)
	if err != nil {
		return "", err
	}
	defer done()
	arg = strings.TrimSpace(arg)
	if rel, e := lib.Evidence(arg); e != nil {
		return lib.ShowEvidence(rel, e, from)
	}
	if !hexPrefix.MatchString(arg) {
		return "", fmt.Errorf("show takes an evidence ID or a node ID prefix (at least %d hex characters)", embedded.MinIDPrefix)
	}
	if v.store == nil {
		return "", embedded.ErrBusy
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
