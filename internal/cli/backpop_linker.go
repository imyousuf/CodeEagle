package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/linker"
)

func newBackpopCmd() *cobra.Command {
	var allPhases bool

	cmd := &cobra.Command{
		Use:   "backpop",
		Short: "Run linker phases on an already-indexed graph",
		Long: `Backpop runs linker phases on an existing graph database without re-indexing.

By default only the new phases (cross-file implements + test coverage) are run.
Use --all to run all linker phases.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			store, _, err := openBranchStore(cfg)
			if err != nil {
				return err
			}
			defer store.Close()

			out := cmd.OutOrStdout()

			logFn := func(format string, args ...any) {
				fmt.Fprintf(out, format+"\n", args...)
			}

			lnk := linker.NewLinker(store, nil, logFn, verbose)

			var phases []linker.Phase
			if allPhases {
				phases = lnk.Phases()
				fmt.Fprintln(out, "Running all linker phases...")
			} else {
				phases = lnk.NewPhases()
				fmt.Fprintln(out, "Running new linker phases (implements + tests)...")
			}

			results, err := lnk.RunPhases(context.Background(), phases)
			if err != nil {
				return err
			}

			fmt.Fprintln(out, "\nResults:")
			total := 0
			for _, phase := range phases {
				count := results[phase.Name]
				fmt.Fprintf(out, "  %-15s %d linked\n", phase.Name+":", count)
				total += count
			}
			fmt.Fprintf(out, "  %-15s %d\n", "total:", total)

			return nil
		},
	}

	cmd.Flags().BoolVar(&allPhases, "all", false, "run all linker phases (not just new ones)")

	return cmd
}
