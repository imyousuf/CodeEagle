package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/gitutil"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/transcript"
	"github.com/imyousuf/CodeEagle/internal/vectorstore"
	"github.com/imyousuf/CodeEagle/pkg/jev"
)

// --- relate ---

func newMeetingsRelateCmd() *cobra.Command {
	var (
		dryRun      bool
		limit       int
		nearest     int
		concurrency int
	)

	cmd := &cobra.Command{
		Use:   "relate",
		Short: "Judge which topics are about one thing, so a search reaches all of them",
		Long: `Relate the topics meetings were filed under to each other.

Each meeting names its subject in its own words, so most topic labels are
used by exactly one meeting and a search for one label finds one meeting
while the same conversation sits under a different label. Merging labels
would fuse distinct discussions, so the labels stay as they are and the
commonality goes on edges between them: for each pair worth asking about,
the decision model's probability that a meeting filed under either is worth
showing to someone asking about the other. Search then follows those edges
as far as --breadth allows.

Pairs are proposed by four signals — the nearest labels by embedding, the
topics under which meetings said the nearest things, the topics one meeting
discussed together, and the other children of a topic's parent — and every
pair proposed is judged, related or not, so this command never asks about a
pair twice. It runs as meetings are indexed when transcripts.jev_api_key is
configured; run it by hand to cover a corpus indexed before that, or after
building the vector index, which adds the two embedding signals.

--dry-run counts the pairs still to judge and what they would cost.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			tc := cfg.Transcripts
			if strings.TrimSpace(tc.JevAPIKey) == "" {
				return fmt.Errorf("relating topics needs the decision model: set transcripts.jev_api_key")
			}
			out := cmd.OutOrStdout()
			log := func(format string, args ...any) { fmt.Fprintf(out, format+"\n", args...) }

			// A preview must not take the write lock.
			var store *embedded.BranchStore
			if dryRun {
				store, err = embedded.OpenMeetingsReadOnly(cfg, dbPath)
			} else {
				store, err = openMeetingStore(cfg)
			}
			if err != nil {
				return err
			}
			defer store.Close()

			relater, closeVectors, err := newTopicRelater(cfg, tc, store, relaterSetup{
				nearest: nearest, concurrency: concurrency, log: log,
			})
			if err != nil {
				return err
			}
			defer closeVectors()

			ctx := cmd.Context()
			if dryRun {
				pairs, tokens, err := relater.Pending(ctx)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "%d topic pairs to judge, about %d input tokens in %d requests.\n",
					pairs, tokens, (pairs+9)/10)
				return nil
			}

			started := time.Now()
			stats, err := relater.RelateAll(ctx, limit)
			printRelateStats(out, stats, time.Since(started))
			return err
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "count the pairs to judge without judging any")
	cmd.Flags().IntVar(&limit, "limit", 0, "judge at most N pairs")
	cmd.Flags().IntVar(&nearest, "nearest", transcript.DefaultNearestK,
		"how many nearest labels by embedding each topic proposes")
	cmd.Flags().IntVar(&concurrency, "concurrency", 4, "requests to keep in flight")
	return cmd
}

// relaterSetup carries what a command chooses about a relater.
type relaterSetup struct {
	nearest     int
	concurrency int
	log         func(string, ...any)
}

// newTopicRelater builds the relater when a decision model is configured,
// or returns nil when none is. The vector index, when it can be opened,
// supplies the nearest-label signal; without it pairs come from
// co-occurrence and the tree alone.
func newTopicRelater(cfg *config.Config, tc config.TranscriptsConfig, store *embedded.BranchStore, setup relaterSetup) (*transcript.TopicRelater, func(), error) {
	noop := func() {}
	key := strings.TrimSpace(tc.JevAPIKey)
	if key == "" {
		return nil, noop, nil
	}
	var opts []jev.Option
	if tc.JevModel != "" {
		opts = append(opts, jev.WithModel(tc.JevModel))
	}
	client, err := jev.New(key, opts...)
	if err != nil {
		return nil, noop, fmt.Errorf("topic decision model: %w", err)
	}

	relOpts := transcript.RelaterOptions{
		NearestK:    setup.nearest,
		Concurrency: setup.concurrency,
		Log:         setup.log,
	}
	closer := noop
	branch, _ := gitutil.BuildReadBranches(repoPaths(cfg))
	if vs, _ := vectorstore.OpenReadOnlyWithLoad(cfg, store, branch); vs != nil {
		relOpts.Vectors = vs.Vector
		embedder := vs.Embedder()
		relOpts.Embed = func(ctx context.Context, text string) ([]float32, error) {
			vecs, err := embedder.Embed(ctx, []string{text})
			if err != nil {
				return nil, err
			}
			if len(vecs) != 1 {
				return nil, fmt.Errorf("embedded %d texts, want 1", len(vecs))
			}
			return vecs[0], nil
		}
		closer = func() { vs.Close() }
	} else if setup.log != nil {
		setup.log("Note: no vector index open; topics are related by co-occurrence and the tree only")
	}
	return transcript.NewTopicRelater(store, client, relOpts), closer, nil
}

// printRelateStats reports what relating did and cost.
func printRelateStats(out io.Writer, st transcript.RelateStats, elapsed time.Duration) {
	fmt.Fprintf(out, "\n── Related topics ──\n")
	fmt.Fprintf(out, "  pairs judged  %d (%d at or above the default search gate)\n", st.Pairs, st.Related)
	fmt.Fprintf(out, "  %d requests, %d input tokens, %s", st.Judging.Requests, st.Judging.InputTokens, elapsed.Round(time.Second))
	if st.Judging.Model != "" {
		fmt.Fprintf(out, ", %s", st.Judging.Model)
	}
	fmt.Fprintln(out)
}
