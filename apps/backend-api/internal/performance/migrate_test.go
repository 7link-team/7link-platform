package performance

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestMigrationsDeclareEquivalentPrefixedImmutableSchemeTables(t *testing.T) {
	source := (&Module{}).MigrationSource()
	require.Equal(t, "goose_performance", source.TableName)
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		b, e := migrationsFS.ReadFile("sql/" + dialect + "/00001_schemes.sql")
		require.NoError(t, e)
		sql := string(b)
		for _, fragment := range []string{"{{.Prefix}}performance_schemes", "{{.Prefix}}performance_scheme_revisions", "UNIQUE (tenant_id,scheme_id,version)", "FOREIGN KEY (tenant_id,scheme_id)", "-- +goose Down"} {
			require.Contains(t, sql, fragment)
		}
		if dialect == "mysql" {
			require.NotContains(t, strings.ToUpper(sql), "TIMESTAMP")
		}
		reports, e := migrationsFS.ReadFile("sql/" + dialect + "/00002_reports.sql")
		require.NoError(t, e)
		for _, fragment := range []string{"{{.Prefix}}performance_reports", "{{.Prefix}}performance_report_revisions", "UNIQUE (tenant_id,scheme_id,subject_kind,subject_id,cadence,period_start,period_end)", "UNIQUE (tenant_id,report_id,version)", "FOREIGN KEY (tenant_id,report_id)", "FOREIGN KEY (tenant_id,scheme_id)", "-- +goose Down"} {
			require.Contains(t, string(reports), fragment)
		}
		if dialect == "mysql" {
			require.NotContains(t, strings.ToUpper(string(reports)), "TIMESTAMP")
		}
	}
}
