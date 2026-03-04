package cli

import "github.com/spf13/cobra"

// registerFacesCmd is set by faces.go (build tag: faces) to register face
// detection commands. When built without the faces tag this remains nil.
var registerFacesCmd func(rootCmd *cobra.Command)
