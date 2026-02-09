// Package cli implements the command-line interface for CodeEagle.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile string
	verbose bool
)

// rootCmd is the base command.
var rootCmd = &cobra.Command{
	Use:   "codeeagle",
	Short: "CodeEagle - Codebase knowledge graph and AI-powered code intelligence",
	Long: `CodeEagle watches codebases (monorepos, multi-repo setups, or combinations),
builds a knowledge graph of source code and documentation, and exposes
non-coding AI agents for planning, designing, and code review — all grounded
in deep codebase understanding.

Commands:
  init       Initialize a .codeeagle.yaml config file
  watch      Start watching and building/updating the knowledge graph
  status     Show indexing status and graph stats
  agent      Interact with AI agents (plan, design, review)
  query      Query the knowledge graph
  metrics    Show code quality metrics`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	cobra.OnInitialize(initConfig)

	// Persistent flags (available to all subcommands)
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: .codeeagle.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")

	// Bind flags to viper
	bindFlag := func(key, flag string) {
		if err := viper.BindPFlag(key, rootCmd.PersistentFlags().Lookup(flag)); err != nil {
			panic(fmt.Sprintf("failed to bind %s flag: %v", flag, err))
		}
	}
	bindFlag("config_file", "config")

	// Add subcommands
	rootCmd.AddCommand(newInitCmd())
	rootCmd.AddCommand(newWatchCmd())
	rootCmd.AddCommand(newStatusCmd())
	rootCmd.AddCommand(newAgentCmd())
	rootCmd.AddCommand(newQueryCmd())
	rootCmd.AddCommand(newMetricsCmd())
	rootCmd.AddCommand(newLLMTestCmd())
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	}
}
