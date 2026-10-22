package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/relations"
	"github.com/imyousuf/CodeEagle/internal/watcher"
	"github.com/imyousuf/CodeEagle/pkg/jev"
)

func newRelationsCmd() *cobra.Command {
	var (
		plan       bool
		doc        string
		meeting    string
		sampleOut  string
		sampleSize int
		top        int
		limit      int
	)

	cmd := &cobra.Command{
		Use:   "relations",
		Short: "Discover which meetings discussed which documents",
		Long: `Propose and judge relations between documents and the meetings that
discussed them, and keep a cited brief of where each stands.
See docs/artifact-relations.md.

--plan shows what a run would do and writes nothing: how much of the corpus
the meaning signal can see, how many candidate pairs each signal proposes,
how many the caps leave for judging, and what judging would cost.

--sample-out writes a stratified sample of candidates for a person to label,
which is how the gate that decides which relations are kept is calibrated.

Without --plan, the selected pairs are judged by the decision model
(transcripts.jev_api_key) with one yes/no question each, and every verdict
is recorded, accepted or not, so no pair is asked twice while neither side
changes. Nothing is ever related on similarity alone. --limit bounds the
pairs judged in one run; the caps in relations.* bound them anyway.`,
		Example: `  codeeagle relations --plan
  codeeagle relations --plan --doc docs/specs/retry-policy.md
  codeeagle relations --plan --sample-out /tmp/relation-labels
  codeeagle relations --limit 20`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			o := relationsPlanOptions{
				doc: doc, meeting: meeting, sampleOut: sampleOut, sampleSize: sampleSize, top: top, limit: limit,
			}
			if plan {
				return runRelationsPlan(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), cfg, o)
			}
			return runRelationsJudge(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), cfg, o)
		},
	}

	cmd.Flags().BoolVar(&plan, "plan", false, "show what a run would do; write nothing")
	cmd.Flags().StringVar(&doc, "doc", "", "only propose for this document (its path as indexed)")
	cmd.Flags().StringVar(&meeting, "meeting", "", "only propose for this meeting (its session ID)")
	cmd.Flags().StringVar(&sampleOut, "sample-out", "", "write a stratified sample of candidates to label into this directory")
	cmd.Flags().IntVar(&sampleSize, "sample-size", 200, "how many candidates --sample-out writes")
	cmd.Flags().IntVar(&top, "top", 10, "how many of the best candidates to list")
	cmd.Flags().IntVar(&limit, "limit", 0, "judge at most this many pairs (default: relations.max_pairs_per_run)")
	return cmd
}

type relationsPlanOptions struct {
	doc, meeting, sampleOut string
	sampleSize, top, limit  int
}

// relationOptions turns configuration into proposal bounds.
func relationOptions(cfg *config.Config) relations.Options {
	opts := relations.DefaultOptions()
	if n := cfg.Relations.MaxPairsPerRun; n > 0 {
		opts.MaxPairs = n
	}
	if n := cfg.Relations.MaxCandidatesPerSubject; n > 0 {
		opts.MaxPerSubject = n
	}
	if n := cfg.Relations.MaxCandidatesPerMeeting; n > 0 {
		opts.MaxPerMeeting = n
	}
	return opts
}

// subjectFilter keeps documents not matched by relations.exclude.
func subjectFilter(cfg *config.Config) relations.SubjectFilter {
	if len(cfg.Relations.Exclude) == 0 {
		return nil
	}
	m := watcher.NewExcludeMatcher(cfg.Relations.Exclude)
	return func(path string) bool { return !m.Match(path) }
}

// relationsRun is a loaded corpus and the candidates proposed over it.
type relationsRun struct {
	subjects []*relations.Subject
	meetings map[string]*relations.Meeting
	existing map[string]*relations.Relation
	proposer *relations.Proposer
	cands    []*relations.Candidate
	prop     *relations.Proposal
	hasVec   bool
}

// proposeRelations loads the corpus from store and proposes candidates.
func proposeRelations(ctx context.Context, errOut io.Writer, cfg *config.Config, store graph.Store, vec relations.Vectors, o relationsPlanOptions) (*relationsRun, error) {
	subjects, err := relations.LoadSubjects(ctx, store, subjectFilter(cfg))
	if err != nil {
		return nil, fmt.Errorf("load documents: %w", err)
	}
	meetings, err := relations.LoadMeetings(ctx, store)
	if err != nil {
		return nil, fmt.Errorf("load meetings: %w", err)
	}
	existing, err := relations.LoadRelations(ctx, store)
	if err != nil {
		return nil, fmt.Errorf("load relations: %w", err)
	}
	if vec == nil {
		fmt.Fprintln(errOut, "Note: no vector index, so candidates are proposed by name and identifier only.")
	}
	opts := relationOptions(cfg)
	if o.limit > 0 && o.limit < opts.MaxPairs {
		opts.MaxPairs = o.limit
	}
	p := relations.NewProposer(subjects, meetings, vec, opts)
	var cands []*relations.Candidate
	switch {
	case o.doc != "":
		cands = p.ForSubjects(o.doc)
	case o.meeting != "":
		cands = p.ForMeetings(o.meeting)
	default:
		cands = p.All()
	}
	return &relationsRun{
		subjects: subjects, meetings: meetings, existing: existing, proposer: p,
		cands: cands, prop: p.Select(cands, relations.JudgedBy(existing)), hasVec: vec != nil,
	}, nil
}

