package cli

import "github.com/spf13/cobra"

// registerAppCmd is set by app_cmd.go (build tag: app) to register the desktop
// app command. When built without the app tag this remains nil.
var registerAppCmd func(rootCmd *cobra.Command)
