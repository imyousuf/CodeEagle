package cli

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/worker"
)

func newWorkerCmd() *cobra.Command {
	var (
		once        bool
		list        bool
		only        []string
		settle      time.Duration
		concurrency int
		quiet       bool
	)

	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Watch every registered project and sync the one that changed",
		Long: `Watch every CodeEagle project registered on this machine and re-sync a
project when its own files settle.

Projects come from the registry at ~/.codeeagle.conf, and what is watched is
the directories each project's configuration actually indexes -- not the
project root, which may hold far more. A change is attributed to the most
specific project that indexes it, so a project nested inside another's tree is
synced as itself rather than as its parent.

Each sync runs as a separate ` + "`codeeagle sync`" + ` from that project's root and
against that project's configuration, so it behaves exactly as if you had run
it there by hand. One project's failure never stops the others being watched.

Nothing is installed: this runs in the foreground until interrupted, and a
sync already in flight is allowed to finish rather than being killed halfway.`,
		Example: `  codeeagle worker --list            # what would be watched, and what was skipped
  codeeagle worker --once            # sync every project once, then exit
  codeeagle worker                   # watch and sync on change
  codeeagle worker --project opal-app --settle 10s`,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			projects, skipped := worker.Discover()
			projects = filterProjects(projects, only)

			for _, s := range skipped {
				fmt.Fprintf(out, "Skipping %s: %s\n", s.Name, s.Reason)
			}
			if len(projects) == 0 {
				return fmt.Errorf("no usable projects; register one with `codeeagle init`")
			}

			if list {
				return printProjects(out, projects)
			}

			logf := func(format string, args ...any) {
				if quiet {
					return
				}
				fmt.Fprintf(out, "%s "+format+"\n",
					append([]any{time.Now().Format("15:04:05")}, args...)...)
			}

			sup := worker.NewSupervisor(projects, worker.Options{
				Settle:        settle,
				MaxConcurrent: concurrency,
				Log:           logf,
				// Inherit the terminal so a sync's own progress is visible
				// rather than swallowed.
				Runner: worker.ExecRunner{Stdout: os.Stdout, Stderr: os.Stderr},
			})

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			if once {
				return sup.SyncOnce(ctx)
			}
			return sup.Run(ctx)
		},
	}

	cmd.Flags().BoolVar(&once, "once", false, "sync every project once and exit, without watching")
	cmd.Flags().BoolVar(&list, "list", false, "print the projects and directories that would be watched")
	cmd.Flags().StringSliceVar(&only, "project", nil, "limit to these registered project names (repeatable)")
	cmd.Flags().DurationVar(&settle, "settle", worker.DefaultSettle,
		"how long a project must be quiet before it is synced")
	cmd.Flags().IntVar(&concurrency, "concurrency", worker.DefaultMaxConcurrent,
		"how many projects may sync at the same time")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "only report failures")
	return cmd
}

func filterProjects(projects []worker.Project, only []string) []worker.Project {
	if len(only) == 0 {
		return projects
	}
	want := make(map[string]bool, len(only))
	for _, n := range only {
		want[strings.TrimSpace(n)] = true
	}
	out := projects[:0:0]
	for _, p := range projects {
		if want[p.Name] {
			out = append(out, p)
		}
	}
	return out
}

func printProjects(out interface{ Write([]byte) (int, error) }, projects []worker.Project) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROJECT\tROOT\tWATCHES")
	fmt.Fprintln(w, "-------\t----\t-------")
	for _, p := range projects {
		for i, dir := range p.Repos {
			if i == 0 {
				fmt.Fprintf(w, "%s\t%s\t%s\n", p.Name, p.Root, dir)
			} else {
				fmt.Fprintf(w, "\t\t%s\n", dir)
			}
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}

	fmt.Fprintf(out, "\n%d project(s), %d director(ies) after removing nested duplicates.\n",
		len(projects), len(worker.WatchRoots(projects)))
	return nil
}