// openRelationVectors opens the vector index for reading, or returns nil.
func openRelationVectors(cfg *config.Config, store *embedded.BranchStore, branch string) (relations.Vectors, func()) {
	vs, err := openReadOnlyVectorStore(cfg, store, branch, nil)
	if err != nil || vs == nil {
		return nil, func() {}
	}
	if loaded, err := vs.Load(); err != nil || !loaded {
		vs.Close()
		return nil, func() {}
	}
	return vs, func() { vs.Close() }
}

func runRelationsPlan(ctx context.Context, out, errOut io.Writer, cfg *config.Config, o relationsPlanOptions) error {
	store, branch, err := openReadOnlyBranchStore(cfg)
	if err != nil {
		return err
	}
	defer store.Close()
	vec, closeVec := openRelationVectors(cfg, store, branch)
	defer closeVec()

	run, err := proposeRelations(ctx, errOut, cfg, store, vec, o)
	if err != nil {
		return err
	}
	printProposal(out, run, o.top)

	if o.sampleOut != "" {
		n, err := relations.WriteSample(o.sampleOut, run.cands, o.sampleSize)
		if err != nil {
			return fmt.Errorf("write sample: %w", err)
		}
		fmt.Fprintf(out, "\nWrote %d candidates to label to %s/pairs.jsonl (instructions in README.txt).\n", n, o.sampleOut)
	}
	return nil
}

// runRelationsJudge proposes, judges and records verdicts.
func runRelationsJudge(ctx context.Context, out, errOut io.Writer, cfg *config.Config, o relationsPlanOptions) error {
	key := strings.TrimSpace(cfg.Transcripts.JevAPIKey)
	if key == "" {
		return fmt.Errorf("judging needs a decision model: set transcripts.jev_api_key (a relation is never asserted on similarity alone)")
	}
	var jopts []jev.Option
	if cfg.Transcripts.JevModel != "" {
		jopts = append(jopts, jev.WithModel(cfg.Transcripts.JevModel))
	}
	client, err := jev.New(key, jopts...)
	if err != nil {
		return fmt.Errorf("decision model: %w", err)
	}

	base, branch, err := openBranchStore(cfg)
	if err != nil {
		return err
	}
	defer base.Close()
	reads := append(append([]string(nil), base.ReadBranches()...), embedded.MeetingScope, embedded.RelationScope)
	store := base.View(embedded.RelationScope, reads)
	vec, closeVec := openRelationVectors(cfg, base, branch)
	defer closeVec()

	run, err := proposeRelations(ctx, errOut, cfg, store, vec, o)
	if err != nil {
		return err
	}
	printProposal(out, run, 0)
	if len(run.prop.Candidates) == 0 {
		fmt.Fprintln(out, "\nNothing to judge.")
		return nil
	}

	rel := relations.NewRelater(store, relations.NewJudge(client, relations.NewFileTranscripts()), relations.RelaterOptions{
		Gate: cfg.Relations.Gate,
		Log:  func(f string, a ...any) { fmt.Fprintf(errOut, f+"\n", a...) },
	})
	started := time.Now()
	stats, err := rel.Judge(ctx, run.prop.Candidates, run.existing)
	if err != nil {
		return err
	}
	j := stats.Judging
	fmt.Fprintf(out, "\nJudged %d pairs, accepted %d, across %d documents · %d requests, %dk input tokens (%s), %s\n",
		stats.Judged, stats.Accepted, stats.Relations, j.Requests, (j.InputTokens+999)/1000,
		j.Model, time.Since(started).Round(time.Second))
	if j.Failed > 0 {
		fmt.Fprintf(out, "%d pairs could not be judged and will be retried on a later run.\n", j.Failed)
	}
	return nil
}

// printProposal reports what was proposed and what would be judged.
func printProposal(out io.Writer, run *relationsRun, top int) {
	prop := run.prop
	segments := 0
	for _, m := range run.meetings {
		segments += len(m.Segments)
	}
	fmt.Fprintf(out, "Documents: %d · Meetings: %d (%d segments) · Relations: %d\n",
		len(run.subjects), len(run.meetings), segments, len(run.existing))
	if run.hasVec {
		secWith, secs, segWith, segs := run.proposer.Coverage()
		fmt.Fprintf(out, "Vectors:   %d of %d sections, %d of %d segments\n", secWith, secs, segWith, segs)
		if secs > 0 && secWith*2 < secs {
			fmt.Fprintln(out, "           most sections have no vector yet; each sync embeds more")
		}
	}
	fmt.Fprintf(out, "Proposed:  %d candidates (%s)\n", prop.Proposed, signalCounts(prop.BySignal))
	fmt.Fprintf(out, "Set aside: %d already judged, %d cut by caps\n", prop.AlreadyJudged, prop.CutByCaps)
	reqs, tokens := relations.EstimateJudging(prop.Candidates)
	fmt.Fprintf(out, "Judging:   %d pairs in %d requests, ~%dk input tokens\n",
		len(prop.Candidates), reqs, (tokens+999)/1000)

	if top > 0 && len(prop.Candidates) > 0 {
		fmt.Fprintln(out, "\nBest candidates:")
		for i, c := range prop.Candidates {
			if i >= top {
				break
			}
			fmt.Fprintf(out, "  %.2f  %s  ↔  %s %s  [%s]\n", c.Score, c.Subject.Path,
				c.Meeting.Date.Format("2006-01-02"), truncateText(c.Meeting.Title, 60),
				strings.Join(c.Signals.Names(), ","))
		}
	}
}

// signalCounts renders per-signal counts in a fixed order.
func signalCounts(by map[string]int) string {
	var parts []string
	for _, name := range []string{"said", "name", "ids", "topic"} {
		parts = append(parts, fmt.Sprintf("%s %d", name, by[name]))
	}
	return strings.Join(parts, " · ")
}
