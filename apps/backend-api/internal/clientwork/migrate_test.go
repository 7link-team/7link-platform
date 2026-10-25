package clientwork

import (
	"github.com/stretchr/testify/require"
	"io/fs"
	"testing"
)

func TestMigrationsEmbedEveryDialectAndImmutableContentBindings(t *testing.T) {
	s := (&Module{}).MigrationSource()
	require.Equal(t, "goose_clientwork", s.TableName)
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		files, e := fs.Glob(s.FS, "sql/"+dialect+"/*.sql")
		require.NoError(t, e)
		require.Len(t, files, 32)
		for _, name := range files {
			data, e := fs.ReadFile(s.FS, name)
			require.NoError(t, e)
			require.Contains(t, string(data), "{{.Prefix}}")
			require.Contains(t, string(data), "-- +goose Up")
			require.Contains(t, string(data), "-- +goose Down")
		}
	}
}
