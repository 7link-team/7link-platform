package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// Building the schema is the same work every test, and goose running every
// migration this module depends on cost about 55 ms of each one. It is done once
// per prefix and copied after that -- a file somebody else migrated is
// indistinguishable from one migrated here, and the copy costs about four.
//
// TestMain owns the directory so a template outlives the test that built it and
// nothing is left behind afterwards.

var (
	schemaTemplateDirectory string
	schemaTemplates         sync.Map
)

func TestMain(m *testing.M) {
	directory, err := os.MkdirTemp("", "core-schema-templates")
	if err != nil {
		panic(err)
	}
	schemaTemplateDirectory = directory
	code := m.Run()
	_ = os.RemoveAll(directory)
	os.Exit(code)
}

func templateSQLiteDSN(path string) string {
	return fmt.Sprintf("file:%s?_busy_timeout=10000&_journal_mode=WAL", filepath.ToSlash(path))
}

// templateSchemaDatabase returns the path of a migrated database for one prefix,
// building it on first use. The builder closes its connection, which checkpoints
// the write-ahead log into the file a copy is taken from.
func templateSchemaDatabase(t *testing.T, prefix string) string {
	t.Helper()
	if cached, ok := schemaTemplates.Load(prefix); ok {
		return cached.(string)
	}
	name := prefix
	if name == "" {
		name = "plain"
	}
	path := filepath.Join(schemaTemplateDirectory, name+".sqlite")
	initializeRepositoryDatabase(t, templateSQLiteDSN(path), prefix)
	cached, _ := schemaTemplates.LoadOrStore(prefix, path)
	return cached.(string)
}

func cloneSchemaDatabase(t *testing.T, prefix, destination string) {
	t.Helper()
	schema, err := os.ReadFile(templateSchemaDatabase(t, prefix))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(destination, schema, 0o600))
}
