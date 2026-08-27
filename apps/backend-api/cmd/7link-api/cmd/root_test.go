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
	assert.NotNil(t, migrateCmd.Flags().Lookup("config"))
	assert.NotNil(t, serveCmd.Flags().Lookup("config"))
}
