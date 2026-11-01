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

	"github.com/imyousuf/CodeEagle/internal/watcher"
	"github.com/imyousuf/CodeEagle/internal/worker"
)

func newWorkerCmd() *cobra.Command {
	var (
		once        bool
		list        bool
		only        []string
		exclude     []string
		settle      time.Duration
		concurrency int
		quiet       bool
		watchAll    bool
		hotWindow   time.Duration
		scanEvery   time.Duration
		maxWatches  int
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

Only directories in use hold an OS watch. A directory whose contents changed
within --hot-window is watched; the rest are scanned every --scan-interval
and watched again as soon as a scan finds a change, and a watched directory
quiet for the whole window is released. The roots are always watched, so a
new folder directly under one, such as a new meeting recording, is seen at
once. Watches across all projects stay within --max-watches, by default a
quarter of the system's limit. --watch-all watches every directory instead,
for as long as the worker runs.

Nothing is installed: this runs in the foreground until interrupted, and a
sync already in flight is allowed to finish rather than being killed halfway.`,
		Example: `  codeeagle worker --list            # what would be watched, and what was skipped
  codeeagle worker --once            # sync every project once, then exit
  codeeagle worker                   # watch and sync on change
  codeeagle worker --project opal-app --settle 10s`,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			// Located first, before any sync can replace the binary. Inherit
			// the terminal so a sync's own progress is visible rather than
			// swallowed.
			runner, err := worker.NewExecRunner(os.Stdout, os.Stderr)
			if err != nil {
				return err
			}

			projects, skipped := worker.Discover()
			projects = filterProjects(projects, only)
			projects = excludeProjects(projects, exclude)

			for _, s := range skipped {
				fmt.Fprintf(out, "Skipping %s: %s\n", s.Name, s.Reason)
			}
			if len(projects) == 0 {
				return fmt.Errorf("no usable projects; register one with `codeeagle init`")
			}

			if list {
				return printProjects(out, projects, watchAll, hotWindow, scanEvery, maxWatches)
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
				Runner:        runner,
				WatchAll:      watchAll,
				HotWindow:     hotWindow,
				ScanInterval:  scanEvery,
				MaxWatches:    maxWatches,
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
	cmd.Flags().StringSliceVar(&exclude, "exclude-project", nil,
		"skip these registered project names (repeatable); everything else is still watched")
	cmd.Flags().DurationVar(&settle, "settle", worker.DefaultSettle,
		"how long a project must be quiet before it is synced")
	cmd.Flags().IntVar(&concurrency, "concurrency", worker.DefaultMaxConcurrent,
		"how many projects may sync at the same time")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "only report failures")
	cmd.Flags().BoolVar(&watchAll, "watch-all", false, "watch every directory for as long as the worker runs, instead of only those in use")
	cmd.Flags().DurationVar(&hotWindow, "hot-window", watcher.DefaultHotWindow,
		"watch a directory whose contents changed within this long; scan the rest")
	cmd.Flags().DurationVar(&scanEvery, "scan-interval", watcher.DefaultScanInterval,
		"how often directories that are not watched are scanned for changes")
	cmd.Flags().IntVar(&maxWatches, "max-watches", 0,
		"most directories watched across all projects (default: a quarter of the system's limit)")
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

// excludeProjects drops the named projects, leaving every other one watched.
func excludeProjects(projects []worker.Project, exclude []string) []worker.Project {
	if len(exclude) == 0 {
		return projects
	}
	skip := make(map[string]bool, len(exclude))
	for _, n := range exclude {
		skip[strings.TrimSpace(n)] = true
	}
	out := projects[:0:0]
	for _, p := range projects {
		if !skip[p.Name] {
			out = append(out, p)
		}
	}
	return out
}

func printProjects(out interface{ Write([]byte) (int, error) }, projects []worker.Project,
	watchAll bool, hotWindow, scanEvery time.Duration, maxWatches int) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROJECT\tROOT\tWATCHES")
	fmt.Fprintln(w, "-------\t----\t-------")
	for _, p := range projects {
		// Transcript directories are marked, because a change in one runs
		// meetings enrichment rather than an ordinary sync.
		lines := append([]string(nil), p.Repos...)
		for _, dir := range p.TranscriptDirs {
			lines = append(lines, dir+"   (recordings)")
		}
		for i, dir := range lines {
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

	// What the worker would hold if it started now.
	if maxWatches <= 0 {
		maxWatches = watcher.DefaultMaxWatches()
	}
	if scanEvery <= 0 {
		scanEvery = watcher.DefaultScanInterval
	}
	var totalWatched, totalScanned int
	fmt.Fprintln(out)
	for _, p := range projects {
		watched, scanned, err := watcher.Survey(watcher.WatcherConfig{Paths: worker.WatchPaths(p), ExcludePatterns: p.Excludes},
			time.Now(), hotWindow)
		if err != nil {
			fmt.Fprintf(out, "%s: %v\n", p.Name, err)
			continue
		}
		if watchAll {
			watched, scanned = watched+scanned, 0
		}
		totalWatched += watched
		totalScanned += scanned
		fmt.Fprintf(out, "%s: %d director(ies) watched, %d scanned\n", p.Name, watched, scanned)
	}
	if watchAll {
		fmt.Fprintf(out, "Starting now, the worker would watch %d directories (--watch-all).\n", totalWatched)
		return nil
	}
	fmt.Fprintf(out, "Starting now, the worker would watch %d directories changed within %s and scan %d more every %s; "+
		"watch budget %d.\n", totalWatched, worker.HumanDuration(hotWindow), totalScanned, worker.HumanDuration(scanEvery), maxWatches)
	return nil
}
