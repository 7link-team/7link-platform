package cmd

import (
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
