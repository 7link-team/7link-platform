package cmd

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
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
