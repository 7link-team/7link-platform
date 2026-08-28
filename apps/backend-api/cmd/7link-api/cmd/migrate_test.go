package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func executeCommand(args ...string) (string, error) {
	output := &bytes.Buffer{}
	rootCmd.SetArgs(args)
	rootCmd.SetOut(output)
	rootCmd.SetErr(output)
	err := rootCmd.Execute()
	return output.String(), err
}

func TestMigrateCommandUsesSharedApplicationConfig(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.yaml")
	databasePath := filepath.Join(directory, "cli.sqlite")
	configData := fmt.Appendf(nil, `database:
  main:
    type: sqlite
    dsn: %s
    writable: true
    readable: true
`, databasePath)
	require.NoError(t, os.WriteFile(configPath, configData, 0o600))

	output, err := executeCommand("migrate", "--config", filepath.Join(directory, "missing.yaml"))
	require.NoError(t, err)
	assert.Contains(t, output, "Available Commands:")
	assert.Contains(t, output, "auto")
	assert.NotContains(t, output, "MODULE\tCURRENT")

	output, err = executeCommand("migrate", "auto", "--config", configPath)
	require.NoError(t, err)
	assert.Contains(t, output, "dlock")
	assert.Contains(t, output, "wuid")
	assert.Contains(t, output, "clean")

	output, err = executeCommand("migrate", "status", "--config", configPath)
	require.NoError(t, err)
	assert.Contains(t, output, "MODULE")
	assert.Contains(t, output, "clean")

	output, err = executeCommand(
		"migrate", "down-to", "--module=wuid", "--to=0", "--config", configPath,
	)
	require.NoError(t, err)
	assert.Contains(t, output, "wuid")
	assert.Contains(t, output, "pending")

	output, err = executeCommand("migrate", "validate", "--config", configPath)
	assert.ErrorContains(t, err, "wuid migration state is pending")
	assert.Contains(t, output, "pending")
}
