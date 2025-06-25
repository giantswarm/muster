package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newVersionCmd creates the Cobra command for displaying the application version.
// The actual version information is typically managed by the root command or a global variable.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version number of muster",
		Long:  `All software has versions. This is muster's.`,
		Run: func(cmd *cobra.Command, args []string) {
			// rootCmd.Version is expected to be set, typically in root.go during build time.
			fmt.Fprintf(cmd.OutOrStdout(), "muster version %s\n", rootCmd.Version)
		},
	}
}
