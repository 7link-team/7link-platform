package cmd

import (
	"errors"
	"os"
	"path/filepath"

	"7link.com/platform/pkg/config"
	"github.com/spf13/cobra"
)

// exitFailure is the status every command reports for an ordinary failure.
// Commands whose outcomes an operator has to branch on return an exitCodeError
// instead so the shell can tell those outcomes apart.
const exitFailure = 1

// rootCmd represents the base command when called without any subcommands.
var rootCmd = &cobra.Command{
	Use:          filepath.Base(os.Args[0]),
	Short:        "API service",
	SilenceUsage: true,
}

func init() {
	// Every command reads its settings through the same configurator, which
	// accepts either separator in a flag name. That only held for commands
	// carrying their settings on their own flag set: cobra merges a group
	// persistent flags into each subcommand without the normalizer the group
	// installed, so the same spelling worked on serve and was an unknown flag
	// on migrate and iam. Installing it here covers the whole tree, including
	// subcommands added after this runs.
	rootCmd.SetGlobalNormalizationFunc(config.NormalizeFlagName)
}

// exitCodeError carries the process status a command wants alongside its error.
// Scripted operations need to distinguish "already done" from "conflict" from
// "try again later", and parsing stderr for that is not a contract.
type exitCodeError struct {
	code int
	err  error
}

func (e exitCodeError) Error() string { return e.err.Error() }

func (e exitCodeError) Unwrap() error { return e.err }

// withExitCode tags err with the status to exit with. A nil error stays nil so
// callers can wrap unconditionally.
func withExitCode(code int, err error) error {
	if err == nil {
		return nil
	}
	return exitCodeError{code: code, err: err}
}

// exitCode reports the status for err, defaulting to the ordinary failure
// status when no command asked for a specific one.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var coded exitCodeError
	if errors.As(err, &coded) {
		return coded.code
	}
	return exitFailure
}

// Execute runs the root command. It is called once by main.main.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		// Cobra has already reported err; only the status still has to travel.
		os.Exit(exitCode(err))
	}
}
