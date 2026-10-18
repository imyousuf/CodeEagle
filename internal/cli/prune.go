package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/indexer"
	"github.com/imyousuf/CodeEagle/internal/watcher"
)

func newPruneCmd() *cobra.Command {
	var (
		paths  []string
		dryRun bool
	)

	cmd := &cobra.Command{
		Use:   "prune --path <dir>",
		Short: "Remove a directory that should not have been indexed from the graph",
		Long: `Rebuild the graph database without a directory: every node recorded under it,
every edge touching one, and every index entry naming either.

This is for a directory indexed by mistake -- a scratch area, a dump of
generated files. Add it to watch.exclude first, or the next sync indexes it
again. A sync would eventually remove it too, once it noticed the files were
no longer walked, but only after re-running the linker and enrichment over the
whole graph; this removes just the directory.

What stays is copied into a new database, which then replaces the old one.
Such a directory can be most of the graph, and deleting it key by key is far
slower than copying the rest, and leaves the space unreclaimed until a later
compaction. The old database is kept beside the new one, named
<db>.before-prune-<time>; delete it once the result looks right. Writing the
copy needs free space for what is kept, not for what is removed.

--path takes the directory on disk (~/Downloads/mark) or as the graph records
it (Downloads/mark), and matches whole path components. Meetings are not
touched: they live outside the branch this prunes.

Stop the worker first. The database is held exclusively while it is copied,
and the prune fails rather than wait for another writer.`,
		Example: `  codeeagle prune --path ~/Downloads/mark --dry-run
  codeeagle prune --path ~/Downloads/mark`,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if len(paths) == 0 {
				return fmt.Errorf("--path is required")
			}

			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			roots := repoPaths(cfg)
			nonGit := make(map[string]bool)
			for _, r := range roots {
				if !indexer.IsGitRepo(r) {
					nonGit[r] = true
				}
			}

			excluded := watcher.NewExcludeMatcher(cfg.Watch.Exclude)
			prefixes := make([]string, 0, len(paths))
			for _, p := range paths {
				prefix, abs := pruneTarget(roots, nonGit, p)
				if abs != "" && !excluded.Match(abs) {
					fmt.Fprintf(out, "Warning: %s is not excluded by watch.exclude; the next sync will index it again.\n", abs)
				}
				prefixes = append(prefixes, prefix)
			}

			livePath := cfg.ResolveDBPath(dbPath)
			if livePath == "" {
				return fmt.Errorf("no graph database path; run 'codeeagle init' or use --db-path")
			}

			// Read-write, for the lock: it guarantees nothing else writes to the
			// database while it is copied, and that a worker is not holding the
			// directory about to be swapped out from under it.
			store, branch, err := embedded.OpenReadWrite(cfg, roots, dbPath)
			if err != nil {
				return err
			}
			defer func() {
				if store != nil {
					store.Close()
				}
			}()

			for _, prefix := range prefixes {
				res, err := store.CountUnder(prefix)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "%s/ on branch %q: %d files, %d nodes\n", prefix, branch, res.Files, res.Nodes)
			}
			if dryRun {
				fmt.Fprintln(out, "DRY RUN: nothing was changed")
				return nil
			}

			stamp := time.Now().Format("20060102-150405")
			newPath := livePath + ".pruned-" + stamp
			oldPath := livePath + ".before-prune-" + stamp

			fmt.Fprintf(out, "Copying everything else to %s...\n", newPath)
			start := time.Now()
			res, err := store.PruneCopy(cmd.Context(), newPath, prefixes, func(p embedded.PruneResult) {
				fmt.Fprintf(out, "  %s: %d keys kept; left out %d edges, %d index entries\n",
					time.Since(start).Round(time.Second), p.Kept, p.Edges, p.Indexes)
			})
			if err != nil {
				return fmt.Errorf("copy: %w (the database is unchanged; remove %s)", err, newPath)
			}
			fmt.Fprintf(out, "Left out %d files, %d nodes, %d edges, %d index entries", res.Files, res.Nodes, res.Edges, res.Indexes)
			if res.Dangling > 0 {
				fmt.Fprintf(out, ", and %d index entries naming edges that no longer existed", res.Dangling)
			}
			fmt.Fprintf(out, "; kept %d keys, in %s.\n", res.Kept, time.Since(start).Round(time.Second))

			if err := store.Close(); err != nil {
				return fmt.Errorf("close %s: %w", livePath, err)
			}
			store = nil

			// Check the copy before it replaces anything.
			check, err := embedded.NewBranchStore(newPath, branch, []string{branch})
			if err != nil {
				return fmt.Errorf("the copy at %s does not open: %w (the database is unchanged)", newPath, err)
			}
			for _, prefix := range prefixes {
				left, err := check.CountUnder(prefix)
				if err != nil || left.Nodes != 0 {
					check.Close()
					return fmt.Errorf("the copy at %s still holds %d nodes under %s (%v); the database is unchanged",
						newPath, left.Nodes, prefix, err)
				}
			}
			if err := check.Close(); err != nil {
				return fmt.Errorf("close the copy: %w", err)
			}

			if err := os.Rename(livePath, oldPath); err != nil {
				return fmt.Errorf("move the old database aside: %w (the copy is at %s)", err, newPath)
			}
			if err := os.Rename(newPath, livePath); err != nil {
				_ = os.Rename(oldPath, livePath)
				return fmt.Errorf("put the copy in place: %w (the old database was restored; the copy is at %s)", err, newPath)
			}

			fmt.Fprintf(out, "\n%s now holds the pruned graph (%s, was %s).\n", livePath, dirSize(livePath), dirSize(oldPath))
			fmt.Fprintf(out, "The old database is at %s. Delete it once the graph looks right:\n  rm -rf %s\n", oldPath, oldPath)
			return nil
		},
	}

	cmd.Flags().StringSliceVar(&paths, "path", nil, "directory to remove from the graph (repeatable)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "count what would be removed without changing anything")
	return cmd
}

// pruneTarget turns a --path argument into the graph prefix to prune, and the
// absolute directory it names when that is known. A path that exists on disk
// is mapped the way the indexer maps it; anything else is taken to be a graph
// path already.
func pruneTarget(roots []string, nonGit map[string]bool, p string) (prefix, abs string) {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	if filepath.IsAbs(p) {
		return filepath.ToSlash(indexer.GraphPath(roots, nonGit, filepath.Clean(p))), filepath.Clean(p)
	}
	if _, err := os.Stat(p); err == nil {
		if a, err := filepath.Abs(p); err == nil {
			return filepath.ToSlash(indexer.GraphPath(roots, nonGit, a)), a
		}
	}
	return strings.Trim(filepath.ToSlash(p), "/"), ""
}

// dirSize reports the total size of the files directly in dir, which for a
// badger directory is all of them.
func dirSize(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "unknown size"
	}
	var total int64
	for _, e := range entries {
		if info, err := e.Info(); err == nil && !e.IsDir() {
			total += info.Size()
		}
	}
	const gb, mb = 1 << 30, 1 << 20
	if total >= gb {
		return fmt.Sprintf("%.1f GB", float64(total)/gb)
	}
	return fmt.Sprintf("%.1f MB", float64(total)/mb)
}
