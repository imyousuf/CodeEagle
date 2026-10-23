package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/relations"
	"github.com/imyousuf/CodeEagle/internal/watcher"
	"github.com/imyousuf/CodeEagle/pkg/jev"
	"github.com/imyousuf/CodeEagle/pkg/llm"
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
		measure    string
		target     float64
		briefsOnly bool
		reset      bool
		jopts      relations.JudgeOptions
		scoredOut  string
		docs       bool
		limitDocs  int
		sampleDocs string
		measureDoc string
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
pairs judged in one run; the caps in relations.* bound them anyway.

--reset deletes every relation, verdict and brief, and the record of what
automatic runs have considered. Every pair becomes eligible to be judged
again, so the next runs pay to rebuild what was there.`,
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
				briefsOnly: briefsOnly, judge: jopts, docs: docs || sampleDocs != "", limitDocs: limitDocs,
			}
			o.sampleDocs = sampleDocs
			if measureDoc != "" {
				return runRelationsMeasureDocs(cmd.Context(), cmd.OutOrStdout(), cfg, measureDoc, scoredOut)
			}
			if reset {
				return runRelationsReset(cmd.OutOrStdout(), cfg)
			}
			if measure != "" {
				return runRelationsMeasure(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), cfg, measure, target, jopts, scoredOut)
			}
			// Writing a sample is read-only; asked for without --plan it
			// used to be ignored, and a full judging run went ahead instead.
			if plan || sampleOut != "" || sampleDocs != "" {
				return runRelationsPlan(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), cfg, o)
			}
			return runRelationsJudge(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), cfg, o)
		},
	}

	cmd.Flags().BoolVar(&plan, "plan", false, "show what a run would do; write nothing")
	cmd.Flags().StringVar(&doc, "doc", "", "only propose for this document (its path as indexed)")
	cmd.Flags().StringVar(&meeting, "meeting", "", "only propose for this meeting (its session ID)")
	cmd.Flags().StringVar(&sampleOut, "sample-out", "", "write a stratified sample of candidates to label into this directory (implies --plan)")
	cmd.Flags().IntVar(&sampleSize, "sample-size", 200, "how many candidates --sample-out writes")
	cmd.Flags().IntVar(&top, "top", 10, "how many of the best candidates to list")
	cmd.Flags().IntVar(&limit, "limit", 0, "judge at most this many pairs (default: relations.max_pairs_per_run)")
	cmd.Flags().StringVar(&measure, "measure", "", "judge a labelled sample (from --sample-out) and report how well the gate separates it; writes nothing to the graph")
	cmd.Flags().Float64Var(&target, "target-precision", 0.95, "precision the suggested gate must reach, with --measure")
	cmd.Flags().BoolVar(&briefsOnly, "briefs-only", false, "rewrite briefs whose inputs changed, without judging new pairs")
	cmd.Flags().BoolVar(&reset, "reset", false, "delete every relation, verdict and brief")
	cmd.Flags().BoolVar(&docs, "docs", false, "also propose and judge how documents relate to each other (changes, replaces, records, companion)")
	cmd.Flags().IntVar(&limitDocs, "limit-docs", 0, "judge at most this many document pairs (default: relations.max_doc_pairs_per_run)")
	cmd.Flags().StringVar(&sampleDocs, "sample-out-docs", "", "write a stratified sample of document pairs to label into this directory (implies --plan)")
	cmd.Flags().StringVar(&measureDoc, "measure-docs", "", "judge labelled document pairs (from --sample-out-docs) and report how the answers match")
	// Experiments on what the judge is shown, measured with --measure.
	cmd.Flags().IntVar(&jopts.BatchSize, "judge-batch", 0, "with --measure: pairs per decision-model request")
	cmd.Flags().BoolVar(&jopts.Opening, "judge-opening", false, "show a segment from its start, as before the judge was measured")
	cmd.Flags().IntVar(&jopts.SpokenChars, "judge-spoken", 0, "with --measure: characters of the speakers' words shown per segment")
	cmd.Flags().StringVar(&scoredOut, "scored-out", "", "with --measure: where to write per-pair probabilities")
	for _, f := range []string{"judge-batch", "judge-opening", "judge-spoken", "scored-out"} {
		_ = cmd.Flags().MarkHidden(f)
	}
	return cmd
}

// runRelationsReset deletes the relations scope whole. Relations are kept
// apart from documents and meetings so that this touches nothing else.
func runRelationsReset(out io.Writer, cfg *config.Config) error {
	store, _, err := openBranchStore(cfg)
	if err != nil {
		return err
	}
	defer store.Close()
	types, err := store.ScopeNodeTypes(embedded.RelationScope)
	if err != nil {
		return fmt.Errorf("count relations: %w", err)
	}
	if err := store.DeleteByBranch(embedded.RelationScope); err != nil {
		return fmt.Errorf("delete relations: %w", err)
	}
	fmt.Fprintf(out, "Deleted %d relations and the record of what was considered.\n", types[graph.NodeRelation])
	return nil
}

type relationsPlanOptions struct {
	doc, meeting, sampleOut string
	sampleSize, top, limit  int
	judge                   relations.JudgeOptions
	limitDocs               int
	docs                    bool
	sampleDocs              string
	briefsOnly              bool
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
	// focused is set when the run is limited to one document or meeting.
	focused bool
	// storeView is where verdicts are written; docs are the document pairs
	// proposed alongside.
	storeView *embedded.BranchStore
	docs      *docRun
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
		focused: o.doc != "" || o.meeting != "",
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
	if o.docs {
		// References are recomputed in memory: a plan writes nothing.
		resolver := relations.NewResolver(subjectPaths(run.subjects))
		for _, subj := range run.subjects {
			if rel := run.existing[subj.Path]; rel != nil {
				rel.Refs = relations.References(subj, resolver)
			} else {
				rel := relations.New(subj.Path)
				rel.Refs = relations.References(subj, resolver)
				run.existing[subj.Path] = rel
			}
		}
		dr := proposeDocPairs(cfg, run, vec, o)
		printDocProposal(out, dr, o.top)
		if o.sampleDocs != "" {
			n, err := relations.WriteDocSample(o.sampleDocs, dr.cands, o.sampleSize)
			if err != nil {
				return fmt.Errorf("write document sample: %w", err)
			}
			fmt.Fprintf(out, "\nWrote %d document pairs to label to %s/pairs.jsonl (instructions in README.txt).\n", n, o.sampleDocs)
		}
	}

	if o.sampleOut != "" {
		n, err := relations.WriteSample(o.sampleOut, run.cands, o.sampleSize)
		if err != nil {
			return fmt.Errorf("write sample: %w", err)
		}
		fmt.Fprintf(out, "\nWrote %d candidates to label to %s/pairs.jsonl (instructions in README.txt).\n", n, o.sampleOut)
	}
	return nil
}

// newRelationJudge builds the decision-model judge, or explains why it cannot.
func newRelationJudge(cfg *config.Config) (*relations.Judge, error) {
	return newRelationJudgeWith(cfg, relations.JudgeOptions{})
}

func newRelationJudgeWith(cfg *config.Config, opts relations.JudgeOptions) (*relations.Judge, error) {
	client, err := newJevAsker(cfg)
	if err != nil {
		return nil, err
	}
	return relations.NewJudgeWithOptions(client, relations.NewFileTranscripts(), opts), nil
}

// newJevAsker builds the decision-model client, or explains why it cannot.
func newJevAsker(cfg *config.Config) (*jev.Client, error) {
	key := strings.TrimSpace(cfg.Transcripts.JevAPIKey)
	if key == "" {
		return nil, fmt.Errorf("judging needs a decision model: set transcripts.jev_api_key (a relation is never asserted on similarity alone)")
	}
	var jopts []jev.Option
	if cfg.Transcripts.JevModel != "" {
		jopts = append(jopts, jev.WithModel(cfg.Transcripts.JevModel))
	}
	client, err := jev.New(key, jopts...)
	if err != nil {
		return nil, fmt.Errorf("decision model: %w", err)
	}
	return client, nil
}

// runRelationsMeasure judges a labelled sample and reports how well the
// probabilities separate related pairs from unrelated ones.
func runRelationsMeasure(ctx context.Context, out, errOut io.Writer, cfg *config.Config, labelsPath string, target float64,
	jopts relations.JudgeOptions, scoredOut string) error {
	judge, err := newRelationJudgeWith(cfg, jopts)
	if err != nil {
		return err
	}
	labelled, err := relations.ReadLabels(labelsPath)
	if err != nil {
		return err
	}
	if len(labelled) == 0 {
		return fmt.Errorf("%s has no pairs labelled related or unrelated", labelsPath)
	}
	store, _, err := openReadOnlyBranchStore(cfg)
	if err != nil {
		return err
	}
	defer store.Close()
	subjects, err := relations.LoadSubjects(ctx, store, nil)
	if err != nil {
		return err
	}
	meetings, err := relations.LoadMeetings(ctx, store)
	if err != nil {
		return err
	}
	cands, labels, missing := relations.Rebuild(labelled, subjects, meetings)
	if missing > 0 {
		fmt.Fprintf(errOut, "Note: %d labelled pairs are no longer in the corpus and were skipped.\n", missing)
	}
	judgments, report, err := judge.Judge(ctx, cands)
	if err != nil {
		return fmt.Errorf("judge the sample: %w", err)
	}
	byKey := make(map[string]float64, len(judgments))
	for _, jd := range judgments {
		byKey[jd.Candidate.Key()] = jd.P
	}
	probs := make([]float64, len(cands))
	for i, c := range cands {
		probs[i] = byKey[c.Key()]
	}

	cal := relations.Calibrate(probs, labels, target)
	fmt.Fprintf(out, "Pairs: %d (%d related) · judged with %s · %d requests, %dk input tokens\n",
		cal.Pairs, cal.Related, report.Model, report.Requests, (report.InputTokens+999)/1000)
	fmt.Fprintf(out, "AUC:   %.3f\n\n", cal.AUC)
	fmt.Fprintln(out, "gate  admitted  precision  recall")
	for _, b := range cal.Buckets {
		fmt.Fprintf(out, " %.1f  %8d  %9.2f  %6.2f\n", b.Gate, b.Admitted, b.Precision, b.Recall)
	}
	if cal.Gate > 0 {
		fmt.Fprintf(out, "\nLowest gate with precision ≥ %.2f: %.1f (set relations.gate)\n", target, cal.Gate)
	} else {
		fmt.Fprintf(out, "\nNo gate reaches precision %.2f on this sample.\n", target)
	}

	scored := strings.TrimSuffix(labelsPath, ".jsonl") + ".scored.jsonl"
	if scoredOut != "" {
		scored = scoredOut
	}
	if err := writeScored(scored, cands, probs, labels); err != nil {
		return err
	}
	fmt.Fprintf(out, "Per-pair probabilities: %s\n", scored)
	return nil
}

// writeScored writes each judged pair with its label and probability.
func writeScored(path string, cands []*relations.Candidate, probs []float64, labels []bool) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for i, c := range cands {
		if err := enc.Encode(map[string]any{
			"subject_path": c.Subject.Path, "session_id": c.Meeting.SessionID,
			"related": labels[i], "p": probs[i], "signals": c.Signals.Names(), "cosine": c.Cosine,
		}); err != nil {
			return err
		}
	}
	return nil
}

// runRelationsJudge proposes, judges and records verdicts.
func runRelationsJudge(ctx context.Context, out, errOut io.Writer, cfg *config.Config, o relationsPlanOptions) error {
	judge, err := newRelationJudgeWith(cfg, o.judge)
	if err != nil {
		return err
	}

	base, branch, err := openBranchStore(cfg)
	if err != nil {
		return err
	}
	defer base.Close()
	reads := append(append([]string(nil), base.ReadBranches()...), embedded.MeetingScope, embedded.RelationScope)
	store := base.View(embedded.RelationScope, reads)
	vec, closeVec := openRelationVectors(cfg, base, branch)
	defer func() { closeVec() }()

	run, err := proposeRelations(ctx, errOut, cfg, store, vec, o)
	if err != nil {
		return err
	}
	run.storeView = store
	if _, err := relations.RefreshReferences(ctx, store, run.subjects, run.existing); err != nil {
		return err
	}
	if o.docs {
		run.docs = proposeDocPairs(cfg, run, vec, o)
	}
	// Proposing is the only step that reads vectors. The read-only handle
	// is let go now, so the rewritten briefs can be embedded through a
	// writable one at the end.
	closeVec()
	closeVec = func() {}
	// Every document gets a relation, and so a brief.
	if n, err := relations.EnsureRelations(ctx, store, run.subjects, run.existing, time.Now()); err != nil {
		return err
	} else if n > 0 {
		fmt.Fprintf(out, "Created relations for %d documents not yet related to anything.\n", n)
	}
	if o.briefsOnly {
		return writeRelationBriefs(ctx, out, errOut, cfg, store, run)
	}
	printProposal(out, run, 0)
	if run.docs != nil {
		printDocProposal(out, run.docs, 0)
		if err := judgeDocPairsRun(ctx, out, cfg, run, run.docs); err != nil {
			return err
		}
	}
	if len(run.prop.Candidates) == 0 {
		fmt.Fprintln(out, "\nNothing to judge.")
		return writeRelationBriefs(ctx, out, errOut, cfg, store, run)
	}

	rel := relations.NewRelater(store, judge, relations.RelaterOptions{
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
	return writeRelationBriefs(ctx, out, errOut, cfg, store, run)
}

// writeRelationBriefs rewrites the briefs of relations whose inputs changed,
// up to relations.max_briefs_per_run, and stores them.
func writeRelationBriefs(ctx context.Context, out, errOut io.Writer, cfg *config.Config, store *embedded.BranchStore, run *relationsRun) error {
	writer, client, err := newRelationBriefWriter(ctx, cfg, store)
	if err != nil {
		return err
	}
	defer client.Close()

	bySubject := make(map[string]*relations.Subject, len(run.subjects))
	for _, s := range run.subjects {
		bySubject[s.Path] = s
	}
	limit := cfg.Relations.MaxBriefsPerRun
	if limit <= 0 {
		limit = 10
	}
	br, err := relations.WriteBriefs(ctx, store, writer, run.existing, bySubject, run.meetings, limit, relations.DefaultBriefWorkers)
	if verbose {
		for path, lines := range br.Unverified {
			for _, u := range lines {
				fmt.Fprintf(errOut, "  unverified in %s: %s\n", path, u)
			}
		}
	}
	fmt.Fprintf(out, "Briefs: %d written (%s, %dk in / %dk out tokens", br.Written, client.Model(), (br.TokensIn+999)/1000, (br.TokensOut+999)/1000)
	if br.JudgeRequests > 0 {
		fmt.Fprintf(out, ", %d conflict checks", br.JudgeRequests)
	}
	fmt.Fprint(out, ")")
	if br.Dropped > 0 {
		fmt.Fprintf(out, " · %d unverified lines dropped", br.Dropped)
	}
	if br.Failed > 0 {
		fmt.Fprintf(out, " · %d failed and will be retried", br.Failed)
	}
	if br.Pending > 0 {
		fmt.Fprintf(out, " · %d more pending, past relations.max_briefs_per_run", br.Pending)
	}
	fmt.Fprintln(out)
	if n := reembedWritable(ctx, cfg, store, br.WrittenPaths); n > 0 {
		fmt.Fprintf(out, "Re-embedded %d rewritten briefs for search.\n", n)
	}
	return err
}

// reembedWritable opens the vector index for writing and indexes again the
// relations whose briefs were rewritten. The bulk command reads vectors
// through a read-only handle, closed by now.
func reembedWritable(ctx context.Context, cfg *config.Config, store *embedded.BranchStore, paths []string) int {
	if len(paths) == 0 {
		return 0
	}
	vs, err := openVectorStore(cfg, vectorGraphView(store), store.WriteBranch(), nil)
	if err != nil || vs == nil {
		return 0
	}
	defer vs.Close()
	if loaded, err := vs.Load(); err != nil || !loaded {
		return 0
	}
	return reembedRelations(ctx, store, vs, paths)
}

// newRelationBriefWriter builds the brief writer from configuration. The
// caller closes the returned client.
func newRelationBriefWriter(ctx context.Context, cfg *config.Config, store graph.Store) (*relations.BriefWriter, llm.Client, error) {
	client, err := relationBriefClient(cfg)
	if err != nil {
		return nil, nil, err
	}
	var asker jev.Asker
	if key := strings.TrimSpace(cfg.Transcripts.JevAPIKey); key != "" {
		var jopts []jev.Option
		if cfg.Transcripts.JevModel != "" {
			jopts = append(jopts, jev.WithModel(cfg.Transcripts.JevModel))
		}
		if c, err := jev.New(key, jopts...); err == nil {
			asker = c
		}
	}
	opts := relations.DefaultBriefOptions()
	if n := cfg.Relations.MaxBriefInputTokens; n > 0 {
		opts.MaxInputChars = n * 4
	}
	if g := cfg.Relations.ConflictGate; g > 0 {
		opts.ConflictGate = g
	}
	if d, err := time.ParseDuration(cfg.Relations.Settle); err == nil {
		opts.Settle = d
	}
	writer := relations.NewBriefWriter(client, asker, relations.NewFileTranscripts(),
		relations.NewStoreSpeakers(ctx, store), opts, nil)
	return writer, client, nil
}

// relationBriefClient builds the language model that writes briefs: the
// relations provider if one is set, else the transcripts one.
func relationBriefClient(cfg *config.Config) (llm.Client, error) {
	tc := cfg.Transcripts
	if cfg.Relations.Provider != "" {
		tc.Provider = cfg.Relations.Provider
	}
	if cfg.Relations.Model != "" {
		tc.Model = cfg.Relations.Model
	}
	return newTranscriptClient(tc)
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
	fmt.Fprintf(out, "Set aside: %d with no support in meaning, %d already judged, %d cut by caps\n",
		prop.Unsupported, prop.AlreadyJudged, prop.CutByCaps)
	reqs, tokens := relations.EstimateJudging(prop.Candidates)
	fmt.Fprintf(out, "Judging:   %d pairs in %d requests, ~%dk input tokens\n",
		len(prop.Candidates), reqs, (tokens+999)/1000)

	if run.focused {
		// For one document or meeting, every candidate and what became of
		// it: what was proposed and not judged is where recall is lost.
		cands := append([]*relations.Candidate(nil), run.cands...)
		sort.SliceStable(cands, func(i, j int) bool { return cands[i].Score > cands[j].Score })
		fmt.Fprintln(out, "\nEvery candidate, best first:")
		for i, c := range cands {
			if i >= max(top, 1)*5 {
				fmt.Fprintf(out, "  … %d more\n", len(cands)-i)
				break
			}
			status := c.Status
			if rel := run.existing[c.Subject.Path]; rel != nil {
				if v := rel.Links[c.Meeting.SessionID]; v != nil {
					verdict := "rejected"
					if v.Accepted {
						verdict = "accepted"
					}
					status = fmt.Sprintf("%s p=%.2f", verdict, v.P)
				}
			}
			fmt.Fprintf(out, "  %.2f  %-22s %s  ↔  %s %s  [%s]\n", c.Score, status, c.Subject.Path,
				c.Meeting.Date.Format("2006-01-02"), truncateText(c.Meeting.Title, 50),
				strings.Join(c.Signals.Names(), ","))
		}
		return
	}
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

func subjectPaths(subjects []*relations.Subject) []string {
	out := make([]string, len(subjects))
	for i, s := range subjects {
		out[i] = s.Path
	}
	return out
}
