package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/relations"
	"github.com/imyousuf/CodeEagle/internal/watcher"
)

func newRelationsCmd() *cobra.Command {
	var (
		plan       bool
		doc        string
		meeting    string
		sampleOut  string
		sampleSize int
		top        int
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
which is how the gate that decides which relations are kept is calibrated.`,
		Example: `  codeeagle relations --plan
  codeeagle relations --plan --doc docs/specs/retry-policy.md
  codeeagle relations --plan --sample-out /tmp/relation-labels`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !plan {
				return fmt.Errorf("only --plan is available so far; judging arrives with the decision model step")
			}
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			return runRelationsPlan(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), cfg, relationsPlanOptions{
				doc: doc, meeting: meeting, sampleOut: sampleOut, sampleSize: sampleSize, top: top,
			})
		},
	}

	cmd.Flags().BoolVar(&plan, "plan", false, "show what a run would do; write nothing")
	cmd.Flags().StringVar(&doc, "doc", "", "only propose for this document (its path as indexed)")
	cmd.Flags().StringVar(&meeting, "meeting", "", "only propose for this meeting (its session ID)")
	cmd.Flags().StringVar(&sampleOut, "sample-out", "", "write a stratified sample of candidates to label into this directory")
	cmd.Flags().IntVar(&sampleSize, "sample-size", 200, "how many candidates --sample-out writes")
	cmd.Flags().IntVar(&top, "top", 10, "how many of the best candidates to list")
	return cmd
}

type relationsPlanOptions struct {
	doc, meeting, sampleOut string
	sampleSize, top         int
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

func runRelationsPlan(ctx context.Context, out, errOut io.Writer, cfg *config.Config, o relationsPlanOptions) error {
	store, branch, err := openReadOnlyBranchStore(cfg)
	if err != nil {
		return err
	}
	defer store.Close()

	subjects, err := relations.LoadSubjects(ctx, store, subjectFilter(cfg))
	if err != nil {
		return fmt.Errorf("load documents: %w", err)
	}
	meetings, err := relations.LoadMeetings(ctx, store)
	if err != nil {
		return fmt.Errorf("load meetings: %w", err)
	}
	existing, err := relations.LoadRelations(ctx, store)
	if err != nil {
		return fmt.Errorf("load relations: %w", err)
	}

	var vec relations.Vectors
	if vs, err := openReadOnlyVectorStore(cfg, store, branch, nil); err == nil && vs != nil {
		defer vs.Close()
		if loaded, err := vs.Load(); err == nil && loaded {
			vec = vs
		}
	}
	if vec == nil {
		fmt.Fprintln(errOut, "Note: no vector index, so candidates are proposed by name and identifier only.")
	}

	p := relations.NewProposer(subjects, meetings, vec, relationOptions(cfg))
	var cands []*relations.Candidate
	switch {
	case o.doc != "":
		cands = p.ForSubjects(o.doc)
	case o.meeting != "":
		cands = p.ForMeetings(o.meeting)
	default:
		cands = p.All()
	}
	prop := p.Select(cands, relations.JudgedBy(existing))

	segments := 0
	for _, m := range meetings {
		segments += len(m.Segments)
	}
	fmt.Fprintf(out, "Documents: %d · Meetings: %d (%d segments) · Relations: %d\n",
		len(subjects), len(meetings), segments, len(existing))
	if vec != nil {
		secWith, secs, segWith, segs := p.Coverage()
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

	if o.top > 0 && len(prop.Candidates) > 0 {
		fmt.Fprintln(out, "\nBest candidates:")
		for i, c := range prop.Candidates {
			if i >= o.top {
				break
			}
			fmt.Fprintf(out, "  %.2f  %s  ↔  %s %s  [%s]\n", c.Score, c.Subject.Path,
				c.Meeting.Date.Format("2006-01-02"), truncateText(c.Meeting.Title, 60),
				strings.Join(c.Signals.Names(), ","))
		}
	}

	if o.sampleOut != "" {
		n, err := relations.WriteSample(o.sampleOut, cands, o.sampleSize)
		if err != nil {
			return fmt.Errorf("write sample: %w", err)
		}
		fmt.Fprintf(out, "\nWrote %d candidates to label to %s/pairs.jsonl (instructions in README.txt).\n", n, o.sampleOut)
	}
	return nil
}

// signalCounts renders per-signal counts in a fixed order.
func signalCounts(by map[string]int) string {
	var parts []string
	for _, name := range []string{"said", "name", "ids", "topic"} {
		parts = append(parts, fmt.Sprintf("%s %d", name, by[name]))
	}
	return strings.Join(parts, " · ")
}
