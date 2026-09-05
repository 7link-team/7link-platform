package cmd

import (
	"errors"
	"fmt"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRootCommandOnlyOwnsTopLevelRouting(t *testing.T) {
	assert.Nil(t, rootCmd.PersistentFlags().Lookup("config"))
	assert.Nil(t, rootCmd.PersistentFlags().Lookup("debug"))
	assert.Same(t, rootCmd, migrateCmd.Parent())
	assert.Same(t, rootCmd, serveCmd.Parent())
	assert.NotNil(t, migrateCmd.PersistentFlags().Lookup("config"))
	assert.Nil(t, migrateCmd.PersistentFlags().Lookup("action"))
	assert.NotNil(t, migrateCmd.PersistentFlags().Lookup("module"))
	assert.NotNil(t, migrateCmd.PersistentFlags().Lookup("to"))
	assert.Same(t, migrateCmd, migrateAutoCmd.Parent())
	assert.Same(t, migrateCmd, migrateStatusCmd.Parent())
	assert.Same(t, migrateCmd, migrateValidateCmd.Parent())
	assert.Same(t, migrateCmd, migrateUpCmd.Parent())
	assert.Same(t, migrateCmd, migrateUpToCmd.Parent())
	assert.Same(t, migrateCmd, migrateDownCmd.Parent())
	assert.Same(t, migrateCmd, migrateDownToCmd.Parent())
	assert.NotNil(t, serveCmd.Flags().Lookup("config"))
	assert.NotNil(t, serveCmd.Flags().Lookup("migrate"))
}

// TestExitCodeDefaultsToOrdinaryFailure keeps the status contract narrow: only
// a command that deliberately asked for a distinct status gets one, and success
// is never accidentally reported as a failure.
func TestExitCodeDefaultsToOrdinaryFailure(t *testing.T) {
	assert.NoError(t, withExitCode(exitFailure, nil), "a nil error stays nil so callers can wrap unconditionally")
	assert.Equal(t, 0, exitCode(nil))
	assert.Equal(t, exitFailure, exitCode(errors.New("boom")))
}

// TestExitCodeSurvivesWrappingAndKeepsTheUnderlyingError proves the tag travels
// out through RunE, where cobra reports the message and only the status is
// still ours to carry.
func TestExitCodeSurvivesWrappingAndKeepsTheUnderlyingError(t *testing.T) {
	cause := errors.New("conflicting administrator")
	tagged := withExitCode(exitConflict, cause)

	assert.Equal(t, cause.Error(), tagged.Error(), "the operator reads the cause, not the status")
	assert.ErrorIs(t, tagged, cause)
	assert.Equal(t, exitConflict, exitCode(tagged))
	assert.Equal(t, exitConflict, exitCode(fmt.Errorf("bootstrap: %w", tagged)))
}

// TestExitCodeTakesTheOutermostRequestedStatus pins which status wins when a
// command tags an error a second time.
func TestExitCodeTakesTheOutermostRequestedStatus(t *testing.T) {
	inner := withExitCode(exitTransient, errors.New("boom"))
	assert.Equal(t, exitConflict, exitCode(withExitCode(exitConflict, inner)))
}

// TestEveryCommandAcceptsEitherFlagSeparator pins the property a command group
// silently lost. Settings are named with underscores in the configuration file
// and with dashes on the command line, so both spellings have to reach the same
// flag -- and they did not: a group hangs its settings on persistent flags,
// cobra merges those into each subcommand without the normalizer the group
// installed, and the spelling that worked on serve was an unknown flag on
// migrate and iam.
func TestEveryCommandAcceptsEitherFlagSeparator(t *testing.T) {
	for name, command := range map[string]*cobra.Command{
		"a command carrying its own settings":    serveCmd,
		"a subcommand of the migrate group":      migrateUpCmd,
		"a subcommand of the iam group":          bootstrapPlatformAdminCmd,
		"the newest subcommand of the iam group": repairTenantOwnerCmd,
	} {
		t.Run(name, func(t *testing.T) {
			// Cobra folds a parent group persistent flags into a subcommand
			// own set only once it is about to run, and this is the earliest
			// call that forces it.
			command.InheritedFlags()
			flags := command.Flags()
			require.NotNil(t, flags.Lookup("table-prefix"),
				"every command reads the table prefix, so every command has to expose it")
			assert.Same(t, flags.Lookup("table-prefix"), flags.Lookup("table_prefix"),
				"both separators have to resolve to one flag, not to one flag and an error")
		})
	}
}
