package knowledge

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMigrationIncludesThreeDialects(t *testing.T) {
	m, _, _ := knowledgeHarness(t)
	source := m.MigrationSource()
	require.Equal(t, "goose_knowledge", source.TableName)
	for _, dialect := range []string{"sqlite", "postgres", "mysql"} {
		b, e := source.FS.Open("sql/" + dialect + "/00001_entries.sql")
		require.NoError(t, e)
		require.NoError(t, b.Close())
	}
}
