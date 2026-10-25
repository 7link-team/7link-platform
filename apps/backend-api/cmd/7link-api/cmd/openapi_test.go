package cmd

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newOpenAPICLIConfig writes a deployment the export can compose: a schema it
// migrates itself, so the command needs nothing set up in advance beyond the
// configuration it would have anyway.
func newOpenAPICLIConfig(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 1)
	}
	key := base64.RawStdEncoding.EncodeToString(seed)

	configPath := filepath.Join(directory, "config.yaml")
	configData := fmt.Appendf(nil, `name: openapi-export-test
version: "9.9.9"
migrate: true
database:
  main:
    type: sqlite
    dsn: %s?_journal_mode=WAL&_busy_timeout=30000
    writable: true
    readable: true
authn:
  enabled: true
  issuer: https://iam.example.test
  access_token_ttl: 1h
  signing_key: %s
  mfa:
    encryption_key: %s
`, filepath.ToSlash(filepath.Join(directory, "openapi.sqlite")), key, key)
	require.NoError(t, os.WriteFile(configPath, configData, 0o600))
	return configPath
}

// keepOpenAPIFlagsFromLeaking restores the export flags after a test that sets
// them. Cobra keeps a flag set for the life of the process, and these tests all
// drive the same command object, so without this the first test to name a file
// or a format would decide what every later test in this package exported --
// and a test that meant to check the pipe would quietly be writing a file.
func keepOpenAPIFlagsFromLeaking(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		require.NoError(t, openapiCmd.Flags().Set("out", ""))
		require.NoError(t, openapiCmd.Flags().Set("format", ""))
	})
}

// TestOpenAPICommandIsWiredIntoTheRootCommand keeps the export a sibling of
// serve rather than a member of any one group: the document describes the whole
// application, not the part of it any single group operates on.
func TestOpenAPICommandIsWiredIntoTheRootCommand(t *testing.T) {
	assert.Same(t, rootCmd, openapiCmd.Parent())
	assert.NotNil(t, openapiCmd.Flags().Lookup("out"))
	assert.NotNil(t, openapiCmd.Flags().Lookup("format"))
	assert.Contains(t, openapiCmd.Long, "--out",
		"the operator has to learn where the document goes from the command, not the source")
}

// TestOpenAPICommandWritesADocumentAndNamesWhereItPutIt covers the run a build
// step makes. The path is reported because a command that writes a file and
// says nothing leaves the caller to guess whether it wrote the one they meant.
func TestOpenAPICommandWritesADocumentAndNamesWhereItPutIt(t *testing.T) {
	keepOpenAPIFlagsFromLeaking(t)
	configPath := newOpenAPICLIConfig(t)
	out := filepath.Join(t.TempDir(), "openapi.json")

	output, err := executeCommand("openapi", "--config", configPath, "--out", out)
	require.NoError(t, err)
	assert.Contains(t, output, "openapi: "+out)

	document, err := os.ReadFile(out)
	require.NoError(t, err)
	var specification struct {
		Info  struct{ Version string }  `json:"info"`
		Paths map[string]map[string]any `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(document, &specification))
	assert.Equal(t, "9.9.9", specification.Info.Version,
		"the configured version, not the build stamp this binary carries")
	assert.NotEmpty(t, specification.Paths)
}

// TestOpenAPICommandReplacesAnExistingDocumentWholeOrNotAtAll is why the write
// goes through a rename. A file left half written would be read by the next
// step as an API that never existed, and the difference it reported would
// describe an interrupted command rather than a change anybody made.
func TestOpenAPICommandReplacesAnExistingDocumentWholeOrNotAtAll(t *testing.T) {
	keepOpenAPIFlagsFromLeaking(t)
	configPath := newOpenAPICLIConfig(t)
	directory := t.TempDir()
	out := filepath.Join(directory, "openapi.json")
	previous := []byte("{\"openapi\":\"3.1.0\",\"paths\":{}}\n")
	require.NoError(t, os.WriteFile(out, previous, 0o644))

	_, err := executeCommand("openapi", "--config", configPath, "--out", out)
	require.NoError(t, err)
	replaced, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.NotEqual(t, previous, replaced)

	// The temporary file the rename came from must be gone, or a build that
	// globs the directory would pick up a second, older document.
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "openapi.json", entries[0].Name())
}

// TestOpenAPICommandLeavesTheTargetAloneWhenItCannotProduceADocument is the
// case that matters most to a repository holding a good document: a refused run
// must not be the reason the file is now empty.
func TestOpenAPICommandLeavesTheTargetAloneWhenItCannotProduceADocument(t *testing.T) {
	keepOpenAPIFlagsFromLeaking(t)
	configPath := newOpenAPICLIConfig(t)
	out := filepath.Join(t.TempDir(), "openapi.json")
	previous := []byte("{\"openapi\":\"3.1.0\"}\n")
	require.NoError(t, os.WriteFile(out, previous, 0o644))

	_, err := executeCommand("openapi", "--config", configPath, "--out", out, "--format", "toml")
	require.Error(t, err)
	assert.Equal(t, exitFailure, exitCode(err))

	kept, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, previous, kept, "a rejected format may not cost the caller the document they had")
}

// TestOpenAPICommandSendsTheDocumentAloneToStandardOutput covers the pipe. The
// application logs its startup, and those lines would land in the middle of the
// document unless they are sent somewhere else -- which turns a redirect into a
// file no parser accepts, at the point where somebody is least likely to look.
func TestOpenAPICommandSendsTheDocumentAloneToStandardOutput(t *testing.T) {
	keepOpenAPIFlagsFromLeaking(t)
	configPath := newOpenAPICLIConfig(t)

	output, err := executeCommand("openapi", "--config", configPath)
	require.NoError(t, err)

	var specification map[string]any
	require.NoError(t, json.Unmarshal([]byte(output), &specification),
		"standard output has to parse on its own, with nothing else mixed into it")
	assert.Equal(t, "3.1.0", specification["openapi"])
}
