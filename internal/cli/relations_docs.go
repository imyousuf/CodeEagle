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

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/relations"
)

// docGates turns configuration into document-relation gates.
func docGates(cfg *config.Config) relations.DocGates {
	g := relations.DefaultDocGates()
	c := cfg.Relations.DocGates
	for _, set := range []struct {
		from float64
		to   *float64
	}{{c.Related, &g.Related}, {c.Changes, &g.Changes}, {c.Replaces, &g.Replaces}, {c.Records, &g.Records}} {
		if set.from > 0 {
			*set.to = set.from
		}
	}
	return g
}

// docOptions turns configuration into document-pair proposal bounds; limit,
// when set, lowers or raises the per-run cap for this run.
func docOptions(cfg *config.Config, limit int) relations.DocOptions {
	o := relations.DefaultDocOptions()
	if n := cfg.Relations.MaxDocPairsPerRun; n > 0 {
		o.MaxPairs = n
	}
	if limit > 0 {
		o.MaxPairs = limit
	}
	return o
}

// docRun is the document pairs proposed in one run.
type docRun struct {
	cands, selected, deferred []*relations.DocCandidate
}

// proposeDocPairs proposes document pairs over every subject, or over one
// document with --doc.
func proposeDocPairs(cfg *config.Config, run *relationsRun, vec relations.Vectors, o relationsPlanOptions) *docRun {
	p := relations.NewDocProposer(run.subjects, run.existing, vec, docOptions(cfg, o.limitDocs))
	var paths []string
	if o.doc != "" {
		paths = []string{o.doc}
	} else {
		for _, s := range run.subjects {
			paths = append(paths, s.Path)
		}
	}
	cands := p.ForDocuments(paths...)
	sel, def := p.SelectDocs(cands, relations.JudgedDoc(run.existing))
	return &docRun{cands: cands, selected: sel, deferred: def}
}

func printDocProposal(out io.Writer, dr *docRun, top int) {
	by := make(map[string]int)
	judged := 0
	for _, c := range dr.cands {
		for _, s := range c.Signals {
			by[s]++
		}
		if c.Status == relations.StatusJudged {
			judged++
		}
	}
	keys := make([]string, 0, len(by))
	for k := range by {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, by[k]))
	}
	fmt.Fprintf(out, "\nDocument pairs: %d proposed (%s) · %d already judged · %d to judge, %d deferred\n",
		len(dr.cands), strings.Join(parts, " · "), judged, len(dr.selected), len(dr.deferred))
	for i, c := range dr.selected {
		if i >= top {
			break
		}
		fmt.Fprintf(out, "  %.2f  %s  ↔  %s  [%s]\n", c.Score, c.A.Path, c.B.Path, strings.Join(c.Signals, ","))
	}
}

// judgeDocPairsRun judges the selected document pairs and reports the cost.
func judgeDocPairsRun(ctx context.Context, out io.Writer, cfg *config.Config, run *relationsRun, dr *docRun) error {
	if len(dr.selected) == 0 {
		return nil
	}
	asker, err := newJevAsker(cfg)
	if err != nil {
		return err
	}
	started := time.Now()
	stats, err := relations.JudgeDocPairs(ctx, run.storeView, relations.NewDocJudge(asker), dr.selected, run.existing, docGates(cfg), time.Now())
	if err != nil {
		return err
	}
	j := stats.Judging
	fmt.Fprintf(out, "Judged %d document pairs: %s · %d requests, %dk input tokens (%s), %s\n", stats.Judged,
		acceptedKinds(stats.Accepted), j.Requests, (j.InputTokens+999)/1000, j.Model, time.Since(started).Round(time.Second))
	if j.Failed > 0 {
		fmt.Fprintf(out, "%d document pairs could not be judged and will be retried on a later run.\n", j.Failed)
	}
	return nil
}

func acceptedKinds(by map[string]int) string {
	var parts []string
	for _, k := range []string{relations.DocReplaces, relations.DocChanges, relations.DocRecords, relations.DocCompanion} {
		if by[k] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", by[k], k))
		}
	}
	if len(parts) == 0 {
		return "none accepted"
	}
	return strings.Join(parts, ", ")
}

// runRelationsMeasureDocs judges labelled document pairs and reports how the
// answers match the labels.
func runRelationsMeasureDocs(ctx context.Context, out io.Writer, cfg *config.Config, labelsPath, scoredOut string) error {
	asker, err := newJevAsker(cfg)
	if err != nil {
		return err
	}
	labels, err := relations.ReadDocLabels(labelsPath)
	if err != nil {
		return err
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
	cands, kept := relations.RebuildDocs(labels, subjects)
	judgments, report, err := relations.NewDocJudge(asker).Judge(ctx, cands)
	if err != nil {
		return err
	}
	byKey := make(map[string]relations.DocVerdict, len(judgments))
	for _, jd := range judgments {
		byKey[jd.Candidate.Key()] = jd.Verdict
	}
	var verdicts []relations.DocVerdict
	var labelled []relations.DocPairSample
	var as, bs []*relations.Subject
	for i, c := range cands {
		if v, ok := byKey[c.Key()]; ok {
			verdicts = append(verdicts, v)
			labelled = append(labelled, kept[i])
			as, bs = append(as, c.A), append(bs, c.B)
		}
	}
	cal := relations.CalibrateDocs(verdicts, labelled, as, bs, docGates(cfg))
	fmt.Fprintf(out, "Pairs: %d (%d related) · %d requests, %dk input tokens (%s)\n", cal.Pairs, cal.Related,
		report.Requests, (report.InputTokens+999)/1000, report.Model)
	fmt.Fprintf(out, "related AUC: %.3f\n\ngate  admitted  precision  recall\n", cal.AUC)
	for _, b := range cal.RelatedAt {
		fmt.Fprintf(out, " %.1f  %8d  %9.2f  %6.2f\n", b.Gate, b.Admitted, b.Precision, b.Recall)
	}
	fmt.Fprintln(out, "\nkind      precision  recall   (direction included, at the configured gates)")
	for _, k := range []string{relations.DocChanges, relations.DocReplaces, relations.DocRecords} {
		pr := cal.Kinds[k]
		fmt.Fprintf(out, "%-9s %9.2f  %6.2f\n", k, pr[0], pr[1])
	}
	if scoredOut == "" {
		scoredOut = strings.TrimSuffix(labelsPath, ".jsonl") + ".scored.jsonl"
	}
	f, err := os.Create(scoredOut)
	if err != nil {
		return err
	}
	defer f.Close()
	for i, v := range verdicts {
		b, _ := json.Marshal(map[string]any{"pair_id": labelled[i].ID, "a": labelled[i].A, "b": labelled[i].B,
			"labels": map[string]string{"related": labelled[i].Related, "changes": labelled[i].Changes,
				"replaces": labelled[i].Replaces, "records": labelled[i].Records}, "verdict": v})
		f.Write(append(b, '\n'))
	}
	fmt.Fprintf(out, "Per-pair probabilities: %s\n", scoredOut)
	return nil
}
