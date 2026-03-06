package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/queue"
)

func newQueueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "queue",
		Short: "Manage the enrichment job queue",
	}

	cmd.AddCommand(newQueueStatusCmd())
	cmd.AddCommand(newQueuePurgeCmd())

	return cmd
}

func newQueueStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show queue job counts by type and status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			queuePath := cfg.ConfigDir + "/queue.db"
			qs, err := queue.Open(queuePath)
			if err != nil {
				return fmt.Errorf("open queue: %w", err)
			}
			defer qs.Close()

			stats, err := qs.Stats()
			if err != nil {
				return fmt.Errorf("queue stats: %w", err)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Queue Status\n")
			fmt.Fprintf(out, "  Pending:  %d\n", stats.Pending)
			fmt.Fprintf(out, "  Running:  %d\n", stats.Running)
			fmt.Fprintf(out, "  Done:     %d\n", stats.Done)
			fmt.Fprintf(out, "  Failed:   %d\n", stats.Failed)
			fmt.Fprintf(out, "  Skipped:  %d\n", stats.Skipped)

			if len(stats.ByType) > 0 {
				fmt.Fprintf(out, "\nBy Type:\n")
				for jobType, ts := range stats.ByType {
					fmt.Fprintf(out, "  %s: %d pending, %d done, %d failed\n",
						jobType, ts.Pending, ts.Done, ts.Failed)
				}
			}

			return nil
		},
	}
}

func newQueuePurgeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "purge",
		Short: "Remove completed jobs from the queue",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			queuePath := cfg.ConfigDir + "/queue.db"
			qs, err := queue.Open(queuePath)
			if err != nil {
				return fmt.Errorf("open queue: %w", err)
			}
			defer qs.Close()

			before, _ := qs.Stats()
			if err := qs.PurgeCompleted(); err != nil {
				return fmt.Errorf("purge: %w", err)
			}
			after, _ := qs.Stats()

			purged := before.Done - after.Done
			fmt.Fprintf(cmd.OutOrStdout(), "Purged %d completed jobs\n", purged)
			return nil
		},
	}
}
