package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSourceCandidates(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{"custom"}, sourceCandidates("custom", "windows"))
	require.Equal(t,
		[]string{`D:\Projects\Chaosplus\chaosplus-api\apps\server`},
		sourceCandidates("", "windows"),
	)
	require.Equal(t,
		[]string{"/Volumes/Macintosh/Projects/chaosplus/chaosplus/apps/server"},
		sourceCandidates("", "darwin"),
	)
}

func TestSchemaTables(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	for _, dialect := range []string{"mysql", "postgres", "sqlite"} {
		directory := filepath.Join(source, "internal", "modules", "iam", "sql", dialect)
		require.NoError(t, os.MkdirAll(directory, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(directory, "00001.sql"), []byte(`
CREATE TABLE iam_roles (id BIGINT PRIMARY KEY);
CREATE TABLE IF NOT EXISTS "iam_menus" (id BIGINT PRIMARY KEY);
CREATE TABLE iam_roles (id BIGINT PRIMARY KEY);
`), 0o600))

		tables, err := schemaTables(source, dialect)
		require.NoError(t, err)
		require.Equal(t, []string{"iam_menus", "iam_roles"}, tables)
	}
}

func TestContainsSequence(t *testing.T) {
	t.Parallel()

	require.True(t, containsSequence([]string{"iam", "sql", "mysql", "00001.sql"}, "sql", "mysql"))
	require.False(t, containsSequence([]string{"iam", "mysql", "sql", "00001.sql"}, "sql", "mysql"))
}

func TestMakeEscape(t *testing.T) {
	t.Parallel()

	require.Equal(t, `C:/Source\ Tree/\#1/$$work`, makeEscape(`C:/Source Tree/#1/$work`))
}
