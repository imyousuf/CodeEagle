package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/service"
)

func newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Run the worker automatically when you log in",
		Long: `Install the CodeEagle worker as a user-scope service that starts at login.

A systemd user unit on Linux, a launchd LaunchAgent on macOS. Neither needs
root, and neither runs for anyone else on the machine.

It starts at login rather than at boot on purpose. Syncs read credentials from
the login keyring -- config values like $(keyring get ...) -- and a service
started before anyone logs in has no unlocked keyring to read. CodeEagle stops
rather than continue with an empty credential, so a boot-time service would
fail on its first sync. Tying it to the login session avoids that entirely.`,
	}
	cmd.AddCommand(newServiceInstallCmd(), newServiceUninstallCmd(), newServiceStatusCmd())
	return cmd
}

func newServiceInstallCmd() *cobra.Command {
	var (
		dryRun      bool
		settle      string
		concurrency int
	)

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install and start the worker service for this user",
		Example: `  codeeagle service install
  codeeagle service install --dry-run        # print the unit or plist, change nothing
  codeeagle service install --settle 60s --concurrency 2`,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			workerArgs := []string{"worker"}
			if settle != "" {
				workerArgs = append(workerArgs, "--settle", settle)
			}
			if concurrency > 0 {
				workerArgs = append(workerArgs, "--concurrency", fmt.Sprint(concurrency))
			}

			plan, err := service.Build(service.Config{Args: workerArgs})
			if err != nil {
				return err
			}

			if dryRun {
				fmt.Fprintf(out, "Would write %s (%s):\n\n%s\n", plan.Path, plan.Platform, plan.Contents)
				fmt.Fprintln(out, "Would then run:")
				for _, c := range plan.Activate {
					fmt.Fprintf(out, "  %s\n", strings.Join(c, " "))
				}
				return nil
			}

			if err := service.Install(cmd.Context(), plan, out); err != nil {
				return err
			}
			fmt.Fprintf(out, "\nInstalled as a %s. It will start again at your next login.\n", plan.Platform)
			fmt.Fprintln(out, "Follow it with:")
			fmt.Fprintln(out, "  codeeagle service status")
			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be installed without installing it")
	cmd.Flags().StringVar(&settle, "settle", "", "pass --settle to the worker (e.g. 60s)")
	cmd.Flags().IntVar(&concurrency, "concurrency", 0, "pass --concurrency to the worker")
	return cmd
}

func newServiceUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "uninstall",
		Short:   "Stop the worker service and remove it",
		Aliases: []string{"remove"},
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := service.Build(service.Config{})
			if err != nil {
				return err
			}
			return service.Uninstall(cmd.Context(), plan, cmd.OutOrStdout())
		},
	}
}

func newServiceStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether the worker service is installed and running",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			plan, err := service.Build(service.Config{})
			if err != nil {
				return err
			}
			st := service.Query(cmd.Context(), plan)

			fmt.Fprintf(out, "Mechanism: %s\n", plan.Platform)
			fmt.Fprintf(out, "File:      %s\n", st.Path)
			if st.Installed {
				fmt.Fprintln(out, "Installed: yes")
			} else {
				fmt.Fprintln(out, "Installed: no")
			}
			if st.Running {
				fmt.Fprintln(out, "Running:   yes")
			} else {
				fmt.Fprintf(out, "Running:   no")
				if st.Detail != "" {
					fmt.Fprintf(out, " (%s)", st.Detail)
				}
				fmt.Fprintln(out)
			}
			if !st.Installed {
				fmt.Fprintln(out, "\nInstall it with: codeeagle service install")
			}
			return nil
		},
	}
}
