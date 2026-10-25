package execution

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMigrationHasThreeDialects(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		data, err := migrationsFS.ReadFile("sql/" + dialect + "/00001_executions.sql")
		require.NoError(t, err)
		require.Contains(t, string(data), "{{.Prefix}}execution_executions")
		require.Contains(t, string(data), "{{.Prefix}}execution_receipts")
	}
	require.Equal(t, "goose_execution", (&Module{}).MigrationSource().TableName)
}
