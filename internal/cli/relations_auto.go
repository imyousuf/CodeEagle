package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/gitutil"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/relations"
)

// relationsCodeScope returns the scope documents are read from for
// relations, and whether this checkout is on it. Briefs describe documents
// as they are on the default branch, or in the working tree where there is
// no git, so a feature branch does not rewrite them with work in progress.
func relationsCodeScope(repos []string) (string, bool) {
	current, reads := gitutil.BuildReadBranches(repos)
	return current, len(reads) == 1 || current == "default"
}

// autoRelations runs one bounded incremental relations pass over store's
// database, when relations are enabled and this checkout is where documents
// are read from. vec may be nil. It never fails its caller: what goes wrong
// is reported, and whatever did not finish is picked up by the next run.
func autoRelations(ctx context.Context, cfg *config.Config, store *embedded.BranchStore, repos []string,
	vec relations.Vectors, logFn, warnFn func(string, ...any)) {
	if !cfg.RelationsEnabled() {
		return
	}
	code, ok := relationsCodeScope(repos)
	if !ok {
		return
	}
	view := store.View(embedded.RelationScope, []string{code, embedded.MeetingScope, embedded.RelationScope})

	judge, err := newRelationJudge(cfg)
	if err != nil {
		warnFn("Warning: relations: %v", err)
		return
	}
	// Without a brief writer, relations are still judged; their briefs are
	// written by a later run.
	writer, client, err := newRelationBriefWriter(ctx, cfg, view)
	if err != nil {
		warnFn("Warning: relations: briefs: %v", err)
		writer = nil
	} else {
		defer client.Close()
	}

	o := relations.DefaultIncrementalOptions()
	if n := cfg.Relations.MaxBriefsPerRun; n > 0 {
		o.MaxBriefs = n
	}
	if cfg.Relations.AutoDocLinks {
		if asker, err := newJevAsker(cfg); err != nil {
			warnFn("Warning: relations: document links: %v", err)
		} else {
			o.DocJudge, o.DocOptions, o.DocGates = relations.NewDocJudge(asker), docOptions(cfg, 0), docGates(cfg)
		}
	}
	rep, err := relations.Incremental(ctx, view, vec, judge, writer, subjectFilter(cfg),
		relationOptions(cfg), cfg.Relations.Gate, o)
	if err != nil {
		warnFn("Warning: relations: %v", err)
	}
	if rep == nil {
		return
	}
	if n := reembedRelations(ctx, view, vec, rep.Rewritten); n > 0 {
		logFn("relations: re-embedded %d rewritten briefs for search", n)
	}
	if rep.SubjectsConsidered+rep.MeetingsConsidered+rep.BriefsWritten+rep.BriefsPending+rep.DocsConsidered > 0 {
		logFn("%s", rep.Summary())
	}
}

// reembedder is a vector index that can index nodes again and save.
type reembedder interface {
	IndexNodes(ctx context.Context, nodes []*graph.Node) error
	Save() error
}

// reembedRelations indexes again the relations whose briefs were rewritten.
// A relation's ID never changes, and the index only adds nodes it lacks, so
// without this search kept matching each relation's first brief forever.
func reembedRelations(ctx context.Context, store graph.Store, vec relations.Vectors, paths []string) int {
	re, ok := vec.(reembedder)
	if !ok || len(paths) == 0 {
		return 0
	}
	var nodes []*graph.Node
	for _, path := range paths {
		if n, err := store.GetNode(ctx, relations.RelationID(path)); err == nil && n != nil {
			nodes = append(nodes, n)
		}
	}
	if len(nodes) == 0 || re.IndexNodes(ctx, nodes) != nil || re.Save() != nil {
		return 0
	}
	return len(nodes)
}

// relateNewMeetings relates newly indexed meetings to the documents they
// discussed, through the meeting store's database.
func relateNewMeetings(ctx context.Context, cfg *config.Config, store *embedded.BranchStore, out io.Writer) {
	if !cfg.RelationsEnabled() {
		return
	}
	vec, closeVec := openRelationVectors(cfg, store, embedded.MeetingScope)
	defer closeVec()
	logFn := func(f string, a ...any) { fmt.Fprintf(out, "\n"+f+"\n", a...) }
	autoRelations(ctx, cfg, store, repoPaths(cfg), vec, logFn, logFn)
}
