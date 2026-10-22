package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/gitutil"
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
	if !cfg.RelationsEnabled() || vec == nil {
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
	rep, err := relations.Incremental(ctx, view, vec, judge, writer, subjectFilter(cfg),
		relationOptions(cfg), cfg.Relations.Gate, o)
	if err != nil {
		warnFn("Warning: relations: %v", err)
	}
	if rep == nil || rep.NoVectors {
		return
	}
	if rep.SubjectsConsidered+rep.MeetingsConsidered+rep.BriefsWritten+rep.BriefsPending > 0 {
		logFn("%s", rep.Summary())
	}
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
