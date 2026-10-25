package yunxiao

import (
	"github.com/stretchr/testify/require"
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

func TestEveryDialectPrefixesEveryNamedObjectWithinEngineLimits(t *testing.T) {
	identifiers := regexp.MustCompile(`\{\{\.Prefix\}\}([a-z_][a-z0-9_]*)`)
	expected := map[string]int{
		"00001_yunxiao.sql":                7,
		"00002_work.sql":                   2,
		"00003_worklog_dimensions.sql":     0,
		"00004_intake_channels.sql":        1,
		"00005_intake_receiving.sql":       0,
		"00006_automation_receipts.sql":    2,
		"00007_planning.sql":               5,
		"00008_automation_definitions.sql": 0,
		"00009_automation_dispatch.sql":    0,
		"00010_effort_confirmation.sql":    1,
		"00011_ingress_observations.sql":   1,
	}
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		entries, e := fs.ReadDir(migrationsFS, "sql/"+dialect)
		require.NoError(t, e)
		require.Len(t, entries, len(expected))
		for _, entry := range entries {
			b, e := fs.ReadFile(migrationsFS, "sql/"+dialect+"/"+entry.Name())
			require.NoError(t, e)
			sql := string(b)
			count, known := expected[entry.Name()]
			require.True(t, known, entry.Name())
			if entry.Name() == "00003_worklog_dimensions.sql" {
				require.Contains(t, sql, "ADD COLUMN work_item_id")
				require.Contains(t, sql, "CREATE INDEX {{.Prefix}}idx_worklog_work_period")
				require.Contains(t, sql, "DROP COLUMN work_item_id")
			}
			if entry.Name() == "00005_intake_receiving.sql" {
				for _, column := range []string{"credential_hash", "receiving_day", "received_count"} {
					require.Contains(t, sql, "ADD COLUMN "+column)
					require.Contains(t, sql, "DROP COLUMN "+column)
				}
			}
			if entry.Name() == "00008_automation_definitions.sql" {
				for _, column := range []string{"definition", "archived_at", "definition_snapshot"} {
					require.Contains(t, sql, "ADD COLUMN "+column+" ")
					require.Contains(t, sql, "DROP COLUMN "+column+";")
				}
			}
			if entry.Name() == "00009_automation_dispatch.sql" {
				for _, column := range []string{"run_as", "actions_result", "trigger_key"} {
					require.Contains(t, sql, "ADD COLUMN "+column+" ")
					require.Contains(t, sql, "DROP COLUMN "+column+";")
				}
				require.Contains(t, sql, "CREATE UNIQUE INDEX {{.Prefix}}uq_automation_run_source")
			}
			require.Equal(t, count, strings.Count(sql, "CREATE TABLE {{.Prefix}}"))
			dropCount := count
			if entry.Name() == "00011_ingress_observations.sql" {
				dropCount++
				require.Contains(t, sql, "integration_observation_rollback_guard")
			}
			if entry.Name() == "00010_effort_confirmation.sql" {
				dropCount++ // Named rollback guard is created only if absent.
				require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS {{.Prefix}}worklog_confirmation_rollback_guard")
				for _, column := range []string{"timezone", "confirmed_by", "confirmed_at"} {
					require.Contains(t, sql, "ADD COLUMN "+column+" ")
					require.Contains(t, sql, "DROP COLUMN "+column+";")
				}
			}
			require.Equal(t, dropCount, strings.Count(sql, "DROP TABLE {{.Prefix}}"))
			for _, match := range identifiers.FindAllStringSubmatch(sql, -1) {
				require.LessOrEqual(t, len("p12345678901234567890"+match[1]), 63, match[1])
			}
			require.NotContains(t, sql, " TIMESTAMP ")
			if dialect == "postgres" {
				require.NotContains(t, sql, "`")
			}
		}
	}
	require.Equal(t, "goose_yunxiao", (&Module{}).MigrationSource().TableName)
}
