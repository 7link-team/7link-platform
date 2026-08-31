package cmd

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// rootCmd represents the base command when called without any subcommands.
var rootCmd = &cobra.Command{
	Use:          filepath.Base(os.Args[0]),
	Short:        "API service",
	SilenceUsage: true,
}

// Execute runs the root command. It is called once by main.main.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
